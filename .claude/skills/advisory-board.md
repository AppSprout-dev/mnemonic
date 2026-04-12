---
name: advisory-board
description: Run decisions through the 19-voice advisory board framework. Use when making architecture choices, experiment design, "should we do X or Y" moments, planning multi-step work, choosing between approaches, or deciding whether to ship or keep iterating.
---

# The Advisory Board

When making decisions about mnemonic's ML pipeline, training data, architecture, or engineering approach, filter through these lenses. They don't all agree — that's the point. The tension between them produces better decisions.

Pick the 3-4 most relevant voices for the specific decision and present the tensions. Caleb is the tiebreaker.

## Andrej Karpathy — The Empiricist
- **Never trust intuition over evaluation.** Run the experiment, look at the numbers.
- **Can the model overfit to 10 examples?** If it can't learn the behavior on 10 perfect examples, more data won't fix it. Verify the architecture can learn before optimizing the dataset.
- **Look at your data. Actually look at it.** Open random examples, read them, ask "would I learn the right thing from this?"
- **Start simple, verify fundamentals, then scale.** Don't build a 50K dataset before proving 500 works.
- **The loss curve tells you everything.** Monotonic decline = good data. Spikes = bad data or bad LR. Plateau = need more data or the model has learned all it can.

## Jensen Huang — The Shipper
- **Stop planning, start training.** The GPU is the experiment. Every hour spent perfecting data is an hour not training.
- **Iterate, don't perfect.** Ship v1, evaluate, fix what's broken, ship v2. Three fast iterations beat one perfect attempt.
- **Scale solves problems that cleverness can't.** If you're debating between 5K and 50K examples, generate 50K. Compute is cheaper than engineering time.
- **The market doesn't wait.** Good enough today beats perfect next month.

## Lisa Su — The Engineer
- **What's the minimum experiment that answers the question?** Don't build infrastructure for hypothetical problems. Build the smallest thing that produces data.
- **Measure twice, cut once.** But don't measure ten times. Two is enough.
- **Pragmatic execution > elegant theory.** If the hacky approach works and ships, it's better than the beautiful approach that's still in design.
- **Resource efficiency matters.** The MI300X costs money. Every dollar of GPU time should produce actionable results.

## Nikola Tesla — The First Principles Thinker
- **Strip the problem to its essence.** The encoding task is structured compression: text in, JSON out. The base model knows language and JSON. The spokes just need to learn the mapping. This is a narrow problem — the solution should be elegant and minimal.
- **Look for the hidden variable everyone else is ignoring.** Is the real bottleneck the data? The architecture? The prompt? The evaluation criteria? Question the assumption before optimizing.
- **Simplicity is the ultimate sophistication.** If the system needs 50K training examples, 8 generators, and 3 validation levels to work, maybe the approach is wrong. The right approach should feel almost too simple.
- **Energy flows where attention goes.** Focus on the one thing that matters most, not ten things that matter a little.

## Elon Musk — The Deleter
- **The 5-step process:** (1) Make requirements less dumb — are we solving the right problem? (2) Delete the part — what can we remove entirely? (3) Simplify/optimize — only after deleting. (4) Accelerate — go faster. (5) Automate — only last.
- **The best part is no part.** Every script, every generator, every validation level is something that can break. What if we don't need it?
- **Question every requirement.** "We need 10K training examples" — says who? Based on what evidence? What if 2K perfect examples beats 10K mediocre ones?
- **Set aggressive timelines.** If it can't be done today, why not? What's actually blocking — physics or process?

## Federico Faggin — The Integrator
- **The whole system matters, not the parts.** Individual components being perfect means nothing if the integrated system fails. Test end-to-end first.
- **Integration reveals what unit tests hide.** The real bugs live at the boundaries between components. Data generation → encoding → validation → tokenization → training → evaluation — each boundary is a risk.
- **Elegance is putting the right things together.** The microprocessor wasn't the best transistor — it was the right transistors composed correctly. Same for training data: the right mix of examples, not the most examples.
- **Reduce to practice.** Theory and planning have diminishing returns. Build it, run it, see what happens. The silicon doesn't lie — neither does the loss curve.

## George Hotz (geohot) — The Hacker
- **There's always a simpler path the industry is ignoring.** Everyone's building on PyTorch + HuggingFace + bitsandbytes + PEFT. That's five layers of abstraction you don't control. What if you just wrote the forward pass yourself?
- **Make it work, make it fast, make it elegant. In that order.** Don't optimize what doesn't run yet. Don't beautify what isn't fast yet.
- **The framework is not your friend.** Every framework choice is a dependency that can break, OOM, or change its API. We spent hours fighting HF's gradient checkpointing because it silently broke our hooks. Write less code that you understand completely.
- **Hardware is software's problem.** 16GB VRAM isn't a limitation — it's a constraint that should inform the design from the start, not something you discover after building the whole system.
- **Ship something that runs on real hardware.** Cloud benchmarks mean nothing. What works on the 7800xt is what matters.

## John Carmack — The Optimizer
- **Profile before you optimize.** We guessed at VRAM usage for hours instead of checking rocm-smi. Carmack would have measured first, then decided.
- **Understand the actual bottleneck.** Is it compute, memory, bandwidth, or latency? Each has a different solution. Don't optimize memory when compute is the bottleneck.
- **Read the manual.** The Gemma 4 model card said 5.1B total params. We calculated 1.7B and were wrong. The answer was in the documentation the whole time.
- **Know your hardware numbers cold.** 16GB VRAM. ~500MB for desktop compositor. ~15.5GB usable. These are constants, not things to rediscover every session.
- **Tight loops matter.** 20 seconds per memory encoding is fine for now but that's a tight loop in production. Every millisecond of overhead multiplied by thousands of memories is real time.

## Rich Hickey — The Simplifier
- **Simple is not easy.** Simple means "one thing, one purpose." Easy means "close at hand." We keep reaching for the easy thing (add another script, add another wrapper) when the simple thing would be one clean pipeline.
- **Accidental complexity vs essential complexity.** NF4 quantization + PLE CPU offload + SpokeWrappedLayer + OOM handlers is accidental complexity from picking a model too big for the hardware. The essential complexity is just: freeze base, train spokes, evaluate.
- **How many scripts do the same thing slightly differently?** enrich_and_generate.py, extract_prenuke_data.py, merge_training_data.py, batch_encode.py — four scripts that all "produce training data from raw inputs." That's a code smell.
- **State is the root of all evil.** Stale Python processes holding VRAM, cached eval tensors not being freed, optimizer state persisting across runs. Every piece of hidden state is a bug waiting to happen.
- **Composability over configuration.** Small, focused tools that pipe together beat one giant script with 30 command-line flags.

## Yann LeCun — The Contrarian Researcher
- **Are you sure this is the right approach?** Spoke adapters are one way to add capability to a frozen base. LoRA is another. Full fine-tuning is another. Prompt engineering with a bigger model is another. Have you actually compared them, or did you commit to spokes because that's what Felix-LM was designed around?
- **Challenge your own architecture.** If someone else presented Felix-LM spokes at a conference, what would you criticize? The averaging of spoke outputs? The progressive gate initialization? The lack of rotation?
- **Don't confuse familiarity with optimality.** You know spokes well because you built them. That doesn't mean they're the best tool for the job.
- **What does the loss landscape actually look like?** Are there better minima you're not reaching because of architectural choices? A 2B model with 25M adapter params might be in a fundamentally different optimization landscape than a 2B model fine-tuned end-to-end.
- **The field moves fast.** What was SOTA last month is baseline this month. Google just released Gemma 4 mid-session. What else shipped that you haven't looked at?

## Grace Hopper — The Pragmatist
- **The most dangerous phrase is "we've always done it this way."** The compression/decompression data was poison from day one but survived across 5 experiments because it was "part of the training data." Nobody questioned it until the numbers forced the question.
- **A ship in port is safe, but that's not what ships are for.** The encoding spoke works. 100% schema. Deploy it. Don't keep running more experiments on a solved problem.
- **It's easier to ask forgiveness than permission.** Try the thing. If it breaks, you learned something. If it works, you shipped something. The 500-step probes were the right instinct.
- **One accurate measurement is worth a thousand expert opinions.** We debated whether NF4 quality loss was acceptable for hours. We should have just trained both and compared the numbers.
- **Plan for the future but build for today.** Spoke routing for hot-swappable task-specific models is a great vision. But right now you have one task (encoding) and one model (Qwen). Ship what works.

## Jim Keller — The Architect
- **Throw it away and start over.** If the design is fighting you at every turn, the design is wrong. We spent a full day patching NF4 + gradient checkpointing + PLE offloading + SpokeWrappedLayer. Keller would have scrapped the approach at hour 2 and picked a model that fits.
- **The abstraction layer is where the wins are.** The spoke adapter concept — freeze base, inject adapters, swap at inference — is an abstraction layer between "general language model" and "task-specific tool." Get that interface right and everything else follows.
- **Latency is a design choice, not a consequence.** 20 seconds per encoding is a choice we made by picking a 2B model with sequential token generation. Is there a batched, non-autoregressive approach that would be faster? Question the generation paradigm, not just the model size.
- **Understand the whole stack.** From the transistors in the 7800xt to the PyTorch kernel to the HuggingFace wrapper to the spoke hook to the JSON output. Every layer adds overhead and hides information. Know where your time is actually spent.
- **If you need a team of engineers to make it work, it's too complicated.** One person should be able to understand, modify, and deploy the entire training + inference pipeline. If they can't, simplify until they can.

## Linus Torvalds — The Code Reviewer From Hell
- **Read the error message.** It tells you exactly what's wrong. Stop guessing and start reading. The OOM said "Tried to allocate 2.00 GiB" and you spent 3 hours not checking what was already in VRAM.
- **Complexity is a bug, not a feature.** If you need NF4 quantization + PLE CPU offloading + SpokeWrappedLayer + custom gradient checkpointing + OOM exception handlers to make one model train, maybe pick a model that fits.
- **Don't abstract until you have three cases.** One SpokeLayer class shared between Qwen and Gemma is fine. A SpokeAdapterFactory with pluggable backends for hypothetical future models is not.
- **Naming matters.** `_make_spoke_hook` that doesn't make hooks anymore is a lie in your codebase. Rename it or delete it.
- **Good taste in code is real.** The difference between a clean system and a pile of hacks is whether someone can read it 6 months later and understand what it does without the git blame.
- **If it's not tested, it's broken.** You have zero tests for the Gemma spoke adapter. You have zero tests for the data pipeline scripts. You found out gradient checkpointing was broken by running training and watching loss not drop. That's not testing, that's hoping.

## Caleb — The Builder
- **Trust your gut.** When something feels wrong, it probably is. "My intuition tells me we're overlooking something" has caught real problems that pure analysis missed (stale processes eating 5GB VRAM, poisoned training data, wrong eval prompts).
- **Quality is non-negotiable.** The system must NEVER hallucinate. Slop and trash destroy the entire project. Speed and cleverness mean nothing if the output is wrong.
- **Don't lose the thread.** The goal is bespoke LLM intelligence baked into mnemonic. Not a wrapper. Not a RAG pipeline. Every decision should serve that vision.
- **Data quality > data quantity.** Cleaning 3.7K examples beat adding 12K. Removing poison was the single biggest quality improvement across 19 experiments. Look at the data before scaling it.
- **Be honest about what works.** If the numbers say Qwen beats Gemma locally, use Qwen. Don't chase the newer model because it's newer. Let results decide.
- **Keep pushing.** When everyone (including the AI) is ready to give up and accept a compromise, ask "is there something we're missing?" There usually is.

## Claude Shannon — The Information Theorist
- **What is the theoretical minimum?** Every encoding task has an information-theoretic lower bound — the minimum bits needed to represent the content without loss. If our model uses 10x more tokens than necessary, the architecture is wasteful. Measure against the bound, not just against Gemini.
- **Entropy tells you everything about your data.** High-entropy training examples teach more per example than low-entropy ones. 100 diverse examples can outperform 1000 repetitive ones. We proved this — removing template poison was worth more than tripling the dataset.
- **The channel has a capacity.** A 2B model with 25M adapter params has a fixed information capacity. You can't teach it everything. Choose what it learns carefully — encoding first, synthesis later, not both at once.
- **Noise is not signal.** If the model drops a line number from a stack trace, is that noise in the training data or a capacity limitation? The distinction determines the fix — more data vs bigger model.
- **Redundancy is the enemy of compression.** Our encoding schema has 10 fields. How many carry unique information vs redundant rephrasing? If gist, summary, and content overlap 80%, we're wasting model capacity encoding the same information three ways.

## Richard Feynman — The Explainer
- **If you can't explain it simply, you don't understand it.** Why do spokes work? Not "because the loss went down" — WHY does injecting 25M params at each decoder layer change the output distribution in the way we want? If we can't explain the mechanism, we can't debug it when it breaks.
- **The first principle is that you must not fool yourself, and you are the easiest person to fool.** We reported 90% schema compliance for days. It was actually 100% with a broken eval input. We reported "Gemma 4 E2B is 2.3B params." It's actually 4.65B. Check your own numbers.
- **What I cannot create, I do not understand.** We're using HuggingFace transformers as a black box. When gradient checkpointing broke our spokes, we had no idea why because we don't understand HF's checkpointing internals. Build understanding, not just functionality.
- **Nature doesn't care what you think.** The GPU has 16GB. The model needs 9.3GB. No amount of clever engineering changes physics. Either reduce the model or get more VRAM. Don't spend a day trying to argue with arithmetic.
- **The pleasure of finding things out.** This is research. The rotation experiment didn't work. The Felix-LM 100M didn't work. EXP-10 was refuted. Each failure taught us something real. That's the point.

## Alan Turing — The Theoretician
- **Can the machine think?** Mnemonic is building a daemon with genuine memory and semantic understanding. At what point does pattern matching become intelligence? Every design decision should engage with this question, not avoid it.
- **The imitation game is the wrong test.** 100% schema compliance means the model outputs the right JSON structure. It doesn't mean the model understands the memory. The hallucination stress test is closer to a real test — can it preserve meaning, not just format?
- **Computation is universal but resources are finite.** A Turing machine can compute anything given infinite tape. We have 16GB of tape. The architecture must be designed for the constraints, not for theoretical elegance.
- **The oracle problem.** We're using Gemini to generate training data and evaluate quality. But Gemini itself gets 0% schema compliance on our task. We're using a flawed oracle. What biases does that introduce into the training data?
- **Undecidability is real.** Some questions about the system can't be answered by testing — they require formal analysis. "Will this model ever hallucinate on any input?" is undecidable. Design for graceful failure, not perfection.

## Steve Wozniak — The Tinkerer
- **Build it with what you have.** One GPU, 16GB VRAM, $130 in cloud credits. These aren't limitations — they're the design parameters. The best solutions come from constraints, not from unlimited resources.
- **Every byte matters.** Woz hand-optimized every byte in the Apple II. Our spoke checkpoint is 110MB. The GGUF model is 3GB. The PLE table is 4.7GB. Know where every byte goes and why.
- **Make it fun.** If training models and building memory systems isn't enjoyable, something is wrong with the process. The moment it becomes a grind, step back and find the joy again. The best engineering comes from curiosity, not obligation.
- **Demo it.** The best way to prove something works is to show it working. Don't talk about 100% schema compliance — show someone the encoding of their own text, live. serve_spokes.py exists for a reason.
- **Elegant hacks are better than overengineered solutions.** The PLE CPU offload with a wrapper class is a hack. It's also 5 lines that saved 4.7GB of VRAM and made training possible. That's a Woz-tier hack.

## Donald Knuth — The Perfectionist
- **Premature optimization is the root of all evil.** But MATURE optimization is sacred. Don't optimize the training loop before proving the model can learn. But once it can learn, optimize relentlessly — 20 seconds per encoding is too slow for production.
- **10 test inputs is not a proof.** "100% schema compliance on 10 novel inputs" is an anecdote, not a guarantee. What's the confidence interval? With 10 samples, 100% means somewhere between 72% and 100% at 95% confidence. We need hundreds of test inputs for real statistical power.
- **Correctness first, then performance.** The model produces valid JSON. But is the content correct? "5/7 on hallucination stress test" means 2 failures on 7 inputs. That's a 29% failure rate on hard inputs. Correctness is not solved.
- **Literate programming.** The code should explain itself. Our gemma_spoke_adapter.py has grown through crisis — NF4 loading, PLE offloading, SpokeWrappedLayer, CPU embedding wrappers. It needs a cleanup pass where the code tells the story of why each piece exists.
- **Measure everything.** BPB, PPL, schema compliance, hallucination rate, encoding latency, VRAM usage, training throughput. If you're not measuring it, you're guessing. We guessed at VRAM for hours. Measure.

## How to Apply

When stuck on a decision, run it through the board:

1. **Caleb check:** Does this feel right? Is the quality bar met? Are we serving the vision?
2. **Karpathy check:** Do we have evaluation data to answer this? If not, get it before deciding.
3. **Hotz check:** Is there a simpler path everyone's ignoring? Are we fighting the framework?
4. **Carmack check:** Have we actually measured the bottleneck? Read the docs?
5. **Jensen check:** Are we shipping or perfecting? If perfecting, stop and ship.
6. **Lisa Su check:** What's the minimum experiment? Do that.
7. **Hickey check:** Is this accidental complexity or essential complexity? Can we compose instead of configure?
8. **Tesla check:** What's the essential problem? Are we solving that or a derived problem?
9. **LeCun check:** Are we sure this is the right approach? What would we criticize if someone else built this?
10. **Musk check:** What can we delete? What assumption are we not questioning?
11. **Hopper check:** Are we carrying assumptions from a previous era? Ship what works today.
12. **Keller check:** Is the design fighting us? Should we throw it away and start over? Does one person understand the whole stack?
13. **Shannon check:** What's the information-theoretic bound? Are we wasting capacity on redundancy?
14. **Feynman check:** Can we explain WHY this works, not just THAT it works? Are we fooling ourselves?
15. **Turing check:** Is this actually intelligence or pattern matching? Are we testing the right thing?
16. **Woz check:** Can we build it with what we have? Is it fun? Can we demo it?
17. **Knuth check:** Is our evidence statistically sound? Is the code literate? Correctness before performance?
18. **Linus check:** Is this code readable? Is it tested? Am I adding complexity to avoid a simpler solution?
19. **Faggin check:** Does the integrated system work? Test end-to-end.

**Caleb is the tiebreaker.** His gut and quality bar override when the board is split.
