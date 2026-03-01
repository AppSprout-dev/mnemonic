#!/bin/bash
# Block dangerous git operations for Mnemonic repo.
# Hook input is JSON on stdin.

INPUT=$(cat)
COMMAND=$(echo "$INPUT" | jq -r '.tool_input.command // empty')

if [ -z "$COMMAND" ]; then
  exit 0
fi

# Only check git commands
if ! echo "$COMMAND" | grep -q 'git '; then
  exit 0
fi

# Block: git push --force or git push -f
if echo "$COMMAND" | grep -qE 'git push\s.*(\s-f\b|\s--force\b)'; then
  echo "BLOCKED: force push is not allowed. It can destroy remote branch history." >&2
  exit 2
fi

# Block: git reset --hard
if echo "$COMMAND" | grep -qE 'git reset\s+--hard'; then
  echo "BLOCKED: git reset --hard is destructive. Use git stash or git checkout <file> for specific files instead." >&2
  exit 2
fi

# Block: git clean -f (force clean untracked files)
if echo "$COMMAND" | grep -qE 'git clean\s.*-f'; then
  echo "BLOCKED: git clean -f permanently deletes untracked files. Be more targeted." >&2
  exit 2
fi

# Block: git checkout . (discard all changes)
if echo "$COMMAND" | grep -qE 'git checkout\s+\.$'; then
  echo "BLOCKED: git checkout . discards all unstaged changes. Use git checkout <specific-file> instead." >&2
  exit 2
fi

# Block: git restore . (discard all changes)
if echo "$COMMAND" | grep -qE 'git restore\s+\.$'; then
  echo "BLOCKED: git restore . discards all unstaged changes. Use git restore <specific-file> instead." >&2
  exit 2
fi

exit 0
