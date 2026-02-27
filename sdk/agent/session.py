from __future__ import annotations

import json
import logging
import time
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path

logger = logging.getLogger(__name__)

MAX_SESSIONS = 100

from claude_agent_sdk import (
    AssistantMessage,
    ClaudeSDKClient,
    ResultMessage,
    SystemMessage,
    TextBlock,
    ThinkingBlock,
    ToolUseBlock,
)

from agent.config import Config
from agent.options import build_options

# --- Orchestration prompts ---

PRE_TASK_PROMPT = """\
Before working on the task below, recall relevant context:
1. Call mcp__mnemonic__recall with a query summarizing the upcoming task: "{task}"
2. Call mcp__mnemonic__get_patterns to check for relevant patterns.
3. Call mcp__mnemonic__get_insights for metacognition observations.
Briefly summarize what you found, then say "Ready." and stop.
"""

POST_TASK_PROMPT = """\
Reflect on the task you just completed:
1. What worked well? What didn't?
2. Store key learnings via mcp__mnemonic__remember (type=insight or learning).
3. If you discovered a new reliable principle, add it to evolution/principles.yaml.
4. If you developed a better strategy for this task type, update evolution/strategies.yaml.
5. If you realized your prompt is missing something, add to evolution/prompt_patches.yaml.
6. Log any evolution/ changes in evolution/changelog.md with today's date and rationale.
Be concise. Focus on what's genuinely worth preserving.
"""

EVOLVE_PROMPT = """\
Time for a self-improvement cycle. Reflect deeply on recent experience:
1. Call mcp__mnemonic__get_insights to review metacognition observations.
2. Call mcp__mnemonic__get_patterns to review recurring patterns.
3. Read evolution/principles.yaml — remove stale principles, increase confidence on validated ones.
4. Read evolution/strategies.yaml — refine strategies based on recent experience.
5. Consider adding new prompt_patches if you notice behavioral gaps.
6. Call mcp__mnemonic__audit_encodings (limit=5) — review recent encoding quality.
   If you see systematic quality gaps, call mcp__mnemonic__coach_local_llm to improve them.
7. Log ALL changes in evolution/changelog.md with today's date, what changed, and why.
Only change things you have evidence for. Don't speculate.
"""


# --- Telemetry ---

@dataclass
class TaskMetrics:
    cost_usd: float = 0.0
    turns: int = 0


async def _stream_responses(
    client: ClaudeSDKClient,
    verbose: bool = False,
) -> TaskMetrics:
    """Stream and print messages from the client. Returns metrics from ResultMessage."""
    metrics = TaskMetrics()
    async for msg in client.receive_messages():
        if isinstance(msg, AssistantMessage):
            for block in msg.content:
                if isinstance(block, TextBlock):
                    print(block.text, flush=True)
                elif isinstance(block, ThinkingBlock) and verbose:
                    logger.debug("[thinking] %s...", block.thinking[:200])
                elif isinstance(block, ToolUseBlock) and verbose:
                    logger.debug("[tool] %s(%s)", block.name, _truncate(str(block.input), 120))
        elif isinstance(msg, SystemMessage) and verbose:
            logger.debug("[system] %s: %s", msg.subtype, _truncate(str(msg.data), 200))
        elif isinstance(msg, ResultMessage):
            metrics.cost_usd = getattr(msg, "total_cost_usd", 0.0) or 0.0
            metrics.turns = getattr(msg, "num_turns", 0) or 0
            if verbose:
                parts = []
                if metrics.turns:
                    parts.append(f"turns={metrics.turns}")
                if metrics.cost_usd:
                    parts.append(f"cost=${metrics.cost_usd:.4f}")
                if parts:
                    logger.debug("[done] %s", ", ".join(parts))
            break
    return metrics


def _truncate(s: str, max_len: int) -> str:
    return s if len(s) <= max_len else s[: max_len - 3] + "..."


def _record_task(
    evolution_dir: str,
    session_id: str,
    model: str,
    description: str,
    started: str,
    duration_ms: int,
    cost_usd: float,
    turns: int,
    evolved: bool,
) -> None:
    """Append a task record to sessions.json."""
    sessions_path = Path(evolution_dir) / "sessions.json"

    # Load existing data
    data: dict = {"sessions": []}
    if sessions_path.exists():
        try:
            data = json.loads(sessions_path.read_text())
        except (json.JSONDecodeError, OSError):
            data = {"sessions": []}

    # Find or create current session
    current_session = None
    for s in data["sessions"]:
        if s.get("id") == session_id:
            current_session = s
            break

    if current_session is None:
        current_session = {
            "id": session_id,
            "started": started,
            "model": model,
            "tasks": [],
        }
        data["sessions"].append(current_session)

    # Append task
    current_session["tasks"].append({
        "description": description[:200],
        "started": started,
        "duration_ms": duration_ms,
        "cost_usd": round(cost_usd, 6),
        "turns": turns,
        "evolved": evolved,
    })

    # Rotate: keep only the most recent MAX_SESSIONS sessions
    if len(data["sessions"]) > MAX_SESSIONS:
        data["sessions"] = data["sessions"][-MAX_SESSIONS:]

    # Write back
    try:
        sessions_path.write_text(json.dumps(data, indent=2))
    except OSError as e:
        logger.warning("Failed to write sessions.json: %s", e)  # Non-critical — don't crash the agent


async def run_session(cfg: Config, initial_prompt: str | None = None) -> None:
    """Main REPL loop with pre/post task orchestration."""
    options = build_options(cfg)
    task_count = 0
    session_id = f"session-{uuid.uuid4().hex[:8]}"

    async with ClaudeSDKClient(options=options) as client:
        # If an initial task was provided via CLI, run it
        if initial_prompt:
            await _run_task(client, cfg, initial_prompt, session_id, task_count)
            task_count += 1
            if task_count % cfg.evolve_interval == 0:
                await _run_evolution(client, cfg)
            return

        # Interactive REPL
        print("mnemonic-agent ready. Type your task, or 'exit' to quit.\n", flush=True)
        while True:
            try:
                user_input = input("> ").strip()
            except (EOFError, KeyboardInterrupt):
                print("\nGoodbye.", flush=True)
                break

            if not user_input:
                continue
            if user_input.lower() in ("/exit", "/quit", "exit", "quit"):
                break

            await _run_task(client, cfg, user_input, session_id, task_count)
            task_count += 1

            # Periodic evolution cycle
            if task_count % cfg.evolve_interval == 0:
                await _run_evolution(client, cfg)


async def _run_task(
    client: ClaudeSDKClient,
    cfg: Config,
    task: str,
    session_id: str,
    task_count: int,
) -> None:
    """Execute a single task with pre-task recall and post-task reflection."""
    task_start = time.monotonic()
    started_iso = datetime.now(timezone.utc).isoformat()
    total_cost = 0.0
    total_turns = 0

    # === PRE-TASK: automatic context recall ===
    if not cfg.no_reflect:
        if cfg.verbose:
            logger.debug("\n[pre-task] Recalling context...")
        await client.query(PRE_TASK_PROMPT.format(task=task))
        m = await _stream_responses(client, verbose=cfg.verbose)
        total_cost += m.cost_usd
        total_turns += m.turns

    # === MAIN TASK ===
    if cfg.verbose:
        logger.debug("\n[task] %s", task)
    await client.query(task)
    m = await _stream_responses(client, verbose=cfg.verbose)
    total_cost += m.cost_usd
    total_turns += m.turns

    # === POST-TASK: automatic reflection ===
    if not cfg.no_reflect:
        if cfg.verbose:
            logger.debug("\n[post-task] Reflecting...")
        await client.query(POST_TASK_PROMPT)
        m = await _stream_responses(client, verbose=cfg.verbose)
        total_cost += m.cost_usd
        total_turns += m.turns

    # === Record telemetry ===
    evolved = (task_count + 1) % cfg.evolve_interval == 0
    duration_ms = int((time.monotonic() - task_start) * 1000)
    _record_task(
        evolution_dir=cfg.evolution_dir,
        session_id=session_id,
        model=cfg.model,
        description=task,
        started=started_iso,
        duration_ms=duration_ms,
        cost_usd=total_cost,
        turns=total_turns,
        evolved=evolved,
    )


async def _run_evolution(
    client: ClaudeSDKClient,
    cfg: Config,
) -> None:
    """Run a self-improvement cycle."""
    if cfg.no_reflect:
        return
    logger.info("\n[evolution] Running self-improvement cycle...")
    await client.query(EVOLVE_PROMPT)
    await _stream_responses(client, verbose=cfg.verbose)
    logger.info("[evolution] Complete.")
