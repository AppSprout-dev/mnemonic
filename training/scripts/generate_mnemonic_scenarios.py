#!/usr/bin/env python3
"""Generate 500+ mnemonic-specific training scenarios using real codebase details.

These are submitted to Gemini Batch API for raw input generation, then
encoded in a second batch. Every scenario uses real file paths, function names,
struct names, and agent names from the mnemonic codebase.

Usage:
    # Create batch file and submit
    LLM_API_KEY=... python generate_mnemonic_scenarios.py submit

    # Check status
    LLM_API_KEY=... python generate_mnemonic_scenarios.py status --job batches/JOB_ID

    # Download results
    LLM_API_KEY=... python generate_mnemonic_scenarios.py download --job batches/JOB_ID
"""

import argparse
import json
import os
import random
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

API_KEY = os.environ.get("LLM_API_KEY", "")
MODEL = "gemini-3.1-pro-preview"
OUTPUT_DIR = Path("training/data/targeted")

# ---------------------------------------------------------------------------
# 500+ mnemonic-specific scenarios organized by subsystem
# Each scenario is a seed that Gemini will expand into a natural observation
# ---------------------------------------------------------------------------

SCENARIOS = []

# ---- Perception Agent (internal/agent/perception/agent.go) ----
SCENARIOS.extend([
    "PerceptionAgent.processEvent() filtered 340 filesystem events down to 12 meaningful observations. Ignored node_modules (180), .git objects (95), tmp files (42). Kept Go source changes (7), config edits (3), doc updates (2). Heuristic scores ranged from 0.12 to 0.89.",
    "PerceptionAgent.callLLMGate() timed out after 10 seconds on a large clipboard paste (8KB of base64 image data). Fell back to heuristic score of 0.34, which was below the 0.5 threshold. Event correctly filtered out.",
    "contentHash() produced duplicate hash for two different file events — both were config.yaml edits within 500ms. The SHA256 matched because content was identical (same line changed twice). Dedup correctly prevented duplicate raw memory.",
    "PerceptionAgent.isRecentGitOp() detected .git/FETCH_HEAD mtime update, suppressed 47 filesystem events from a git pull. Without this guard, each file change would have created a separate raw memory.",
    "PerceptionAgent.promoteExclusion() added pattern '*.pyc' at runtime after 200 Python bytecode events in 5 minutes. The exclusion reduced filesystem event volume by 60% for the Python SDK directory.",
    "Bug: PerceptionAgent.Start() initialized filesystem watcher but terminal watcher failed (zsh history file permission denied). Agent continued with partial watcher set — terminal commands weren't captured for 3 days until the permission was fixed.",
    "PerceptionAgent heuristic filter scored a 2-line Go comment change at 0.08 (below 0.1 threshold). Correctly filtered — trivial formatting changes shouldn't become memories. But a similar 2-line change that fixed a nil pointer scored 0.72 because it contained error-related keywords.",
    "LLM gate returned invalid JSON for a clipboard event containing mixed Japanese and English text. parse_json_response() fallback found the JSON object nested inside markdown fences. Extracted successfully, relevance score 0.65.",
])

# ---- Encoding Agent (internal/agent/encoding/agent.go) ----
SCENARIOS.extend([
    "EncodingAgent.encodeRawMemory() processed raw memory ID a1b2c3d4 in 19.7 seconds via Qwen spoke model on port 8899. Schema compliance: 10/10 fields valid. Concepts extracted: [go, debugging, nil-pointer, agent, retrieval]. Salience: 0.78.",
    "EncodingAgent.callCompressionLLM() failed with malformed JSON — the Qwen spoke model returned a trailing comma after the last structured_concepts.causality entry. parse_json_response() recovered by stripping the comma. encoding.go:95 logged the warning.",
    "EncodingAgent backoff triggered after 3 consecutive encoding failures (LLM timeout at 30s each). Backoff delay: 30s base * 2^2 = 120 seconds. During backoff, 8 raw memories queued up. After recovery, processed all 8 in 3 minutes.",
    "EncodingAgent.extractConcepts() mapped raw input about SQLite WAL mode to concepts: [sqlite, database, performance, wal, concurrent-reads]. The controlled vocabulary matched 4/5 concepts. 'wal' was kept as a free-form concept.",
    "EncodingAgent.deduplicateSimilar() found cosine similarity 0.92 between a new memory about 'SQLite lock timeout' and an existing memory about 'SQLite busy timeout configuration'. Threshold is 0.85, so it flagged as near-duplicate. Kept the new one (higher salience 0.75 vs 0.45).",
    "EncodingAgent.generateEmbedding() called hugot BatchEmbed with 32 texts. 3 texts exceeded the 512-token limit and were truncated. Embedding dimensions: 384. Total batch time: 1.2 seconds.",
    "Encoding queue backed up to 47 items during a heavy coding session. Dashboard at http://127.0.0.1:9999/ showed the queue growing. Normal depth is 0-3. Root cause: the spoke server was processing each memory in 20s and only handles one at a time (GENERATE_LOCK serialization).",
    "coaching.yaml was updated with new instructions to preserve file:line references verbatim. EncodingAgent.callCompressionLLM() picked up the change on the next encoding cycle without restart. Encoding quality for stack traces improved — spread.go:142 now preserved in content field.",
    "EncodingAgent processed a raw memory from the clipboard watcher that contained a 50KB base64-encoded image. The LLM call took 45 seconds and returned a generic 'image data pasted' encoding. Salience correctly set to 0.1 (trivial).",
])

# ---- Retrieval Agent (internal/agent/retrieval/agent.go) ----
SCENARIOS.extend([
    "RetrievalAgent.Query() for 'WebSocket race condition' found 5 direct matches via FTS5, spread activation expanded to 8 more via associations (decay factor 0.7). Top result: a decision about mutex locking in handler.go from 3 weeks ago. Activation: query -> memory_a (0.95) -> memory_b (0.67) -> memory_c (0.47).",
    "RetrievalAgent.spreadActivation() hit max hops (3) while traversing associations from a memory about 'SQLite WAL mode'. The activation path: WAL config (0.95) -> concurrent reads (0.67) -> connection pooling (0.47) -> stopped. Without the hop limit, it would have reached unrelated Redis memories.",
    "RetrievalAgent.rankResults() combined scores: FTS match 0.82, embedding similarity 0.76, recency bonus 0.15 (3 days old), source weight 1.5 (MCP source), feedback adjustment +0.12 (marked helpful twice). Final score: 2.35. Ranked #1 out of 8 results.",
    "RetrievalAgent.synthesizeResults() called the LLM to generate a narrative over 5 recall results about 'encoding quality improvements'. Synthesis took 4.2 seconds. The narrative correctly linked the poison data removal (EXP-17) to the schema compliance improvement.",
    "RetrievalAgent.diversifyResults() applied maximal marginal relevance with lambda=0.7. Removed 2 of 7 results that were near-duplicates (cosine sim > 0.9). Final result set: 5 diverse memories covering different aspects of the query.",
    "Bug: RetrievalAgent.Query() returned memories from project 'felixlm' when querying project 'mnemonic'. Root cause: FTS5 query path in store/sqlite/retrieval.go:156 didn't include the project filter. Vector search path did filter correctly. Fixed by adding 'AND project = ?' to the FTS5 query.",
    "recall with synthesize=true timed out after 30 seconds. The LLM was hung on a complex synthesis of 10 results. MCP client received empty response. Partial results (the 10 memories) were available but not returned. Need per-tool timeout config.",
    "batch_recall processed 3 parallel queries at session start: 'project context' (4 results), 'recent errors' (2 results), 'training decisions' (6 results). Total time: 340ms. Spread activation found 2 cross-linked memories between training and daemon error categories.",
])

# ---- Consolidation Agent (internal/agent/consolidation/agent.go) ----
SCENARIOS.extend([
    "ConsolidationAgent.runCycle() completed: processed 847 memories, decayed 23 below threshold (0.1), merged 4 near-duplicate pairs, extracted 2 new patterns, pruned 12 weak associations (strength < 0.3). Total cycle time: 12.4 seconds.",
    "ConsolidationAgent.decayMemories() applied salience decay with recency protection: memories < 24h old got 0.8x decay factor, 24h-168h got 0.9x, older got full 0.95x. 23 memories dropped below archive threshold (0.1) and were archived.",
    "ConsolidationAgent.mergeMemories() found 4 pairs with cosine similarity > 0.85. Created 4 merged 'gist_of' memories. One pair was two separate memories about the same SQLite FTS5 migration — merged into a single comprehensive memory with higher salience.",
    "ConsolidationAgent.extractPatterns() identified pattern 'test before commit' from 15 evidence memories across 8 sessions. Pattern strength: 0.92. The LLM correctly identified the recurring behavior and generated a description linking it to successful PR merges.",
    "ConsolidationAgent.pruneAssociations() removed 12 associations with strength < 0.3. One removed association linked 'ROCm GPU setup' to 'breakfast recipe' (strength 0.05) — a spurious cross-pollination from the dreaming agent. Correct pruning.",
    "Bug: ConsolidationAgent.mergeMemories() created merged memory M1. Next cycle, M1's similarity to another memory M2 was 0.87. Merged again into M3. Original memories lost in the chain. Need to mark merged memories as immutable to prevent cascade.",
    "Consolidation held a long-running read transaction for 45 seconds during a large merge operation. SQLite WAL file grew to 890MB. The dreaming agent's write was blocked. Fixed by adding a 10-second timeout on consolidation reads in store/sqlite/consolidation.go:178.",
    "ConsolidationAgent salience floor issue: decay_rate 0.95, access_resistance_cap 0.3, recency_protection 0.9 — these combine so that frequently-accessed memories never reach archive_threshold 0.1. 340 'zombie' memories stuck between 0.15 and 0.25 salience indefinitely.",
])

# ---- Dreaming Agent (internal/agent/dreaming/agent.go) ----
SCENARIOS.extend([
    "DreamingAgent.runCycle() at 2:00am: replayed 50 high-salience memories, strengthened 12 associations, generated 3 new insights. One insight linked 'exponential backoff in API retry' with 'consolidation decay formula' — both use exponential decay patterns. Confidence: 0.67.",
    "DreamingAgent.replayMemories() selected 50 memories by salience (top 5%) for replay. The LLM identified 3 cross-domain connections: (1) caching patterns across API and database layers, (2) error handling similarities in encoding and retrieval agents, (3) configuration management patterns across daemon and CLI.",
    "DreamingAgent.strengthenAssociations() boosted 12 existing associations based on LLM analysis. Strongest boost: +0.15 for the link between 'SQLite WAL mode decision' and 'concurrent read performance' (from 0.72 to 0.87).",
    "Scheduling dreaming for 2am-6am tripled insights compared to the previous 8am schedule. The 2am run processes a full day's memories with no competition for LLM resources. Recall precision improved from 0.42 to 0.67 after a week of nightly dreaming.",
    "Bug: DreamingAgent.generateInsights() hallucinated a connection between 'Redis caching' and 'authentication middleware' — the only shared concept was 'timeout'. The spurious association was committed to the store. Metacognition agent flagged it 2 cycles later. Need user feedback loop before committing insights.",
    "DreamingAgent selected 10 memories about git commits for replay. LLM generated the same insight 10 times: 'developer follows commit-then-push pattern'. Database flooded with duplicate insights. Need to deduplicate LLM outputs by concept similarity before writing.",
    "DreamingAgent.runCycle() at 2pm produced only 1 insight (vs 3 at 2am). Hypothesis: fewer unprocessed memories accumulate by midday. The 2pm run acts as a catch-up for morning work, but most consolidation already happened overnight.",
])

# ---- Episoding Agent (internal/agent/episoding/agent.go) ----
SCENARIOS.extend([
    "EpisodingAgent.clusterMemoriesIntoEpisodes() grouped 12 raw memories into 3 episodes: (1) 'debugging spread activation panic' (4 memories, 10:15-10:35am), (2) 'config.yaml tuning' (3 memories, 10:40-10:55am), (3) 'training data review' (5 memories, 11:00-11:30am). Minimum 2 events per episode.",
    "EpisodingAgent.synthesizeEpisodeTitle() called LLM to name episode containing 4 memories about nil pointer debugging. Generated title: 'Nil pointer fix in spread activation retrieval path'. Took 3.2 seconds.",
    "Episoding timestamp skew: terminal watcher reported command at 10:05am, filesystem watcher reported the same file edit at 10:01am. The 4-minute gap split what should have been one episode into two separate episodes. Need to use central clock instead of event timestamps.",
    "EpisodingAgent processed 0 raw memories for 6 hours — the encoding agent was in backoff mode and no new encoded memories were being created. Episode gap in the timeline from 2pm to 8pm.",
])

# ---- Abstraction Agent (internal/agent/abstraction/agent.go) ----
SCENARIOS.extend([
    "AbstractionAgent.evaluatePattern() promoted pattern 'test before commit' (strength 0.92, 15 evidence memories) to principle. The LLM generated: 'Code changes should be tested before committing to maintain CI stability and prevent regression.' Confidence: 0.88.",
    "AbstractionAgent.deriveAxiom() synthesized axiom from 3 principles about code quality: 'Quality gates at each stage (test, review, CI) compound to produce reliable software.' This is mnemonic's first axiom — level 3 abstraction.",
    "User called dismiss_pattern via MCP for pattern 'always restart daemon after config change' — it was too obvious to keep surfacing in recall results. Pattern archived, no longer returned by get_patterns.",
    "AbstractionAgent confidence decay: user marked principle about 'alphabetical imports' as irrelevant via feedback. Confidence decayed by 0.85x from 0.78 to 0.66. Two more negative feedbacks would push it below the archive threshold of 0.5.",
    "AbstractionAgent found circular dependency: Axiom A derived from Principle P. Pattern X feeds evidence to Axiom A. Axiom A used to evaluate Pattern Y, which refined Principle P. The cycle was detected by DAG validation and the newest link was rejected.",
])

# ---- Metacognition Agent (internal/agent/metacognition/agent.go) ----
SCENARIOS.extend([
    "MetacognitionAgent.runCycle() reviewed 50 recent encodings. Found 3 with missing structured_concepts.entities (person names dropped), 2 with salience > 0.9 for routine events, 1 with fabricated entity 'DataManager' not in original input. Flagged for review via get_insights.",
    "MetacognitionAgent.analyzeMemoryCohesion() found 34 orphaned memories with zero associations. These were all from a single ingest_project run that didn't generate embeddings. Recommended re-encoding with association linking.",
    "MetacognitionAgent.detectAnomalies() identified that 89% of memories in the last week have emotional_tone='analytical'. Flagged as potential bias in the encoding model — the spoke model may be defaulting to 'analytical' regardless of input content.",
    "Metacognition observation backpressure: the agent wrote 200 observations in a single cycle after a long dreaming session. Store write queue was saturated for 8 seconds. Recall latency spiked to 2.3 seconds during the write burst. Need to batch MetaObservation writes.",
])

# ---- Orchestrator (internal/agent/orchestrator/orchestrator.go) ----
SCENARIOS.extend([
    "Orchestrator.checkLLMHealth() pinged the spoke server at http://localhost:8899/health — returned healthy in 12ms. But the actual encoding model was corrupted (GGUF partial download). Health check only tests network connectivity, not model quality.",
    "Orchestrator.runSelfTest() queried 3 known patterns and verified recall returned relevant memories for each. Pass rate: 3/3. Self-test latency: 890ms total. Health report written to ~/.mnemonic/health.json.",
    "Orchestrator.checkStoreHealth() called store.CountMemories() — returned 12,847 total memories (10,234 active, 1,891 fading, 722 archived). DB size: 487MB. Below the 1GB threshold so no consolidation trigger.",
    "Orchestrator adaptive intervals: encoding queue depth was 0 for 2 hours, so orchestrator increased the consolidation interval from 6h to 12h. When encoding queue spiked to 15 items, it dropped the interval back to 6h. Adaptive scheduling based on system load.",
    "Orchestrator detected store health degradation: CountMemories latency increased from 2ms to 450ms over 3 days. Likely cause: FTS5 index fragmentation. Recommended running 'INSERT INTO memories_fts(memories_fts) VALUES(\"rebuild\")' via the API consolidation endpoint.",
])

# ---- Reactor (internal/agent/reactor/engine.go) ----
SCENARIOS.extend([
    "Reactor.handleEvent() processed ConsolidationCompletedEvent. Matched chain 'post-consolidation-dream' (priority 10). CooldownCondition checked last execution (4 hours ago, cooldown is 6h) — condition failed. Action not fired. Dreaming agent will wait 2 more hours.",
    "Reactor.handleEvent() processed MemoryEncodedEvent. Matched 2 chains: (1) 'update-embedding-index' (priority 5, always fires), (2) 'check-dedup' (priority 8, fires if memory count > 10000). Both actions executed successfully.",
    "Reactor DBSizeCondition estimated database at 890MB (threshold 800MB). Triggered 'emergency-consolidation' chain. ConsolidationAgent.runCycle() was invoked with aggressive settings: archive_threshold raised from 0.1 to 0.2, pruned 340 low-salience memories.",
    "Bug: Reactor CooldownCondition race — two MemoryEncodedEvents arrived 50ms apart. Both checked LastExecution map (no entry). Both passed cooldown check. Both fired the dedup action. Two concurrent dedup runs conflicted on store writes. Need mutex on LastExecution update.",
    "Reactor SendToChannelAction failed silently — the consolidation trigger channel was full (agent hung in a long merge). Select default case fired, no error logged. Consolidation never ran. Fixed by logging at WARN level in reactor/actions.go.",
])

# ---- Store / SQLite (internal/store/sqlite/) ----
SCENARIOS.extend([
    "SQLiteStore.WriteMemory() inserted memory with 384-dimensional embedding vector. loadEmbeddingIndex() added it to the in-memory cosine similarity index. Total index size: 12,847 vectors. Peak RAM for index: 19MB.",
    "SQLiteStore.SearchByEmbedding() linear scan of 12,847 vectors took 4.2ms. Top 5 results by cosine similarity: 0.94, 0.87, 0.82, 0.79, 0.76. This is fast enough for interactive recall but will need approximate nearest neighbors at 100K+ memories.",
    "SQLiteStore.SearchFTS() query 'authentication middleware' returned 12 results via FTS5 with unicode61 tokenizer. Previously returned 0 results with the default tokenizer because it split 'middleware' into 'middle' + 'ware'. Migration 005 fixed this.",
    "SQLite WAL checkpoint completed after a consolidation cycle. WAL file shrank from 45MB to 2MB. Checkpoint mode: PASSIVE (doesn't block readers). WAL was growing because consolidation held a read transaction for 12 seconds.",
    "Schema migration 14->15: added 'version' column to memories table for optimistic locking. Migration wrapped in transaction. Index creation on version column took 3.4 seconds for 10K memories. PRAGMA user_version updated to 15.",
    "SQLiteStore.RawMemoryExistsByHash() prevented duplicate raw memory creation. Two filesystem events with identical SHA256 hashes (same file content, 200ms apart) — second was rejected. Dedup working correctly.",
    "Bug: SQLite lock timeout during concurrent access. Encoding agent held a write lock for 6 seconds while processing a large memory. Retrieval agent's read query waited 5 seconds (busy_timeout) then failed with 'database is locked'. Fixed by reducing encoding transaction scope.",
    "store.CountMemories() returned unexpected results: 10,234 active but only 9,100 had embeddings in the in-memory index. 1,134 memories were missing embeddings — they were created during a period when the embedding model was down. Fixed by re-embedding on startup.",
    "SQLite FTS5 trigger corruption: a power failure during memory insertion left the FTS index out of sync with the memories table. Full-text searches were missing 3 recent memories. Fixed by running 'INSERT INTO memories_fts(memories_fts) VALUES(\"rebuild\")'.",
])

# ---- MCP Server (internal/mcp/server.go) ----
SCENARIOS.extend([
    "MCP remember: Claude Code stored decision about choosing JWT over sessions for API auth. Type: decision, project: mnemonic, salience: 0.75. Encoding agent processed in 18.2s via spoke model. Concepts: [authentication, security, api, scaling, jwt].",
    "MCP recall: query='SQLite FTS5 migration' returned 3 results in 120ms. Top result (salience 0.89) was a decision from 2 weeks ago about switching tokenizers. Claude used it to avoid re-investigating the same issue. Feedback submitted: helpful.",
    "MCP batch_recall: session start with 3 parallel queries. Results: 'project context' (4 memories), 'recent errors' (2 memories), 'training decisions' (6 memories). Total: 12 memories in 340ms. Cross-linked memories found between training and error categories.",
    "MCP amend: updated memory about SQLite schema from 'using FTS4' to 'migrated to FTS5 with unicode61 tokenizer'. Preserved 4 existing associations and bumped version from 2 to 3. The original memory was from 6 sessions ago.",
    "MCP create_handoff: session summary with 8 decisions, 3 errors, 2 insights. Salience: 0.95. Total handoff text: 1,200 words. Encoding took 34 seconds via spoke model — longer than usual due to the large input size.",
    "MCP feedback: recall query 'authentication middleware' rated as 'partial' — 2 of 5 results were relevant (the JWT decision and the rate limiting fix), 3 were noise (unrelated security memories). Feedback adjusted association strengths for 455 linked memories.",
    "MCP get_context: proactive suggestions returned 3 memories relevant to current file being edited (internal/api/routes/memories.go). The activity watcher detected the file open event and the daemon surfaced related memories about API route patterns.",
    "MCP get_patterns: returned 5 active patterns with min_strength=0.7. Top pattern: 'test before commit' (strength 0.92, 15 evidence). User dismissed pattern 'restart after config change' (too obvious) via dismiss_pattern.",
    "MCP session_summary: summarized current session — 12 remember calls, 8 recall calls, 3 feedback submissions, 1 handoff. Session duration: 2.5 hours. Top concepts: [encoding, training, data-quality, spoke].",
    "MCP ingest_project: bulk-loaded ~/Projects/felixlm/ into mnemonic. Processed 847 files, created 312 raw memories (filtered by .gitignore and file size limits). Ingest took 45 seconds for directory traversal, encoding queue has 312 items pending.",
    "MCP tool error: recall returned 0 results for 'authentication middleware' despite 12 relevant memories. Root cause: FTS5 tokenizer was splitting 'middleware' into 'middle' + 'ware'. Fixed by switching to unicode61 tokenizer.",
    "MCP list_sessions: returned 15 recent sessions. Most active: session from 3 hours ago (34 memories). Oldest: session from 2 weeks ago. Sessions with handoffs highlighted for easy context retrieval.",
    "MCP exclude_path: added '*.pyc' exclusion at runtime. Watcher immediately stopped tracking Python bytecode files. 200 pending filesystem events for .pyc files were dropped from the queue.",
])

# ---- LLM Provider (internal/llm/) ----
SCENARIOS.extend([
    "LLM provider switched from Gemini API to local Qwen spoke server. Config change: llm.endpoint from 'https://generativelanguage.googleapis.com/...' to 'http://localhost:8899/v1'. Encoding latency increased from 7.3s to 19.7s but reliability went from 50% to 100%.",
    "EmbeddedProvider.BatchEmbed() processed 32 texts in 1.2 seconds via hugot library. 3 texts exceeded 512-token limit and were truncated. Total embedding dimensions: 384. Memory usage: 45MB peak.",
    "Hugot embedding batch failure: 429 error after processing 200 memories. Batch size of 100 was too aggressive. Reduced to 32 with 500ms delays between batches. Error in internal/llm/hugot.go:134. Total re-embedding took 45 minutes for 10K memories.",
    "serve_spokes.py GENERATE_LOCK serialization: one encoding request blocks all others. During peak load (15 raw memories queued), average wait time was 5 minutes per memory. The single-GPU constraint means no parallelism. Throughput: ~3 memories per minute.",
    "LLM structured output parsing: Qwen spoke model returned JSON with thinking tags (<think>...</think>) before the JSON object. parse_json_response() stripped the tags and extracted valid JSON. This happens on ~5% of generations.",
])

# ---- Watcher Subsystem (internal/watcher/) ----
SCENARIOS.extend([
    "FilesystemWatcher (Linux/fsnotify): added watches on 847 directories under ~/Projects/mem/. Total inotify watches: 2,341 (system limit: 8192). Hot directory tracking: internal/agent/ promoted to hot after 15 events in 5 minutes.",
    "FilesystemWatcher (macOS/fsevents): latency set to 500ms. During a heavy refactoring session, 200 file changes in 3 seconds were coalesced into 45 events. Each event created a raw memory — the perception agent's heuristic filter reduced to 12 meaningful observations.",
    "TerminalWatcher.pollHistory() detected 5 new bash commands: 'make build', 'systemctl --user restart mnemonic', 'curl localhost:9999/api/health', 'git diff', 'git add internal/agent/encoding/agent.go'. Each became a raw memory. The password regex excluded 'export LLM_API_KEY=...'.",
    "ClipboardWatcher detected a 10KB JSON paste — a Gemini API response being inspected. MaxContentBytes (1MB) was not exceeded. Content hash was unique, so it became a raw memory. The perception agent scored it 0.72 (contains technical data).",
    "GitWatcher.pollRepositories() detected HEAD change in ~/Projects/mem/. Set git sentinel flag. PerceptionAgent.isRecentGitOp() suppressed 47 filesystem events from the subsequent git operation. Single 'repo_changed' event created instead.",
    "Bug: FilesystemWatcher on Linux hit inotify limit (8192) after watching 3 large project directories. New directories silently ignored. Changes in newly created subdirectories were missed for 2 hours until the daemon was restarted with a higher limit.",
    "Watcher debounce issue: config.yaml edited, 500ms debounce timer started. User made another edit 400ms later. First timer cancelled, new timer started. But the perception agent had already processed the first event. Duplicate raw memory created.",
])

# ---- Daemon / Service Management (internal/daemon/) ----
SCENARIOS.extend([
    "systemctl --user restart mnemonic: service stopped (SIGTERM), PID file cleaned, new instance started in 1.2 seconds. All agents re-initialized, embedding index reloaded (12,847 vectors in 340ms), FTS5 index intact.",
    "Daemon install on Linux: systemctl --user enable mnemonic.service succeeded but service didn't start at boot. Root cause: loginctl enable-linger not set. After running 'loginctl enable-linger hubcaps', daemon starts at boot without requiring login session.",
    "Stale PID file: daemon crashed, PID file ~/.mnemonic/mnemonic.pid not cleaned up. User ran 'mnemonic start', checked PID file, found PID 12345. kill -0 12345 succeeded (PID reused by another process). New daemon didn't start. Fixed by checking command line of PID process.",
    "mnemonic serve (foreground mode): started with config.yaml, all 8 agents initialized. Dashboard available at http://127.0.0.1:9999/. CTRL+C sends SIGINT, graceful shutdown takes 2.3 seconds (waits for in-flight encoding to complete).",
    "macOS launchd plist had wrong binary path — pointed to /usr/local/bin/mnemonic but binary was at ~/go/bin/mnemonic. Jason reported the Mac Mini deployment failing. Updated com.appsprout.mnemonic.plist and ran launchctl load.",
    "Windows Service: mnemonic install registered with Service Control Manager. 'mnemonic start' maps to sc start mnemonic. Service runs as LocalSystem. Logs go to Windows Event Log instead of stderr.",
])

# ---- Event Bus (internal/events/) ----
SCENARIOS.extend([
    "InMemoryBus.Publish() dispatched MemoryEncodedEvent to 4 subscribers: retrieval (update index), reactor (check rules), episoding (cluster), metacognition (audit). All handlers completed in 12ms total. No errors.",
    "Event bus handler panic: RetrievalAgent's MemoryEncoded handler panicked on a nil embedding vector. Bus didn't recover. All subsequent MemoryEncoded events to retrieval were lost. 47 memories didn't get indexed. Fixed by adding recover() in bus dispatch.",
    "Event ordering issue: ConsolidationAgent published PatternDiscovered. AbstractionAgent subscribed and immediately queried store for the pattern. But the store write hadn't completed yet (publish returned before write finished). AbstractionAgent found nothing. Fixed by ensuring publish waits for store write.",
    "InMemoryBus.Subscribe() registered 23 total handlers across 8 agents. Event type distribution: MemoryEncoded (4 handlers), ConsolidationCompleted (3), DreamCycleCompleted (2), PatternDiscovered (2), others (12).",
])

# ---- Config (internal/config/) ----
SCENARIOS.extend([
    "Config.Load() parsed config.yaml: llm.endpoint=http://localhost:8899/v1, llm.chat_model=qwen-spokes, store.db_path=~/.mnemonic/mnemonic.db, consolidation.interval=6h, dreaming.schedule='0 2 * * *'. All fields validated.",
    "Config tuning: changed dreaming.schedule from '0 2 * * *' to '0 2,14 * * *' (twice daily). The 2am run produces 3x more insights than 2pm. Added a second run at 2pm as a catch-up for morning work.",
    "Config env var substitution: llm.endpoint set to ${LLM_ENDPOINT}. Environment variable was not set. Config loaded literal string '${LLM_ENDPOINT}' as the endpoint URL. API calls failed to connect. Need to validate no unresolved ${...} placeholders after loading.",
    "Config type mismatch: max_concurrent_encodings was set to '4' (string in YAML) instead of 4 (integer). YAML unmarshaling silently used zero value. All encoding was serialized instead of running 4 concurrent. Took 3 hours to notice the throughput drop.",
    "retrieval.source_weights configured: mcp=1.5, filesystem=1.0, terminal=0.8, clipboard=0.5. MCP memories ranked 50% higher than filesystem memories. This reflects that explicit remember calls (MCP) are more intentional than passive observations.",
])

# ---- Training / Felix-LM Observations ----
SCENARIOS.extend([
    "EXP-18 completed: Qwen 3.5 2B + 4 spokes rank 64 on all 24 layers. Best eval loss 0.7134 at step 11,400 on v5 dataset (11,436 train / 1,270 eval). Novel schema compliance: 10/10. Gate values range 0.12 (layer 0) to 0.88 (layer 23).",
    "Spoke adapter architecture: W_down (2048->64) and W_up (64->2048) per layer, 4 spokes each. W_up initialized to zeros — spokes start as identity (zero disruption to frozen base). Gate bias controls contribution via sigmoid. Total trainable params: 25.2M (0.7% overhead on 3.5B base).",
    "Training data poison discovered: 37% of v1 dataset (1,420 examples) was synthetic compression/decompression data with fictional entities like 'daxBautista|Feb2019|9662C@Ferrum Initiative'. Removing this was the single biggest quality improvement — novel schema went from 60% to 100%.",
    "Hallucination stress test: Qwen+Spokes scored 5/7. Failed on: (1) multi-topic test — dropped person name 'Jason' while preserving all technical terms, (2) stack trace test — preserved error message but dropped line numbers spread.go:142 and agent.go:89. Both failures are detail omission, not fabrication.",
    "Muon optimizer routing: spoke matrices (W_down, W_up) through MuonAdamW, gate scalars through AdamW with 0.1x LR. Muon maintains orthogonal Q,R factors which prevents spoke collapse. The mixed optimizer adds ~50MB overhead. Import path: ~/Projects/nanochat/nanochat/optim.py.",
    "Gemma 4 E2B evaluation: 100% novel schema but 1.7x slower than Qwen (33.9s vs 19.7s per memory) due to NF4 quantization on RX 7800 XT. Gemma requires NF4 because bf16 model is 9.3GB (exceeds 16GB with activations). Sticking with Qwen for production.",
    "Training on RX 7800 XT: batch_size=1, grad_accum=8, seq_len=2048. Peak VRAM: 7.3GB. Gradient checkpointing enabled. OOM handler in train_qwen_spokes.py:390 catches rare long-sequence failures. ROCm 7.2 with TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1.",
    "Data pipeline: batch_encode.py submitted 3,338 SWE-bench examples to Gemini Batch API. 99.2% success rate. Failed examples had inputs > 3000 chars (truncated, losing context). merge_training_data.py deduplicated by content hash and re-tokenized for Qwen.",
    "Checkpoint comparison: exp17 (v2 data, 4.5K examples) eval loss 0.6080 vs exp18 (v5 data, 11.4K examples) eval loss 0.7134. Higher loss in exp18 reflects larger, more diverse eval set (1,270 vs 507 examples), not regression. Both achieve 100% novel schema.",
    "v6 dataset quality audit: validate.py Level 1 found 139 gist-too-long, 1 invalid enum. Level 2 found 251 missing file:lines, 119 fabricated entities. Level 3 found 24 duplicate gists. Cleaned dataset: 11,113 examples. Added 3-level validation pipeline for all new data.",
    "serve_spokes.py deployment: OpenAI-compatible API on port 8899. Routes: POST /v1/chat/completions, GET /v1/models, GET /health. GENERATE_LOCK serializes GPU inference. ~20 seconds per encoding. Connected to daemon via config.yaml llm.endpoint.",
    "EXP-20 registered: MI300X production run with v6 targeted dataset. Hypothesis: targeted data (stack traces, entities, sparse, domain terms, numerical) + quality audit will improve stress test from 5/7 to 7/7. Config: batch 16, 5 epochs, LR 3e-4, no gradient checkpointing.",
])

# ---- Dashboard / Web UI (internal/web/) ----
SCENARIOS.extend([
    "Dashboard at http://127.0.0.1:9999/ shows agent activity feed, memory timeline, encoding queue status, and forum-style agent posts. Built with embedded ES modules and CSS via //go:embed. No external dependencies.",
    "Dashboard encoding queue display showed 47 items backed up. The queue visualization updates via WebSocket at /ws. Each item shows: raw memory ID, source (filesystem/terminal/clipboard/mcp), creation time, and estimated wait time.",
    "Forum view: agents post observations in their personality. Consolidation agent posted: 'Cleaned house — archived 23 faded memories and merged 4 near-duplicates. The memory garden is looking tidy.' Metacognition replied: 'Noticed 3 encodings with missing entities. Worth investigating.'",
    "Dashboard stats page: 12,847 total memories, 487MB database size, 23,184 unique concepts, 4,521 associations. Average encoding time: 19.7s. Average recall latency: 120ms. Uptime: 14 days.",
])

# ---- Debugging / Incidents ----
SCENARIOS.extend([
    "Incident: daemon stopped encoding at 3am. DreamingAgent entered infinite loop replaying 3 memories (a1b2c3, d4e5f6, g7h8i9) that formed circular association chain. Fixed by adding cycle detection in agent/dreaming/replay.go:203.",
    "Incident: after PyTorch upgrade to 2.11.0+ROCm 7.2, spoke training segfaulted on first backward pass. Cause: PYTORCH_ROCM_ALLOC_CONF=expandable_segments:True in stale .bashrc — expandable_segments not supported on ROCm. Removed the env var, training resumed.",
    "Debug: mnemonic CPU spiked to 100% after clipboard event with 50KB base64 image. Perception agent tried to encode the entire blob. Added 10KB content limit in watcher/clipboard/watcher.go:67.",
    "Debug: Sarah found embedding model returns different vectors for 'authentication' vs 'Authentication'. Hugot tokenizer is case-sensitive by default. Caused duplicate concept entries. Fixed by lowercasing all input text before embedding in internal/llm/hugot.go:89.",
    "Debug: Caleb and Jason pair-debugged memory corruption. store.UpdateMemory() wasn't wrapping transaction properly — crash during write left partial row. Added deferred rollback in store/sqlite/memories.go:312. Caleb wrote fix, Jason reviewed.",
    "Incident: recall returned memories from project 'felixlm' when querying 'mnemonic'. FTS5 query path in store/sqlite/retrieval.go:156 didn't include project filter. Vector search path filtered correctly. Fixed by adding 'AND project = ?' to FTS query.",
    "rocm-smi showed stale Python process holding 14.2GB VRAM. PID 23456 was a training run from yesterday that didn't exit cleanly. Killed it with kill -9. VRAM freed. Always check rocm-smi --showpids before starting training.",
    "Debug: encoding agent produced valid JSON but with wrong field types — salience was string '0.75' instead of float 0.75. The Qwen spoke model occasionally outputs numbers as strings. Added type coercion in the encoding pipeline after JSON parse.",
])

# ---- Architecture Decisions ----
SCENARIOS.extend([
    "Decision: event bus over direct agent calls for inter-agent communication. Agents subscribe to event types and react independently. New agents don't require modifying existing ones. Tradeoff: harder to trace execution flow, but reactor's rule engine helps debugging.",
    "Decision: SQLite with WAL mode over Postgres. WAL gives concurrent reads during consolidation cycles. The daemon runs on consumer hardware (Mac Mini, Linux desktop) where Postgres is deployment overhead. Store interface abstracts the implementation for future migration.",
    "Decision: Qwen 3.5 2B as frozen base over Gemma 4 E2B. Both achieve 100% schema but Qwen runs natively in bf16 on RX 7800 XT (19.7s) while Gemma requires NF4 (33.9s). Gemma reserved for DO droplet training with 192GB VRAM.",
    "Decision: spoke rank 64 with 4 spokes per layer. Rank 128 showed no quality improvement in HP sweep but doubled memory. 4 spokes gives enough capacity for encoding. Gate mechanism handles per-layer contribution — early layers gate low (0.12), late layers high (0.88).",
    "Decision: 0.7 decay factor for spread activation. At hop 1: 0.7 activation. Hop 2: 0.49. Hop 3: 0.34. This limits distant associations to 0.34 activation by third hop, preventing noise from dominating results. Tested values 0.5 (too aggressive) and 0.9 (too noisy).",
    "Decision: JWT over sessions for API auth. Enables horizontal scaling behind a load balancer without shared session state. Tradeoff: can't revoke tokens immediately (must wait for expiry). Acceptable for a local-first daemon.",
    "Decision: pure-Go SQLite driver (modernc.org/sqlite) instead of CGo mattn/go-sqlite3. No CGO_ENABLED=1 required for SQLite operations. CGo still needed on macOS for fsevents watcher, but Linux builds are pure Go.",
    "Decision: in-memory embedding index (linear scan) instead of HNSW. At 12K memories, linear scan takes 4ms — fast enough. HNSW adds complexity and memory overhead. Will revisit at 100K+ memories when linear scan exceeds 50ms.",
])

# ---- Code Review / Collaboration ----
SCENARIOS.extend([
    "PR #342: Jason added Windows Service support via golang.org/x/sys/windows/svc. Three platform files: service_windows.go, service_darwin.go, service_linux.go with build tags. Follows existing pattern in internal/daemon/.",
    "PR #358 review: Caleb suggested lowering abstraction agent's pattern-to-principle promotion threshold from 0.95 to 0.85. Agent was too conservative — only 2 patterns promoted in a month. Jason agreed and lowered it.",
    "Merge conflict on internal/mcp/server.go: autoresearch/ft-mar25 and main both added new MCP tools at the same location. Resolved by keeping both additions and reordering alphabetically. 12 tool registrations total after merge.",
    "PR #375: fix for handoff content preservation. create_handoff was losing detail during encoding. Switched to using remember with full text for handoffs. Memory fidelity improved significantly for session handoffs.",
    "Code review feedback: the new activity tracker in internal/api/routes/activity.go was making N+1 queries — one per concept. Refactored to batch query all concepts in a single SELECT with IN clause. Response time dropped from 800ms to 45ms.",
])

# ---- Backup / Export / Migration ----
SCENARIOS.extend([
    "mnemonic export: dumped 12,847 memories to JSON backup file (89MB). Export includes all fields: content, embeddings, associations, patterns, abstractions. Took 12 seconds. Backup stored at ~/.mnemonic/backups/2026-04-04.json.",
    "Self-update: mnemonic update checked GitHub releases. Current version 0.8.2, latest 0.8.5. Downloaded binary (23MB), verified SHA256, replaced in-place. Daemon restarted automatically. No data migration needed for this version bump.",
    "Database migration from schema version 14 to 15: added optimistic locking via version column on memories table. Migration took 3.4 seconds for 10K memories. Verified with PRAGMA user_version. No data loss.",
])

# ---- Cross-Agent Interactions ----
SCENARIOS.extend([
    "PerceptionAgent created raw memory from filesystem event (Go file edit). EncodingAgent picked it up 200ms later, encoded in 19.7s. MemoryEncodedEvent fired. RetrievalAgent updated embedding index. EpisodingAgent clustered it into the current episode. Full pipeline: 20.1 seconds end-to-end.",
    "ConsolidationAgent merged two memories about SQLite WAL mode into one. DreamingAgent picked up the merged memory in the 2am cycle. Generated insight linking WAL checkpointing to daemon restart latency. AbstractionAgent evaluated the insight — too weak (confidence 0.3) to become a pattern.",
    "Reactor fired 'post-encoding-dedup' chain when MemoryEncodedEvent arrived. The dedup action found the new memory was 93% similar to an existing one. ConsolidationAgent.mergeMemories() was triggered. Merged memory had combined salience (max of both). Association graph updated.",
    "MetacognitionAgent flagged that EncodingAgent's average latency increased from 19s to 35s over 3 days. Orchestrator.checkLLMHealth() reported healthy. Root cause: spoke server's GPU was thermal throttling at 92°C. rocm-smi confirmed. Improved case airflow, latency returned to 20s.",
    "EpisodingAgent created episode 'Morning debugging session' with 7 memories. DreamingAgent replayed the episode at 2am. Found that 3 of the 7 memories were about the same nil pointer bug approached from different angles. Suggested consolidation merge.",
    "PerceptionAgent's LLM gate rejected a filesystem event (score 0.12). But the event was a critical config.yaml change. MetacognitionAgent detected the false negative 2 hours later when the user manually remembered the config change via MCP. Adjusted gate threshold from 0.5 to 0.4.",
    "RetrievalAgent's recall for 'spread activation' returned a memory from DreamingAgent's insight generation. The insight cross-linked spread activation with PageRank algorithms. User rated it 'helpful' via feedback. Association strength between the two topics boosted by 0.2.",
    "Orchestrator adaptive scheduling: encoding queue depth hit 30 items. Orchestrator reduced consolidation interval from 6h to 12h to free LLM resources. When queue cleared, interval restored. Total adaptation time: 45 minutes of reduced consolidation.",
])

# ---- Production Usage Patterns (what Claude Code does with mnemonic) ----
SCENARIOS.extend([
    "Session start pattern: Claude Code called batch_recall with 3 queries — 'project context', 'recent decisions about encoding', 'known errors in retrieval'. Got 14 memories in 280ms. Used the encoding decisions to inform approach to current task.",
    "Mid-session recall: while editing internal/agent/retrieval/agent.go, Claude Code called recall with query='spread activation hop limit'. Got a decision from 2 weeks ago explaining why max_hops=3 was chosen (0.7^3 = 0.34 activation floor). Avoided re-investigating.",
    "Claude Code called remember with type='decision': 'Chose to implement optimistic locking via version column instead of pessimistic locking with SELECT FOR UPDATE. SQLite doesn't support row-level locks anyway.' Salience: 0.8. Project: mnemonic.",
    "Claude Code called remember with type='error': 'EncodingAgent panicked on nil embedding vector — the hugot model was unloaded after an OOM. Added nil check before calling store.WriteMemory() in encoding/agent.go:234.' Salience: 0.85.",
    "Claude Code called remember with type='insight': 'Gate values in spoke adapter correlate with layer depth — early layers (0-5) gate low (0.12-0.20), late layers (18-23) gate high (0.75-0.88). This suggests early layers need minimal correction while late layers make significant semantic adjustments.' Salience: 0.9.",
    "Claude Code called remember with type='learning': 'Go sql.NullString is needed for nullable VARCHAR columns in SQLite. Without it, scanning a NULL value into a string panics. All optional string fields in the memories table should use sql.NullString.' Salience: 0.7.",
    "Claude Code called create_handoff at end of session: summarized 5 completed tasks (encoding agent refactor, FTS5 migration, stress test improvements, data quality pipeline, droplet setup), 2 pending items (batch job completion, v6 merge), and 3 key decisions made during the session.",
    "Claude Code called get_context while editing training/scripts/train_qwen_spokes.py. Daemon's activity watcher detected the file open. Proactive recall surfaced 3 relevant memories: HP sweep results, EXP-18 configuration, and a learning about Muon optimizer routing.",
    "Claude Code called amend on a memory about 'using Gemini for encoding' — updated to 'switched from Gemini to local Qwen spoke server for encoding, 100% reliability vs 50%'. Preserved 6 existing associations. Version bumped from 1 to 2.",
    "Claude Code called feedback with quality='partial' for recall query 'database performance'. 2 of 5 results were relevant (WAL mode decision, busy timeout fix). 3 were noise (unrelated performance memories from other projects). Feedback adjusted 455 association strengths.",
    "Claude Code called recall_project for 'mnemonic' — returned 15 memories including recent patterns, key decisions, and activity summary. Used to orient at session start without needing detailed queries.",
    "Claude Code called recall_timeline for the last 24 hours — returned 23 memories chronologically. Identified a gap between 2pm-6pm (daemon was restarting during a deploy). Used timeline to understand what happened while user was away.",
    "Claude Code called list_sessions — found 8 sessions in the last week. Most productive: Tuesday (42 memories, 3 decisions). Least active: Saturday (2 memories). Used to find a specific decision from Wednesday's session.",
    "Claude Code called session_summary for the current session: '15 remember calls, 12 recall calls, 4 feedback submissions. Top concepts: encoding, training, data-quality. Key decision: switched to Batch API for data generation. Duration: 3.5 hours.'",
    "Claude Code called get_patterns with min_strength=0.8 — returned 3 strong patterns: (1) 'test before commit' (0.92), (2) 'check rocm-smi before training' (0.85), (3) 'validate config after editing' (0.81). All actionable recurring behaviors.",
    "Claude Code called get_insights — returned 2 metacognition observations: (1) '89% of memories have emotional_tone=analytical — possible encoding bias', (2) '34 orphaned memories with zero associations from bulk ingest'. Used to plan quality improvements.",
])

# ---- Error Recovery and Edge Cases ----
SCENARIOS.extend([
    "Encoding agent received raw memory with empty content (clipboard watcher glitch). callCompressionLLM() returned valid JSON but with placeholder gist 'unknown event'. validate.py placeholder detection caught it. Memory rejected, not stored.",
    "Recall query with special characters: user searched for 'func (s *Store) GetMemory()'. FTS5 tokenizer stripped the asterisk and parentheses. Query became 'func Store GetMemory'. Still matched the correct memory about the GetMemory implementation.",
    "MCP remember with salience=0.0 — user explicitly marked a memory as trivial. Encoding agent preserved the salience. ConsolidationAgent.decayMemories() decayed it to -0.05 (below 0). Clamped to 0.0. Memory archived on next cycle.",
    "Batch recall with empty query string: the MCP tool returned an error 'query must not be empty'. Claude Code retried with a specific query. The error handling was clean — no panic, no state corruption.",
    "MCP remember with 10KB content (full stack trace paste). Encoding took 45 seconds — 2x normal due to input length. The encoded memory preserved the complete stack trace including all file:line references. Content field was 800 chars (compressed from 10K).",
    "Recall returned a memory that was amended 3 times. The version history showed: v1 (original), v2 (corrected a typo), v3 (updated after finding root cause). Each amend preserved associations. The final version was the one returned.",
    "User called forget on a memory about an abandoned feature branch. Memory archived (state: archived). Associations weakened by 0.5x but not deleted. Future recall won't return it unless explicitly querying archived state.",
    "Encoding agent processed a raw memory containing mixed code and natural language (Go function with inline comments). The structured_concepts correctly separated: topics=[go, store, memory], entities=[SQLiteStore, GetMemory], actions=[query database, scan row, return memory].",
    "MCP coach_local_llm wrote new coaching instructions to ~/.mnemonic/coaching.yaml. Instructions: 'Always preserve file paths with line numbers verbatim in the content field. Never substitute approximate descriptions for exact technical identifiers.' Encoding agent picked up changes on next cycle.",
    "Recall with include_associations=true returned memory about SQLite WAL with 3 associated memories: (1) concurrent read performance benchmark (strength 0.87), (2) checkpoint configuration decision (strength 0.72), (3) consolidation lock timeout fix (strength 0.65).",
])

# ---- Performance Observations ----
SCENARIOS.extend([
    "Encoding throughput: 3 memories per minute with single GPU (RX 7800 XT). Spoke server processes one at a time (GENERATE_LOCK). During peak coding sessions with 15+ file changes, queue depth reaches 20-30 items. Drain time: ~10 minutes.",
    "Recall latency breakdown: FTS5 query 8ms, embedding search 4ms, spread activation 15ms, ranking 2ms, synthesis (when enabled) 4200ms. Total without synthesis: 29ms. Total with synthesis: 4229ms. Synthesis is the bottleneck.",
    "Store statistics: 12,847 memories, 4,521 associations, 156 patterns, 12 principles, 1 axiom. Database file: 487MB. WAL file: typically 2-8MB, spikes to 50MB+ during consolidation. Embedding index RAM: 19MB.",
    "Dashboard WebSocket: 3 connected clients. Event broadcast rate: ~2 events/second during active coding, 0.1 events/second idle. No measurable overhead on daemon performance. Each event is ~500 bytes JSON.",
    "Embedding pipeline throughput: hugot BatchEmbed processes 32 texts in 1.2 seconds. At 12,847 memories, full re-embedding takes 8 minutes. Incremental embedding (new memories only) averages 50ms per memory.",
    "Daemon memory footprint: RSS 340MB (Go runtime 45MB, embedding index 19MB, SQLite cache 128MB, agent goroutines 48MB, GGUF model 100MB). Acceptable for consumer hardware. Mac Mini M4 runs comfortably at 280MB.",
    "Consolidation cycle performance: 847 memories scanned in 2.1 seconds. Decay computation: 0.3s. Merge clustering: 4.8s (dominated by pairwise cosine similarity for 847 memories = 358K comparisons). Pattern extraction LLM call: 5.3s. Total: 12.4s.",
    "FTS5 query performance: simple query ('sqlite') returns in 2ms. Complex query ('sqlite WAL concurrent read performance') returns in 8ms. FTS5 with unicode61 tokenizer handles compound technical terms correctly after migration 005.",
])

# ---- Ingest and Bulk Operations ----
SCENARIOS.extend([
    "mnemonic ingest ~/Projects/mem --project mnemonic: scanned 2,341 files in 847 directories. Filtered by .gitignore: excluded node_modules, .git, vendor, bin, *.db. Created 312 raw memories from Go source files, markdown docs, and config files. Queue depth: 312.",
    "Ingest of ~/Projects/felixlm created 89 raw memories from Python training scripts, design docs, and config files. Cross-project associations formed between felixlm spoke architecture decisions and mnemonic encoding agent configuration.",
    "Bulk dedup after ingest: mnemonic dedup found 47 near-duplicate memories (cosine similarity > 0.92). 23 were from the same file being ingested and later modified. Merged into 24 canonical memories. 23 removed.",
    "Purge of archived memories older than 90 days: mnemonic purge --older-than 90d removed 156 archived memories. Freed 12MB of database space. Associations to purged memories were also removed (89 associations deleted).",
])

# ---- Specific File Path References (real mnemonic code) ----
SCENARIOS.extend([
    "Bug fix in internal/store/sqlite/memories.go:312 — store.UpdateMemory() wasn't wrapping the version check and update in the same transaction. A concurrent read between the check and update could see stale version, allowing lost updates. Added BEGIN IMMEDIATE before the version comparison.",
    "Refactored internal/agent/retrieval/spread.go:142 — the spreadActivation function was using a visited map keyed by memory ID but not checking for circular associations. Two memories with bidirectional associations caused infinite recursion. Added cycle detection with a visited set.",
    "New feature in internal/mcp/tools.go — added exclude_path tool that calls watcher.AddExclusion(pattern). The exclusion is applied at runtime without daemon restart. Patterns are persisted in the store so they survive restarts.",
    "Performance fix in internal/llm/hugot.go:134 — BatchEmbed was sending all texts in a single request. API returned 429 after ~200 texts. Split into chunks of 32 with 500ms delays. Error rate dropped from 15% to 0%.",
    "Bug in internal/agent/perception/agent.go — isRecentGitOp() checked .git/FETCH_HEAD mtime but FETCH_HEAD doesn't exist in freshly cloned repos. Caused a panic on nil stat result. Added os.IsNotExist check before mtime comparison.",
    "Migration in migrations/005_fts_tokenizer.sql — switched FTS5 from default tokenizer to unicode61. The default tokenizer split 'middleware' into 'middle' + 'ware', causing recall to miss exact matches. unicode61 keeps compound words intact.",
    "Fix in internal/agent/encoding/agent.go:234 — encodeRawMemory() didn't check for nil embedding before calling store.WriteMemory(). If the embedding model was down, a nil vector was stored. Subsequent SearchByEmbedding() panicked on nil dot product. Added nil guard.",
    "Optimization in internal/api/routes/activity.go — the activity endpoint was making N+1 queries (one per concept in the response). Refactored to batch all concept lookups into a single SELECT with IN clause. Response time dropped from 800ms to 45ms for 50 concepts.",
    "Config validation in internal/config/config.go — added bounds checking for retrieval.source_weights. Previously a weight of 10.0 could dominate results. Now clamped to [0.1, 5.0] with a warning log if the original value was out of bounds.",
    "WebSocket fix in internal/api/routes/ws.go — broadcast to connected clients was blocking on unresponsive clients. One hung browser tab caused all other clients to miss events. Switched to non-blocking sends with per-client goroutines and 5-second write deadline.",
    "Event bus fix in internal/events/inmemory.go — handler dispatch didn't recover from panics. A panicking handler in the retrieval agent (nil embedding) caused all subsequent events to that handler to be lost. Added defer recover() with error logging in the dispatch loop.",
    "Platform fix in internal/watcher/filesystem/watcher_other.go:89 — Linux fsnotify hot/cold directory promotion was based on event count over 5 minutes. But directories with one important file (like config.yaml) never got promoted because event count was low. Added a 'pinned directories' config option.",
])

GEN_SYSTEM = (
    "You rewrite scenarios into natural developer observations. Keep ALL specific details "
    "(file paths with line numbers, function names, person names, exact numbers, error messages, "
    "struct names, config values) EXACTLY as given. Vary the writing style — some terse, some "
    "analytical, some frustrated. Output ONLY the observation text, no markdown fences."
)

GEN_PROMPT_TEMPLATE = (
    "Rewrite this mnemonic daemon scenario as a natural developer observation, as if recording "
    "it in a work log. Preserve every technical detail verbatim. 3-6 sentences.\n\n"
    "Scenario: {scenario}"
)


def build_batch_requests() -> list[dict]:
    """Build Gemini Batch API request JSONL from scenarios."""
    requests = []
    for i, scenario in enumerate(SCENARIOS):
        requests.append({
            "key": f"mnemonic-{i}",
            "request": {
                "contents": [{"parts": [{"text": GEN_PROMPT_TEMPLATE.format(scenario=scenario)}]}],
                "system_instruction": {"parts": [{"text": GEN_SYSTEM}]},
                "generation_config": {
                    "temperature": 0.8,
                    "max_output_tokens": 2048,
                },
            },
        })
    return requests


def submit():
    from google import genai
    from google.genai import types

    client = genai.Client(api_key=API_KEY)

    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)

    # Save scenario metadata
    meta_path = OUTPUT_DIR / "mnemonic_scenarios_meta.jsonl"
    with open(meta_path, "w") as f:
        for i, s in enumerate(SCENARIOS):
            f.write(json.dumps({"key": f"mnemonic-{i}", "scenario": s}) + "\n")
    print(f"Saved {len(SCENARIOS)} scenario metadata -> {meta_path}")

    # Build and write batch file
    requests = build_batch_requests()
    batch_path = OUTPUT_DIR / "mnemonic_batch_requests.jsonl"
    with open(batch_path, "w") as f:
        for r in requests:
            f.write(json.dumps(r) + "\n")
    print(f"Created batch file: {batch_path} ({len(requests)} requests)")

    # Upload and submit
    print(f"Uploading {batch_path}...")
    uploaded = client.files.upload(
        file=str(batch_path),
        config=types.UploadFileConfig(
            display_name="mnemonic-scenarios-rawgen",
            mime_type="jsonl",
        ),
    )
    print(f"Uploaded: {uploaded.name}")

    print(f"Creating batch job (model={MODEL})...")
    job = client.batches.create(
        model=MODEL,
        src=uploaded.name,
        config={"display_name": "mnemonic-scenarios-rawgen"},
    )
    print(f"Job created: {job.name}")
    print(f"State: {job.state.name}")
    print(f"Scenarios: {len(SCENARIOS)}")
    print(f"\nCheck status: python generate_mnemonic_scenarios.py status --job {job.name}")


def check_status(job_name):
    from google import genai
    client = genai.Client(api_key=API_KEY)
    job = client.batches.get(name=job_name)
    print(f"Job: {job.name}")
    print(f"State: {job.state.name}")
    if hasattr(job, "dest") and job.dest:
        print(f"Result file: {job.dest.file_name}")


def download(job_name):
    from google import genai
    client = genai.Client(api_key=API_KEY)
    job = client.batches.get(name=job_name)

    if job.state.name != "JOB_STATE_SUCCEEDED":
        print(f"Job not complete: {job.state.name}")
        return

    print(f"Downloading from {job.dest.file_name}...")
    content = client.files.download(file=job.dest.file_name)
    result_lines = content.decode("utf-8").strip().split("\n")
    print(f"Got {len(result_lines)} results")

    output_path = OUTPUT_DIR / "mnemonic_raw_inputs.jsonl"
    success = 0
    fail = 0

    with open(output_path, "w") as f:
        for line in result_lines:
            try:
                result = json.loads(line)
                text = result["response"]["candidates"][0]["content"]["parts"][0]["text"].strip()
                if text.startswith("```"):
                    lines = text.split("\n")
                    text = "\n".join(l for l in lines if not l.strip().startswith("```")).strip()
                if len(text) < 30:
                    fail += 1
                    continue
                f.write(json.dumps({
                    "raw_input": text,
                    "source": "targeted_mnemonic",
                    "task_type": "encoding",
                    "category": "mnemonic_specific",
                }) + "\n")
                success += 1
            except (KeyError, IndexError, json.JSONDecodeError):
                fail += 1

    print(f"Results: {success} success, {fail} fail ({success/(success+fail)*100:.1f}%)")
    print(f"Written to: {output_path}")
    print(f"\nNext: encode via Batch API:")
    print(f"  python batch_encode.py submit --input {output_path}")


def main():
    parser = argparse.ArgumentParser(description="Generate mnemonic-specific scenarios via Batch API")
    sub = parser.add_subparsers(dest="command")
    sub.add_parser("submit")
    s = sub.add_parser("status")
    s.add_argument("--job", required=True)
    d = sub.add_parser("download")
    d.add_argument("--job", required=True)
    sub.add_parser("count", help="Just print scenario count")

    args = parser.parse_args()
    if args.command == "submit":
        if not API_KEY:
            print("Error: LLM_API_KEY required")
            sys.exit(1)
        submit()
    elif args.command == "status":
        if not API_KEY:
            print("Error: LLM_API_KEY required")
            sys.exit(1)
        check_status(args.job)
    elif args.command == "download":
        if not API_KEY:
            print("Error: LLM_API_KEY required")
            sys.exit(1)
        download(args.job)
    elif args.command == "count":
        print(f"Total scenarios: {len(SCENARIOS)}")
    else:
        parser.print_help()


if __name__ == "__main__":
    main()
