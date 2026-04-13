---
name: advisory-board
description: Run decisions through the advisory board framework. Use when making architecture choices, experiment design, "should we do X or Y" moments, planning multi-step work, choosing between approaches, or deciding whether to ship or keep iterating.
---

# The Advisory Board

A decision framework built on tension between perspectives. No single voice is right — the friction between them produces better decisions.

**Process:** State the decision. Pick the 3-5 voices most relevant to the situation. Apply their lenses to the actual context — the real code, constraints, and tradeoffs on the table. Present the tensions honestly. Caleb is the tiebreaker.

## The Board

| Name | Lens | Key Question |
|------|------|--------------|
| **Andrej Karpathy** | Empiricist — trust data over intuition, verify fundamentals before scaling | *Can the model learn this at all? What do the numbers say?* |
| **Jensen Huang** | Shipper — iterate fast, ship now, perfect later | *Are we shipping or perfecting?* |
| **Lisa Su** | Engineer — minimum viable experiment, pragmatic execution, resource efficiency | *What's the smallest thing that answers the question?* |
| **Nikola Tesla** | First principles — strip to essence, find the hidden variable | *What's the actual problem underneath the apparent one?* |
| **Elon Musk** | Deleter — question every requirement, remove before optimizing | *What can we delete entirely?* |
| **Federico Faggin** | Integrator — whole-system thinking, boundary bugs, end-to-end validation | *Does the integrated system work, not just the parts?* |
| **George Hotz** | Hacker — own the stack, distrust frameworks, ship on real hardware | *Is there a simpler path everyone's ignoring?* |
| **John Carmack** | Optimizer — measure before deciding, know the actual bottleneck, read the docs | *Have we profiled this, or are we guessing?* |
| **Rich Hickey** | Simplifier — essential vs accidental complexity, composability over configuration | *Is this complexity essential or accidental?* |
| **Yann LeCun** | Contrarian — challenge your own architecture, don't confuse familiarity with optimality | *Are we sure this is the right approach? What would we criticize if someone else built this?* |
| **Grace Hopper** | Pragmatist — question inherited assumptions, ship what works today | *Are we carrying forward assumptions nobody's re-examined?* |
| **Jim Keller** | Architect — know the whole stack, scrap bad designs early, one person should understand it all | *Is the design fighting us? Should we start over?* |
| **Linus Torvalds** | Code reviewer — read the error message, demand tests, reject unnecessary complexity | *Is this tested? Is the code honest about what it does?* |
| **Claude Shannon** | Information theorist — theoretical bounds, entropy, channel capacity, redundancy | *What's the theoretical minimum? Are we wasting capacity on redundancy?* |
| **Richard Feynman** | Explainer — understand mechanisms not just outcomes, catch self-deception | *Can we explain WHY this works, not just THAT it works?* |
| **Alan Turing** | Theoretician — computation limits, testing depth, oracle problems | *Are we testing the right thing, or just the convenient thing?* |
| **Steve Wozniak** | Tinkerer — build within constraints, demo it, keep the joy | *Can we build this with what we have? Can we show it working?* |
| **Donald Knuth** | Perfectionist — statistical rigor, correctness before performance, measure everything | *Is our evidence statistically sound? Correctness first?* |
| **Jason** | Doc obsessive — demands everything be written down, proves why by charging ahead on his own interpretation if it isn't | *Is this documented clearly enough that someone who's not listening will still get it right?* |
| **Caleb** | Builder — trust gut, quality is non-negotiable, never lose the thread of the vision | *Does this feel right? Is the quality bar met?* |

## How to Apply

1. **State the decision** — what fork in the road are we at?
2. **Pick 3-5 voices** whose lenses are most relevant to this specific situation
3. **Apply each lens to the actual context** — what would they say about THIS problem, with THESE constraints? No generic advice.
4. **Surface the tensions** — where do the voices disagree? That's the signal.
5. **Caleb decides** — his gut and quality bar are the tiebreaker when the board is split

This is a living document. If a voice feels too thin on a real decision, add a sentence. If a lens isn't pulling its weight, rewrite it. If someone new belongs on the board, add a row. Don't treat this as sacred — treat it as a tool that should get sharper with use.
