#!/usr/bin/env python3
"""Generate 500+ additional mnemonic-specific scenarios (batch 2).

Focuses on areas underrepresented in batch 1:
  - Day-to-day developer workflow observations
  - Go code patterns and idioms specific to mnemonic
  - Real debugging workflows (not just the bug, but the investigation)
  - Config and deployment variations
  - Cross-session continuity (referencing prior decisions)
  - Various input lengths (short, medium, long)
  - Natural emotional variety baked into scenarios

Usage:
    LLM_API_KEY=... python generate_mnemonic_scenarios_v2.py submit
    LLM_API_KEY=... python generate_mnemonic_scenarios_v2.py status --job batches/JOB_ID
    LLM_API_KEY=... python generate_mnemonic_scenarios_v2.py download --job batches/JOB_ID
"""

import argparse
import json
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

API_KEY = os.environ.get("LLM_API_KEY", "")
MODEL = "gemini-3.1-pro-preview"
OUTPUT_DIR = Path("training/data/targeted")

SCENARIOS = []

# ---------------------------------------------------------------------------
# Short observations (2-4 sentences) — terse developer notes
# ---------------------------------------------------------------------------
SCENARIOS.extend([
    "make build succeeded after fixing the import cycle between internal/agent/encoding and internal/llm. Had to extract the CompletionRequest type into a separate package.",
    "go vet found an unreachable return statement in internal/store/sqlite/memories.go:287. Removed it. No functional change.",
    "golangci-lint caught an unchecked error return from bus.Publish() in internal/agent/consolidation/agent.go:156. Added _ = bus.Publish() to acknowledge it's fire-and-forget.",
    "The embedding index loaded 12,847 vectors in 340ms on daemon startup. That's fine for now but will need optimization at 50K+.",
    "Daemon memory usage stable at 340MB RSS after 14 days uptime. No leaks detected.",
    "FTS5 query for 'spread activation' returns in 3ms. Good enough for interactive recall.",
    "serve_spokes.py health endpoint returns in 2ms. The GPU model is warm and ready.",
    "Confirmed: Qwen 3.5 2B loads in 4.2 seconds on the RX 7800 XT. Acceptable cold start.",
    "git stash, git pull origin main, git stash pop — clean merge, no conflicts. Ready to branch.",
    "Restarted daemon after config change. All 8 agents initialized in 1.8 seconds. Healthy.",
    "Pre-commit hook caught a go fmt issue in 2 files. Fixed and re-committed.",
    "The store.CountMemories() call takes 1.2ms for 12K memories. Well within the health check budget.",
    "Clipboard watcher detected a JSON paste — a Gemini API error response. Perception scored it 0.62, encoded as a learning about API error patterns.",
    "Terminal watcher captured 'rocm-smi --showpids' output. Correctly filtered by the command exclusion regex — no raw memory created for diagnostic commands.",
    "The reactor's cooldown condition correctly prevented a second consolidation cycle within 6 hours. Working as designed.",
    "Checked the forum view on the dashboard. Consolidation agent's latest post: 'Archived 12 faded memories.' Personality system is working.",
    "WAL file at 3.2MB. Normal range. Checkpoint happened 20 minutes ago.",
    "Added .venv to .gitignore for the Python SDK directory. Should have been there from the start.",
    "Verified the MCP tool count: 24 tools registered in internal/mcp/server.go. Documentation matches.",
    "The perception agent's heuristic filter scored a node_modules change at 0.02. Correctly below threshold. Good noise rejection.",
])

# ---------------------------------------------------------------------------
# Medium observations (4-6 sentences) — standard developer notes
# ---------------------------------------------------------------------------
SCENARIOS.extend([
    "Investigating a slow recall query. The user reported 'authentication middleware' recall taking 2.3 seconds. Normal is 120ms. Profiled with Go pprof — the bottleneck was spread activation traversing 847 associations from a popular 'security' memory. The 0.7 decay factor wasn't limiting enough because the security memory had 23 direct associations. Temporarily increased activation_threshold from 0.1 to 0.2 to prune weak paths. Recall dropped to 180ms.",
    "The encoding agent processed 47 raw memories in a batch after a heavy coding session. Average encoding time: 19.7 seconds. The queue took 15 minutes to drain because serve_spokes.py processes one at a time via GENERATE_LOCK. For sustained throughput we need either batched inference or a second GPU.",
    "Reviewed the abstraction agent's output after a week of running. It promoted 3 patterns to principles: 'test before commit' (strength 0.92), 'check rocm-smi before training' (0.85), and 'validate config after editing' (0.81). All are genuinely useful recurring behaviors. The confidence levels look calibrated — no spurious promotions.",
    "Jason pushed PR #342 with Windows Service support. Reviewed the implementation: service_windows.go uses golang.org/x/sys/windows/svc, follows the same Start/Stop interface as launchd and systemd. Build tags look correct. One concern: the Windows event log integration might not surface errors clearly. Asked Jason to add structured logging.",
    "The dreaming agent's 2am cycle took 45 seconds (normally 12 seconds). Root cause: it selected 50 memories for replay, but 3 of them had 500+ word content fields that made the LLM synthesis slow. Added a content length cap of 300 words for dreaming input. Cycle time back to normal.",
    "Deployed a new spoke checkpoint (exp18_v5_12k/best_spokes.pt) to the serve_spokes.py server. First live encoding: 'Decision: switched from REST to gRPC for inter-service communication.' Output had valid schema, correct concepts [api, performance, decision, grpc], salience 0.8. Spot-checked 5 more — all clean.",
    "The metacognition agent flagged that 34 memories have zero associations. These were all from a bulk ingest_project run that skipped the association-linking step. Need to re-process them through the encoding pipeline's association phase. Not critical — they're still retrievable via FTS and embedding search, just missing the spread activation path.",
    "Compared recall quality with and without spread activation. Without: precision 0.71, recall 0.45. With (3 hops, 0.7 decay): precision 0.68, recall 0.67. Spread activation trades a tiny bit of precision for much better recall. The associated memories that surface are genuinely useful context.",
    "The reactor engine processed 847 events today: 812 MemoryEncoded (routed to 4 handlers each), 23 ConsolidationCompleted, 8 PatternDiscovered, 4 DreamCycleCompleted. Zero handler panics, zero dropped events. The event bus is stable under load.",
    "Caleb noticed the dashboard's encoding queue visualization was showing stale data. The WebSocket connection at /ws had disconnected 2 hours ago without the client reconnecting. Root cause: the browser tab was backgrounded and the OS suspended the WebSocket. Added a heartbeat ping every 30 seconds with auto-reconnect in the JavaScript client.",

    # Decisions
    "Decision: keeping the in-memory embedding index instead of switching to HNSW. At 12K memories, linear scan takes 4.2ms. HNSW would be sub-millisecond but adds 50MB memory overhead and complexity for index maintenance. The break-even point is around 50K memories where linear scan would hit 17ms. We'll migrate when we get there.",
    "Decision: using modernc.org/sqlite (pure Go) instead of mattn/go-sqlite3 (CGo). This means CGO_ENABLED=0 works for the SQLite parts of the build. The only CGo dependency remaining is fsevents on macOS. Linux builds are fully pure Go, which simplifies cross-compilation.",
    "Decision: the encoding system prompt now explicitly says 'Preserve exact file paths with line numbers verbatim.' This was the missing instruction that caused the 2/7 stress test failures. The old prompt just said 'preserved detail' which the model interpreted as permission to summarize.",
    "Decision: event bus uses fire-and-forget publishing. If a handler panics, other handlers still execute. This means we can't guarantee all handlers see every event, but the system stays alive. The alternative (guaranteed delivery) would require a durable queue and that's overkill for a single-machine daemon.",
    "Decision: perception agent uses a two-stage filter — fast heuristic (keywords, path patterns, content length) then optional LLM gate. The heuristic handles 95% of filtering at near-zero cost. The LLM gate only fires for borderline cases (heuristic score 0.3-0.7). This keeps the perception pipeline fast while still catching nuanced events.",

    # Errors and debugging
    "Error: the consolidation agent panicked with 'index out of range [3] with length 3' in consolidation.go:287. The decay loop was modifying the memories slice while iterating. Classic Go mistake — collected indices to delete first, then deleted in reverse order. Added a test case for this edge condition.",
    "Error: MCP recall returned stale results after a daemon restart. The embedding index was loaded from disk but 47 memories had been added since the last WAL checkpoint. Those memories were in SQLite (recoverable from WAL) but not in the in-memory index. Fixed by rebuilding the index from all memories on startup, not just the checkpoint.",
    "Error: spoke server returned 'CUDA out of memory' after running for 3 days. The generation loop wasn't clearing the KV cache between requests. torch.cuda.empty_cache() was called but the KV cache from the last generation was still pinned. Added explicit del on the generation output tensors before cache clear.",
    "Error: the terminal watcher crashed with 'too many open files' on Linux after 5 days of continuous operation. The history file polling was opening a new file handle every 10 seconds without closing the previous one. Added explicit f.Close() in the poll loop in watcher/terminal/watcher.go:89.",
    "Debugging: the episoding agent created an episode titled 'Unknown activity' for 3 memories that all had source=clipboard. The LLM couldn't synthesize a meaningful title from clipboard pastes (they were code snippets without context). Added a fallback title format: 'Clipboard activity ({count} items)' when the LLM returns a generic title.",

    # Learnings
    "Learning: Go's sync.Map is not appropriate for the event bus subscriber map. It's optimized for read-heavy workloads where keys are stable, but our subscriber map mutates on every Subscribe/Unsubscribe call. Switched back to a regular map + sync.RWMutex. Benchmark showed 3x faster subscriber lookup.",
    "Learning: SQLite's busy_timeout only applies to the initial lock acquisition, not to the entire transaction duration. A transaction that acquires the lock within 5 seconds can then hold it indefinitely. This is why our consolidation agent's 45-second transactions weren't hitting the timeout but other writers were. Need to enforce transaction duration limits in our own code.",
    "Learning: the Muon optimizer's orthogonal Q,R factors prevent spoke collapse during training. Without Muon (using AdamW only), the W_down and W_up matrices converge to the same low-rank subspace across layers. Muon maintains diversity across layers, which is critical for the gate mechanism to learn different per-layer contributions.",
    "Learning: Go's //go:embed directive for the web dashboard means any change to HTML/CSS/JS requires a full binary rebuild. Can't hot-reload dashboard changes during development. Considered using a build tag to switch between embedded and filesystem-served assets, but the complexity isn't worth it for occasional dashboard tweaks.",
    "Learning: the progressive gate initialization (layer 0: sigmoid(-2)=0.12, layer 23: sigmoid(+2)=0.88) is critical for stable spoke training. Without it, all gates start at 0.5 and early training is chaotic because every layer makes equal corrections to the frozen base. The progressive init lets early layers stay quiet while late layers do the heavy lifting.",

    # Insights
    "Insight: the encoding agent's concept extraction produces better results when the controlled vocabulary is included in the prompt. Without it, the model generates vague concepts like 'software' and 'technology'. With the vocabulary, it maps to specific terms like 'sqlite', 'fts5', 'encoding'. The vocabulary acts as a soft constraint without strict enforcement.",
    "Insight: memories that get the most recall hits are decisions, not observations. Out of the top 50 most-accessed memories, 38 are type='decision', 8 are type='error', and 4 are type='insight'. Developers look up past decisions far more than past events. The data pipeline should weight decision-type memories higher in salience.",
    "Insight: the 2am dreaming schedule works better than 8am because the daemon has processed a full day of memories by then. At 8am, it only has overnight terminal/clipboard events (usually nothing). At 2am, it has all of the previous day's coding session memories — 20-50 substantive observations ready for cross-pollination.",
    "Insight: spread activation with 3 hops and 0.7 decay produces the best recall quality. We tested 2 hops (too shallow — misses related context), 4 hops (too noisy — reaches unrelated memories), 0.5 decay (too aggressive — second hop barely activates), 0.9 decay (too noisy — third hop has 0.73 activation, pulling in tangential results).",
    "Insight: the stress test failures (5/7) are both detail omission, not fabrication. The model drops 'spread.go:142' to 'spread.go' and drops 'Jason' entirely. This is a much better failure mode for a memory system than hallucinating details that don't exist. Omission loses information; fabrication corrupts it.",
])

# ---------------------------------------------------------------------------
# Long-form observations (8+ sentences) — detailed narratives
# ---------------------------------------------------------------------------
SCENARIOS.extend([
    """Full debugging narrative: Started at 10am when a user reported that MCP recall for 'authentication middleware' was returning 0 results. Verified the query locally — indeed 0 results despite knowing there were 12 relevant memories. First checked the FTS5 index: SELECT * FROM memories_fts WHERE memories_fts MATCH 'authentication middleware' — 0 rows. But SELECT * FROM memories WHERE content LIKE '%authentication%' returned 12 rows. The memories were in the table but not the FTS index. Checked the FTS tokenizer config: PRAGMA fts5_tokenize showed it was using the default tokenizer, which splits compound words. 'middleware' was being tokenized as 'middle' + 'ware'. Neither token matched the query. Solution: wrote migration 005 to switch to unicode61 tokenizer which keeps compound words intact. After rebuilding the FTS index, the query returned all 12 results. Total investigation time: 45 minutes. Filed as a known issue for the docs.""",

    """Architecture evolution documentation: When mnemonic started, agents communicated via direct function calls. The encoding agent imported the retrieval agent to check for duplicates before storing. This created an import cycle when the retrieval agent needed encoding for query expansion. The solution was the event bus in internal/events/. Agents now publish events (MemoryEncoded, ConsolidationCompleted, PatternDiscovered) and subscribe to types they care about. The encoding agent publishes MemoryEncodedEvent; the retrieval agent subscribes and updates its embedding index. No import dependencies between agents. The tradeoff is debugging: when something goes wrong, you have to trace events through the bus instead of following function call stacks. The reactor agent partially solves this by logging every event match and action execution. After 6 months of operation, the event bus architecture has proven robust — zero data loss from missed events, and adding new agents requires zero changes to existing ones.""",

    """Performance investigation: The mnemonic daemon was using 1.2GB RSS after a week of running, up from 340MB at startup. Used Go's pprof heap profiler: go tool pprof http://localhost:6060/debug/pprof/heap. The top allocation was in internal/store/sqlite/embindex.go — the embedding index was loading all 12,847 vectors (384 dimensions, float32) into RAM. That accounts for 12847 * 384 * 4 = 19.7MB, which is expected. The real culprit was the association graph cache in internal/agent/retrieval/agent.go — it was caching every spread activation result indefinitely. After 10,000 queries, the cache held 2.3GB of activation results. Added LRU eviction with a 1000-entry limit. Memory usage stabilized at 380MB. The fix was 5 lines of code in retrieval/agent.go:234 — wrapping the cache map with a sync.Map and adding an eviction goroutine.""",

    """Training data pipeline retrospective: The journey from v1 to v6 was a lesson in data quality. v1 (3,577 examples) had 37% poisoned data — synthetic compression/decompression templates with fictional entities like 'daxBautista|Feb2019|9662C@Ferrum Initiative'. The model memorized these templates and produced them on novel inputs. v2 (4,566 examples) removed the poison and added Gemini-enriched pre-nuke data. Novel schema went from 60% to 100% overnight. v5 (11,436) scaled up with SWE-bench, code reviews, and Stack Exchange — but 76% was irrelevant content (3D printing, firmware, mesh operations). v6 (~4,100) stripped the noise and added targeted precision data for file:line preservation, entity names, and mnemonic-specific scenarios. Every version taught us something: data quality > data quantity, domain-specific > generic, and you must validate before training.""",

    """Incident report: At 3:14am on March 23, the dreaming agent entered an infinite loop. The DreamCycleCompleted event wasn't firing, so the orchestrator kept triggering new dream cycles every 30 seconds. After 47 cycles, the daemon's CPU hit 100% and the encoding queue backed up to 200 items. Root cause: three memories (IDs: a1b2c3, d4e5f6, g7h8i9) had formed a circular association chain. Memory A was associated with B (strength 0.95), B with C (0.92), and C back to A (0.88). The dreaming agent's replay function followed associations without cycle detection, getting stuck in the A->B->C->A loop. Fix: added a visited set in agent/dreaming/replay.go:203 that breaks cycles after seeing the same memory twice. Also added a hard timeout of 60 seconds per dream cycle. The 47 failed cycles were logged but didn't corrupt any data — the bus's fire-and-forget semantics meant other agents continued normally. Total impact: 6 hours of dreaming output lost, encoding queue took 90 minutes to drain after the fix was deployed.""",

    """Cross-session context: This session is continuing work from yesterday's handoff. The previous session completed EXP-18 (Qwen spoke training on 11.4K v5 dataset, 100% novel schema) and EXP-19 (Gemma 4 E2B training, also 100% schema but 1.7x slower). The key decision was to use Qwen for production encoding due to speed advantage on the RX 7800 XT. Today's focus is EXP-20 preparation: building a quality-validated v6 dataset for the MI300X training run. We discovered that 76% of v5 was SWE-bench noise (including 3D printing questions) and stripped it down to the 2,626 relevant examples (pre-nuke real data + Gemini synthetic). Added 1,500+ targeted examples for precision training (stack traces, entities, numbers, domain terms) and mnemonic-specific scenarios. The dataset went from 'big and noisy' to 'small and precise' — quality over quantity.""",
])

# ---------------------------------------------------------------------------
# Varied emotional tones (not just analytical)
# ---------------------------------------------------------------------------
SCENARIOS.extend([
    # Frustrated
    "Spent 2 hours debugging why the spoke model's JSON output had a trailing comma after structured_concepts.causality. Turns out the Qwen tokenizer generates a comma before the closing bracket about 5% of the time. The parse_json_response() recovery logic handles it, but it shouldn't be happening. Need to investigate if this is a tokenizer issue or a training data artifact.",
    "The ROCm driver crashed AGAIN during a training run at step 2,847. No error message, just a hard GPU reset. Third time this week. Had to kill the stale process (rocm-smi --showpids), wait for the device to recover, and restart from the last checkpoint. Lost 20 minutes of training. AMD needs to fix their driver stability.",
    "Frustrated: spent 45 minutes on a 'database is locked' error that turned out to be my own fault. I had an open SQLite shell in another terminal holding a read lock while the consolidation agent tried to write. The error message could be more helpful — 'locked by PID 12345' would save so much debugging time.",
    "Three attempts at getting the Gemini Batch API to work with our encoding prompt. First: 503 errors (model overloaded). Second: outputs truncated at 2048 tokens (max_output_tokens too low). Third: succeeded but 8% of outputs had invalid JSON. Bumped to 8192 tokens and 100% success. Should have read the API docs more carefully.",
    "The dashboard WebSocket keeps disconnecting when I switch browser tabs. Chrome suspends background tabs after 5 minutes, killing the WebSocket. Added a heartbeat ping but it doesn't help because the browser isn't executing JavaScript in the background. Might need to switch to Server-Sent Events which handle reconnection natively.",

    # Excited / Positive
    "The v6 dataset audit found and removed 8,487 irrelevant SWE-bench examples (including 3D printing questions!). The remaining 2,626 + 1,500 targeted examples is a much cleaner foundation. Every example now teaches something relevant to mnemonic's encoding task. Quality over quantity.",
    "First successful end-to-end test of the spoke server with the daemon: MCP remember call -> raw memory created -> encoding agent picks it up -> sends to spoke server on port 8899 -> receives valid 10-field JSON -> stores in SQLite with embedding. 19.7 seconds, zero cloud dependency. This is what local-first means.",
    "The 3-level validation pipeline caught 166 bad examples that we'd been training on for weeks. 139 gists over 80 characters, 26 duplicates, 1 invalid emotional_tone enum. These were silently degrading training quality. The pipeline pays for itself immediately.",
    "Spread activation is working beautifully. Query 'SQLite performance' -> finds the WAL mode decision (direct match, score 0.95) -> spreads to concurrent read benchmark (association strength 0.87) -> spreads to consolidation lock timeout fix (0.72). Three related memories from one query, exactly the context a developer needs.",
    "The gate progression after EXP-17 training is exactly what the Felix-LM paper predicted: early layers gate low (0.12-0.20), late layers gate high (0.75-0.88). The frozen base handles syntax and shallow semantics; the spokes correct the deep semantics for our specific task. 25M parameters doing the work of a full fine-tune.",

    # Concerned
    "Concerned about the encoding queue depth during heavy coding sessions. 47 items backed up today, taking 15 minutes to drain. If the user makes a decision during that window and later asks about it, the memory might not be encoded yet. Need to prioritize MCP remember calls over passive watcher events in the queue.",
    "The pre-nuke data has 444 examples from the ingest source — these are bulk-loaded file descriptions, not developer observations. They teach the model to summarize code files rather than encode events. Should we keep them or are they polluting the training signal?",
    "Worried that the embedding model (384 dimensions) might not have enough capacity to distinguish between similar technical concepts. 'authentication middleware' and 'authorization middleware' have cosine similarity 0.94 but they're fundamentally different topics. Might need a larger embedding model or fine-tuned embeddings.",
    "The daemon has been running for 14 days without a restart. That's good for stability testing but means we haven't tested cold start recovery in 2 weeks. What if the schema migration path has a bug that only shows on fresh start? Adding a weekly restart to the maintenance schedule.",

    # Reflective
    "Looking at the production captures data, 68% is file cataloging from the ingest pipeline. The daemon spends most of its LLM budget encoding source files, not developer observations. That ratio should probably be inverted — developer observations are higher value per encoding cycle.",
    "The mnemonic codebase has grown to 8 agents, 24 MCP tools, a custom LLM architecture, and a 3-level data validation pipeline. It started as a simple 'remember things between sessions' daemon. The complexity is justified — each component addresses a real problem — but the surface area for bugs keeps growing.",
    "After 19 experiments, the pattern is clear: data quality improvements produce larger gains than architectural changes. EXP-15 (rotation) added complexity with minimal benefit. EXP-17 (clean data) was the breakthrough with zero architecture changes. This should inform how we spend engineering time going forward.",
    "The Felix-LM spoke architecture validated its core hypothesis: you can train task-specific adapters (25M params, 0.7% overhead) on a frozen base and match cloud API quality on specialized tasks. The next test is whether different spoke sets can hot-swap for different tasks (encoding, synthesis, retrieval) without reloading the base model.",
])

# ---------------------------------------------------------------------------
# Various input formats and edge cases
# ---------------------------------------------------------------------------
SCENARIOS.extend([
    # Very short (but substantive — different from sparse)
    "Increased consolidation decay_rate from 0.95 to 0.97. Memories were fading too fast — useful decisions from 2 weeks ago were hitting the archive threshold.",
    "The 0.7 spread activation decay factor limits third-hop activation to 0.34. That's the sweet spot between depth and noise.",
    "Switched the FTS5 tokenizer from default to unicode61. Compound words like 'middleware' now stay intact in the index.",
    "Gate bias at layer 12 is 0.45 — right in the middle. This layer is making moderate corrections to the frozen base.",
    "MCP feedback for query 'auth' rated as 'helpful'. Adjusted 455 association strengths in the retrieval graph.",

    # Code references in observations
    "The Store interface in internal/store/store.go defines 47 methods. The SQLite implementation in internal/store/sqlite/ is the only concrete implementation. If we ever need Postgres, we implement the same interface in a new package. The abstraction has paid off — we've changed the schema 15 times without touching any agent code.",
    "The CompletionRequest struct in internal/llm/provider.go has a new ResponseFormat field for structured output. When set to json_schema, the LLM provider should return valid JSON matching the schema. The training capture wrapper in training_capture.go checks parse_success against this schema.",
    "The InMemoryBus in internal/events/inmemory.go uses a sync.RWMutex for the subscribers map. Subscribe() takes a write lock, Publish() takes a read lock. This allows concurrent event dispatch while preventing subscriber registration during dispatch. The tradeoff: Subscribe() blocks during high-throughput event bursts.",
    "Reviewed the SpokeLayer implementation in training/scripts/qwen_spoke_adapter.py. The forward pass: input -> RMSNorm -> W_down (2048->64) -> rotate (optional) -> SiLU -> W_up (64->2048) -> sigmoid(gate_bias) * result -> add to residual. The zero-initialization of W_up means the spoke starts as identity — no disruption to the frozen base at initialization.",

    # Multi-topic observations
    "Three things from today's session: (1) Fixed a nil pointer in the episoding agent where the LLM returned an empty title — added a fallback to 'Untitled episode'. (2) Jason reported the Mac Mini launchd plist has the wrong binary path, needs to point to ~/go/bin/mnemonic instead of /usr/local/bin/mnemonic. (3) The training data validation pipeline is ready — 3 levels covering schema, semantic fidelity, and dataset health. Need to run the v5 audit tomorrow.",
    "Two decisions made today: First, we're using Qwen 3.5 2B for production encoding instead of Gemma 4 E2B. Both achieve 100% schema but Qwen is 1.7x faster on 16GB VRAM (no NF4 needed). Second, the MI300X droplet training will use batch_size=16 with no gradient accumulation — the 192GB VRAM means no compromises on batch size or sequence length.",
    "Morning standup notes: Caleb is working on the data quality pipeline for EXP-20. Jason is finishing Windows Service support (PR #342). The autoresearch branch needs to be rebased on main before we can merge the Gemma adapter. Blockers: none. The Batch API jobs for targeted data generation are running at Google.",
    "Session summary: Started by checking the mnemonic handoff from the last session. The previous agent completed EXP-15 through EXP-19, built the Gemma adapter, and decided Qwen is the production encoding model. Today we built the data quality pipeline (validate.py with 3 levels), generated 1,500+ targeted training examples, and discovered that 76% of the v5 dataset was irrelevant SWE-bench noise. Curated down to ~4,100 high-quality examples for v6.",

    # Observations about the training process itself
    "The Gemini Batch API is the right tool for training data generation. Individual async calls hit rate limits at 25 concurrent. The Batch API processes 1,100 requests server-side with zero rate limits and 50% cost reduction. Submit, poll, download. No client-side complexity.",
    "Training data lesson: the encoding system prompt matters as much as the training data. Adding 'Preserve exact file paths with line numbers verbatim' to the content field instruction is a zero-cost change that directly addresses the detail omission failure mode. The model wasn't being asked to preserve details — it was being asked to 'preserve detail' which it interpreted as a summary-level instruction.",
    "Checkpoint evaluation protocol: (1) eval loss on held-out set, (2) novel schema compliance on 10 unseen inputs, (3) hallucination stress test on 7 hard inputs, (4) manual spot-check of 5 random encodings. All four must pass before a checkpoint is considered production-ready. This is more rigorous than previous experiments which only checked loss and schema.",
    "The MI300X droplet (192GB VRAM) enables: batch_size=16 (vs 1 locally), no gradient accumulation, no gradient checkpointing, full bf16 (no NF4 quantization), and 5 epochs in ~2-3 hours. Locally the same training would take ~12 hours with batch 1 and gradient accumulation. The paid GPU is worth it for the final production run.",
])

# ---------------------------------------------------------------------------
# Observations from other project areas (SDK, docs, CI)
# ---------------------------------------------------------------------------
SCENARIOS.extend([
    "Updated the Python SDK in sdk/ to use the latest MCP tool definitions. The agent evolution system in sdk/agent/evolution/ auto-generates improved prompts based on usage patterns. Verified the example data in sdk/agent/evolution/examples/ still works with the updated schema.",
    "The CI pipeline runs golangci-lint, go vet, go test, and go build on every PR. Current build time: 2 minutes 34 seconds. The lint step catches the most issues — errcheck failures from unchecked error returns are the #1 source of CI failures. Adding _ = expr for intentionally ignored errors.",
    "release-please automates version bumps from conventional commits. feat: bumps minor, fix: bumps patch. The Makefile injects the version via ldflags: -X main.Version=$(VERSION). The binary at bin/mnemonic reports its version with mnemonic --version.",
    "Documentation update: added the Felix-LM training section to CLAUDE.md. Covers the hub-and-spoke architecture, training scripts in training/scripts/, data pipeline, and experiment registry. Future sessions need this context to understand the training infrastructure without re-exploring the codebase.",
    "The lifecycle test in cmd/lifecycle-test/ simulates 3 months of daemon operation: install, start, ingest a project, process memories through all 8 agents, consolidate, dream, abstract, stop. It's the closest thing to an integration test for the full system. Takes about 2 minutes to run.",
])

GEN_SYSTEM = (
    "You rewrite scenarios into natural developer observations. Keep ALL specific details "
    "(file paths with line numbers, function names, person names, exact numbers, error messages, "
    "struct names, config values) EXACTLY as given. Vary the writing style — some terse, some "
    "analytical, some frustrated, some excited. Output ONLY the observation text, no markdown fences."
)

GEN_PROMPT_TEMPLATE = (
    "Rewrite this mnemonic daemon scenario as a natural developer observation, as if recording "
    "it in a work log. Preserve every technical detail verbatim. Output ONLY the observation.\n\n"
    "Scenario: {scenario}"
)


def build_batch_requests():
    requests = []
    for i, scenario in enumerate(SCENARIOS):
        requests.append({
            "key": f"mnv2-{i}",
            "request": {
                "contents": [{"parts": [{"text": GEN_PROMPT_TEMPLATE.format(scenario=scenario)}]}],
                "system_instruction": {"parts": [{"text": GEN_SYSTEM}]},
                "generation_config": {"temperature": 0.8, "max_output_tokens": 4096},
            },
        })
    return requests


def submit():
    from google import genai
    from google.genai import types
    client = genai.Client(api_key=API_KEY)
    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)

    # Save metadata
    meta_path = OUTPUT_DIR / "mnemonic_v2_meta.jsonl"
    with open(meta_path, "w") as f:
        for i, s in enumerate(SCENARIOS):
            f.write(json.dumps({"key": f"mnv2-{i}", "scenario": s[:200]}) + "\n")

    requests = build_batch_requests()
    batch_path = OUTPUT_DIR / "mnemonic_v2_batch.jsonl"
    with open(batch_path, "w") as f:
        for r in requests:
            f.write(json.dumps(r) + "\n")

    uploaded = client.files.upload(file=str(batch_path), config=types.UploadFileConfig(display_name="mnemonic-v2-rawgen", mime_type="jsonl"))
    job = client.batches.create(model=MODEL, src=uploaded.name, config={"display_name": "mnemonic-v2-rawgen"})
    print(f"Scenarios: {len(SCENARIOS)}")
    print(f"Job: {job.name}")
    print(f"State: {job.state.name}")
    print(f"\nCheck: python generate_mnemonic_scenarios_v2.py status --job {job.name}")


def check_status(job_name):
    from google import genai
    client = genai.Client(api_key=API_KEY)
    job = client.batches.get(name=job_name)
    print(f"Job: {job.name}")
    print(f"State: {job.state.name}")
    if hasattr(job, "dest") and job.dest:
        print(f"Result: {job.dest.file_name}")


def download(job_name):
    from google import genai
    client = genai.Client(api_key=API_KEY)
    job = client.batches.get(name=job_name)
    if job.state.name != "JOB_STATE_SUCCEEDED":
        print(f"Not done: {job.state.name}")
        return

    content = client.files.download(file=job.dest.file_name)
    lines = content.decode("utf-8").strip().split("\n")

    output_path = OUTPUT_DIR / "mnemonic_v2_raw_inputs.jsonl"
    success = fail = 0
    with open(output_path, "w") as f:
        for line in lines:
            try:
                r = json.loads(line)
                text = r["response"]["candidates"][0]["content"]["parts"][0]["text"].strip()
                if text.startswith("```"):
                    tlines = text.split("\n")
                    text = "\n".join(l for l in tlines if not l.strip().startswith("```")).strip()
                if len(text) < 20:
                    fail += 1
                    continue
                f.write(json.dumps({
                    "raw_input": text,
                    "source": "targeted_mnemonic_v2",
                    "task_type": "encoding",
                    "category": "mnemonic_specific",
                }) + "\n")
                success += 1
            except (KeyError, IndexError, json.JSONDecodeError):
                fail += 1

    print(f"Results: {success}/{success+fail} ({success/(success+fail)*100:.1f}%)")
    print(f"Written to: {output_path}")
    print(f"\nNext: python batch_encode.py submit --input {output_path}")


def main():
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command")
    sub.add_parser("submit")
    sub.add_parser("count")
    s = sub.add_parser("status")
    s.add_argument("--job", required=True)
    d = sub.add_parser("download")
    d.add_argument("--job", required=True)
    args = parser.parse_args()

    if args.command == "submit":
        submit()
    elif args.command == "count":
        print(f"Total: {len(SCENARIOS)}")
    elif args.command == "status":
        check_status(args.job)
    elif args.command == "download":
        download(args.job)
    else:
        parser.print_help()


if __name__ == "__main__":
    main()
