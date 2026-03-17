#!/usr/bin/env bash
# PreToolUse hook: block Edit/Write when on main branch.
# Forces Claude to create a feature branch before making code changes.

set -euo pipefail

# Only check inside the project git repo
cd "$CLAUDE_PROJECT_DIR" 2>/dev/null || exit 0

branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null) || exit 0

if [ "$branch" = "main" ] || [ "$branch" = "master" ]; then
    echo "BLOCKED: You are on the '$branch' branch. Create a feature branch (fix/* or feat/*) before editing files."
    exit 2
fi
