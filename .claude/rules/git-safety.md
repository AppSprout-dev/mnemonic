# Git Safety

## Branch Workflow

- Remote: `origin` (https://github.com/CalebisGross/mnemonic.git)
- Primary branch: `main`
- Feature branches for non-trivial changes: `feat/<description>`, `fix/<description>`
- Direct commits to `main` are OK for small fixes during solo development

## Forbidden Operations

Enforced by `.claude/hooks/protect-git.sh` and `.claude/hooks/no-secrets.sh`:

- `git push --force` / `git push -f` -- destroys remote history
- `git reset --hard` -- destroys local changes
- `git clean -f` -- permanently deletes untracked files
- `git checkout .` / `git restore .` -- discards all unstaged changes
- Staging `.env`, `credentials`, `*.db`, `settings.local.json`

## Commit Messages

- Short, direct subject line describing the change
- Body for context when non-obvious
- No issue-closing keywords in commit messages unless explicitly asked
- Use Co-Authored-By for Claude contributions

## Secrets

- `settings.local.json` contains machine-specific permissions -- NEVER commit
- `*.db` files contain user data -- gitignored
- Never include API tokens in commit messages or code
