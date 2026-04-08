#!/usr/bin/env python3
"""Procedural generator for mnemonic-specific training observations.

Generates thousands of varied, realistic observations by combining:
  - Real agent names, file paths, function names, struct names from the codebase
  - Realistic operations (start, error, success, config, metric, debug)
  - Randomized numbers (latencies, memory sizes, counts, versions)
  - Varied emotional tones and significance levels
  - Different lengths (short, medium, long)

Each observation is grounded in real mnemonic code paths.
Output is raw_input JSONL ready for batch encoding.

Usage:
    python procedural_generator.py --count 500 --output training/data/targeted/procedural_raw.jsonl
    # Then encode:
    LLM_API_KEY=... python batch_encode.py submit --input training/data/targeted/procedural_raw.jsonl
"""

import argparse
import json
import random
import sys
from pathlib import Path

# ---------------------------------------------------------------------------
# Real codebase components (extracted from the actual mnemonic repo)
# ---------------------------------------------------------------------------

AGENTS = [
    {"name": "PerceptionAgent", "file": "internal/agent/perception/agent.go", "funcs": ["processEvent", "callLLMGate", "contentHash", "isRecentGitOp", "promoteExclusion", "Start"], "struct": "PerceptionAgent"},
    {"name": "EncodingAgent", "file": "internal/agent/encoding/agent.go", "funcs": ["encodeRawMemory", "callCompressionLLM", "extractConcepts", "generateEmbedding", "deduplicateSimilar", "Start"], "struct": "EncodingAgent"},
    {"name": "RetrievalAgent", "file": "internal/agent/retrieval/agent.go", "funcs": ["Query", "spreadActivation", "rankResults", "synthesizeResults", "diversifyResults"], "struct": "RetrievalAgent"},
    {"name": "ConsolidationAgent", "file": "internal/agent/consolidation/agent.go", "funcs": ["runCycle", "decayMemories", "mergeMemories", "extractPatterns", "pruneAssociations"], "struct": "ConsolidationAgent"},
    {"name": "DreamingAgent", "file": "internal/agent/dreaming/agent.go", "funcs": ["runCycle", "replayMemories", "strengthenAssociations", "generateInsights"], "struct": "DreamingAgent"},
    {"name": "EpisodingAgent", "file": "internal/agent/episoding/agent.go", "funcs": ["runCycle", "clusterMemoriesIntoEpisodes", "synthesizeEpisodeTitle"], "struct": "EpisodingAgent"},
    {"name": "AbstractionAgent", "file": "internal/agent/abstraction/agent.go", "funcs": ["runCycle", "evaluatePattern", "deriveAxiom"], "struct": "AbstractionAgent"},
    {"name": "MetacognitionAgent", "file": "internal/agent/metacognition/agent.go", "funcs": ["runCycle", "analyzeMemoryCohesion", "detectAnomalies"], "struct": "MetacognitionAgent"},
    {"name": "Orchestrator", "file": "internal/agent/orchestrator/orchestrator.go", "funcs": ["Start", "checkLLMHealth", "checkStoreHealth", "runSelfTest", "writeHealthReport"], "struct": "Orchestrator"},
    {"name": "Reactor", "file": "internal/agent/reactor/engine.go", "funcs": ["handleEvent", "RegisterChain", "Start"], "struct": "Engine"},
]

STORE_FILES = [
    {"file": "internal/store/sqlite/sqlite.go", "funcs": ["NewSQLiteStore", "InitSchema", "loadEmbeddingIndex"]},
    {"file": "internal/store/sqlite/memories.go", "funcs": ["WriteMemory", "UpdateMemory", "GetMemory", "SearchByEmbedding", "SearchFTS"]},
    {"file": "internal/store/sqlite/associations.go", "funcs": ["WriteAssociation", "GetAssociations", "PruneWeakAssociations"]},
    {"file": "internal/store/sqlite/patterns.go", "funcs": ["WritePattern", "GetPatterns", "ArchivePattern"]},
    {"file": "internal/store/sqlite/episodes.go", "funcs": ["WriteEpisode", "GetEpisode", "ClusterMemories"]},
    {"file": "internal/store/sqlite/feedback_scores.go", "funcs": ["WriteFeedback", "GetFeedbackScores"]},
    {"file": "internal/store/sqlite/embindex.go", "funcs": ["loadEmbeddingIndex", "SearchByEmbedding"]},
]

MCP_TOOLS = [
    "remember", "recall", "batch_recall", "feedback", "amend", "check_memory",
    "forget", "create_handoff", "get_context", "get_patterns", "get_insights",
    "recall_project", "recall_session", "recall_timeline", "list_sessions",
    "session_summary", "ingest_project", "exclude_path", "list_exclusions",
    "dismiss_pattern", "dismiss_abstraction", "audit_encodings", "coach_local_llm", "status",
]

EVENT_TYPES = [
    "RawMemoryCreated", "MemoryEncoded", "MemoryAccessed", "MemoryAmended",
    "ConsolidationStarted", "ConsolidationCompleted", "QueryExecuted",
    "DreamCycleCompleted", "MetaCycleCompleted", "SystemHealth",
    "WatcherEvent", "EpisodeClosed", "PatternDiscovered", "AbstractionCreated",
]

WATCHER_SOURCES = ["filesystem", "terminal", "clipboard", "git"]
WATCHER_FILES = {
    "filesystem": ["internal/watcher/filesystem/watcher_other.go", "internal/watcher/filesystem/watcher_darwin.go"],
    "terminal": ["internal/watcher/terminal/watcher.go"],
    "clipboard": ["internal/watcher/clipboard/watcher.go"],
    "git": ["internal/watcher/git/watcher.go"],
}

CONFIG_FIELDS = [
    "llm.endpoint", "llm.chat_model", "llm.embedding_model", "llm.max_tokens", "llm.temperature",
    "store.db_path", "store.journal_mode", "store.busy_timeout_ms",
    "consolidation.interval", "consolidation.decay_rate", "consolidation.archive_threshold",
    "retrieval.max_hops", "retrieval.activation_threshold", "retrieval.diversity_lambda",
    "dreaming.schedule", "perception.llm_gating_enabled", "encoding.max_concurrent_encodings",
    "orchestrator.self_test_interval", "reactor.chains_file",
]

CONCEPTS = [
    "go", "python", "sqlite", "fts5", "embedding", "encoding", "retrieval", "consolidation",
    "dreaming", "episoding", "abstraction", "metacognition", "mcp", "daemon", "watcher",
    "debugging", "performance", "testing", "configuration", "migration", "deployment",
    "security", "api", "database", "agent", "llm", "spoke", "training", "felix-lm",
]

PEOPLE = ["Caleb", "Jason"]

ERROR_TYPES = [
    "nil pointer dereference", "index out of range", "context deadline exceeded",
    "database is locked", "connection refused", "invalid JSON", "CUDA out of memory",
    "permission denied", "file not found", "timeout", "panic recovery",
    "FTS5 tokenizer mismatch", "embedding dimension mismatch", "WAL checkpoint stall",
]

# ---------------------------------------------------------------------------
# Template generators
# ---------------------------------------------------------------------------

def rand_line():
    return random.randint(45, 450)

def rand_latency():
    return random.choice(["0.3ms", "1.2ms", "4.5ms", "8.7ms", "19.7ms", "23ms", "45ms", "120ms", "340ms", "890ms", "1.2s", "2.3s", "4.8s", "12.4s", "19.7s", "33.9s", "45s"])

def rand_memory_count():
    return random.choice([47, 234, 847, 1234, 3500, 8000, 10234, 12847, 15000])

def rand_salience():
    return round(random.uniform(0.05, 0.95), 2)

def rand_mem_size():
    return random.choice(["19MB", "45MB", "128MB", "234MB", "340MB", "487MB", "890MB", "1.2GB", "2.4GB"])

def rand_duration():
    return random.choice(["200ms", "500ms", "1.2s", "2.3s", "4.8s", "8.5s", "12.4s", "19.7s", "45s", "90s", "3 minutes", "15 minutes", "45 minutes", "2 hours"])

def rand_percentage():
    return random.choice(["0.1%", "0.3%", "2.5%", "5.7%", "8.4%", "15%", "23%", "47%", "68%", "81%", "95%", "99.9%", "100%"])

def rand_cosine():
    return round(random.uniform(0.3, 0.98), 2)

def rand_uuid():
    return f"{random.randbytes(4).hex()}-{random.randbytes(2).hex()}-{random.randbytes(2).hex()}-{random.randbytes(2).hex()}-{random.randbytes(6).hex()}"


def gen_agent_operation():
    """Generate an observation about an agent performing an operation."""
    agent = random.choice(AGENTS)
    func = random.choice(agent["funcs"])
    line = rand_line()
    templates = [
        f"{agent['name']}.{func}() completed in {rand_latency()}. Processed {rand_memory_count()} memories. No errors.",
        f"{agent['name']}.{func}() at {agent['file']}:{line} — processed {rand_memory_count()} items in {rand_duration()}. Peak memory: {rand_mem_size()}.",
        f"The {agent['name'].replace('Agent', '').lower()} agent's {func}() cycle took {rand_duration()}. {random.choice(['Normal runtime.', 'Slightly slower than usual.', 'Faster than expected.', 'Within acceptable bounds.'])}",
        f"{agent['struct']}.{func}() handled {random.randint(3, 50)} {random.choice(['memories', 'events', 'patterns', 'associations', 'episodes'])} in this cycle. Results look {random.choice(['clean', 'normal', 'good', 'as expected'])}.",
    ]
    return random.choice(templates)


def gen_agent_error():
    """Generate an observation about an agent encountering an error."""
    agent = random.choice(AGENTS)
    func = random.choice(agent["funcs"])
    line = rand_line()
    error = random.choice(ERROR_TYPES)
    templates = [
        f"Error in {agent['name']}.{func}() at {agent['file']}:{line}: {error}. The agent recovered via panic recovery and continued processing. {random.randint(1, 5)} events were skipped.",
        f"{agent['name']} hit '{error}' during {func}(). Root cause: {random.choice(['nil check missing', 'transaction timeout', 'concurrent access', 'malformed input', 'model response invalid'])}. Fix needed in {agent['file']}:{line}.",
        f"Bug: {agent['struct']}.{func}() panicked with {error} at {agent['file']}:{line}. Goroutine {random.randint(10, 200)} was running. The {random.choice(['event bus', 'store', 'LLM provider', 'embedding index'])} was in an inconsistent state. Added a {random.choice(['nil guard', 'mutex lock', 'context timeout', 'retry with backoff', 'deferred rollback'])} to fix.",
        f"{agent['name']}.{func}() failed after {rand_duration()}: {error}. Backoff triggered — will retry in {random.choice(['30s', '60s', '120s', '5 minutes'])}. {random.randint(1, 15)} items queued behind the failure.",
    ]
    return random.choice(templates)


def gen_store_operation():
    """Generate an observation about a store/database operation."""
    store = random.choice(STORE_FILES)
    func = random.choice(store["funcs"])
    line = rand_line()
    templates = [
        f"SQLiteStore.{func}() at {store['file']}:{line} completed in {rand_latency()} for {rand_memory_count()} rows. WAL file size: {random.choice(['2.3MB', '8.5MB', '45MB', '120MB', '890MB'])}.",
        f"Store query: {func}() returned {random.randint(0, 50)} results in {rand_latency()}. FTS5 index is {random.choice(['healthy', 'slightly fragmented', 'needs rebuild'])}. DB size: {rand_mem_size()}.",
        f"Schema migration {random.randint(10, 20)}->{random.randint(11, 21)}: added {random.choice(['version column to memories', 'index on associations.strength', 'FTS5 unicode61 tokenizer', 'episode_id foreign key'])} in {store['file']}:{line}. Migration took {rand_duration()} for {rand_memory_count()} rows.",
        f"SQLite busy_timeout hit in {func}() at {store['file']}:{line}. Write blocked for {random.choice(['5s', '8s', '12s'])} by {random.choice(['consolidation read lock', 'dreaming transaction', 'embedding index rebuild'])}. {random.choice(['Resolved after lock release.', 'Transaction retried successfully.', 'Need to reduce transaction scope.'])}",
    ]
    return random.choice(templates)


def gen_mcp_operation():
    """Generate an observation about an MCP tool call."""
    tool = random.choice(MCP_TOOLS)
    templates = {
        "remember": [
            f"MCP remember: stored {random.choice(['decision', 'error', 'insight', 'learning'])} about {random.choice(['SQLite WAL mode', 'spoke training config', 'encoding agent refactor', 'deployment pipeline', 'authentication middleware'])}. Salience: {rand_salience()}. Encoding queued — {random.randint(0, 10)} items ahead in queue.",
            f"MCP remember (type={random.choice(['decision', 'error', 'insight'])}): '{random.choice(['Chose JWT over sessions for API auth', 'Fixed nil pointer in spread activation', 'Gate values correlate with layer depth', 'SQLite WAL gives concurrent reads'])}'. Project: mnemonic. Encoded in {rand_duration()} via spoke model.",
        ],
        "recall": [
            f"MCP recall: query='{random.choice(['spread activation', 'SQLite FTS5', 'encoding quality', 'consolidation decay', 'spoke training'])}' returned {random.randint(1, 10)} results in {rand_latency()}. Top result salience: {rand_salience()}. Spread activation traversed {random.randint(1, 3)} hops.",
            f"MCP recall with synthesize=true: query='{random.choice(['authentication', 'performance optimization', 'training data quality'])}'. Found {random.randint(3, 8)} memories, synthesis took {rand_duration()}. {random.choice(['Helpful — used prior decision to inform current work.', 'Partial — some results were tangential.', 'Irrelevant — query was too broad.'])}",
        ],
        "feedback": [
            f"MCP feedback: rated recall query '{random.choice(['encoding latency', 'deployment config', 'training results'])}' as {random.choice(['helpful', 'partial', 'irrelevant'])}. Adjusted {random.randint(50, 500)} association strengths.",
        ],
        "batch_recall": [
            f"MCP batch_recall: session start with {random.randint(2, 4)} parallel queries. Results: {', '.join(f'{random.randint(1, 8)} memories' for _ in range(random.randint(2, 4)))}. Total: {rand_latency()}. Cross-linked memories found between {random.choice(['training and encoding', 'deployment and configuration', 'debugging and testing'])} categories.",
        ],
        "amend": [
            f"MCP amend: updated memory about '{random.choice(['SQLite schema version', 'LLM endpoint config', 'training data composition'])}'. Version bumped from {random.randint(1, 5)} to {random.randint(2, 6)}. Preserved {random.randint(2, 8)} associations.",
        ],
        "create_handoff": [
            f"MCP create_handoff: session summary with {random.randint(3, 10)} decisions, {random.randint(1, 5)} errors, {random.randint(1, 4)} insights. Salience: 0.95. {random.randint(500, 2000)} words. Encoding took {rand_duration()} via spoke model.",
        ],
        "get_patterns": [
            f"MCP get_patterns: returned {random.randint(2, 8)} active patterns with min_strength={random.choice(['0.5', '0.7', '0.8'])}. Top: '{random.choice(['test before commit', 'check rocm-smi before training', 'validate config after editing'])}' (strength {round(random.uniform(0.7, 0.98), 2)}).",
        ],
    }
    tool_templates = templates.get(tool, [f"MCP {tool}: completed successfully in {rand_latency()}."])
    return random.choice(tool_templates)


def gen_watcher_event():
    """Generate an observation about a watcher event."""
    source = random.choice(WATCHER_SOURCES)
    file = random.choice(WATCHER_FILES[source])
    templates = {
        "filesystem": [
            f"FilesystemWatcher detected {random.randint(1, 50)} file changes in {random.choice(['internal/agent/', 'internal/store/', 'training/scripts/', 'cmd/mnemonic/'])}. Perception heuristic filtered to {random.randint(1, 10)} meaningful events. Debounce window: {random.choice(['100ms', '200ms', '500ms'])}.",
            f"Watcher event: {random.choice(['file_created', 'file_modified', 'file_deleted'])} at {random.choice(['internal/agent/encoding/agent.go', 'config.yaml', 'training/scripts/train_qwen_spokes.py', 'internal/store/sqlite/memories.go'])}. Heuristic score: {round(random.uniform(0.05, 0.95), 2)}. {random.choice(['Encoded.', 'Filtered out (below threshold).', 'Passed to LLM gate.'])}",
        ],
        "terminal": [
            f"TerminalWatcher captured: '{random.choice(['make build', 'go test ./...', 'systemctl --user restart mnemonic', 'git diff', 'rocm-smi', 'python training/scripts/eval_qwen_encoding.py'])}'. {random.choice(['Created raw memory.', 'Filtered by command exclusion regex.', 'Heuristic score: ' + str(round(random.uniform(0.3, 0.9), 2)) + '.'])}",
        ],
        "clipboard": [
            f"ClipboardWatcher detected {random.choice(['JSON paste', 'code snippet', 'error message', 'URL', 'config block'])} ({random.choice(['200 bytes', '1.2KB', '5KB', '10KB'])}). Content hash unique — created raw memory. Perception score: {round(random.uniform(0.3, 0.8), 2)}.",
        ],
        "git": [
            f"GitWatcher detected HEAD change in {random.choice(['~/Projects/mem', '~/Projects/felixlm'])}. Suppressed {random.randint(10, 100)} filesystem events. Single repo_changed event created.",
        ],
    }
    return random.choice(templates[source])


def gen_config_change():
    """Generate an observation about a config change."""
    field = random.choice(CONFIG_FIELDS)
    templates = [
        f"Config change: {field} updated from {random.choice(['6h', '8h', '0.95', '0.7', '3', '200', 'true', 'http://localhost:1234/v1'])} to {random.choice(['12h', '4h', '0.97', '0.5', '5', '100', 'false', 'http://localhost:8899/v1'])}. Daemon restart required. Verified via curl localhost:9999/api/health.",
        f"Tuned {field} in config.yaml. {random.choice(['Testing impact on recall quality.', 'Reducing consolidation frequency.', 'Adjusting perception sensitivity.', 'Optimizing for throughput.', 'Reverting to previous value after regression.'])}",
    ]
    return random.choice(templates)


def gen_performance_metric():
    """Generate an observation about a performance measurement."""
    templates = [
        f"Encoding throughput: {random.choice(['2.5', '3.0', '3.5', '4.0'])} memories/minute. Queue depth: {random.randint(0, 30)}. Spoke server latency: {rand_latency()}. GPU utilization: {random.randint(60, 95)}%.",
        f"Recall latency: FTS5 {random.choice(['2ms', '4ms', '8ms'])}, embedding search {random.choice(['3ms', '4.2ms', '6ms'])}, spread activation {random.choice(['8ms', '15ms', '22ms'])}, total {rand_latency()}. {rand_memory_count()} memories in index.",
        f"Store stats: {rand_memory_count()} memories, {random.randint(1000, 10000)} associations, {random.randint(50, 300)} patterns, {random.randint(1, 20)} principles. DB size: {rand_mem_size()}. WAL: {random.choice(['1.2MB', '3.5MB', '12MB', '45MB'])}.",
        f"Consolidation cycle: scanned {rand_memory_count()} memories in {rand_duration()}. Decayed {random.randint(5, 50)}, merged {random.randint(0, 10)} pairs, pruned {random.randint(0, 20)} weak associations. Next cycle in {random.choice(['4h', '6h', '8h', '12h'])}.",
        f"Daemon uptime: {random.randint(1, 30)} days. RSS: {rand_mem_size()}. Embedding index: {random.choice(['12MB', '19MB', '28MB', '45MB'])}. No memory leaks detected.",
        f"Dreaming cycle at {random.choice(['2:00am', '2:15am', '2:30am', '3:00am'])}: replayed {random.randint(20, 80)} memories, strengthened {random.randint(5, 25)} associations, generated {random.randint(0, 5)} insights. Duration: {rand_duration()}.",
    ]
    return random.choice(templates)


def gen_training_observation():
    """Generate an observation about model training or evaluation."""
    templates = [
        f"Training step {random.randint(100, 30000)}: eval loss {round(random.uniform(0.5, 1.2), 4)}, train loss {round(random.uniform(0.4, 1.0), 4)}. Gate values: layer 0 = {round(random.uniform(0.08, 0.20), 2)}, layer 23 = {round(random.uniform(0.70, 0.92), 2)}. LR: {random.choice(['3e-4', '2e-4', '1e-4'])}.",
        f"Novel schema evaluation: {random.randint(8, 10)}/10 valid JSON, {random.randint(8, 10)}/10 full schema. {random.choice(['All fields correct.', 'One example missing structured_concepts.', 'Gist too long on example 7.'])} Checkpoint: {random.choice(['exp17', 'exp18', 'exp19', 'exp20'])}_best_spokes.pt.",
        f"Stress test: {random.randint(4, 7)}/7 pass. Failed on: {random.choice(['stack trace (dropped line numbers)', 'multi-topic (dropped person name)', 'websocket (synonym substitution)', 'numerical (rounded values)'])}. Better than {random.choice(['Gemini (1/7)', 'previous checkpoint (3/7)', 'base model without spokes (0/7)'])}.",
        f"Spoke adapter stats: {random.choice(['4', '6', '8'])} spokes, rank {random.choice(['32', '64', '128'])}, {random.choice(['24', '28', '35'])} layers. Trainable params: {random.choice(['12.6M', '18.9M', '25.2M', '27.5M'])} ({random.choice(['0.4%', '0.5%', '0.7%'])} of base). Best eval loss: {round(random.uniform(0.55, 0.80), 4)}.",
        f"Data pipeline: {random.choice(['validated', 'generated', 'merged', 'tokenized'])} {random.randint(100, 2000)} examples. {random.choice(['All passed Level 1 schema.', 'Rejected 12 with invalid enums.', 'Found 5 duplicate gists.', 'File:line preservation 100%.'])}",
    ]
    return random.choice(templates)


def gen_collaboration():
    """Generate an observation about team collaboration."""
    person = random.choice(PEOPLE)
    other = [p for p in PEOPLE if p != person][0] if len(PEOPLE) > 1 else person
    templates = [
        f"{person} reported a bug in {random.choice(AGENTS)['file']}:{rand_line()}: {random.choice(ERROR_TYPES)}. {other} is investigating. Priority: {random.choice(['P0 — production impact', 'P1 — affects encoding', 'P2 — non-blocking'])}.",
        f"PR #{random.randint(340, 400)} from {person}: {random.choice(['adds Windows Service support', 'fixes consolidation merge cascade', 'updates dashboard WebSocket reconnect', 'adds --checkpoint flag to stress test', 'refactors encoding agent error handling'])}. {other} reviewing. {random.choice(['LGTM, ready to merge.', 'One comment about error handling.', 'Needs test coverage for the new path.'])}",
        f"{person} and {other} pair-debugged {random.choice(['a memory corruption issue', 'the FTS5 tokenizer problem', 'a race condition in the event bus', 'the embedding index desync'])} in {random.choice(AGENTS)['file']}. Found the root cause in {rand_duration()}. {person} wrote the fix, {other} reviewed.",
        f"Code review: {person} suggested {random.choice(['lowering the pattern promotion threshold from 0.95 to 0.85', 'adding a nil guard before the embedding call', 'batching the concept extraction queries', 'using sync.RWMutex instead of sync.Mutex'])} in {random.choice(AGENTS)['file']}:{rand_line()}. {other} agreed and made the change.",
    ]
    return random.choice(templates)


def gen_decision():
    """Generate a decision observation."""
    templates = [
        f"Decision: {random.choice(['keeping', 'switching to', 'reverting to', 'evaluating'])} {random.choice(['in-memory embedding index', 'SQLite WAL mode', 'Muon optimizer', 'event bus architecture', 'progressive gate initialization', 'unicode61 FTS5 tokenizer'])} because {random.choice(['performance is acceptable at current scale', 'the alternative added too much complexity', 'benchmarks showed a clear improvement', 'the tradeoff favors simplicity', 'production testing confirmed the hypothesis'])}. Will revisit at {random.choice(['50K memories', '100K memories', 'next quarter', 'the next training run'])}.",
        f"Decision: {random.choice(['Qwen 3.5 2B over Gemma 4 E2B', 'spoke rank 64 over 128', 'batch_size 16 on MI300X', 'LR 3e-4 (not re-sweeping)', '5 epochs for EXP-20'])} for production encoding. Rationale: {random.choice(['1.7x faster locally', 'no quality improvement at higher rank', '192GB enables it', '5 experiments validate this value', 'more epochs with faster throughput'])}.",
    ]
    return random.choice(templates)


# ---------------------------------------------------------------------------
# Master generator
# ---------------------------------------------------------------------------

GENERATORS = [
    (gen_agent_operation, 20),
    (gen_agent_error, 15),
    (gen_store_operation, 12),
    (gen_mcp_operation, 18),
    (gen_watcher_event, 10),
    (gen_config_change, 5),
    (gen_performance_metric, 10),
    (gen_training_observation, 8),
    (gen_collaboration, 7),
    (gen_decision, 5),
]


def generate(count: int) -> list[dict]:
    """Generate count procedural observations."""
    # Build weighted generator list
    weighted = []
    for gen_func, weight in GENERATORS:
        weighted.extend([gen_func] * weight)

    results = []
    seen = set()
    attempts = 0
    max_attempts = count * 3

    while len(results) < count and attempts < max_attempts:
        attempts += 1
        gen_func = random.choice(weighted)
        text = gen_func()

        # Dedup by first 80 chars
        key = text[:80].lower()
        if key in seen:
            continue
        seen.add(key)

        results.append({
            "raw_input": text,
            "source": "targeted_procedural",
            "task_type": "encoding",
            "category": "procedural",
        })

    return results


def main():
    parser = argparse.ArgumentParser(description="Procedural generator for mnemonic training data")
    parser.add_argument("--count", type=int, default=500)
    parser.add_argument("--output", default="training/data/targeted/procedural_raw.jsonl")
    parser.add_argument("--seed", type=int, default=42)
    args = parser.parse_args()

    random.seed(args.seed)
    results = generate(args.count)

    Path(args.output).parent.mkdir(parents=True, exist_ok=True)
    with open(args.output, "w") as f:
        for r in results:
            f.write(json.dumps(r) + "\n")

    print(f"Generated {len(results)} procedural observations -> {args.output}")

    # Distribution
    from collections import Counter
    lengths = Counter()
    for r in results:
        words = len(r["raw_input"].split())
        if words < 30:
            lengths["short (<30w)"] += 1
        elif words < 60:
            lengths["medium (30-60w)"] += 1
        else:
            lengths["long (60w+)"] += 1
    print("Length distribution:")
    for k, v in lengths.most_common():
        print(f"  {k}: {v}")

    print(f"\nNext: encode via Batch API:")
    print(f"  python batch_encode.py submit --input {args.output}")


if __name__ == "__main__":
    main()
