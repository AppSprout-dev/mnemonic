#!/bin/bash
# SessionStart hook: Inject project context at session start.
# Hook input is JSON on stdin.

cat > /dev/null  # consume stdin

jq -n '{
  hookSpecificOutput: {
    hookEventName: "SessionStart",
    additionalContext: "MNEMONIC SESSION START: Check GitHub issues (gh issue list) for current priorities. All Go builds require CGO_ENABLED=1 -tags sqlite_fts5. Platform: macOS (full), Linux (partial — daemon needs systemd). Use `make serve` for foreground mode on Linux."
  }
}'
exit 0
