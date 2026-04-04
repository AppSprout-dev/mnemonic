#!/usr/bin/env python3
"""Generate targeted training data for specific encoding failure modes.

Categories:
  A: stack_trace    — Inputs with file:line pairs that must be preserved verbatim
  B: named_entity   — Inputs with person names in technical context
  C: sparse_input   — Minimal inputs requiring minimal output (template-generated, no API)
  D: domain_terms   — Inputs with precise technical terminology (no synonym substitution)
  E: numerical      — Inputs with exact numbers/metrics that must be preserved

Usage:
    # Generate a single category
    LLM_API_KEY=... python generate_targeted_data.py --category stack_trace --count 400

    # Generate all categories
    LLM_API_KEY=... python generate_targeted_data.py --category all

    # Dry run (show prompts, don't call API)
    python generate_targeted_data.py --category sparse_input --count 10 --dry-run
"""

import argparse
import asyncio
import json
import os
import random
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from training_constants import ENCODING_SYSTEM_PROMPT, REQUIRED_FIELDS  # noqa: E402

API_KEY = os.environ.get("LLM_API_KEY", "")
API_BASE = "https://generativelanguage.googleapis.com/v1beta/openai"
MODEL = "gemini-3.1-pro-preview"
MAX_CONCURRENT = 10
RETRY_LIMIT = 5

OUTPUT_DIR = Path("training/data/targeted")


# ---- Gemini API ----

async def call_gemini(session, system: str, user: str,
                      semaphore: asyncio.Semaphore) -> str | None:
    import aiohttp as _aiohttp  # noqa: F811 — lazy import, only in felixlm venv
    headers = {"Authorization": f"Bearer {API_KEY}", "Content-Type": "application/json"}
    payload = {
        "model": MODEL,
        "messages": [
            {"role": "system", "content": system},
            {"role": "user", "content": user},
        ],
        "temperature": 0.8,
        "max_tokens": 2048,
    }

    for attempt in range(RETRY_LIMIT):
        async with semaphore:
            try:
                async with session.post(f"{API_BASE}/chat/completions",
                                        headers=headers, json=payload,
                                        timeout=_aiohttp.ClientTimeout(total=60)) as resp:
                    if resp.status in (429, 503):
                        wait = min(30, 2 ** attempt * 2)
                        print(f"  Rate limited ({resp.status}), waiting {wait}s...")
                        await asyncio.sleep(wait)
                        continue
                    resp.raise_for_status()
                    data = await resp.json()
                    return data["choices"][0]["message"]["content"]
            except Exception as e:
                if attempt < RETRY_LIMIT - 1:
                    await asyncio.sleep(2 ** attempt)
                    continue
                print(f"  API error after {RETRY_LIMIT} retries: {e}")
                return None
    return None


def parse_json_response(text: str) -> dict | None:
    text = text.strip()
    if text.startswith("```"):
        lines = text.split("\n")
        lines = [line for line in lines if not line.strip().startswith("```")]
        text = "\n".join(lines).strip()
    # Strip thinking tags
    if "<think>" in text:
        end = text.rfind("</think>")
        if end >= 0:
            text = text[end + len("</think>"):].strip()
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        start = text.find("{")
        end = text.rfind("}") + 1
        if start >= 0 and end > start:
            try:
                return json.loads(text[start:end])
            except json.JSONDecodeError:
                return None
    return None


def validate_encoding(data: dict) -> bool:
    return REQUIRED_FIELDS.issubset(data.keys())


# ---- Category A: Stack Traces ----

STACK_TRACE_DOMAINS = [
    "Go panic with goroutine stack trace showing 3-4 frames with exact file.go:line numbers, including the panic message and goroutine ID",
    "Python traceback from a Django REST framework view with 4-5 frames showing exact file paths and line numbers, triggered by a database query timeout",
    "Rust backtrace from an async tokio runtime with 3 frames showing exact source.rs:line locations, caused by an unwrap on None",
    "Go nil pointer panic in an HTTP handler showing handler.go:line, middleware.go:line, and server.go:line in the stack",
    "Python ImportError traceback with 3 frames showing __init__.py:line, loader.py:line, and module.py:line",
    "Go index out of range panic in a data processing pipeline showing processor.go:line and pipeline.go:line",
    "Python TypeError traceback from a Flask route with 4 frames, including the exact line where a None value was used as a dict",
    "Rust thread panic from a crossbeam channel recv showing worker.rs:line and scheduler.rs:line",
    "Go race condition detected by the race detector showing the two conflicting access points with exact file:line pairs",
    "Python RecursionError traceback showing the recursive function with exact file:line for the recursive call",
    "JavaScript TypeError stack trace from a Node.js Express middleware with 3 frames showing router.js:line and handler.js:line",
    "Go deadlock detection output showing two goroutines blocked, each with exact file:line locations",
    "Python KeyError traceback in a data pipeline with 4 frames showing transform.py:line, pipeline.py:line, and runner.py:line",
    "Go context deadline exceeded error with stack showing handler.go:line, client.go:line, and transport.go:line",
    "Python AssertionError in a test file showing test_module.py:line, conftest.py:line, and the exact assertion that failed",
    "Rust borrow checker error message with exact source locations showing the conflicting borrows at file.rs:line1 and file.rs:line2",
    "Go segfault in CGo code showing the C stack alongside Go stack with exact file:line pairs for both",
    "Python MemoryError during model training showing trainer.py:line, dataloader.py:line, and batch.py:line",
    "Go connection refused error with stack trace showing dialer.go:line, pool.go:line, and client.go:line",
    "Python ValueError in JSON parsing showing parser.py:line with the exact problematic input substring",
]

STACK_TRACE_GEN_PROMPT = (
    "Generate a realistic developer observation about encountering {domain}. "
    "The observation MUST include:\n"
    "1. At least 2 specific file:line references (e.g., spread.go:142, agent.py:89)\n"
    "2. The exact error message or panic text\n"
    "3. What the developer did to investigate or fix it\n"
    "4. Specific function or method names from the stack\n"
    "3-6 sentences. Output ONLY the observation text, no markdown."
)


# ---- Category B: Named Entities ----

NAMED_ENTITY_DOMAINS = [
    "a code review where {name1} pointed out a race condition in {name2}'s pull request for the auth middleware",
    "an incident where {name1} was on-call and escalated to {name2} after the database connection pool was exhausted",
    "a design review meeting where {name1}, {name2}, and {name3} debated between event sourcing and CQRS for the order service",
    "a deployment where {name1} rolled back {name2}'s release after latency spiked from 50ms to 800ms in production",
    "a pair programming session where {name1} helped {name2} debug a memory leak in the WebSocket handler",
    "a sprint retrospective where {name1} proposed and {name2} seconded moving from weekly to daily deployments",
    "a security audit where {name1} found that {name2}'s API endpoint was missing rate limiting",
    "an outage report written by {name1} about {name2}'s misconfigured Kubernetes pod resource limits",
    "a mentoring session where {name1} walked {name2} through the caching architecture and TTL strategy",
    "a handoff where {name1} documented the migration plan before {name2} took over the database upgrade",
    "a bug triage where {name1} assigned the high-priority nil pointer crash to {name2} for the next sprint",
    "a standup where {name1} mentioned blocking on {name2}'s API changes and {name3} offered to help unblock",
    "a postmortem where {name1} identified the root cause and {name2} wrote the remediation plan",
    "a knowledge transfer where {name1} explained the Felix-LM spoke architecture to new team member {name2}",
    "a release planning session where {name1} advocated for shipping {name2}'s feature flag behind a gradual rollout",
]

FIRST_NAMES = [
    "Jason", "Sarah", "Miguel", "Priya", "Chen", "Alex", "Jordan", "Fatima",
    "Dmitri", "Amara", "Kenji", "Elena", "Marcus", "Aisha", "Lars",
    "Wei", "Rachel", "Omar", "Sofia", "Takeshi", "Nadia", "Carlos",
]

NAMED_ENTITY_GEN_PROMPT = (
    "Generate a realistic developer observation about {domain}. "
    "The observation MUST:\n"
    "1. Mention all named people by their first name at least once\n"
    "2. Include what each person specifically did or said\n"
    "3. Include at least one specific technical detail (file path, metric, tool name)\n"
    "3-6 sentences. Output ONLY the observation text, no markdown."
)


# ---- Category C: Sparse Inputs (template-generated, no API) ----

SPARSE_INPUTS = [
    "fixed it", "done", "LGTM", "merged", "deployed", "tests pass", "looks good",
    "it works", "ship it", "approved", "ok", "works now", "resolved", "closed",
    "nvm found it", "figured it out", "never mind", "false alarm", "my bad",
    "restarted the service", "rolled back", "pushed the fix", "tagged the release",
    "updated the config", "ran the migration", "cleared the cache",
    "synced with main", "rebased", "cherry-picked", "reverted",
    "builds now", "compiles", "no more errors", "green", "all clear",
    "checked", "verified", "confirmed", "acknowledged", "noted",
    "will look at it later", "need more info", "can't reproduce", "works on my machine",
    "investigating", "looking into it", "on it", "in progress",
    # Slightly longer but still sparse
    "the thing is fixed", "got it working again", "yeah that did it",
    "same as before", "still broken", "no change", "tried that already",
]

# Map sparse inputs to appropriate gists and concepts
SPARSE_MAPPING = {
    # Completion/success
    "fixed it": {"gist": "Issue fixed", "concepts": ["fix", "debugging"], "tone": "positive"},
    "done": {"gist": "Task done", "concepts": ["task completion"], "tone": "positive"},
    "LGTM": {"gist": "Code approved", "concepts": ["code review"], "tone": "positive"},
    "merged": {"gist": "PR merged", "concepts": ["git", "code review"], "tone": "positive"},
    "deployed": {"gist": "Deployment completed", "concepts": ["deployment"], "tone": "positive"},
    "tests pass": {"gist": "Tests passing", "concepts": ["testing"], "tone": "positive"},
    "looks good": {"gist": "Change approved", "concepts": ["code review"], "tone": "positive"},
    "it works": {"gist": "Verification passed", "concepts": ["testing"], "tone": "positive"},
    "ship it": {"gist": "Ready to release", "concepts": ["deployment", "release"], "tone": "excited"},
    "approved": {"gist": "Change approved", "concepts": ["code review"], "tone": "positive"},
    "ok": {"gist": "Acknowledged", "concepts": ["status update"], "tone": "neutral"},
    "works now": {"gist": "Issue resolved", "concepts": ["fix", "debugging"], "tone": "positive"},
    "resolved": {"gist": "Issue resolved", "concepts": ["fix"], "tone": "positive"},
    "closed": {"gist": "Issue closed", "concepts": ["task completion"], "tone": "neutral"},
    "builds now": {"gist": "Build fixed", "concepts": ["build", "fix"], "tone": "positive"},
    "compiles": {"gist": "Build passing", "concepts": ["build"], "tone": "positive"},
    "no more errors": {"gist": "Errors cleared", "concepts": ["debugging", "fix"], "tone": "positive"},
    "green": {"gist": "CI passing", "concepts": ["ci", "testing"], "tone": "positive"},
    "all clear": {"gist": "All checks passed", "concepts": ["testing"], "tone": "positive"},
    # Acknowledgment
    "checked": {"gist": "Item checked", "concepts": ["review"], "tone": "neutral"},
    "verified": {"gist": "Verification done", "concepts": ["testing"], "tone": "neutral"},
    "confirmed": {"gist": "Confirmed working", "concepts": ["testing"], "tone": "positive"},
    "acknowledged": {"gist": "Status noted", "concepts": ["status update"], "tone": "neutral"},
    "noted": {"gist": "Information noted", "concepts": ["status update"], "tone": "neutral"},
    # Git operations
    "synced with main": {"gist": "Branch synced", "concepts": ["git"], "tone": "neutral"},
    "rebased": {"gist": "Branch rebased", "concepts": ["git"], "tone": "neutral"},
    "cherry-picked": {"gist": "Commit cherry-picked", "concepts": ["git"], "tone": "neutral"},
    "reverted": {"gist": "Change reverted", "concepts": ["git", "rollback"], "tone": "neutral"},
    # Quick fixes
    "nvm found it": {"gist": "Root cause found", "concepts": ["debugging"], "tone": "positive"},
    "figured it out": {"gist": "Solution found", "concepts": ["debugging"], "tone": "positive"},
    "never mind": {"gist": "Issue dismissed", "concepts": ["status update"], "tone": "neutral"},
    "false alarm": {"gist": "False alarm", "concepts": ["debugging"], "tone": "neutral"},
    "my bad": {"gist": "Self-correction", "concepts": ["error"], "tone": "neutral"},
    # Operations
    "restarted the service": {"gist": "Service restarted", "concepts": ["deployment", "daemon"], "tone": "neutral"},
    "rolled back": {"gist": "Rollback completed", "concepts": ["deployment", "rollback"], "tone": "frustrated"},
    "pushed the fix": {"gist": "Fix pushed", "concepts": ["git", "fix"], "tone": "positive"},
    "tagged the release": {"gist": "Release tagged", "concepts": ["release", "git"], "tone": "positive"},
    "updated the config": {"gist": "Config updated", "concepts": ["configuration"], "tone": "neutral"},
    "ran the migration": {"gist": "Migration executed", "concepts": ["database", "migration"], "tone": "neutral"},
    "cleared the cache": {"gist": "Cache cleared", "concepts": ["performance"], "tone": "neutral"},
    # Negative
    "still broken": {"gist": "Issue persists", "concepts": ["debugging"], "tone": "frustrated"},
    "no change": {"gist": "No improvement", "concepts": ["debugging"], "tone": "frustrated"},
    "tried that already": {"gist": "Approach exhausted", "concepts": ["debugging"], "tone": "frustrated"},
    "can't reproduce": {"gist": "Cannot reproduce", "concepts": ["debugging", "testing"], "tone": "frustrated"},
    "works on my machine": {"gist": "Environment-specific", "concepts": ["debugging", "environment"], "tone": "frustrated"},
    "same as before": {"gist": "No progress", "concepts": ["debugging"], "tone": "frustrated"},
    # In progress
    "investigating": {"gist": "Investigation started", "concepts": ["debugging"], "tone": "analytical"},
    "looking into it": {"gist": "Investigation started", "concepts": ["debugging"], "tone": "analytical"},
    "on it": {"gist": "Task accepted", "concepts": ["task completion"], "tone": "neutral"},
    "in progress": {"gist": "Work in progress", "concepts": ["task completion"], "tone": "neutral"},
    "will look at it later": {"gist": "Task deferred", "concepts": ["planning"], "tone": "neutral"},
    "need more info": {"gist": "Blocked on info", "concepts": ["debugging"], "tone": "neutral"},
    # Slightly longer
    "the thing is fixed": {"gist": "Issue fixed", "concepts": ["fix"], "tone": "positive"},
    "got it working again": {"gist": "Service restored", "concepts": ["fix", "debugging"], "tone": "positive"},
    "yeah that did it": {"gist": "Fix confirmed", "concepts": ["fix", "debugging"], "tone": "positive"},
}

# Default for variations not in the mapping
SPARSE_DEFAULT = {"gist": "Status update", "concepts": ["status update"], "tone": "neutral"}


def generate_sparse_example(raw: str) -> dict:
    """Template-generate a minimal encoding for a sparse input."""
    # Look up mapping, fall back to default
    mapping = SPARSE_MAPPING.get(raw, None)
    if mapping is None:
        # Try base form (before " — suffix")
        base = raw.split(" — ")[0].strip() if " — " in raw else raw
        mapping = SPARSE_MAPPING.get(base, SPARSE_DEFAULT)

    tone = mapping["tone"]
    concepts = mapping["concepts"]
    gist = mapping["gist"]

    if tone in ("positive", "excited"):
        significance = "routine"
    elif tone == "frustrated":
        significance = "routine"
    else:
        significance = "trivial"

    return {
        "raw_input": raw,
        "encoded": {
            "gist": gist,
            "summary": f"Brief update: {raw}",
            "content": raw,
            "narrative": "A brief status update was recorded.",
            "concepts": concepts,
            "structured_concepts": {
                "topics": [{"label": c, "path": f"workflow/{c}"} for c in concepts[:2]],
                "entities": [],
                "actions": [],
                "causality": [],
            },
            "significance": significance,
            "emotional_tone": tone,
            "outcome": raw,
            "salience": round(random.uniform(0.05, 0.15), 2),
        },
        "source": "targeted_sparse",
        "task_type": "encoding",
        "category": "sparse_input",
    }


# ---- Category D: Domain Terms ----

DOMAIN_TERM_DOMAINS = [
    "diagnosing a race condition (NOT a deadlock, NOT a channel leak) in a Go HTTP server's connection handler",
    "configuring FTS5 (NOT full-text search, the specific SQLite extension) tokenizer with porter stemming",
    "debugging a goroutine leak (NOT a thread leak, NOT a memory leak) in a gRPC streaming server",
    "setting up WAL mode (NOT rollback journal, NOT WAL2) in SQLite for concurrent read access",
    "investigating a segfault (NOT a panic, NOT a crash) in a CGo FFI bridge to a C library",
    "tuning the B+ tree (NOT B-tree, NOT hash index) page size for an on-disk key-value store",
    "implementing spread activation (NOT BFS, NOT graph traversal) for memory association retrieval",
    "configuring ROCm (NOT CUDA, NOT OpenCL) kernel compilation for AMD GPU training on the RX 7800 XT",
    "setting up launchd (NOT systemd, NOT cron) plist for macOS daemon auto-start at boot",
    "diagnosing a livelock (NOT a deadlock, NOT a race condition) in a lock-free concurrent queue",
    "implementing cosine annealing (NOT step decay, NOT linear warmup) learning rate schedule for spoke training",
    "configuring fsnotify (NOT inotify, NOT kqueue) for cross-platform filesystem watching in Go",
    "debugging a nil pointer dereference (NOT null reference, NOT segfault) in a Go interface assertion",
    "setting up LoRA (NOT full fine-tuning, NOT QLoRA) rank-64 adapters on attention Q/V projections",
    "implementing exponential backoff (NOT linear retry, NOT fixed delay) with jitter for API rate limiting",
]

DOMAIN_TERM_GEN_PROMPT = (
    "Generate a realistic developer observation about {domain}. "
    "The observation MUST:\n"
    "1. Use the EXACT technical term specified (in parentheses above) at least twice\n"
    "2. Include why this specific term/approach matters vs alternatives\n"
    "3. Include at least one concrete metric or configuration value\n"
    "3-6 sentences. Output ONLY the observation text, no markdown."
)


# ---- Category E: Numerical Precision ----

NUMERICAL_DOMAINS = [
    "benchmark results comparing B+ tree index (2.3ms lookup, 156MB disk), hash index (0.8ms lookup, 203MB disk), and covering index (1.1ms lookup, 312MB disk) on a 10M row table",
    "training run metrics: learning rate 3e-4, eval loss 0.6435, training steps 5600, batch size 8, gradient accumulation 4, weight decay 0.01",
    "production latency measurements: p50=2.1ms, p90=8.4ms, p95=15.2ms, p99=47.3ms, p999=203ms over a 24-hour window of 1.2M requests",
    "memory profiling results: RSS 2.4GB, heap 1.8GB, stack 12MB, mmap 640MB, with GC pause at 99th percentile of 4.2ms",
    "A/B test results: variant A conversion 3.42%, variant B conversion 4.17%, lift +21.9%, p-value 0.0023, sample size 45,000 per arm",
    "disk I/O metrics: sequential read 2.1GB/s, random read 95K IOPS, write latency p99=0.8ms, queue depth 32, on NVMe PCIe Gen4",
    "model evaluation: accuracy 94.2%, precision 91.8%, recall 96.1%, F1 93.9%, inference latency 23ms, model size 847MB",
    "GPU utilization during training: compute 87%, memory 14.3GB/16GB (89.4%), temperature 72°C, power draw 198W, throughput 1,240 tokens/sec",
    "cost analysis: $0.0004/request at 2000 RPM, monthly estimate $34,560 for Gemini Flash vs $0.012/request for GPT-4, saving 96.7%",
    "network benchmark: TCP throughput 9.41 Gbps, UDP 9.82 Gbps, RTT min/avg/max 0.12/0.34/1.23ms, packet loss 0.002%, MTU 9000",
    "database performance: 12,400 queries/sec, connection pool 25/100, avg query time 3.2ms, slow query threshold 100ms, 47 slow queries in last hour",
    "CI pipeline timing: build 2m34s, unit tests 1m12s, integration tests 4m47s, linting 0m23s, total 8m56s, cache hit rate 78%",
    "embedding pipeline: 47,500 training pairs, 2,500 eval pairs, batch size 32, 500ms delay between batches, total time 45m, throughput 1,111 pairs/min",
    "resource allocation: 4 vCPUs, 16GB RAM, 100GB SSD, network 5Gbps, cost $0.48/hour, monthly $345.60",
    "hyperparameter sweep results: LR 6e-4 loss=4.847, LR 1e-3 loss=4.557, LR 2e-3 loss=4.250, LR 3.5e-3 loss=4.108, best PPL=60.8",
]

NUMERICAL_GEN_PROMPT = (
    "Generate a realistic developer observation about {domain}. "
    "The observation MUST:\n"
    "1. Include ALL of the specific numbers mentioned above — do not round, summarize, or change them\n"
    "2. Explain what the numbers mean and what decisions they inform\n"
    "3. Reference specific tools or systems that produced these measurements\n"
    "3-6 sentences. Output ONLY the observation text, no markdown."
)


# ---- Generation Pipeline ----

async def generate_raw_input(session, semaphore, gen_system, gen_prompt, source, category):
    """Generate a raw input only (encoding done separately via Batch API)."""
    raw = await call_gemini(session, gen_system, gen_prompt, semaphore)
    if raw is None or len(raw.strip()) < 30:
        return None

    raw = raw.strip()
    # Remove markdown fences if present
    if raw.startswith("```"):
        lines = raw.split("\n")
        raw = "\n".join(l for l in lines if not l.strip().startswith("```")).strip()

    return {
        "raw_input": raw,
        "source": f"targeted_{source}",
        "task_type": "encoding",
        "category": category,
    }


async def generate_category(category: str, count: int, dry_run: bool = False):
    """Generate examples for a single category."""
    if category == "sparse_input":
        return generate_sparse_category(count)

    if category == "stack_trace":
        domains = STACK_TRACE_DOMAINS
        prompt_template = STACK_TRACE_GEN_PROMPT
        gen_system = "You generate realistic developer observations about debugging errors and stack traces. Be extremely specific with file names, line numbers, and error messages."
        source = "stack_trace"
    elif category == "named_entity":
        domains = NAMED_ENTITY_DOMAINS
        prompt_template = NAMED_ENTITY_GEN_PROMPT
        gen_system = "You generate realistic developer observations about team collaboration. Always include the specific names of people involved."
        source = "named_entity"
    elif category == "domain_terms":
        domains = DOMAIN_TERM_DOMAINS
        prompt_template = DOMAIN_TERM_GEN_PROMPT
        gen_system = "You generate realistic developer observations using precise technical terminology. Never substitute synonyms for the specific terms requested."
        source = "domain_terms"
    elif category == "numerical":
        domains = NUMERICAL_DOMAINS
        prompt_template = NUMERICAL_GEN_PROMPT
        gen_system = "You generate realistic developer observations with exact numerical data. Preserve ALL numbers exactly as given — do not round, truncate, or summarize."
        source = "numerical"
    else:
        raise ValueError(f"Unknown category: {category}")

    # Build prompts
    prompts = []
    for _ in range(count):
        domain = random.choice(domains)

        # For named_entity, substitute random names
        if category == "named_entity":
            names = random.sample(FIRST_NAMES, min(3, domain.count("{name")))
            for i, name in enumerate(names, 1):
                domain = domain.replace(f"{{name{i}}}", name)
            # Fill any remaining {nameN} placeholders
            domain = re.sub(r"\{name\d\}", lambda _: random.choice(FIRST_NAMES), domain)

        prompt = prompt_template.format(domain=domain)
        prompts.append(prompt)

    if dry_run:
        print(f"\n=== DRY RUN: {category} ({count} examples) ===")
        for p in prompts[:3]:
            print(f"\nPrompt: {p[:200]}...")
        return []

    print(f"\nGenerating {count} {category} examples ({MAX_CONCURRENT} concurrent)...")

    # Write incrementally — append each result as it arrives
    cat_path = OUTPUT_DIR / f"{category}.jsonl"
    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)

    # Resume: count existing results
    existing = 0
    if cat_path.exists():
        existing = sum(1 for _ in open(cat_path))
        if existing > 0:
            print(f"  Resuming: {existing} already generated, need {count - existing} more")
            prompts = prompts[existing:]
            if not prompts:
                print(f"  Already complete!")
                return [json.loads(l) for l in open(cat_path)]

    semaphore = asyncio.Semaphore(MAX_CONCURRENT)
    success_count = existing
    import aiohttp  # lazy import — only available in felixlm venv
    async with aiohttp.ClientSession() as session:
        tasks = [
            generate_raw_input(session, semaphore, gen_system, p, source, category)
            for p in prompts
        ]
        done = 0
        with open(cat_path, "a") as f:
            for coro in asyncio.as_completed(tasks):
                result = await coro
                done += 1
                if result:
                    f.write(json.dumps(result) + "\n")
                    f.flush()
                    success_count += 1
                if done % 25 == 0 or done == len(tasks):
                    print(f"  [{done + existing}/{count}] success={success_count}")

    # Read back all results
    return [json.loads(l) for l in open(cat_path)]


def generate_sparse_category(count: int) -> list[dict]:
    """Generate sparse input examples via templates (no API calls)."""
    print(f"\nGenerating {count} sparse input examples (template, no API)...")
    results = []
    for _ in range(count):
        raw = random.choice(SPARSE_INPUTS)
        results.append(generate_sparse_example(raw))
    # Deduplicate by raw_input to ensure variety
    seen = set()
    unique = []
    for r in results:
        key = r["raw_input"]
        if key not in seen:
            seen.add(key)
            unique.append(r)
    # If we don't have enough unique, extend with variations
    while len(unique) < count:
        raw = random.choice(SPARSE_INPUTS)
        variation = f"{raw} — {random.choice(['just now', 'finally', 'as expected', 'after retry'])}"
        if variation not in seen:
            seen.add(variation)
            unique.append(generate_sparse_example(variation))
    print(f"  Generated {len(unique)} unique sparse examples")
    return unique[:count]


async def main_async(args):
    categories = {
        "stack_trace": 400,
        "named_entity": 250,
        "sparse_input": 400,
        "domain_terms": 200,
        "numerical": 250,
    }

    if args.category != "all":
        categories = {args.category: args.count or categories.get(args.category, 100)}

    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
    all_results = []

    for cat, count in categories.items():
        results = await generate_category(cat, count, dry_run=args.dry_run)
        if results:
            # Write category file
            cat_path = OUTPUT_DIR / f"{cat}.jsonl"
            with open(cat_path, "w") as f:
                for r in results:
                    f.write(json.dumps(r) + "\n")
            print(f"  Written {len(results)} to {cat_path}")
            all_results.extend(results)

    if all_results and not args.dry_run:
        # Separate sparse (already encoded) from raw (need batch encoding)
        sparse = [r for r in all_results if r.get("category") == "sparse_input"]
        raw_only = [r for r in all_results if r.get("category") != "sparse_input"]

        # Write combined raw inputs (for batch_encode.py)
        if raw_only:
            raw_path = OUTPUT_DIR / "raw_inputs.jsonl"
            with open(raw_path, "w") as f:
                for r in raw_only:
                    f.write(json.dumps(r) + "\n")
            print(f"\nRaw inputs (need encoding): {len(raw_only)} -> {raw_path}")

        # Write sparse (already complete)
        if sparse:
            sparse_path = OUTPUT_DIR / "sparse_input.jsonl"
            with open(sparse_path, "w") as f:
                for r in sparse:
                    f.write(json.dumps(r) + "\n")
            print(f"Sparse (complete): {len(sparse)} -> {sparse_path}")

        # Print category breakdown
        from collections import Counter
        cats = Counter(r["category"] for r in all_results)
        total = len(all_results)
        print(f"\nTotal raw inputs generated: {total}")
        print("Category breakdown:")
        for cat, count in cats.most_common():
            print(f"  {cat}: {count}")

        if raw_only:
            print(f"\nNext step: encode raw inputs via Gemini Batch API:")
            print(f"  python batch_encode.py submit --input {raw_path}")


def main():
    parser = argparse.ArgumentParser(description="Generate targeted training data")
    parser.add_argument("--category", required=True,
                        choices=["all", "stack_trace", "named_entity", "sparse_input",
                                 "domain_terms", "numerical"],
                        help="Category to generate")
    parser.add_argument("--count", type=int, default=None,
                        help="Number of examples (default: category-specific)")
    parser.add_argument("--dry-run", action="store_true",
                        help="Show prompts without calling API")
    args = parser.parse_args()

    if args.category != "sparse_input" and not args.dry_run and not API_KEY:
        print("Error: LLM_API_KEY environment variable required for API-based generation")
        sys.exit(1)

    asyncio.run(main_async(args))


if __name__ == "__main__":
    main()
