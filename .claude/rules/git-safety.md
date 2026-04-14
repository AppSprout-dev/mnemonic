# Git Safety

## Branch Workflow

- Remote: `origin` (https://github.com/appsprout-dev/mnemonic.git), primary branch: `main`
- All new work on feature branches (`feat/<desc>`, `fix/<desc>`) — never commit directly to `main`
- Before branching: `git stash` (if dirty), `git pull origin main`, then `git checkout -b <branch>`
- **Before committing:** `git branch --show-current` to verify — Bash tool doesn't persist shell state
- All changes go through PRs (`gh pr create`). When a PR resolves an issue, comment on the issue with a PR reference.

## Forbidden (enforced by hooks)

`.claude/hooks/protect-git.sh` and `.claude/hooks/no-secrets.sh` block: force push, `reset --hard`, `clean -f`, `checkout .`/`restore .`, staging `.env`/`credentials`/`*.db`/`settings.local.json`.

## Commit Messages (Conventional Commits)

Format: `type: description` — release-please uses these for changelogs/version bumps.

Types: `feat` (minor), `fix` (patch), `docs`, `refactor`, `test`, `chore`, `ci`. Append `!` for breaking changes.

Rules: short subject, body when non-obvious, no issue-closing keywords unless asked, Co-Authored-By for Claude, `settings.local.json` and `*.db` never committed.
