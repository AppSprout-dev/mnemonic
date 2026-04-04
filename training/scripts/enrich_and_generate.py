#!/usr/bin/env python3
"""Enrich extracted pre-nuke data and generate synthetic encoding examples via Gemini.

Uses async concurrency for speed — 20 parallel requests instead of sequential.

Usage:
    # Enrich pre-nuke extracted data
    python enrich_and_generate.py enrich --input training/data/prenuke_extracted.jsonl --output training/data/enriched_prenuke.jsonl

    # Generate synthetic encoding examples
    python enrich_and_generate.py generate --output training/data/synthetic_encoding.jsonl --count 2000

    # Both
    python enrich_and_generate.py both --input training/data/prenuke_extracted.jsonl \
        --output-enrich training/data/enriched_prenuke.jsonl \
        --output-generate training/data/synthetic_encoding.jsonl --count 2000
"""

import argparse
import asyncio
import json
import os
import random
import sys
import time

import aiohttp

API_KEY = os.environ.get("LLM_API_KEY", "")
API_BASE = "https://generativelanguage.googleapis.com/v1beta/openai"
MODEL = "gemini-3-flash-preview"
MAX_CONCURRENT = 20  # parallel requests
RETRY_LIMIT = 5

ENCODING_SYSTEM_PROMPT = """You are a memory encoding agent for Mnemonic, a semantic memory system.
You receive raw events (text observations from a developer's work) and output structured JSON.

Your output MUST be a single JSON object with exactly these 10 fields:
- gist: One-line summary, under 80 characters
- summary: 2-3 sentence summary of the key information
- content: Preserved detail — the important facts, decisions, and context
- narrative: A paragraph providing broader context and significance
- concepts: Array of 3-8 keyword strings (lowercase, no phrases longer than 3 words)
- structured_concepts: Object with 4 arrays:
    - topics: [{label, path}] — what domains this touches
    - entities: [{name, type, context}] — people, tools, systems mentioned
    - actions: [{verb, object, details}] — what was done
    - causality: [{relation, description}] — cause/effect relationships
- significance: One of "critical", "important", "notable", "routine", "trivial"
- emotional_tone: One of "positive", "negative", "neutral", "frustrated", "excited", "analytical", "reflective"
- outcome: Brief description of the result or status
- salience: Float 0.0-1.0 (how important is this to remember long-term)

Output ONLY the JSON object. No markdown fences, no explanation, no preamble."""

SYNTHETIC_DOMAINS = [
    "debugging a race condition in a concurrent system",
    "choosing between two database architectures",
    "refactoring a monolith into microservices",
    "performance profiling and optimization",
    "code review feedback on a pull request",
    "CI/CD pipeline failure investigation",
    "dependency upgrade breaking changes",
    "API design decision and trade-offs",
    "security vulnerability discovery and fix",
    "deployment rollback after production incident",
    "setting up monitoring and alerting",
    "writing integration tests for a new feature",
    "migrating from one cloud provider to another",
    "implementing caching strategy",
    "designing a data pipeline",
    "hyperparameter tuning results",
    "model evaluation on held-out test set",
    "data preprocessing pipeline bug",
    "training loss divergence investigation",
    "feature engineering experiment",
    "model deployment and serving setup",
    "dataset quality audit findings",
    "A/B test results analysis",
    "GPU memory optimization for training",
    "fine-tuning a pretrained model",
    "Kubernetes pod crash loop diagnosis",
    "network latency investigation",
    "disk space emergency cleanup",
    "SSL certificate rotation",
    "load balancer configuration change",
    "log aggregation pipeline setup",
    "backup and disaster recovery test",
    "infrastructure cost optimization",
    "meeting notes from a design review",
    "research paper summary and key takeaways",
    "project retrospective findings",
    "onboarding documentation updates",
    "technical specification draft review",
    "customer bug report investigation",
    "quarterly goals and progress tracking",
    "team process improvement proposal",
    "vendor evaluation comparison",
    "open source contribution review",
    "learning a new programming language",
    "reading notes from a technical book",
    "conference talk key insights",
    "side project progress update",
    "debugging environment setup issues",
    "exploring a new tool or framework",
]

REQUIRED_FIELDS = {"gist", "summary", "content", "narrative", "concepts",
                   "structured_concepts", "significance", "emotional_tone",
                   "outcome", "salience"}


def parse_json_response(text: str) -> dict | None:
    text = text.strip()
    if text.startswith("```"):
        lines = text.split("\n")
        lines = [l for l in lines if not l.strip().startswith("```")]
        text = "\n".join(lines).strip()
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        start = text.find("{")
        end = text.rfind("}") + 1
        if start >= 0 and end > start:
            try:
                return json.loads(text[start:end])
            except json.JSONDecodeError:
                return None
    return None


def validate_encoding(data: dict) -> bool:
    return REQUIRED_FIELDS.issubset(data.keys())


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
        "max_tokens": 2048,
    }

    for attempt in range(RETRY_LIMIT):
        async with semaphore:
            try:
                async with session.post(f"{API_BASE}/chat/completions",
                                        headers=headers, json=payload,
                                        timeout=aiohttp.ClientTimeout(total=30)) as resp:
                    if resp.status in (429, 503):
                        wait = min(30, 2 ** attempt * 2)
                        await asyncio.sleep(wait)
                        continue
                    resp.raise_for_status()
                    data = await resp.json()
                    return data["choices"][0]["message"]["content"]
            except Exception as e:
                if attempt < RETRY_LIMIT - 1:
                    await asyncio.sleep(2 ** attempt)
                    continue
                return None
    return None


async def enrich_one(session, semaphore, ex):
    raw = ex.get("raw_input", "")
    if not raw or len(raw.strip()) < 20:
        return None

    response = await call_gemini(session, ENCODING_SYSTEM_PROMPT, raw[:3000], semaphore)
    if response is None:
        return None

    parsed = parse_json_response(response)
    if parsed is None or not validate_encoding(parsed):
        return None

    return {
        "raw_input": raw,
        "encoded": parsed,
        "source": f"prenuke_{ex['source']}",
        "task_type": "encoding",
    }


async def generate_one(session, semaphore, domain):
    gen_prompt = (
        f"Generate a realistic, specific observation that a software developer or "
        f"ML engineer might record about: {domain}. "
        f"Include concrete details (specific numbers, file names, tool versions, "
        f"error messages, metrics). 3-6 sentences. Output ONLY the observation text."
    )

    raw_input = await call_gemini(
        session,
        "You generate realistic developer observations. Be specific and concrete.",
        gen_prompt,
        semaphore,
    )
    if raw_input is None or len(raw_input.strip()) < 30:
        return None

    response = await call_gemini(session, ENCODING_SYSTEM_PROMPT, raw_input[:3000], semaphore)
    if response is None:
        return None

    parsed = parse_json_response(response)
    if parsed is None or not validate_encoding(parsed):
        return None

    return {
        "raw_input": raw_input.strip(),
        "encoded": parsed,
        "source": "synthetic",
        "domain": domain,
        "task_type": "encoding",
    }


async def enrich_examples(input_path: str, output_path: str):
    examples = [json.loads(line) for line in open(input_path)]
    print(f"Enriching {len(examples)} memories via Gemini ({MAX_CONCURRENT} concurrent)...")

    semaphore = asyncio.Semaphore(MAX_CONCURRENT)
    async with aiohttp.ClientSession() as session:
        tasks = [enrich_one(session, semaphore, ex) for ex in examples]
        results = []
        done = 0
        for coro in asyncio.as_completed(tasks):
            result = await coro
            done += 1
            if result:
                results.append(result)
            if done % 100 == 0:
                print(f"  [{done}/{len(examples)}] success={len(results)}")

    with open(output_path, "w") as f:
        for r in results:
            f.write(json.dumps(r) + "\n")

    print(f"Enrichment: {len(results)}/{len(examples)} success. Written to: {output_path}")


async def generate_synthetic(output_path: str, count: int):
    print(f"Generating {count} synthetic examples via Gemini ({MAX_CONCURRENT} concurrent)...")

    domains = [random.choice(SYNTHETIC_DOMAINS) for _ in range(count)]
    semaphore = asyncio.Semaphore(MAX_CONCURRENT)

    async with aiohttp.ClientSession() as session:
        tasks = [generate_one(session, semaphore, d) for d in domains]
        results = []
        done = 0
        for coro in asyncio.as_completed(tasks):
            result = await coro
            done += 1
            if result:
                results.append(result)
            if done % 100 == 0:
                print(f"  [{done}/{count}] success={len(results)}")

    with open(output_path, "w") as f:
        for r in results:
            f.write(json.dumps(r) + "\n")

    print(f"Generation: {len(results)}/{count} success. Written to: {output_path}")


async def run_both(args):
    await enrich_examples(args.input, args.output_enrich)
    await generate_synthetic(args.output_generate, args.count)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("mode", choices=["enrich", "generate", "both"])
    parser.add_argument("--input", help="Input JSONL for enrich mode")
    parser.add_argument("--output", help="Output JSONL (single mode)")
    parser.add_argument("--output-enrich", help="Enrichment output (both mode)")
    parser.add_argument("--output-generate", help="Generation output (both mode)")
    parser.add_argument("--count", type=int, default=2000)
    args = parser.parse_args()

    if not API_KEY:
        print("ERROR: LLM_API_KEY not set")
        sys.exit(1)

    if args.mode == "enrich":
        asyncio.run(enrich_examples(args.input, args.output))
    elif args.mode == "generate":
        asyncio.run(generate_synthetic(args.output, args.count))
    elif args.mode == "both":
        asyncio.run(run_both(args))


if __name__ == "__main__":
    main()
