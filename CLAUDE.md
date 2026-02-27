# Mnemonic - Claude Code Memory System

Mnemonic is your persistent memory on this computer. Use it to remember decisions, errors, insights, and context across sessions.



## MCP Tools Available

You have 10 tools via the `mnemonic` MCP server:

| Tool | When to Use |
|------|-------------|
| `remember` | Store decisions, errors, insights, learnings |
| `recall` | Search for relevant memories before starting work |
| `forget` | Archive irrelevant memories |
| `status` | Check memory system health and stats |
| `recall_project` | Get project-specific context and patterns |
| `recall_timeline` | See what happened in a time range |
| `session_summary` | Summarize current/recent session |
| `get_patterns` | View discovered recurring patterns |
| `get_insights` | View metacognition observations and abstractions |
| `feedback` | Report recall quality (helps system learn) |

## Usage Guidelines

### At Session Start
- Use `recall_project` to load context for the current project
- Use `recall` with relevant keywords to find prior decisions

### During Work
- `remember` decisions with `type: "decision"` — e.g., "chose SQLite over Postgres for simplicity"
- `remember` errors with `type: "error"` — e.g., "nil pointer in auth middleware, fixed with guard clause"
- `remember` insights with `type: "insight"` — e.g., "spread activation works best with 3 hops max"
- `remember` learnings with `type: "learning"` — e.g., "Go's sql.NullString needed for nullable columns"

### After Recalls
- Use `feedback` to rate recall quality — this helps the system improve
- `helpful` = memories were relevant and useful
- `partial` = some relevant, some not
- `irrelevant` = memories didn't help

### For Context
- Use `recall_timeline` to reconstruct what happened in a time period
- Use `session_summary` to review what was accomplished
- Use `get_patterns` to see recurring themes the system has discovered
- Use `get_insights` for higher-level observations about work patterns

## Memory Types

When using `remember`, set the `type` field:
- `decision` — architectural choices, tradeoffs, "we chose X because Y"
- `error` — bugs found, error patterns, debugging insights
- `insight` — realizations about code, architecture, or process
- `learning` — new knowledge, API behaviors, framework quirks
- `general` — everything else (default)

## Project and Session

Project and session are auto-detected:
- **Project** = working directory name (override with `project` param)
- **Session** = auto-generated per MCP server lifetime

All memories are tagged with both, enabling project-scoped recall.
