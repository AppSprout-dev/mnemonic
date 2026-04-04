# Mnemonic MCP Tool Usage — Mandatory

## Session Start

For tasks involving code changes, decisions, or multi-step work:
1. Call `recall_project` to load project context
2. Call `recall` with keywords relevant to the user's first request
3. If either call returns useful context, use it to inform your work
4. If a call fails (FTS error, timeout), note it and move on — don't block the session

Alternative: Use `batch_recall` to combine multiple queries into one round-trip.

For trivial tasks (typo fix, single-line change, quick question): skip recall and just do the work.

## During Work (MUST)

### Remember

- **Decisions**: Architectural/design choices — `type: "decision"`
- **Errors**: Bugs encountered and resolved — `type: "error"`
- **Insights**: Non-obvious discoveries about the codebase — `type: "insight"`
- **Learnings**: Library, API, or framework behavior — `type: "learning"`
- **Experiment results**: HP sweep findings, benchmark baselines, training outcomes — `type: "insight"` or `type: "decision"` depending on whether it's an observation or a choice made from it

Use judgment — remember things a future session would need. Don't remember trivial actions, file paths, or things derivable from git history.

### Recall mid-session

Don't only recall at session start. When entering new territory (new subsystem, unfamiliar pattern, making claims about prior work), call `recall` with specific keywords first. Example: before suggesting HP ranges, recall prior training findings. Before claiming something works a certain way, check if there's a stored decision or learning about it.

### Amend stale memories

If a recall returns a memory that's outdated or partially wrong, use `amend` to update it in place rather than creating a new memory. This preserves associations and history.

## After Recalls (MUST)

- After using `recall` and acting on the results, call `feedback`:
  - `helpful` — memories were relevant and informed your work
  - `partial` — some relevant, some noise
  - `irrelevant` — memories didn't help
- If recall returned 0 results, no feedback needed — but consider whether your query was too broad or too specific
- This trains the retrieval system — skipping it degrades future recall quality

## Between Phases / Major Tasks (MUST)

When working through multi-phase plans (epics, milestones, sequential issues):
- `remember` key decisions, strategy changes, or gotchas from the completed phase before starting the next
- `recall` relevant context before entering a new phase — prior phase decisions may affect the current one
- This ensures continuity across long sessions and prevents rediscovering the same issues

## Reducing Noise

- Use `include_patterns: false` and `include_abstractions: false` on `recall` when you only need memories, not patterns/principles
- Use `types: ["decision", "error"]` to filter recall to actionable memory types
- Use `dismiss_pattern` and `dismiss_abstraction` to archive noise that keeps surfacing

## Before Committing (SHOULD)

- Review the session's work and `remember` any decisions or insights that haven't been stored yet
- Call `session_summary` if the session involved significant work

## General

- Prefer specific `recall` queries over broad ones — "SQLite FTS5 migration" not "database stuff"
- Set the `type` field on every `remember` call — never use the default "general" when a specific type fits
- When a recall returns irrelevant noise, say so via `feedback` — this is how the system improves
- Don't remember things that belong in experiment docs — training results go in `training/docs/`, not just in mnemonic memory. Memory is for cross-session context, not a substitute for proper documentation
