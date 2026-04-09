#!/usr/bin/env python3
"""Generate v7 training inputs for Mnemonic encoding spokes.

Phase 1 of the v7 pipeline: creates raw inputs across 5 diversity categories.
Phase 2 (batch_encode.py) generates gold-standard encodings via Gemini Batch API.
Phase 3 (validate.py + eval_faithfulness.py) validates everything.

Categories:
  1. Production captures (600) — real daemon encoding requests
  2. Out-of-domain diverse (300) — non-tech topics via Gemini
  3. Adversarial twins (100 pairs = 200) — matched pairs via Gemini
  4. Minimal inputs (100) — 1-10 word inputs, script-generated
  5. Dense numbers (100) — metric-heavy inputs via Gemini

Usage:
    # Generate all categories
    export LLM_API_KEY=...
    python generate_v7_inputs.py --output-dir training/data/v7_inputs/

    # Generate only script-based categories (no API needed)
    python generate_v7_inputs.py --output-dir training/data/v7_inputs/ --no-api

    # Generate only one category
    python generate_v7_inputs.py --output-dir training/data/v7_inputs/ --category captures
"""

import argparse
import glob
import json
import os
import random
import sys
import time
from pathlib import Path

# ---------------------------------------------------------------------------
# Category 1: Production captures
# ---------------------------------------------------------------------------

def extract_captures(capture_dir: str, count: int = 600) -> list[dict]:
    """Extract encoding captures with valid raw inputs from daemon capture files.

    Filters for:
      - task_type == "encoding"
      - User message contains "CONTENT:" marker
      - Raw input is at least 20 chars (skip empty/trivial)
      - Dedup by first 100 chars of raw input
    """
    candidates = []
    seen_prefixes = set()

    for path in sorted(glob.glob(os.path.join(capture_dir, "capture_*.jsonl"))):
        with open(path, "rb") as f:
            for line in f:
                try:
                    d = json.loads(line)
                except (json.JSONDecodeError, UnicodeDecodeError):
                    continue

                if d.get("task_type") != "encoding":
                    continue

                msgs = d.get("request", {}).get("messages", [])
                user_msgs = [m for m in msgs if m.get("role") == "user"]
                if not user_msgs:
                    continue

                user_content = user_msgs[0].get("content", "")
                idx = user_content.find("CONTENT:")
                if idx < 0:
                    # Try alternate marker
                    idx = user_content.find("CONTENT:\n")
                if idx < 0:
                    continue

                raw_input = user_content[idx + len("CONTENT:"):].strip()
                if len(raw_input) < 20:
                    continue

                # Dedup
                prefix = raw_input[:100].lower()
                if prefix in seen_prefixes:
                    continue
                seen_prefixes.add(prefix)

                candidates.append({
                    "raw_input": raw_input,
                    "source": "mcp",
                    "type": d.get("caller", "general"),
                    "category": "production_capture",
                })

    print(f"  Found {len(candidates)} unique encoding captures")

    # Sample if we have more than needed
    if len(candidates) > count:
        random.shuffle(candidates)
        candidates = candidates[:count]

    return candidates


# ---------------------------------------------------------------------------
# Category 2: Out-of-domain diverse (requires API)
# ---------------------------------------------------------------------------

# 30 domains, 10 examples each = 300 total
OUT_OF_DOMAIN_SPECS = [
    ("cooking", "A detailed recipe with specific measurements, temperatures, timing, and technique tips"),
    ("legal", "A contract clause, license term, or legal notice with specific conditions and parties"),
    ("medical", "A clinical note, patient case, or medical procedure with vitals, diagnoses, and treatments"),
    ("sports", "A game recap with specific scores, player stats, play-by-play details"),
    ("music", "A music review, concert recap, or recording session notes with specific tracks, keys, tempos"),
    ("history", "A historical event description with dates, people, places, and consequences"),
    ("chemistry", "A lab report or chemical process with specific reagents, quantities, temperatures, yields"),
    ("astronomy", "An astronomical observation with coordinates, magnitudes, distances, spectral data"),
    ("agriculture", "A farming report with crop yields, soil conditions, weather data, planting schedules"),
    ("finance", "A financial report with specific numbers: revenue, margins, P/E ratios, market cap"),
    ("architecture", "A building inspection or design review with dimensions, materials, load calculations"),
    ("linguistics", "A language analysis with phonetic transcriptions, morphological breakdowns, syntax trees"),
    ("marine_biology", "A marine survey with species counts, water conditions, GPS coordinates, depth readings"),
    ("aviation", "A flight log or incident report with altitudes, headings, speeds, timestamps"),
    ("archaeology", "An excavation report with stratigraphy, artifact descriptions, radiocarbon dates"),
    ("nutrition", "A dietary analysis with macros, micros, caloric values, and meal timing"),
    ("geology", "A geological survey with rock types, mineral compositions, fault measurements"),
    ("psychology", "A therapy session note or study report with scales, scores, and behavioral observations"),
    ("veterinary", "A veterinary case with animal vitals, diagnoses, medications, dosages"),
    ("manufacturing", "A production report with unit counts, defect rates, cycle times, machine utilization"),
    ("photography", "A photo session log with camera settings: aperture, shutter speed, ISO, focal length"),
    ("meteorology", "A weather observation with temperature, pressure, humidity, wind speed, precipitation"),
    ("logistics", "A shipment tracking report with weights, dimensions, routes, delivery times, costs"),
    ("environmental", "An environmental impact report with pollutant levels, species counts, water quality data"),
    ("education", "A student assessment report with scores, percentiles, learning objectives, progress notes"),
    ("telecommunications", "A network performance report with bandwidth, latency, packet loss, signal strength"),
    ("real_estate", "A property listing or appraisal with square footage, lot size, assessed value, comparables"),
    ("automotive", "A vehicle inspection or repair log with mileage, part numbers, torque specs, fluid levels"),
    ("energy", "A power generation report with output in MW, efficiency percentages, fuel consumption rates"),
    ("forestry", "A timber survey with tree species, DBH measurements, stand density, volume estimates"),
]

GENERATE_INPUT_PROMPT = """Generate {count} realistic, detailed raw text observations for the domain: {domain}.

Each observation should be:
- A paragraph of 50-200 words
- Rich in specific details: exact numbers, proper nouns, technical terms, measurements
- Written as if someone is recording what they observed/learned/decided
- NOT formatted as JSON — just plain text, as if spoken or typed in a note

Domain description: {description}

Output exactly {count} observations, separated by "---" on its own line.
Do NOT number them or add headers. Just the raw text separated by ---.

IMPORTANT: Each observation must contain at least 3 specific, verifiable details
(numbers, names, dates, measurements) that a reader could fact-check against the text.
"""


def generate_out_of_domain(api_key: str, count_per_domain: int = 10) -> list[dict]:
    """Generate diverse out-of-domain inputs via Gemini API."""
    from google import genai

    client = genai.Client(api_key=api_key)
    results = []

    for domain, description in OUT_OF_DOMAIN_SPECS:
        prompt = GENERATE_INPUT_PROMPT.format(
            count=count_per_domain,
            domain=domain,
            description=description,
        )

        print(f"  Generating {count_per_domain} {domain} inputs...", end=" ", flush=True)
        try:
            response = client.models.generate_content(
                model="gemini-3.1-pro-preview",
                contents=prompt,
                config={
                    "temperature": 0.9,
                    "max_output_tokens": 8192,
                },
            )
            text = response.text
            observations = [o.strip() for o in text.split("---") if o.strip()]

            for obs in observations[:count_per_domain]:
                results.append({
                    "raw_input": obs,
                    "source": "mcp",
                    "type": "general",
                    "category": f"out_of_domain:{domain}",
                })
            print(f"got {len(observations[:count_per_domain])}")
        except Exception as e:
            print(f"ERROR: {e}")

        # Gentle rate limiting
        time.sleep(1)

    return results


# ---------------------------------------------------------------------------
# Category 3: Adversarial twins (requires API)
# ---------------------------------------------------------------------------

ADVERSARIAL_TOPICS = [
    ("PostgreSQL", "SQLite", "database choice for a project"),
    ("React", "Svelte", "frontend framework selection"),
    ("microservices", "monolith", "architecture migration direction"),
    ("Rust", "Go", "systems language choice"),
    ("REST", "GraphQL", "API design approach"),
    ("Kubernetes", "Docker Compose", "deployment orchestration"),
    ("TypeScript", "JavaScript", "type system adoption"),
    ("MongoDB", "DynamoDB", "NoSQL database selection"),
    ("AWS Lambda", "EC2 instances", "compute model"),
    ("Redis", "Memcached", "caching layer choice"),
    ("gRPC", "REST", "inter-service communication"),
    ("pytest", "unittest", "Python testing framework"),
    ("Terraform", "Pulumi", "infrastructure-as-code tool"),
    ("GitHub Actions", "GitLab CI", "CI/CD platform"),
    ("FastAPI", "Flask", "Python web framework"),
    ("Next.js", "Remix", "React meta-framework"),
    ("SQLAlchemy", "raw SQL", "database access pattern"),
    ("Docker", "Podman", "container runtime"),
    ("Nginx", "Caddy", "reverse proxy / web server"),
    ("Datadog", "Grafana+Prometheus", "observability stack"),
    ("JWT", "session cookies", "authentication mechanism"),
    ("WebSocket", "SSE", "real-time communication"),
    ("Tailwind", "CSS Modules", "styling approach"),
    ("pnpm", "yarn", "package manager"),
    ("Vim", "VS Code", "editor/IDE choice"),
    ("Linux", "macOS", "development OS"),
    ("Python 3.12", "Python 3.11", "runtime version"),
    ("async/await", "threading", "concurrency model"),
    ("DDD", "CRUD", "architectural pattern"),
    ("event sourcing", "state mutation", "data persistence pattern"),
    ("feature flags", "branch deploys", "release strategy"),
    ("pair programming", "async code review", "collaboration model"),
    ("monorepo", "polyrepo", "repository structure"),
    ("Postgres JSONB", "separate tables", "semi-structured data storage"),
    ("ECS Fargate", "EKS", "AWS container orchestration"),
    ("Prisma", "Drizzle", "TypeScript ORM"),
    ("Zod", "io-ts", "runtime validation library"),
    ("trunk-based", "gitflow", "branching strategy"),
    ("SQLite WAL", "SQLite rollback journal", "SQLite journal mode"),
    ("bfloat16", "float16", "training precision format"),
    ("LoRA", "full fine-tune", "model adaptation strategy"),
    ("Adam", "Muon", "optimizer choice"),
    ("gradient checkpointing", "full activation caching", "memory vs compute tradeoff"),
    ("quantized inference", "full precision inference", "inference optimization"),
    ("batch API", "streaming API", "API consumption pattern"),
    ("llama.cpp", "vLLM", "local inference engine"),
    ("cosine annealing", "linear decay", "learning rate schedule"),
    ("SentencePiece", "tiktoken", "tokenizer choice"),
    ("GGUF", "safetensors", "model weight format"),
    ("spoke adapters", "LoRA adapters", "parameter-efficient fine-tuning method"),
]

TWIN_PROMPT = """Generate a pair of developer decision notes. Both should describe choosing a technology, but with OPPOSITE choices.

Topic: {topic}
Choice A picks: {choice_a}
Choice B picks: {choice_b}

Requirements:
- Each note is 60-150 words
- Written as a first-person decision record ("Decided to use X because...")
- Must mention specific technical reasons for the choice
- Must include at least 2 concrete details (performance numbers, team size, timeline, etc.)
- The two notes must be structurally similar but semantically opposite
- A faithful encoding model should produce DIFFERENT gists, summaries, and concepts for each

Output format:
NOTE_A:
[the note choosing {choice_a}]

NOTE_B:
[the note choosing {choice_b}]
"""


def generate_adversarial_twins(api_key: str, count_pairs: int = 100) -> list[dict]:
    """Generate adversarial twin pairs via Gemini API."""
    from google import genai

    client = genai.Client(api_key=api_key)
    results = []

    # Use as many topics as needed, cycling if necessary
    topics = ADVERSARIAL_TOPICS * ((count_pairs // len(ADVERSARIAL_TOPICS)) + 1)
    topics = topics[:count_pairs]

    for i, (choice_a, choice_b, topic) in enumerate(topics):
        prompt = TWIN_PROMPT.format(
            topic=topic, choice_a=choice_a, choice_b=choice_b,
        )

        print(f"  Twin pair {i+1}/{count_pairs}: {choice_a} vs {choice_b}...", end=" ", flush=True)
        try:
            response = client.models.generate_content(
                model="gemini-3.1-pro-preview",
                contents=prompt,
                config={
                    "temperature": 0.8,
                    "max_output_tokens": 2048,
                },
            )
            text = response.text

            # Parse NOTE_A and NOTE_B
            note_a = note_b = None
            if "NOTE_A:" in text and "NOTE_B:" in text:
                parts = text.split("NOTE_B:")
                note_a = parts[0].replace("NOTE_A:", "").strip()
                note_b = parts[1].strip()
            elif "Note A:" in text and "Note B:" in text:
                parts = text.split("Note B:")
                note_a = parts[0].replace("Note A:", "").strip()
                note_b = parts[1].strip()

            if note_a and note_b and len(note_a) > 30 and len(note_b) > 30:
                pair_id = i + 1
                results.append({
                    "raw_input": note_a,
                    "source": "mcp",
                    "type": "decision",
                    "category": f"adversarial_twin:{choice_a.lower().replace(' ', '_')}_over_{choice_b.lower().replace(' ', '_')}",
                    "twin_pair_id": pair_id,
                    "twin_side": "A",
                })
                results.append({
                    "raw_input": note_b,
                    "source": "mcp",
                    "type": "decision",
                    "category": f"adversarial_twin:{choice_b.lower().replace(' ', '_')}_over_{choice_a.lower().replace(' ', '_')}",
                    "twin_pair_id": pair_id,
                    "twin_side": "B",
                })
                print("OK")
            else:
                print("PARSE FAIL")
        except Exception as e:
            print(f"ERROR: {e}")

        # Rate limiting
        if (i + 1) % 10 == 0:
            time.sleep(2)

    return results


# ---------------------------------------------------------------------------
# Category 4: Minimal inputs (no API needed)
# ---------------------------------------------------------------------------

MINIMAL_SEEDS = [
    # 1-word
    "SIGKILL", "ENOMEM", "segfault", "deadlock", "rollback", "LGTM",
    "hotfix", "OOM", "timeout", "deprecated", "refactored", "deployed",
    "reverted", "merged", "rebased", "released", "backported", "patched",
    # 2-3 words
    "WAL mode on.", "build passed", "tests green", "PR approved",
    "deploy failed", "config updated", "cache cleared", "schema migrated",
    "index rebuilt", "log rotated", "cert renewed", "DNS propagated",
    "service restarted", "memory leak", "race condition", "null pointer",
    "stack overflow", "type error", "import cycle", "missing dependency",
    # URLs and paths
    "https://github.com/appsprout-dev/mnemonic/pull/381",
    "https://wandb.ai/appsprout/mnemonic-lm/runs/icarq0vu",
    "/home/hubcaps/Projects/mem/internal/agent/encoding/agent.go:142",
    "internal/store/sqlite/queries.go",
    "config.yaml",
    # Short phrases
    "Go 1.24 released today",
    "ROCm 7.3 breaks PyTorch",
    "npm audit found 3 critical",
    "disk at 95%",
    "p99 latency 2.8s",
    "PR #382 needs review",
    "meeting at 3pm",
    "Jason pushed to main",
    "CI pipeline timeout",
    "Dependabot PR merged",
    # Emoji and symbols
    "LGTM",
    "404",
    "200 OK",
    "502 Bad Gateway",
    "git reset --hard HEAD~1",
    "SELECT * FROM memories WHERE salience > 0.8",
    "curl -s localhost:9999/api/v1/status",
    "docker compose up -d",
    "make build && make test",
    "go test ./... -v -count=1",
]


def generate_minimal_inputs(count: int = 100) -> list[dict]:
    """Generate minimal 1-10 word inputs. Script-based, no API."""
    results = []

    # Use all seeds, then generate variations
    seeds = list(MINIMAL_SEEDS)
    random.shuffle(seeds)

    for seed in seeds[:count]:
        results.append({
            "raw_input": seed,
            "source": "mcp",
            "type": "general",
            "category": "minimal",
        })

    # If we need more, generate simple variations
    verbs = ["fixed", "added", "removed", "updated", "deployed", "tested", "reviewed"]
    nouns = ["auth", "cache", "config", "schema", "endpoint", "migration", "index", "query"]
    while len(results) < count:
        phrase = f"{random.choice(verbs)} {random.choice(nouns)}"
        results.append({
            "raw_input": phrase,
            "source": "mcp",
            "type": "general",
            "category": "minimal",
        })

    return results[:count]


# ---------------------------------------------------------------------------
# Category 5: Dense numbers (requires API)
# ---------------------------------------------------------------------------

DENSE_NUMBER_TYPES = [
    "A server monitoring alert with CPU%, memory GB, disk I/O MB/s, network throughput, active connections, error rate, p50/p95/p99 latencies in ms, uptime hours, and request count",
    "A machine learning training log showing loss, perplexity, learning rate, tokens/sec, VRAM GB, batch size, gradient norm, epoch number, and step count",
    "A database performance report with query latency ms, rows scanned, cache hit ratio %, connection pool utilization, deadlock count, replication lag ms, and WAL size MB",
    "A CI/CD pipeline summary with build time seconds, test count, pass/fail/skip counts, code coverage %, artifact size MB, deploy duration, and rollback count",
    "A financial quarterly report with revenue $M, EBITDA margin %, customer count, churn rate %, ARR, MRR, CAC, LTV, burn rate, and runway months",
    "A network diagnostic with ping latency ms, packet loss %, bandwidth Mbps, DNS resolution ms, TLS handshake ms, TCP connection count, and error codes",
    "A vehicle diagnostics readout with RPM, speed km/h, fuel level %, oil pressure PSI, coolant temp C, battery voltage, tire pressures PSI, and odometer km",
    "A weather station log with temperature C, humidity %, barometric pressure hPa, wind speed km/h, wind direction degrees, rainfall mm, UV index, and visibility km",
    "A manufacturing quality report with units produced, defect rate %, cycle time seconds, machine uptime %, scrap weight kg, energy consumption kWh, and OEE %",
    "A sports box score with points, rebounds, assists, steals, blocks, turnovers, FG%, 3P%, FT%, minutes played, and plus/minus for multiple players",
]

DENSE_NUMBER_PROMPT = """Generate {count} realistic observations that are DENSE with specific numbers and metrics.

Type: {description}

Requirements:
- Each observation is 80-200 words
- Written as plain text, as if someone is recording what they see on a dashboard or report
- MUST contain at least 10 specific numeric values with units
- Numbers should be realistic and internally consistent
- Include timestamps, version numbers, or dates where appropriate
- Do NOT use JSON or structured format — write as natural text/notes

Output exactly {count} observations separated by "---" on its own line.
"""


def generate_dense_numbers(api_key: str, count: int = 100) -> list[dict]:
    """Generate number-dense inputs via Gemini API."""
    from google import genai

    client = genai.Client(api_key=api_key)
    results = []
    per_type = max(count // len(DENSE_NUMBER_TYPES), 1)

    for num_type in DENSE_NUMBER_TYPES:
        prompt = DENSE_NUMBER_PROMPT.format(count=per_type, description=num_type)

        short_name = num_type.split(" with ")[0].strip()[:40]
        print(f"  Generating {per_type} dense-number ({short_name})...", end=" ", flush=True)
        try:
            response = client.models.generate_content(
                model="gemini-3.1-pro-preview",
                contents=prompt,
                config={
                    "temperature": 0.8,
                    "max_output_tokens": 8192,
                },
            )
            text = response.text
            observations = [o.strip() for o in text.split("---") if o.strip()]

            for obs in observations[:per_type]:
                results.append({
                    "raw_input": obs,
                    "source": "mcp",
                    "type": "insight",
                    "category": f"dense_numbers:{short_name.lower().replace(' ', '_')}",
                })
            print(f"got {len(observations[:per_type])}")
        except Exception as e:
            print(f"ERROR: {e}")

        time.sleep(1)

    return results


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(description="Generate v7 training inputs")
    parser.add_argument("--output-dir", required=True, help="Output directory for raw inputs")
    parser.add_argument("--no-api", action="store_true", help="Skip categories that need Gemini API")
    parser.add_argument("--category", choices=["captures", "out_of_domain", "adversarial", "minimal", "dense_numbers"],
                        help="Generate only one category")
    parser.add_argument("--capture-dir", default=os.path.expanduser("~/.mnemonic/training-data"),
                        help="Directory with daemon capture files")
    parser.add_argument("--seed", type=int, default=42, help="Random seed")
    args = parser.parse_args()

    random.seed(args.seed)
    os.makedirs(args.output_dir, exist_ok=True)

    api_key = os.environ.get("LLM_API_KEY", "")
    if not api_key and not args.no_api:
        print("WARNING: LLM_API_KEY not set. Use --no-api to skip API categories.")
        print("  export LLM_API_KEY=<your-gemini-api-key>")
        sys.exit(1)

    all_inputs = []
    categories_run = []

    # Category 1: Production captures
    if args.category in (None, "captures"):
        print("\n=== Category 1: Production Captures ===")
        captures = extract_captures(args.capture_dir, count=600)
        all_inputs.extend(captures)
        categories_run.append(("captures", len(captures)))

        # Write separately for inspection
        out = os.path.join(args.output_dir, "captures.jsonl")
        with open(out, "w") as f:
            for item in captures:
                f.write(json.dumps(item, ensure_ascii=False) + "\n")
        print(f"  Wrote {len(captures)} to {out}")

    # Category 2: Out-of-domain diverse
    if args.category in (None, "out_of_domain") and not args.no_api:
        print("\n=== Category 2: Out-of-Domain Diverse ===")
        ood = generate_out_of_domain(api_key, count_per_domain=10)
        all_inputs.extend(ood)
        categories_run.append(("out_of_domain", len(ood)))

        out = os.path.join(args.output_dir, "out_of_domain.jsonl")
        with open(out, "w") as f:
            for item in ood:
                f.write(json.dumps(item, ensure_ascii=False) + "\n")
        print(f"  Wrote {len(ood)} to {out}")

    # Category 3: Adversarial twins
    if args.category in (None, "adversarial") and not args.no_api:
        print("\n=== Category 3: Adversarial Twins ===")
        twins = generate_adversarial_twins(api_key, count_pairs=50)
        all_inputs.extend(twins)
        categories_run.append(("adversarial", len(twins)))

        out = os.path.join(args.output_dir, "adversarial_twins.jsonl")
        with open(out, "w") as f:
            for item in twins:
                f.write(json.dumps(item, ensure_ascii=False) + "\n")
        print(f"  Wrote {len(twins)} to {out}")

    # Category 4: Minimal inputs
    if args.category in (None, "minimal"):
        print("\n=== Category 4: Minimal Inputs ===")
        minimal = generate_minimal_inputs(count=100)
        all_inputs.extend(minimal)
        categories_run.append(("minimal", len(minimal)))

        out = os.path.join(args.output_dir, "minimal.jsonl")
        with open(out, "w") as f:
            for item in minimal:
                f.write(json.dumps(item, ensure_ascii=False) + "\n")
        print(f"  Wrote {len(minimal)} to {out}")

    # Category 5: Dense numbers
    if args.category in (None, "dense_numbers") and not args.no_api:
        print("\n=== Category 5: Dense Numbers ===")
        dense = generate_dense_numbers(api_key, count=100)
        all_inputs.extend(dense)
        categories_run.append(("dense_numbers", len(dense)))

        out = os.path.join(args.output_dir, "dense_numbers.jsonl")
        with open(out, "w") as f:
            for item in dense:
                f.write(json.dumps(item, ensure_ascii=False) + "\n")
        print(f"  Wrote {len(dense)} to {out}")

    # Write combined file
    combined_path = os.path.join(args.output_dir, "all_inputs.jsonl")
    with open(combined_path, "w") as f:
        for item in all_inputs:
            f.write(json.dumps(item, ensure_ascii=False) + "\n")

    # Summary
    print("\n" + "=" * 60)
    print("SUMMARY")
    print("=" * 60)
    for cat, count in categories_run:
        print(f"  {cat:<25s} {count:>5d}")
    print(f"  {'TOTAL':<25s} {len(all_inputs):>5d}")
    print(f"\nCombined file: {combined_path}")

    # Quality check: distribution
    cats = {}
    for item in all_inputs:
        cat = item["category"].split(":")[0]
        cats[cat] = cats.get(cat, 0) + 1
    print("\nCategory distribution:")
    for cat, count in sorted(cats.items(), key=lambda x: -x[1]):
        print(f"  {cat:<25s} {count:>5d}")


if __name__ == "__main__":
    main()
