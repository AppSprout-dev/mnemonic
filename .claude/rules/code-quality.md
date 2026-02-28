# Code Quality & Scope Discipline

## Scope

- Only change what was asked for — don't touch surrounding code
- If you spot something worth fixing but it wasn't requested, call it out instead of silently doing it
- No drive-by refactors, no "while I'm here" improvements
- One logical change per task — don't bundle unrelated fixes

## Change Safety

- Read before edit — always understand a file before modifying it
- Build and test after changes, don't assume it works
- No new dependencies without discussing it first
- Don't delete code you don't fully understand

## Review Mindset

- Don't add comments, docstrings, or type annotations to untouched code
- Don't rename things that aren't part of the task
- Don't "improve" error messages or formatting in adjacent code
- Keep PRs reviewable — small, focused diffs
- If a change is getting large, pause and check in with the user
