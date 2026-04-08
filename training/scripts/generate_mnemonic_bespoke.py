#!/usr/bin/env python3
"""Generate mnemonic-specific training data via OpenRouter (Qwen 3.6 Plus free).

Respects OpenRouter free tier limits:
  - 20 requests/minute (we do ~10/min to be safe)
  - 50 or 1000 requests/day depending on credits
  - Stops gracefully on 429 or daily cap

Usage:
    OPENROUTER_API_KEY=... python generate_mnemonic_bespoke.py
"""

import asyncio
import json
import os
import random
import sys
import time
from pathlib import Path

import aiohttp

sys.path.insert(0, str(Path(__file__).resolve().parent))

API_KEY = os.environ.get("OPENROUTER_API_KEY", "")
API_BASE = "https://openrouter.ai/api/v1"
MODEL = "qwen/qwen3.6-plus:free"
MAX_CONCURRENT = 3  # Conservative — 20 RPM limit, each req ~3-6s
DELAY_BETWEEN = 4.0  # Seconds between launching requests (~15 RPM max)
OUTPUT_DIR = Path("training/data/targeted")

# ---- Mnemonic-specific domains ----
# These are the EXACT kinds of observations the production model will encode.

MNEMONIC_DOMAINS = [
    # Go daemon panics and errors
    "Go panic: nil pointer dereference in spread_activation.go:142 during RetrievalAgent.Retrieve() — the associations slice was nil because the memory had zero associations. Fixed with a nil guard before iteration.",
    "Go panic: index out of range [3] with length 3 in consolidation.go:287 — the decay loop modified the slice while iterating. Goroutine 47 was running the consolidation cycle. Fixed by collecting indices first, then deleting in reverse.",
    "Error in encoding agent: LLM provider returned invalid JSON — the response had a trailing comma after the last field in structured_concepts. The parse_json_response fallback caught it but logged a warning at encoding.go:95.",
    "systemd restart loop: mnemonic.service failed 3 times in 10 seconds, systemd stopped trying. Root cause: config.yaml had an invalid YAML key (tab instead of spaces). Journal showed 'yaml: line 47: found a tab character where an indentation space is expected'.",
    "SQLite WAL checkpoint stall: the consolidation agent held a long-running read transaction for 45 seconds while dreaming agent tried to write. WAL file grew to 890MB. Fixed by adding a 10-second timeout on consolidation reads in store/sqlite/consolidation.go:178.",
    "MCP tool error: recall returned 0 results for query 'authentication middleware' despite 12 relevant memories. Root cause: FTS5 tokenizer was splitting 'middleware' into 'middle' + 'ware'. Fixed by switching to unicode61 tokenizer in migrations/005_fts_tokenizer.sql.",
    "Race condition in event bus: two agents subscribed to the same event type, both tried to update the same memory's salience. Lost update — second write overwrote first. Added optimistic locking with version field in store/sqlite/memories.go:234.",
    "Watcher false positive: fsnotify fired 47 events for a single 'git pull' operation. The perception agent encoded each as a separate memory, flooding the encoding queue. Fixed by adding a 500ms debounce window in watcher/filesystem/watcher_linux.go:89.",
    "Out of memory during dreaming: the dreaming agent loaded all 12,000 memories into RAM for cross-pollination analysis. Peak RSS hit 3.2GB on the Mac Mini. Fixed by streaming with a cursor-based iterator in agent/dreaming/replay.go:156.",
    "Embedding batch failure: hugot library returned 429 after processing 200 memories. The batch size of 100 was too aggressive. Reduced to 32 with 500ms delays between batches. Error was in internal/llm/hugot.go:134.",

    # MCP tool operations
    "MCP session: Claude Code called recall with query='SQLite FTS5 migration' and got 3 results. The top result (salience 0.89) was a decision from 2 weeks ago about switching tokenizers. Claude used it to avoid re-investigating the same issue. Feedback: helpful.",
    "MCP remember: stored a decision about choosing JWT over sessions for API auth. Type: decision, salience: 0.75. The encoding agent processed it in 18.2 seconds via the Qwen spoke model on port 8899.",
    "MCP batch_recall: session start with 3 parallel queries — 'project context', 'recent errors', 'training decisions'. Returned 12 memories total in 340ms. Spread activation found 2 cross-linked memories between training and daemon error categories.",
    "MCP create_handoff: session summary with 8 key decisions, 3 errors encountered, and 2 architectural insights. Salience set to 0.95. The handoff was 1,200 words — encoding took 34 seconds through the spoke model.",
    "MCP amend: updated memory about SQLite schema from 'using FTS4' to 'migrated to FTS5 with unicode61 tokenizer'. Preserved 4 existing associations and bumped the version. The original memory was from 6 sessions ago.",

    # Agent behavior observations
    "Consolidation cycle completed: processed 847 memories, decayed 23 below threshold (0.1), merged 4 near-duplicate pairs, pruned 12 archived memories older than 90 days. Total cycle time: 12.4 seconds. Next cycle scheduled in 6 hours.",
    "Dreaming agent insight: cross-pollinated a memory about 'exponential backoff in API retry' with a memory about 'consolidation decay formula'. Generated insight: both use exponential decay patterns, suggesting a shared utility. Confidence: 0.67.",
    "Metacognition audit: reviewed 50 recent encodings. Found 3 with missing structured_concepts.entities (person names dropped), 2 with salience > 0.9 for routine events, 1 with fabricated entity 'DataManager' not in original input. Flagged for review.",
    "Perception agent filtered 340 filesystem events down to 12 meaningful observations in the last hour. Filter rules: ignored node_modules (180 events), .git objects (95 events), temporary files (42 events). Kept: Go source changes (7), config edits (3), doc updates (2).",
    "Abstraction agent promoted pattern 'test before commit' (observed 15 times across 8 sessions, strength 0.92) to principle. The pattern was consistently associated with successful PR merges and zero CI failures.",
    "Retrieval spread activation: query 'WebSocket race condition' activated 5 memories directly, spread to 8 more via associations (decay factor 0.7). Top result: a decision about mutex locking in handler.go from 3 weeks ago. Activation path: query → memory_a (0.95) → memory_b (0.67) → memory_c (0.47).",

    # Training and Felix-LM observations
    "EXP-18 checkpoint evaluation: Qwen 3.5 2B + spokes at step 11400, eval loss 0.7134. Novel schema compliance 10/10. Gate values range from 0.12 (layer 0) to 0.88 (layer 23). The 0.1x scalar LR kept gates stable throughout training.",
    "Spoke adapter observation: W_up initialized to zeros means spokes start as identity (no disruption to frozen base). During training, early layers develop small corrections (gate ~0.12) while late layers make larger adjustments (gate ~0.88). This matches the hypothesis that shallow layers capture syntax and deep layers capture semantics.",
    "Training data quality issue: found 37% of v1 dataset was poisoned — synthetic compression/decompression examples with fictional researchers, organizations, and locations in ad-hoc notation like 'daxBautista|Feb2019|9662C@Ferrum Initiative'. Removing them was the single biggest quality improvement.",
    "Hallucination stress test result: Qwen+Spokes scored 5/7. Failed on: (1) multi-topic test — dropped person name 'Jason' while preserving all technical terms, (2) stack trace test — preserved error message but dropped line numbers spread.go:142 and agent.go:89.",
    "Muon optimizer observation: routing spoke matrices (W_down, W_up) through Muon and gate scalars through AdamW with 0.1x LR works better than all-AdamW. Muon maintains orthogonal Q,R factors which prevents spoke collapse. The mixed optimizer adds ~50MB memory overhead.",
    "Benchmark: Qwen spoke encoding at 19.7 seconds per memory on RX 7800 XT. Gemma 4 E2B spoke encoding at 33.9 seconds (1.7x slower due to NF4 dequantization). Gemini 3 Flash API at 7.3 seconds but with 50% error rate on our schema.",

    # Configuration and deployment
    "Config change: increased dreaming.schedule from '0 2 * * *' to '0 2,14 * * *' (twice daily instead of once). Dreaming at 2am produced 3x more insights than 2pm, likely because more memories accumulated during the workday. The 2pm run acts as a catch-up.",
    "Dashboard observation: the forum-style web UI at http://127.0.0.1:9999/ shows agent activity, memory timeline, and encoding queue. Noticed the encoding queue backed up to 47 items during a heavy coding session. Normal queue depth is 0-3.",
    "Daemon install on Linux: systemctl --user enable mnemonic.service worked but the service didn't start at boot because lingering wasn't enabled. Fixed with loginctl enable-linger hubcaps. The daemon now starts at boot without requiring a login session.",
    "Config.yaml tuning: set llm.endpoint to http://localhost:8899/v1 (spoke server) and llm.chat_model to 'qwen-spokes'. Fallback to Gemini API if spoke server is down. Encoding latency went from 7.3s (Gemini, unreliable) to 19.7s (local, 100% reliable).",

    # Debugging sessions
    "Jason reported the Mac Mini deployment is failing because the launchd plist has the wrong binary path — it points to /usr/local/bin/mnemonic but the binary is at ~/go/bin/mnemonic. Updated com.appsprout.mnemonic.plist and ran launchctl load.",
    "Sarah found that the embedding model returns different vectors for 'authentication' vs 'Authentication' — the hugot tokenizer is case-sensitive by default. This was causing duplicate concept entries in the store. Fixed by lowercasing all input text before embedding.",
    "Caleb and Jason pair-debugged a memory corruption issue: the store.UpdateMemory() call wasn't wrapping the transaction properly, so a crash during write left a partial row. Added a deferred rollback in store/sqlite/memories.go:312. Caleb wrote the fix, Jason reviewed.",
    "Debug session: mnemonic daemon CPU spiked to 100% after processing a clipboard event containing a 50KB base64 image. The perception agent tried to encode the entire blob. Added a 10KB content limit in watcher/clipboard/watcher.go:67.",

    # Architecture decisions
    "Decision: chose event bus over direct agent calls for inter-agent communication. Agents subscribe to event types and react independently. This allows adding new agents without modifying existing ones. Tradeoff: harder to trace execution flow, but the reactor agent's rule engine helps with debugging.",
    "Decision: SQLite over Postgres for the memory store. WAL mode gives us concurrent reads during consolidation cycles. The daemon runs on consumer hardware (Mac Mini, Linux desktop) where Postgres would be deployment overhead. If we ever need horizontal scaling, the store interface abstracts the implementation.",
    "Decision: Qwen 3.5 2B as the frozen base over Gemma 4 E2B for production encoding. Both achieve 100% novel schema compliance, but Qwen runs natively in bf16 on the RX 7800 XT (19.7s/memory) while Gemma requires NF4 quantization (33.9s/memory). Gemma reserved for future droplet training.",
    "Decision: spoke rank 64 with 4 spokes per layer. Rank 128 showed no quality improvement in EXP-12 sweep but doubled memory. 4 spokes gives enough capacity for the encoding task. The gate mechanism handles per-layer contribution automatically — early layers gate low (~0.12), late layers gate high (~0.88).",

    # Incident responses
    "Incident: the mnemonic daemon stopped encoding new memories at 3am. Investigation showed the dreaming agent entered an infinite loop replaying the same 3 memories (IDs: a1b2c3, d4e5f6, g7h8i9) that formed a circular association chain. Fixed by adding cycle detection in agent/dreaming/replay.go:203.",
    "Incident: user reported that recall was returning memories from a different project. Root cause: the project field wasn't being filtered in the FTS5 query — it was only filtered in the vector search path. Memories found via text search bypassed project scoping. Fixed in store/sqlite/retrieval.go:156.",
    "Incident: after upgrading PyTorch to 2.11.0+ROCm 7.2, the spoke training script segfaulted on the first backward pass. Cause: expandable_segments not supported on ROCm. Environment variable PYTORCH_ROCM_ALLOC_CONF was set to expandable_segments:True from a stale .bashrc entry. Removed it and training resumed.",

    # Code review and collaboration
    "Code review: Jason's PR #342 adds Windows Service support via golang.org/x/sys/windows/svc. The implementation follows the same pattern as the macOS launchd and Linux systemd code in internal/daemon/. Three platform files: service_windows.go, service_darwin.go, service_linux.go with build tags.",
    "PR feedback: Caleb reviewed the new abstraction agent (PR #358) and suggested reducing the pattern-to-principle promotion threshold from 0.95 to 0.85. The agent was too conservative — only 2 patterns had been promoted in a month of usage. Jason agreed and lowered it.",
    "Merge conflict resolution: the autoresearch/ft-mar25 branch conflicted with main on internal/mcp/server.go — both branches added new MCP tools at the same location. Resolved by keeping both additions and reordering alphabetically. 12 tool registrations total.",
]


async def call_openrouter(session, system: str, user: str, semaphore) -> str | None:
    """Call OpenRouter with conservative rate limiting."""
    headers = {
        "Authorization": f"Bearer {API_KEY}",
        "Content-Type": "application/json",
        "HTTP-Referer": "https://github.com/appsprout-dev/mnemonic",
        "X-Title": "Mnemonic Training Data Generation",
    }
    payload = {
        "model": MODEL,
        "messages": [
            {"role": "system", "content": system},
            {"role": "user", "content": user},
        ],
        "max_tokens": 2048,
        "temperature": 0.8,
    }

    for attempt in range(3):
        async with semaphore:
            await asyncio.sleep(DELAY_BETWEEN)  # Rate limit spacing
            try:
                async with session.post(f"{API_BASE}/chat/completions",
                                        headers=headers, json=payload,
                                        timeout=aiohttp.ClientTimeout(total=120)) as resp:
                    if resp.status == 429:
                        body = await resp.text()
                        if "daily limit" in body.lower() or "quota" in body.lower():
                            print(f"\n  DAILY LIMIT REACHED — stopping gracefully")
                            return "DAILY_LIMIT"
                        wait = min(60, 2 ** attempt * 10)
                        print(f"  Rate limited (429), waiting {wait}s...")
                        await asyncio.sleep(wait)
                        continue
                    if resp.status == 503:
                        wait = min(30, 2 ** attempt * 5)
                        print(f"  Service unavailable (503), waiting {wait}s...")
                        await asyncio.sleep(wait)
                        continue
                    resp.raise_for_status()
                    data = await resp.json()
                    content = data["choices"][0]["message"].get("content", "")
                    return content
            except Exception as e:
                if attempt < 2:
                    await asyncio.sleep(2 ** attempt * 3)
                    continue
                print(f"  Error: {e}")
                return None
    return None


GEN_SYSTEM = (
    "You generate realistic developer observations. Be specific and concrete. "
    "Include exact file paths with line numbers, specific metrics, tool versions, "
    "and person names when they appear in the scenario. Output ONLY the observation text."
)

GEN_PROMPT = (
    "Rewrite the following scenario as a natural developer observation, as if you're "
    "recording it in a work log or memory system. Keep all specific details (file paths, "
    "line numbers, names, numbers, error messages) exactly as given. Vary the writing style — "
    "some entries should be terse, some analytical, some frustrated. "
    "3-6 sentences. Output ONLY the observation text, no markdown.\n\n"
    "Scenario: {scenario}"
)


async def main():
    if not API_KEY:
        print("Error: OPENROUTER_API_KEY required")
        sys.exit(1)

    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
    output_path = OUTPUT_DIR / "mnemonic_bespoke.jsonl"

    # Resume support
    existing = 0
    if output_path.exists():
        existing = sum(1 for _ in open(output_path))
        print(f"Resuming: {existing} already generated")

    scenarios = MNEMONIC_DOMAINS[existing:]
    if not scenarios:
        print(f"All {len(MNEMONIC_DOMAINS)} scenarios already generated!")
        return

    print(f"Generating {len(scenarios)} mnemonic-specific observations via Qwen 3.6 Plus")
    print(f"  Model: {MODEL}")
    print(f"  Concurrency: {MAX_CONCURRENT}, delay: {DELAY_BETWEEN}s (~{60/DELAY_BETWEEN/MAX_CONCURRENT:.0f} RPM)")
    print(f"  Output: {output_path}")

    semaphore = asyncio.Semaphore(MAX_CONCURRENT)
    success = existing
    daily_limit = False

    async with aiohttp.ClientSession() as session:
        with open(output_path, "a") as f:
            for i, scenario in enumerate(scenarios):
                if daily_limit:
                    break

                prompt = GEN_PROMPT.format(scenario=scenario)
                result = await call_openrouter(session, GEN_SYSTEM, prompt, semaphore)

                if result == "DAILY_LIMIT":
                    daily_limit = True
                    break
                if result is None or len(result.strip()) < 30:
                    print(f"  [{i + existing + 1}/{len(MNEMONIC_DOMAINS)}] SKIP (empty/short)")
                    continue

                raw = result.strip()
                # Strip markdown fences
                if raw.startswith("```"):
                    lines = raw.split("\n")
                    raw = "\n".join(l for l in lines if not l.strip().startswith("```")).strip()

                entry = {
                    "raw_input": raw,
                    "source": "targeted_mnemonic_bespoke",
                    "task_type": "encoding",
                    "category": "mnemonic_bespoke",
                }
                f.write(json.dumps(entry) + "\n")
                f.flush()
                success += 1
                print(f"  [{success}/{len(MNEMONIC_DOMAINS)}] OK ({len(raw)} chars)")

    print(f"\nDone: {success}/{len(MNEMONIC_DOMAINS)} generated -> {output_path}")
    if daily_limit:
        print("Hit daily limit. Run again tomorrow to continue (resume supported).")
    print(f"\nNext: submit for encoding via Gemini Batch API:")
    print(f"  python batch_encode.py submit --input {output_path}")


if __name__ == "__main__":
    asyncio.run(main())
