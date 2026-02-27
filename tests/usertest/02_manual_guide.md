# Mnemonic User Test — Part 2: Manual Activity Guide

After running the injection script, do some **real activity** on your machine for 5-10 minutes so the watchers (filesystem, terminal) can pick up organic events. This gives us data from both injection and natural capture to compare.

## What to do

Pick 3-4 of these and do them naturally — don't rush, just work the way you normally would.

### Filesystem activity (watched: ~/Documents, ~/Projects)
- Create or edit a file in `~/Projects` — even just add a comment to a source file
- Save some notes in `~/Documents` — a quick `.md` or `.txt` file about anything
- Rename or move a file in a watched directory

### Terminal activity (shell history is polled every 10s)
- Run some real commands: `git status`, `git log --oneline -5`, `docker ps`, `npm test`, whatever you'd normally do
- Run something with interesting output: `curl` an API, `cat` a config file, `grep` through a project
- Do a multi-step thing: navigate to a project, check status, make a small change, commit it

### Query testing (after ~2 minutes of activity)
Try these queries via the dashboard (`http://127.0.0.1:9999`) or CLI:

```bash
# Should retrieve the webhook debugging episode
mnemonic recall "payment webhook errors"

# Should retrieve the SSO research episode
mnemonic recall "which auth library did I pick for SSO"

# Should retrieve the status update and onboarding results
mnemonic recall "onboarding A/B test results"

# Cross-episode retrieval — should connect the rate limiter PR to the webhook fix
mnemonic recall "rate limiting and webhooks"

# Vague/personal query — should find the planning notes
mnemonic recall "what do I have tomorrow"

# Should find nothing or only tangentially related results
mnemonic recall "kubernetes cluster migration"
```

### What to look for

When you run queries, pay attention to:

1. **Relevance** — do the top results actually match what you asked?
2. **Summaries** — are the memory summaries readable and informative, or garbled?
3. **Concepts** — do the extracted concepts make sense for each memory?
4. **Synthesis** — when `--synthesize` is on, is the answer coherent?
5. **Associations** — on the graph view, are related memories connected?
6. **Episodes** — do the episodes tab show 4+ distinct episodes with sensible titles?

## After manual testing

Run the evaluation script:

```bash
./tests/usertest/03_evaluate.sh
```

This will query the API and produce a structured report on data quality.
