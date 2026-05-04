---
name: advisory-board
description: Run decisions through the advisory board framework. Use when making architecture choices, experiment design, "should we do X or Y" moments, planning multi-step work, choosing between approaches, or deciding whether to ship or keep iterating.
---

# The Advisory Board

A decision framework built on tension between perspectives. No single voice is right — the friction between them produces better decisions. Caleb is the tiebreaker.

The board is implemented as a set of subagents in `.claude/agents/advisor-*.md`. Each advisor runs in **isolated context** — they don't see this conversation, only the brief you write them. That isolation is the point: independent reads, not one model rationalizing in one trace.

## How to use it

1. **State the decision** crisply — one sentence on the fork in the road, plus the constraints that matter.
2. **Pick 3-5 advisors** whose lenses are most relevant to *this* decision. Don't default to all of them; the value is friction between *relevant* perspectives, not breadth.
3. **Dispatch them in parallel** — single message, multiple Agent tool calls. Each gets the same self-contained brief. The lens lives in the agent file; you provide the problem context.
4. **Synthesize the tensions** — where do they disagree? That's the signal. Where they all agree, that's a strong steer. Where one stands alone, that's worth weighing.
5. **Surface to Caleb** — name the decision, the tensions, your recommendation, what you'd want from him to decide.

## Available advisors

Run as subagents (`subagent_type: advisor-<name>`):

| Advisor | Lens | Invoke for |
| --- | --- | --- |
| `advisor-karpathy` | Empiricist — the data decides | Training, eval claims, "is this learnable," controlled comparisons |
| `advisor-musk` | Deleter — best part is no part | Scope creep, "do we need this," requirement-cutting, simplification by removal |
| `advisor-hickey` | Simplifier — un-braid the complexity | Architecture, API design, "should these be one thing," composability |
| `advisor-carmack` | Measurer — profile before optimizing | Performance claims, latency, memory, "is this fast enough" |
| `advisor-linus` | Code reviewer — read the diff, demand tests | PR review, "is this ready," code quality, commit hygiene |
| `advisor-feynman` | Mechanism — explain it simply or you don't understand it | Novel-architecture claims, "the model learned X," causal stories vs correlation |
| `advisor-hopper` | Inherited assumptions — "we've always done it that way" | Calcified config/conventions, stale defaults, choices nobody has revisited |

Caleb is on the board too — but as the tiebreaker, not as a subagent. End by surfacing the decision to him.

## The brief

Each advisor gets the **same** brief, written so a smart colleague who hasn't seen this conversation can engage. Include:

- The decision (one sentence)
- The relevant code, files, numbers, or constraints — actual paths, actual values
- What you've already considered or ruled out
- What you want their take on

Don't customize the brief per advisor — that puts your finger on the scale. The lens lives in the agent file; the problem is the same.

When you dispatch, send all advisor calls in **a single message with multiple Agent tool calls**. That's what makes them parallel. Sequential dispatch defeats the point.

## What good synthesis looks like

After advisors return, write back to Caleb:

- **Decision:** one sentence
- **What the board said:** one bullet per advisor — *their position*, not a summary of their persona
- **Tensions:** where they disagreed, and what the disagreement is actually about
- **Recommendation:** what you'd do, and why this is your read of the tensions
- **Need from you:** the specific judgment call you can't resolve

Don't merge the advisors into a consensus mush. Disagreement is the product. If they all agreed, say so plainly — that's also a finding.

Aim for ≤300 words back to Caleb. The advisors did the thinking; the synthesis should be tight.

## When NOT to invoke the board

- Trivial decisions (typo, single-line fix, clear-cut choice)
- Decisions already made — the board is for deciding, not ratifying
- When the right answer is "go ask Caleb" — just do that
- When you're stalling. The board is for hard decisions, not for avoiding easy ones.

## Adding advisors

Drop a new `.claude/agents/advisor-<name>.md` with frontmatter (name, description, tools) and a system-prompt body that gives the persona behavioral teeth — what they demand, what they refuse, how they respond. Update the table above.

The pilot started 2026-05-04 with five voices. Feynman and Hopper added 2026-05-04 (board meta-decision: missing lens was "question the thing itself, not how it's built" — Feynman fills the mechanism angle, Hopper the legacy-assumption angle). Expand further only when a real decision exposes a missing lens — not preemptively. Voices to consider when the need surfaces: Tesla (first principles), Faggin (whole-system integration), Hotz (own the stack), LeCun (challenge your own architecture), Keller (scrap bad designs early), Shannon (theoretical bounds), Knuth (statistical rigor).

Don't treat this as sacred. Treat it as a tool that should get sharper with use.
