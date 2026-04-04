#!/usr/bin/env python3
"""Generate distillation data: Gemini 3.1 Pro reasoning traces for encoding task.

For each raw input, asks Gemini to:
1. Think step-by-step about how to encode it (chain-of-thought)
2. Produce the structured 10-field JSON

The output format matches Qwen 3.5's native thinking template:
  <think>
  [Gemini's reasoning about gist, significance, concepts, etc.]
  </think>

  {"gist": "...", "summary": "...", ...}

This teaches the spoke model WHY to produce each field value, not just WHAT.

Usage:
    python generate_distillation_data.py \
        --input training/data/finetune_qwen_v2/train.jsonl \
        --output training/data/distillation_encoding.jsonl \
        --max 3000
"""

import argparse
import asyncio
import json
import os
import random
import sys
from pathlib import Path

import aiohttp

sys.path.insert(0, str(Path(__file__).resolve().parent))

from training_constants import REQUIRED_FIELDS  # noqa: E402

API_KEY = os.environ.get("LLM_API_KEY", "")
API_BASE = "https://generativelanguage.googleapis.com/v1beta/openai"
MODEL = "gemini-3.1-pro-preview"  # Best model for reasoning traces
MAX_CONCURRENT = 10  # Pro model may have lower rate limits
RETRY_LIMIT = 6

DISTILLATION_PROMPT = """You are a memory encoding teacher. You will receive a raw observation from a developer's work.

Your job is to THINK THROUGH the encoding process step by step, then produce the final JSON.

## Thinking process (show your work):
1. What is the core event or information here? (→ gist, summary)
2. What details are worth preserving? (→ content)
3. What's the broader context and why does this matter? (→ narrative)
4. What are the key concepts/keywords? (→ concepts)
5. What topics, entities, actions, and causal relationships are present? (→ structured_concepts)
6. How important is this? (→ significance: critical/important/notable/routine/trivial)
7. What's the emotional/analytical tone? (→ emotional_tone)
8. What was the outcome? (→ outcome)
9. How memorable is this long-term? (→ salience: 0.0-1.0)

## Output format:
First, write your step-by-step reasoning as plain text.
Then write EXACTLY the separator: ---JSON---
Then write ONLY the JSON object with these 10 fields: gist, summary, content, narrative, concepts, structured_concepts, significance, emotional_tone, outcome, salience.

Do NOT use markdown fences around the JSON."""

async def call_gemini(session: aiohttp.ClientSession, system: str, user: str,
                      semaphore: asyncio.Semaphore) -> str | None:
    headers = {"Authorization": f"Bearer {API_KEY}", "Content-Type": "application/json"}
    payload = {
        "model": MODEL,
        "messages": [
            {"role": "system", "content": system},
            {"role": "user", "content": user},
        ],
        "temperature": 0.7,
        "max_tokens": 3000,  # Longer for reasoning + JSON
    }

    for attempt in range(RETRY_LIMIT):
        async with semaphore:
            try:
                async with session.post(f"{API_BASE}/chat/completions",
                                        headers=headers, json=payload,
                                        timeout=aiohttp.ClientTimeout(total=60)) as resp:
                    if resp.status in (429, 503):
                        wait = min(60, 2 ** attempt * 3)
                        await asyncio.sleep(wait)
                        continue
                    resp.raise_for_status()
                    data = await resp.json()
                    content = data["choices"][0]["message"]["content"]
                    return content
            except Exception:
                if attempt < RETRY_LIMIT - 1:
                    await asyncio.sleep(2 ** attempt)
                    continue
                return None
    return None


def parse_distillation_response(text: str) -> tuple[str, dict] | None:
    """Parse response into (reasoning, json_dict).

    Expected format:
        [reasoning text]
        ---JSON---
        {"gist": ...}
    """
    # Try the explicit separator first
    if "---JSON---" in text:
        parts = text.split("---JSON---", 1)
        reasoning = parts[0].strip()
        json_text = parts[1].strip()
    else:
        # Fallback: find the last JSON object in the text
        last_brace = text.rfind("}")
        first_brace = text.rfind("{", 0, last_brace)
        if first_brace < 0 or last_brace < 0:
            return None
        # Walk backwards to find the matching opening brace
        depth = 0
        for i in range(last_brace, -1, -1):
            if text[i] == "}":
                depth += 1
            elif text[i] == "{":
                depth -= 1
            if depth == 0:
                first_brace = i
                break
        reasoning = text[:first_brace].strip()
        json_text = text[first_brace:last_brace + 1]

    # Strip markdown fences from JSON
    if json_text.startswith("```"):
        lines = json_text.split("\n")
        lines = [l for l in lines if not l.strip().startswith("```")]
        json_text = "\n".join(lines).strip()

    try:
        parsed = json.loads(json_text)
    except json.JSONDecodeError:
        return None

    if not REQUIRED_FIELDS.issubset(parsed.keys()):
        return None

    if len(reasoning) < 20:
        return None  # Reasoning too short to be useful

    return (reasoning, parsed)


async def process_one(session, semaphore, raw_input: str, idx: int):
    """Generate distillation data for one input."""
    response = await call_gemini(session, DISTILLATION_PROMPT, raw_input[:3000], semaphore)
    if response is None:
        return None

    result = parse_distillation_response(response)
    if result is None:
        return None

    reasoning, encoded = result
    return {
        "raw_input": raw_input,
        "reasoning": reasoning,
        "encoded": encoded,
        "task_type": "encoding",
        "source": "distillation",
    }


async def generate_distillation(inputs: list[str], output_path: str):
    print(f"Generating distillation data for {len(inputs)} inputs ({MAX_CONCURRENT} concurrent)...")

    semaphore = asyncio.Semaphore(MAX_CONCURRENT)
    async with aiohttp.ClientSession() as session:
        tasks = [process_one(session, semaphore, inp, i) for i, inp in enumerate(inputs)]
        results = []
        done = 0
        for coro in asyncio.as_completed(tasks):
            result = await coro
            done += 1
            if result:
                results.append(result)
            if done % 100 == 0:
                print(f"  [{done}/{len(inputs)}] success={len(results)}")

    with open(output_path, "w") as f:
        for r in results:
            f.write(json.dumps(r) + "\n")

    print(f"Distillation: {len(results)}/{len(inputs)} success. Written to: {output_path}")

    # Stats
    reasoning_lens = [len(r["reasoning"]) for r in results]
    if reasoning_lens:
        avg = sum(reasoning_lens) / len(reasoning_lens)
        print(f"Reasoning length: avg={avg:.0f} chars, min={min(reasoning_lens)}, max={max(reasoning_lens)}")


def extract_raw_inputs(train_path: str, max_examples: int) -> list[str]:
    """Extract raw input text from existing training data."""
    from transformers import AutoTokenizer
    tokenizer = AutoTokenizer.from_pretrained("Qwen/Qwen3.5-2B")

    inputs = []
    for line in open(train_path):
        d = json.loads(line)
        if d.get("task_type") != "encoding":
            continue

        ids = d["input_ids"]
        cs = d["completion_start"]
        # Decode the user message from the prompt
        prompt = tokenizer.decode(ids[:cs])
        # Extract user content between <|im_start|>user and <|im_end|>
        if "<|im_start|>user\n" in prompt:
            user_msg = prompt.split("<|im_start|>user\n")[-1].split("<|im_end|>")[0]
            if len(user_msg.strip()) > 30:
                inputs.append(user_msg.strip())

        if len(inputs) >= max_examples:
            break

    print(f"Extracted {len(inputs)} raw inputs from training data")
    return inputs


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", required=True, help="Training JSONL to extract inputs from")
    parser.add_argument("--output", required=True, help="Output distillation JSONL")
    parser.add_argument("--max", type=int, default=3000, help="Max examples to process")
    args = parser.parse_args()

    if not API_KEY:
        print("ERROR: LLM_API_KEY not set")
        sys.exit(1)

    inputs = extract_raw_inputs(args.input, args.max)
    random.shuffle(inputs)

    asyncio.run(generate_distillation(inputs, args.output))


if __name__ == "__main__":
    main()
