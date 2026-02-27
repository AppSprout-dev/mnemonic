#!/bin/bash
# Block staging or committing secrets in Mnemonic repo.
# Hook input is JSON on stdin.

INPUT=$(cat)
COMMAND=$(echo "$INPUT" | jq -r '.tool_input.command // empty')

if [ -z "$COMMAND" ]; then
  exit 0
fi

# Only check git commands
if ! echo "$COMMAND" | grep -qE 'git commit|git push|git add'; then
  exit 0
fi

# Block adding sensitive files
if echo "$COMMAND" | grep -qE 'git add'; then
  # Block adding local config files
  if echo "$COMMAND" | grep -qE 'settings\.local\.json'; then
    echo "BLOCKED: attempting to stage local config file that may contain secrets." >&2
    exit 2
  fi
  # Block adding .env files
  if echo "$COMMAND" | grep -qE '\.env|credentials|\.secret'; then
    echo "BLOCKED: attempting to stage files that likely contain secrets." >&2
    exit 2
  fi
  # Block adding database files
  if echo "$COMMAND" | grep -qE 'mnemonic\.db|memory\.db'; then
    echo "BLOCKED: attempting to stage database file. These are gitignored for a reason." >&2
    exit 2
  fi
fi

# For git commit commands, check for inline secrets in the message
if echo "$COMMAND" | grep -qE 'git commit'; then
  # Check for generic API key/token patterns
  if echo "$COMMAND" | grep -qiE 'api[_-]?(key|token|secret)\s*[:=]\s*['\''"][a-zA-Z0-9]{20,}'; then
    echo "BLOCKED: commit message appears to contain an API key or token." >&2
    exit 2
  fi
fi

exit 0
