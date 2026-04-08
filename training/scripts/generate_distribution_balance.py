#!/usr/bin/env python3
"""Generate training data to fix distribution imbalances in the v5/v6 dataset.

Four categories targeting dataset-wide biases:
  A: long_form     — 400+ word inputs (debugging narratives, incident reports, architecture docs)
  B: code_format   — Raw code, JSON, YAML, shell output, log excerpts
  C: low_sig       — Routine/trivial observations with low salience
  D: emotional     — Frustrated, excited, concerned, reflective observations

All submitted via Gemini Batch API.

Usage:
    LLM_API_KEY=... python generate_distribution_balance.py submit
    LLM_API_KEY=... python generate_distribution_balance.py status --job batches/JOB_ID
    LLM_API_KEY=... python generate_distribution_balance.py download --job batches/JOB_ID
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
# Category A: Long-form inputs (400+ words)
# Production will see full debugging narratives, incident reports, etc.
# ---------------------------------------------------------------------------

LONG_FORM_SYSTEM = (
    "You generate realistic, detailed developer observations. These must be LONG — "
    "at least 400 words, up to 800. Include specific file paths, line numbers, exact "
    "error messages, metrics, timestamps, and person names. Write as a thorough developer "
    "documenting a complex situation. Output ONLY the observation text."
)

LONG_FORM_SCENARIOS = [
    # Debugging narratives
    "A 3-hour debugging session tracking down a memory leak in a Go daemon. Started by noticing RSS growing from 340MB to 1.2GB over 6 hours via Grafana. Used pprof heap profile to identify the leak in an event subscriber that wasn't unsubscribing on context cancellation. Include exact pprof output snippets, the fix in internal/events/inmemory.go:142, and the before/after memory graphs.",
    "Investigating why a SQLite FTS5 query returns 0 results for 'authentication middleware' despite 12 matching memories. Walk through: checking the FTS5 tokenizer config, discovering the default tokenizer splits compound words, testing with unicode61 tokenizer, writing migration 005, verifying results. Include exact SQL queries used and output.",
    "Debugging a race condition between the encoding agent and consolidation agent. Both trying to update the same memory's salience simultaneously. Timeline: 10:15am first report, 10:30am reproduced with -race flag, 10:45am identified the unsynchronized read-modify-write in store/sqlite/memories.go:312, 11:00am fixed with optimistic locking, 11:15am verified fix. Include the race detector output.",
    "A full incident investigation: the mnemonic daemon stopped encoding new memories at 3am. Discovery: user noticed stale memories in morning recall. Investigation: checked systemd journal, found repeated panic/recover in encoding agent. Root cause: the LLM provider returned empty responses after a model update on the spoke server. Fix: added response validation in encoding/agent.go:234. Impact: 6 hours of memories lost, 47 raw memories queued.",
    "Tracking down why the spread activation in the retrieval agent returns the same 3 memories for every query. Step-by-step: verified query embedding is correct, checked FTS5 returns diverse results, found that spread activation always follows the same high-strength association path. Root cause: circular bidirectional associations between 3 popular memories. Fix: cycle detection in retrieval/spread.go:142. Include activation scores at each hop.",
    "Debugging a training script failure on the RX 7800 XT. The spoke training crashed with a segfault after 3000 steps. Investigation: checked rocm-smi (no stale processes), verified VRAM (7.3GB used, 8.7GB free), ran with ROCM_AOTRITON debug logging. Found that a stale PYTORCH_ROCM_ALLOC_CONF=expandable_segments:True was in .bashrc from a previous PyTorch version. Removed it, training completed successfully after restart.",
    "Investigating a data quality issue discovered during v5 audit. The validate.py Level 2 fidelity check found 251 examples where file:line pairs in the raw input were missing from the encoded output. Traced the cause: the encoding system prompt didn't explicitly instruct preservation of technical identifiers. Added 'Preserve exact file paths with line numbers verbatim' to the prompt. Re-tested on 10 samples — all now preserve file:line.",
    "Full postmortem of a production deployment gone wrong. Deployed new mnemonic binary with updated encoding agent at 2pm. By 3pm, encoding latency spiked from 20s to 90s. By 4pm, encoding queue backed up to 200 items. Root cause: the new binary loaded a larger embedding model that consumed 4GB more VRAM, leaving insufficient memory for the spoke model. Rollback at 4:15pm. Lesson: always check total VRAM budget before deploying model changes.",

    # Architecture discussions
    "Detailed analysis of the tradeoffs between event bus and direct agent calls for mnemonic's inter-agent communication. Cover: decoupling benefits (agents don't import each other), testing advantages (mock bus), scalability (new agents don't modify existing code), debugging challenges (event flow is implicit), ordering guarantees (or lack thereof), performance (pub/sub overhead vs direct function call). Include specific examples from the codebase.",
    "Architecture review of the Felix-LM spoke adapter design. Cover: why frozen base + trainable spokes (parameter efficiency), the bottleneck architecture (W_down 2048->64, W_up 64->2048), gate mechanism (sigmoid, initialized at progressive values 0.12-0.88), Muon optimizer for matrices vs AdamW for scalars, zero initialization of W_up for safe startup. Include the math: 25.2M params = 0.7% overhead on 3.5B base.",
    "Detailed comparison of embedding storage strategies for mnemonic. Current: in-memory linear scan (4ms for 12K vectors). Alternative 1: HNSW index (sub-ms but 2x memory, complex to maintain). Alternative 2: SQLite vec extension (disk-based, slower but zero RAM overhead). Alternative 3: separate vector DB (Qdrant/Milvus, overkill for single-machine). Decision: stay with linear scan until 100K memories, benchmark quarterly.",
    "Full design document for the spoke routing system. Requirement: hot-swap different spoke sets (encoding, synthesis, retrieval) on the same frozen Qwen 3.5 2B base at inference time. Design: config.yaml maps task_type to spoke checkpoint path. serve_spokes.py loads the appropriate checkpoint per request. Challenges: VRAM management (can't load all spokes simultaneously on 16GB), checkpoint switching latency, graceful degradation if a spoke fails.",
    "Detailed analysis of the training data pipeline evolution. v1: 3,577 examples, 37% poisoned with synthetic compression/decompression templates. v2: cleaned to 4,566 after removing poison, adding Gemini-enriched pre-nuke data. v5: scaled to 11,436 with SWE-bench, code reviews, Stack Exchange. v6: quality-audited with 3-level validation pipeline, added targeted precision data and mnemonic-specific scenarios. Cover the metrics at each stage.",

    # Incident reports
    "Complete incident report for a data loss event. At 2:47am the consolidation agent's merge operation corrupted 3 memories. The merge created a gist_of memory but the original memories were marked as archived before the merge completed (transaction wasn't atomic). The dreaming agent then accessed the archived originals, got empty content, and generated null insights. Impact: 3 memories lost, 2 null insights created. Fix: wrap merge + archive in single transaction.",
    "Incident: user reported recall returning memories from wrong project. Timeline: 9am report, 9:15am reproduced (query 'mnemonic auth' returned felixlm memories), 9:30am found root cause in store/sqlite/retrieval.go:156 — FTS5 query path didn't include project filter (vector path did), 10am fixed and deployed, 10:15am verified. Root cause analysis: the FTS5 query was added in a rush during the v3 refactor and skipped project scoping.",
    "Security incident: a user's LLM_API_KEY was accidentally included in a raw memory from the terminal watcher. The terminal history contained 'export LLM_API_KEY=sk-...' and the perception agent didn't filter it. The memory was encoded and stored with the key in the content field. Fix: added regex pattern for common secret formats (sk-, ghp_, AKIA, etc.) to the terminal watcher's exclude list in watcher/terminal/watcher.go:45.",

    # Performance analysis
    "Comprehensive performance analysis of the encoding pipeline. End-to-end latency breakdown: watcher event -> perception filter (5ms) -> raw memory write (2ms) -> encoding agent pickup (200ms polling) -> LLM call (19.7s for Qwen spokes, 7.3s for Gemini) -> concept extraction (50ms) -> embedding generation (50ms) -> association linking (100ms) -> memory write (5ms). Total: 20.1s for spoke, 7.7s for Gemini. Bottleneck: LLM inference.",
    "Database performance analysis after reaching 10K memories. Query times: FTS5 simple query 2ms, complex query 8ms, embedding search 4.2ms (linear scan 12,847 vectors), association graph traversal 15ms (3 hops). Write times: memory insert 3ms, association insert 1ms, FTS trigger 2ms. WAL checkpoint: 500ms average, 45s worst case during consolidation. Conclusion: performance is fine for current scale, linear scan becomes bottleneck at 50K+.",
    "Training throughput analysis for EXP-18 on RX 7800 XT. Batch 1, accum 8, seq_len 2048. Forward pass: 1.1s, backward pass: 0.9s, optimizer step (Muon): 0.1s. Total per micro-step: 2.1s. Per optimizer step (8 micro): 16.8s. Steps per epoch (11,436 examples): 11,436. Time per epoch: ~6.7 hours. With early stopping at step 11,400 (end of epoch 1). MI300X projection: batch 16, no accum, ~3x throughput.",
]

# ---------------------------------------------------------------------------
# Category B: Code/config format inputs
# Raw code, JSON, YAML, shell output, log files
# ---------------------------------------------------------------------------

CODE_FORMAT_SYSTEM = (
    "You generate realistic developer observations that contain raw code, config, "
    "or terminal output. The observation should include the actual code/config/output "
    "embedded in the narrative. Use real-looking file paths, function names, and "
    "realistic code patterns. Output ONLY the observation text."
)

CODE_FORMAT_SCENARIOS = [
    # Go code snippets
    "Developer observation about reviewing a Go function that implements spread activation for memory retrieval. Include the actual function signature and key logic: func (ra *RetrievalAgent) spreadActivation(ctx context.Context, seeds []Memory, maxHops int, decayFactor float64) []ActivationResult. Show the visited map, the BFS loop with decay, and the early termination condition.",
    "Observation about fixing a nil pointer dereference in a Go HTTP handler. Include the actual problematic code (accessing resp.Body without checking if resp is nil) and the fix (adding the nil guard). File: internal/api/routes/memories.go:89. Include the go vet warning that caught it.",
    "Developer documented a new Go test they wrote for the consolidation agent's decay function. Include the table-driven test with 5 cases: fresh memory (< 24h), recent (< 168h), old (> 168h), already-at-threshold, and below-threshold. Show the actual test function with t.Run() calls.",
    "Observation about a Go interface design decision. Include the actual Provider interface definition: type Provider interface { Complete(ctx, req) (*Response, error); Embed(ctx, text) ([]float64, error); BatchEmbed(ctx, texts) ([][]float64, error); Health(ctx) error }. Discuss why Health() was added after a production incident.",
    "Code review of a new Go migration file. Include the actual SQL: CREATE TABLE IF NOT EXISTS episodes (id TEXT PRIMARY KEY, title TEXT, start_time DATETIME, end_time DATETIME, memory_ids TEXT); CREATE INDEX idx_episodes_time ON episodes(start_time). Note the TEXT type for memory_ids (JSON array) and discuss alternatives.",
    "Developer noted a refactoring opportunity in the event bus. Include the current code showing 3 nearly-identical handler registration blocks and the proposed extraction into a generic registerHandler[T Event]() function using Go generics.",
    "Observation about implementing context cancellation propagation through the agent pipeline. Include code showing how ctx.Done() is checked in the encoding loop: select { case <-ctx.Done(): return ctx.Err(); case raw := <-ea.queue: ea.encodeRawMemory(ctx, raw) }.",
    "Developer documented a subtle Go concurrency bug. Include the code: two goroutines reading and writing to a map without synchronization. Show the race detector output with exact goroutine IDs and stack traces. Show the fix using sync.RWMutex.",

    # JSON blobs
    "Observation about a malformed JSON response from the encoding LLM. Include the actual response (with the trailing comma that breaks parsing): {\"gist\": \"Fixed auth bug\", \"concepts\": [\"auth\", \"security\",], ...}. Show the parse_json_response() recovery logic that strips the comma.",
    "Developer recorded the output of a memory encoding for quality review. Include the full 10-field JSON: gist, summary, content (preserving file:line refs), narrative, concepts array, structured_concepts with all 4 sub-arrays, significance, emotional_tone, outcome, salience.",
    "Observation about a config.yaml change. Include the full diff: before (llm.endpoint pointing to Gemini API) and after (pointing to localhost:8899 spoke server). Show the YAML structure with comments explaining each field.",
    "Developer documented the health check JSON output from the orchestrator. Include: {\"llm_available\": true, \"store_healthy\": true, \"memory_count\": 12847, \"db_size_mb\": 487, \"encoding_queue_depth\": 3, \"last_consolidation\": \"2026-04-03T02:00:00Z\", \"agent_status\": {\"perception\": \"running\", ...}}.",

    # Shell/terminal output
    "Observation about a systemd service debugging session. Include actual journalctl output: 'Apr 04 10:15:23 ubuntu mnemonic[12345]: level=ERROR msg=\"encoding failed\" error=\"context deadline exceeded\" raw_id=\"a1b2c3d4\"'. Show the fix and the successful restart.",
    "Developer recorded the output of the training evaluation script. Include: 'Novel schema compliance: 10/10 (100%)\\nJSON valid: 10/10\\nSchema full: 10/10\\nUnique gists: 10/10\\nMean salience MAE: 0.12'. Discuss what each metric means.",
    "Observation about running rocm-smi to diagnose GPU issues before training. Include the actual output table showing: GPU 0, 72°C, 198W, 14.3GB/16GB VRAM, 87% utilization. Note the stale process from yesterday holding 2GB that needed to be killed.",
    "Developer documented a git bisect session to find a regression. Include: 'git bisect start', 'git bisect bad HEAD', 'git bisect good v0.8.0', then 5 bisect steps with commit hashes and test results, ending with 'abc1234 is the first bad commit'. Show the commit message that introduced the bug.",
    "Observation about running the hallucination stress test. Include the summary table output showing 7 tests, pass/fail for each model (Qwen 5/7, Gemma 5/7, Gemini 1/7), and the specific missing terms for each failure.",
    "Developer recorded make build output including a linker warning about unused symbol, the successful build to bin/mnemonic, and the subsequent systemctl --user restart mnemonic output confirming the new binary is live.",

    # Log file excerpts
    "Observation about analyzing daemon logs to find a pattern. Include 5 log lines showing the encoding agent failing repeatedly: timestamps, log levels, error messages, raw memory IDs. Note the pattern: all failures are for clipboard events with large content (> 10KB).",
    "Developer recorded the output of the database integrity check. Include: PRAGMA integrity_check output (ok), PRAGMA journal_mode (wal), PRAGMA user_version (15), and the FTS5 rebuild command with its output.",
    "Observation about a slow query identified in the daemon logs. Include the log line: 'level=WARN msg=\"slow query\" duration=4.5s query=\"SELECT * FROM memories WHERE ...\" rows=847'. Show the EXPLAIN QUERY PLAN output and the index that was missing.",

    # YAML/Config
    "Developer documented a reactor chain configuration. Include the YAML: chain name, priority, event_type trigger, conditions (cooldown 6h, db_size > 800MB), and action (trigger consolidation). Explain each field.",
    "Observation about adding a new watcher exclusion pattern via config. Include the before/after YAML diff for the perception.filesystem section, showing the new exclude pattern for *.pyc files and the glob syntax.",

    # Mixed format
    "Developer documented a curl command testing the MCP server, the JSON request body, and the JSON response. Include: curl -X POST localhost:9999/api/query -H 'Content-Type: application/json' -d '{\"query\": \"spread activation\", \"limit\": 5}' and the response with 5 memory summaries.",
    "Observation about a failed database migration. Include the SQL that was attempted, the SQLite error message, the PRAGMA user_version showing the stuck state, and the manual fix SQL.",
]

# ---------------------------------------------------------------------------
# Category C: Low-significance routine observations
# Most real observations are routine — the model needs to learn this
# ---------------------------------------------------------------------------

LOW_SIG_SYSTEM = (
    "You generate realistic, mundane developer observations about routine work. "
    "These are the boring, everyday things — small config tweaks, minor dependency "
    "updates, formatting fixes, routine deploys, standard maintenance. Keep them "
    "short (2-4 sentences). They should clearly be low-significance. "
    "Output ONLY the observation text."
)

LOW_SIG_SCENARIOS = [
    # Dependency updates
    "Updated Go module dependencies: go get -u ./... bumped 3 indirect dependencies. No breaking changes. Ran make test, all passing.",
    "Bumped transformers from 5.4.0 to 5.5.0 in the felixlm venv. No API changes affecting our training scripts. pip install --upgrade transformers completed without errors.",
    "Dependabot PR #380 merged: bumps golang.org/x/crypto from 0.31.0 to 0.32.0. Security patch for CVE-2026-xxxxx. No code changes required.",
    "Updated .gitignore to add *.pyc and __pycache__/ patterns. Was missing from the Python SDK directory.",
    "Ran go mod tidy — removed 2 unused indirect dependencies. go.sum reduced by 12 lines.",

    # Formatting and style
    "Ran go fmt ./... — fixed formatting in 3 files. No logic changes. Pre-commit hook caught this before commit.",
    "Fixed a typo in internal/agent/encoding/agent.go comment: 'compresion' -> 'compression'. No functional change.",
    "Renamed variable 'tmp' to 'tempMemory' in consolidation.go for clarity. No behavior change.",
    "Added missing copyright header to 4 new files. Standard boilerplate, no code changes.",
    "Reformatted config.example.yaml to align comments. Purely cosmetic.",

    # Routine operations
    "Ran make test — all 47 tests passing. No changes since last run, just verifying before a deploy.",
    "Restarted mnemonic daemon after config change: systemctl --user restart mnemonic. Verified healthy via curl localhost:9999/api/health.",
    "Cleared old log files from ~/.mnemonic/logs/. Freed 230MB. Logs older than 30 days.",
    "Ran git pull origin main — fast-forward, 2 new commits from Jason (Windows service support).",
    "Created new feature branch: git checkout -b feat/improve-encoding. Ready to start work.",
    "Cherry-picked commit abc1234 from the training branch to main. Clean apply, no conflicts.",
    "Ran golangci-lint run — 0 issues. Clean codebase.",
    "Updated the README with the new MCP tool count (24 tools). Minor doc update.",
    "Backed up the mnemonic database: cp ~/.mnemonic/mnemonic.db ~/.mnemonic/backups/2026-04-04.db. 487MB.",
    "Checked mnemonic daemon status: systemctl --user status mnemonic shows active (running), uptime 14 days, memory 340MB. All normal.",

    # Minor config tweaks
    "Changed consolidation.interval from 6h to 8h in config.yaml. Testing whether less frequent consolidation affects recall quality.",
    "Adjusted retrieval.max_hops from 3 to 4 in config.yaml. Want to see if deeper spread activation improves recall for loosely-related queries.",
    "Set perception.filesystem.debounce_ms from 100 to 200 in config.yaml. Reducing duplicate events during rapid file saves.",
    "Updated the LLM temperature from 0.7 to 0.6 for encoding. Slightly more deterministic output.",
    "Added a new exclusion pattern to perception: '*.tmp'. Temporary files were creating noise.",

    # Standard maintenance
    "Ran SQLite VACUUM on the mnemonic database. Size reduced from 512MB to 487MB. Took 3.2 seconds.",
    "Checked WAL file size: 2.3MB, normal range. Last checkpoint was 45 minutes ago.",
    "Verified FTS5 index health: ran test query, returned expected results. No rebuild needed.",
    "Rotated the daemon log file. Old log archived to logs/2026-04-03.log.gz (8.2MB compressed).",
    "Updated the launchd plist on the Mac Mini to increase the KeepAlive threshold. Minor operational tweak.",

    # Trivial observations
    "Switched VS Code theme. No impact on anything.",
    "Organized bookmarks in the browser. Found 3 useful SQLite FTS5 documentation links.",
    "Cleaned up old branches: deleted 5 merged feature branches from local and remote.",
    "Updated terminal prompt to show current git branch. Nice quality-of-life improvement.",
    "Added a shell alias: alias mn='systemctl --user status mnemonic'. Small convenience.",

    # Clipboard/terminal noise
    "Copied a UUID from the daemon logs for debugging: a1b2c3d4-e5f6-7890-abcd-ef1234567890.",
    "Ran 'which go' to verify Go installation path: /home/hubcaps/go-install/go/bin/go. Confirmed correct.",
    "Checked disk usage: df -h shows 45% used on /. Plenty of space.",
    "Ran 'uptime' — system up 23 days. No issues.",
    "Looked up the Go documentation for context.WithTimeout. Standard library reference, nothing new.",
]

# ---------------------------------------------------------------------------
# Category D: Emotionally varied observations
# Breaking out of the 91% "analytical" rut
# ---------------------------------------------------------------------------

EMOTIONAL_SYSTEM = (
    "You generate realistic developer observations with strong emotional coloring. "
    "The emotion should be natural and genuine — not exaggerated. Include specific "
    "technical details alongside the emotional context. The tone should be clear "
    "from the writing style without explicitly stating the emotion. "
    "Output ONLY the observation text."
)

EMOTIONAL_SCENARIOS = [
    # Frustrated
    "Frustrated debugging: spent 3 hours tracking a SQLite 'database is locked' error that only happens under load. The busy_timeout is set to 5000ms but the consolidation agent holds write locks for 6+ seconds during large merges. Every 'fix' introduces a new edge case. Tried reducing transaction scope, adding retry logic, increasing timeout — nothing works reliably. The fundamental problem is SQLite's single-writer model.",
    "Frustrated: the encoding agent keeps producing 'analytical' emotional_tone for EVERYTHING. A frustrated debugging rant gets 'analytical'. An excited feature launch gets 'analytical'. The training data is 91% analytical so of course the model learned this bias. Now I need to fix the data distribution before the MI300X run.",
    "Frustrated: the ROCm driver crashed again during training. No error message, just a hard GPU reset. rocm-smi shows the device but torch.cuda.is_available() returns False until reboot. This is the third time this week. AMD really needs to fix their driver stability on consumer cards.",
    "Frustrated with Gemini API reliability. 5 out of 10 encoding requests returned 503. The model is 'experiencing high demand' at 2pm on a Tuesday. This is why we built the local spoke model — can't depend on cloud APIs for a memory system that needs to work 24/7.",
    "Frustrated: the FTS5 tokenizer still splits 'middleware' into 'middle' and 'ware' even after switching to unicode61. Turns out I applied the migration to the wrong database (the test DB, not production). Facepalm moment. Applied to production, works now.",
    "Third attempt at getting gradient checkpointing to work with NF4 quantized Gemma 4. HuggingFace's implementation doesn't support SpokeWrappedLayer because the checkpoint boundary cuts the gradient flow. Tried 5 different workarounds. Finally gave up and disabled checkpointing, which means seq_len limited to 1024 on 16GB.",
    "Frustrated: accidentally pushed to main instead of the feature branch. Pre-commit hook caught the go fmt issue but didn't check the branch. Now I need to revert on main and cherry-pick to the right branch. Adding a branch check hook immediately.",
    "Spent 45 minutes debugging why the mnemonic daemon wasn't picking up config changes. Turns out I was editing config.yaml in the wrong directory — ~/Projects/mem/config.yaml instead of ~/.mnemonic/config.yaml. The daemon reads from the home directory location, not the repo.",

    # Excited / Positive
    "The Qwen spoke model just hit 100% novel schema compliance. 10 out of 10 completely new inputs, all valid JSON, all 10 fields present, all enum values correct. This is up from 60% on the old 100M model. The frozen base + spoke architecture actually works.",
    "Major breakthrough: removing the 1,420 poisoned compression/decompression examples from the training data fixed everything. Novel schema went from 70% to 100% overnight. The model was learning to generate fictional template patterns instead of real encodings. Data quality > data quantity.",
    "The stress test results are in: Qwen+Spokes 5/7, Gemma+Spokes 5/7, Gemini 1/7. Our 2B local model decisively beats the cloud API on our specific encoding task. And it runs with zero inference cost on consumer hardware.",
    "Just deployed the spoke server via serve_spokes.py. First end-to-end encoding through the daemon: 19.7 seconds, valid schema, correct concepts, reasonable salience. The local model is actually serving production traffic now. No cloud dependency.",
    "Scheduling dreaming for 2am-6am tripled insights. The overnight run processes a full day of memories with no competition for resources. Recall precision jumped from 0.42 to 0.67. This is a genuine improvement in memory quality from a simple scheduling change.",
    "The 3-level validation pipeline caught 166 bad examples in our training data that we'd been training on for weeks. 139 gists too long, 26 duplicates, 1 invalid enum. No wonder the model had quirks. Clean data makes everything better.",
    "EXP-20 data generation is going smoothly. Gemini Batch API processed 1,099 encoding requests with 100% success rate at 8192 max tokens. Zero rate limits, 50% cheaper than individual calls. Should have been using batch from the start.",
    "The Felix-LM spoke architecture just proved itself: we can train task-specific adapters (25M params, 0.7% overhead) on a frozen 3.5B base and get 100% schema compliance on a specialized task. The post-and-spoke vision is working.",

    # Concerned / Worried
    "Concerned about the embedding index scaling. Linear scan of 12,847 vectors takes 4.2ms now, but it's O(n). At 100K memories that's ~33ms. At 1M it's 330ms — too slow for interactive recall. Need to plan the migration to approximate nearest neighbors before we hit 50K.",
    "Worried about the training data distribution. 91% of our data has emotional_tone='analytical'. The model will default to analytical for everything, even frustrated debugging rants or excited breakthroughs. This is a systematic bias that will take hundreds of varied examples to fix.",
    "Concerned about the reliance on a single GPU. The RX 7800 XT handles inference fine for now, but if it fails there's no fallback. The daemon should have a graceful degradation path — maybe fall back to Gemini API when the local model is unavailable.",
    "Worried about the MI300X training run cost. If the hyperparameters are wrong or the data has issues we discover mid-training, we've wasted paid GPU time. Need to validate everything locally first. The smoke test on the RX 7800 XT is critical.",
    "Concerned that we're overfitting to the stress test. We're generating targeted data specifically to pass 7/7 on those 7 inputs. But production will throw thousands of different inputs. Are we teaching the model to pass a test or to be genuinely robust?",
    "Security concern: the terminal watcher captured 'export LLM_API_KEY=...' and it became a memory. We need a secrets filter in the perception pipeline. Regex for common patterns: sk-, ghp_, AKIA, API_KEY=, password=, token=.",

    # Reflective / Retrospective
    "Looking back at the last 19 experiments, the biggest lesson is that data quality matters more than model architecture. EXP-15 (rotation) and EXP-15b (bottleneck rotation) added architectural complexity but didn't improve quality. EXP-17 (clean data) was the breakthrough — same architecture, better data, 100% compliance.",
    "Reflecting on the decision to use SQLite over Postgres. 6 months in, it was the right call. The daemon runs on consumer hardware where Postgres would be deployment overhead. WAL mode handles our concurrency needs. The only pain point is the single-writer lock during consolidation, and that's manageable with transaction scope optimization.",
    "Retrospective on the mnemonic project so far: started as a simple memory daemon, evolved into a multi-agent system with 8 cognitive agents, a custom LLM architecture (Felix-LM spokes), and a sophisticated data pipeline. The scope grew but each piece justified itself. The encoding quality would be impossible without the spoke model.",
    "Reflecting on the difference between the 100M model and Qwen 3.5 2B. The 100M model could follow the schema ~60% of the time but couldn't generalize to novel inputs. The 2B model with spokes gets 100% on novel inputs. The extra 2.9B parameters of the frozen base provide the general knowledge; the 25M spoke parameters adapt it to our task. This is the core insight of the spoke architecture.",
    "Looking back at the poison data incident — 37% of our training data was synthetic garbage from an earlier compression experiment. It took us until EXP-17 to find it. The lesson: always validate training data before committing to a full training run. The 3-level validation pipeline we built for EXP-20 should have existed from EXP-1.",
    "Retrospective on choosing Qwen over Gemma for production. Both models achieve 100% schema. Gemma is architecturally more interesting (PLE, native thinking mode, 128K context) but pragmatically Qwen wins: native bf16 on 16GB, 1.7x faster, simpler inference pipeline. Engineering decisions should favor what ships, not what's elegant.",
    "Reflecting on how mnemonic's architecture evolved. Started with direct agent calls, moved to event bus. Started with single LLM provider, now have spoke routing. Started with manual memory creation, now have watchers (filesystem, terminal, clipboard, git). Each evolution was driven by a specific pain point, not speculative design.",
    "One year of mnemonic development. The project started as 'what if an AI coding agent could remember things between sessions.' Now it's a daemon with genuine cognitive capabilities — perception, encoding, retrieval, consolidation, dreaming, episoding, abstraction, metacognition. The dreaming agent generating insights at 2am that improve recall quality the next morning is still the most surprising emergent behavior.",
]

# Combine all with metadata
for s in LONG_FORM_SCENARIOS:
    SCENARIOS.append({"text": s, "system": LONG_FORM_SYSTEM, "category": "long_form"})
for s in CODE_FORMAT_SCENARIOS:
    SCENARIOS.append({"text": s, "system": CODE_FORMAT_SYSTEM, "category": "code_format"})
for s in LOW_SIG_SCENARIOS:
    SCENARIOS.append({"text": s, "system": LOW_SIG_SYSTEM, "category": "low_significance"})
for s in EMOTIONAL_SCENARIOS:
    SCENARIOS.append({"text": s, "system": EMOTIONAL_SYSTEM, "category": "emotional_variety"})


GEN_PROMPT_TEMPLATE = (
    "Rewrite this scenario as a natural developer observation. Preserve ALL technical "
    "details verbatim. Output ONLY the observation text, no markdown fences.\n\n"
    "Scenario: {scenario}"
)


def build_batch_requests() -> list[dict]:
    requests = []
    for i, s in enumerate(SCENARIOS):
        requests.append({
            "key": f"balance-{i}",
            "request": {
                "contents": [{"parts": [{"text": GEN_PROMPT_TEMPLATE.format(scenario=s["text"])}]}],
                "system_instruction": {"parts": [{"text": s["system"]}]},
                "generation_config": {
                    "temperature": 0.8,
                    "max_output_tokens": 4096,
                },
            },
        })
    return requests


def submit():
    from google import genai
    from google.genai import types
    client = genai.Client(api_key=API_KEY)

    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)

    # Save metadata
    meta_path = OUTPUT_DIR / "balance_meta.jsonl"
    with open(meta_path, "w") as f:
        for i, s in enumerate(SCENARIOS):
            f.write(json.dumps({"key": f"balance-{i}", "category": s["category"], "scenario": s["text"][:200]}) + "\n")

    # Build batch
    requests = build_batch_requests()
    batch_path = OUTPUT_DIR / "balance_batch_requests.jsonl"
    with open(batch_path, "w") as f:
        for r in requests:
            f.write(json.dumps(r) + "\n")

    from collections import Counter
    cats = Counter(s["category"] for s in SCENARIOS)
    print(f"Total scenarios: {len(SCENARIOS)}")
    for k, v in cats.most_common():
        print(f"  {k}: {v}")

    # Upload and submit
    uploaded = client.files.upload(file=str(batch_path), config=types.UploadFileConfig(display_name="balance-rawgen", mime_type="jsonl"))
    job = client.batches.create(model=MODEL, src=uploaded.name, config={"display_name": "mnemonic-balance-rawgen"})
    print(f"\nJob: {job.name}")
    print(f"State: {job.state.name}")
    print(f"\nCheck: python generate_distribution_balance.py status --job {job.name}")


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

    # Load metadata for category mapping
    meta = {}
    for line in open(OUTPUT_DIR / "balance_meta.jsonl"):
        m = json.loads(line)
        meta[m["key"]] = m["category"]

    output_path = OUTPUT_DIR / "balance_raw_inputs.jsonl"
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
                cat = meta.get(r.get("key", ""), "unknown")
                f.write(json.dumps({
                    "raw_input": text,
                    "source": f"targeted_{cat}",
                    "task_type": "encoding",
                    "category": cat,
                }) + "\n")
                success += 1
            except (KeyError, IndexError, json.JSONDecodeError):
                fail += 1

    from collections import Counter
    cats = Counter()
    for line in open(output_path):
        cats[json.loads(line)["category"]] += 1

    print(f"Results: {success} success, {fail} fail ({success/(success+fail)*100:.1f}%)")
    print(f"Written to: {output_path}")
    for k, v in cats.most_common():
        print(f"  {k}: {v}")
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
        from collections import Counter
        cats = Counter(s["category"] for s in SCENARIOS)
        print(f"Total: {len(SCENARIOS)}")
        for k, v in cats.most_common():
            print(f"  {k}: {v}")
    elif args.command == "status":
        check_status(args.job)
    elif args.command == "download":
        download(args.job)
    else:
        parser.print_help()


if __name__ == "__main__":
    main()
