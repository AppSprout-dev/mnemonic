#!/usr/bin/env python3
"""Generate synthetic multi-turn synthesis training data with tool-use.

Pulls real memories and associations from the mnemonic DB, constructs
retrieval scenarios, and uses Gemini as a teacher to generate multi-turn
conversations with tool calls and final synthesis.

Usage:
    export LLM_API_KEY=<gemini-key>
    python training/scripts/generate_synthesis_data.py \
        --db ~/.mnemonic/memory.db \
        --output training/data/synthesis_data.jsonl \
        --num-scenarios 200

Output format (JSONL): one training example per line with multi-turn messages
including tool_calls and tool results, matching the retrieval agent's format.
"""

import argparse
import json
import os
import random
import sqlite3
import time
from pathlib import Path
from collections import defaultdict

try:
    import requests
    HAS_REQUESTS = True
except ImportError:
    HAS_REQUESTS = False


# Tool definitions matching internal/agent/retrieval/agent.go
SYNTHESIS_TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "search_memories",
            "description": "Search for additional memories by keyword or phrase.",
            "parameters": {
                "type": "object",
                "properties": {
                    "query": {
                        "type": "string",
                        "description": "The search query — a keyword, phrase, or concept",
                    }
                },
                "required": ["query"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_related",
            "description": "Follow connections from a specific memory to find related ones.",
            "parameters": {
                "type": "object",
                "properties": {
                    "memory_id": {
                        "type": "string",
                        "description": "The ID of the memory to explore connections from",
                    }
                },
                "required": ["memory_id"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_details",
            "description": "Get the full detail of a specific memory.",
            "parameters": {
                "type": "object",
                "properties": {
                    "memory_id": {
                        "type": "string",
                        "description": "The ID of the memory to get full details for",
                    }
                },
                "required": ["memory_id"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "search_timeline",
            "description": "Find memories from a specific time period.",
            "parameters": {
                "type": "object",
                "properties": {
                    "from": {"type": "string", "description": "Start date YYYY-MM-DD"},
                    "to": {"type": "string", "description": "End date YYYY-MM-DD"},
                },
                "required": ["from", "to"],
            },
        },
    },
]


def connect_db(db_path: str) -> sqlite3.Connection:
    return sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)


def load_scenario_data(conn: sqlite3.Connection) -> dict:
    """Load memories, associations, and patterns for scenario generation."""
    c = conn.cursor()

    # Load MCP-sourced memories (highest quality)
    c.execute("""
        SELECT id, summary, content, concepts, project, timestamp
        FROM memories
        WHERE source = 'mcp' AND state = 'active'
        AND summary IS NOT NULL AND length(summary) > 20
        ORDER BY timestamp DESC
        LIMIT 5000
    """)
    memories = []
    for row in c.fetchall():
        mid, summary, content, concepts_json, project, ts = row
        concepts = []
        if concepts_json:
            try:
                concepts = json.loads(concepts_json)
            except json.JSONDecodeError:
                pass
        memories.append({
            "id": mid,
            "summary": summary,
            "content": content or summary,
            "concepts": concepts[:5] if concepts else [],
            "project": project or "unknown",
            "timestamp": ts,
        })

    # Load associations for these memories
    mem_ids = {m["id"] for m in memories}
    c.execute("""
        SELECT source_id, target_id, strength, relation_type
        FROM associations
        WHERE strength >= 0.6
    """)
    associations = defaultdict(list)
    for src, tgt, strength, rel in c.fetchall():
        if src in mem_ids:
            associations[src].append({"target": tgt, "strength": strength, "relation": rel})
        if tgt in mem_ids:
            associations[tgt].append({"target": src, "strength": strength, "relation": rel})

    # Load patterns
    c.execute("SELECT title, description, strength FROM patterns WHERE strength > 0.5 LIMIT 20")
    patterns = [{"title": r[0], "description": r[1], "strength": r[2]} for r in c.fetchall()]

    return {
        "memories": memories,
        "associations": associations,
        "patterns": patterns,
        "mem_by_id": {m["id"]: m for m in memories},
    }


def build_scenario(data: dict) -> dict | None:
    """Build a retrieval scenario from real data.

    Returns a scenario with: query, initial memories, related memories
    (reachable via tools), and expected synthesis.
    """
    memories = data["memories"]
    if len(memories) < 10:
        return None

    # Pick a cluster: start with a random memory, find its associates
    anchor = random.choice(memories)
    assocs = data["associations"].get(anchor["id"], [])

    if len(assocs) < 2:
        return None

    # Initial memories (what the retriever would surface)
    initial = [anchor]
    for a in assocs[:3]:
        target = data["mem_by_id"].get(a["target"])
        if target:
            initial.append(target)

    if len(initial) < 2:
        return None

    # Hidden memories (reachable via tools, not in initial set)
    hidden = []
    initial_ids = {m["id"] for m in initial}
    for a in assocs[3:6]:
        target = data["mem_by_id"].get(a["target"])
        if target and target["id"] not in initial_ids:
            hidden.append(target)

    # Generate a plausible query from the anchor's concepts
    concepts = anchor.get("concepts", [])
    if concepts:
        query = f"What do I know about {' and '.join(concepts[:2])}?"
    else:
        # Use keywords from summary
        words = anchor["summary"].split()[:5]
        query = f"What happened with {' '.join(words)}?"

    # Pick relevant patterns
    relevant_patterns = random.sample(
        data["patterns"], min(2, len(data["patterns"]))
    ) if data["patterns"] else []

    return {
        "query": query,
        "initial_memories": initial,
        "hidden_memories": hidden,
        "patterns": relevant_patterns,
        "anchor": anchor,
    }


def format_memory_for_prompt(mem: dict, idx: int) -> str:
    """Format a memory like the retrieval agent does."""
    concepts_str = ", ".join(mem.get("concepts", []))
    ts = mem.get("timestamp", "")[:16]
    detail = mem.get("content", mem.get("summary", ""))[:200]
    return (
        f"{idx}. (id:{mem['id']})[{mem['project']}] {mem['summary']}\n"
        f"   Detail: {detail}\n"
        f"   Concepts: [{concepts_str}] | Created: {ts}"
    )


def format_tool_result_memories(memories: list[dict]) -> str:
    """Format memories as they would appear in tool results."""
    lines = []
    for i, m in enumerate(memories, 1):
        concepts_str = ", ".join(m.get("concepts", []))
        lines.append(f"{i}. (id:{m['id']}) {m['summary']} [{concepts_str}]")
    return "\n".join(lines) if lines else "No additional memories found."


def build_synthesis_prompt(scenario: dict) -> str:
    """Build the user prompt matching the retrieval agent's format."""
    parts = [
        "Answer this memory search concisely. Summarize what the memories tell you "
        "— focus on concrete facts, decisions, and specifics. Do NOT pad with filler "
        "or restate what each memory says individually.\n\n"
        "You have tools available to search for more context if needed. "
        "Use them only if the memories below are clearly incomplete.\n\n"
        f"They're asking: {scenario['query']}\n\n"
        "Specific memories:"
    ]

    for i, mem in enumerate(scenario["initial_memories"], 1):
        parts.append(format_memory_for_prompt(mem, i))

    if scenario["patterns"]:
        parts.append("\nPatterns you've noticed over time:")
        for p in scenario["patterns"]:
            parts.append(f"- [{p['strength']:.2f}] {p['title']}: {p['description'][:100]}")

    parts.append(
        "\nRespond in 2-5 sentences. Include specific details (file names, commands, "
        "decisions). Skip patterns/principles unless directly relevant."
    )

    return "\n".join(parts)


def generate_with_gemini(
    messages: list[dict],
    tools: list[dict] | None = None,
    api_key: str = "",
    model: str = "gemini-3-flash-preview",
) -> dict:
    """Call Gemini API via OpenAI-compatible endpoint."""
    if not HAS_REQUESTS:
        raise RuntimeError("requests library required: pip install requests")

    endpoint = "https://generativelanguage.googleapis.com/v1beta/openai"
    headers = {
        "Authorization": f"Bearer {api_key}",
        "Content-Type": "application/json",
    }

    body = {
        "model": model,
        "messages": messages,
        "max_tokens": 1024,
        "temperature": 0.5,
    }
    if tools:
        body["tools"] = tools

    resp = requests.post(
        f"{endpoint}/chat/completions",
        headers=headers,
        json=body,
        timeout=30,
    )
    resp.raise_for_status()
    data = resp.json()

    choice = data["choices"][0]
    message = choice["message"]

    return {
        "content": message.get("content", ""),
        "tool_calls": message.get("tool_calls", []),
        "finish_reason": choice.get("finish_reason", "stop"),
    }


def generate_training_example(
    scenario: dict,
    api_key: str,
    max_tool_rounds: int = 3,
) -> dict | None:
    """Generate a multi-turn synthesis conversation using Gemini as teacher.

    Returns a training example with messages including tool calls.
    """
    prompt = build_synthesis_prompt(scenario)
    messages = [{"role": "user", "content": prompt}]
    tool_round = 0

    while tool_round < max_tool_rounds:
        # Ask Gemini to respond (with tools available)
        tools = SYNTHESIS_TOOLS if tool_round < max_tool_rounds - 1 else None

        try:
            resp = generate_with_gemini(messages, tools, api_key)
        except Exception as e:
            print(f"  API error: {e}")
            return None

        # If no tool calls, we have the final synthesis
        if not resp["tool_calls"]:
            if resp["content"]:
                messages.append({"role": "assistant", "content": resp["content"]})
            break

        # Record assistant's tool call
        messages.append({
            "role": "assistant",
            "content": resp.get("content", ""),
            "tool_calls": resp["tool_calls"],
        })

        # Execute tool calls using our scenario data
        for tc in resp["tool_calls"]:
            fn = tc["function"]
            tool_name = fn["name"]
            try:
                args = json.loads(fn["arguments"])
            except json.JSONDecodeError:
                args = {}

            result = execute_simulated_tool(
                tool_name, args, scenario
            )

            messages.append({
                "role": "tool",
                "content": result,
                "tool_call_id": tc["id"],
            })

        tool_round += 1

    # Must have at least a final text response
    if not messages or messages[-1]["role"] != "assistant":
        return None
    if not messages[-1].get("content"):
        return None

    return {
        "task_type": "synthesis",
        "messages": messages,
        "tools": SYNTHESIS_TOOLS,
        "query": scenario["query"],
        "tool_rounds": tool_round,
        "has_tool_calls": any(
            m.get("tool_calls") for m in messages if m["role"] == "assistant"
        ),
    }


def execute_simulated_tool(
    tool_name: str, args: dict, scenario: dict
) -> str:
    """Simulate tool execution using scenario data."""
    if tool_name == "search_memories":
        # Return hidden memories if available, else some initial ones
        hidden = scenario.get("hidden_memories", [])
        if hidden:
            return format_tool_result_memories(hidden)
        return format_tool_result_memories(scenario["initial_memories"][:2])

    elif tool_name == "get_related":
        mem_id = args.get("memory_id", "")
        hidden = scenario.get("hidden_memories", [])
        if hidden:
            lines = []
            for m in hidden[:3]:
                lines.append(
                    f"- (id:{m['id']}) {m['summary']} [similar, strength: 0.80]"
                )
            return "\n".join(lines) if lines else "No related memories found."
        return "No related memories found."

    elif tool_name == "get_details":
        mem_id = args.get("memory_id", "")
        # Find in initial or hidden
        all_mems = scenario["initial_memories"] + scenario.get("hidden_memories", [])
        for m in all_mems:
            if m["id"] == mem_id:
                return f"Gist: {m['summary']}\n\nFull narrative: {m.get('content', m['summary'])}"
        return "Memory not found."

    elif tool_name == "search_timeline":
        # Return some initial memories as timeline results
        mems = scenario["initial_memories"][:3]
        lines = []
        for i, m in enumerate(mems, 1):
            lines.append(f"{i}. (id:{m['id']}) [{m.get('timestamp', '')[:10]}] {m['summary']}")
        return "\n".join(lines) if lines else "No memories in that time range."

    return f"Unknown tool: {tool_name}"


def main():
    parser = argparse.ArgumentParser(
        description="Generate synthetic multi-turn synthesis training data"
    )
    parser.add_argument(
        "--db", type=str,
        default=str(Path.home() / ".mnemonic" / "memory.db"),
    )
    parser.add_argument(
        "--output", type=str,
        default="training/data/synthesis_data.jsonl",
    )
    parser.add_argument(
        "--num-scenarios", type=int, default=200,
        help="Number of scenarios to generate",
    )
    parser.add_argument(
        "--max-tool-rounds", type=int, default=3,
        help="Max tool call rounds per conversation",
    )
    parser.add_argument("--seed", type=int, default=42)
    parser.add_argument(
        "--dry-run", action="store_true",
        help="Generate scenarios without calling Gemini (for testing)",
    )
    args = parser.parse_args()

    random.seed(args.seed)

    api_key = os.environ.get("LLM_API_KEY", "")
    if not api_key and not args.dry_run:
        print("Error: LLM_API_KEY environment variable required (or use --dry-run)")
        return

    print(f"Connecting to {args.db}...")
    conn = connect_db(args.db)

    print("Loading scenario data...")
    data = load_scenario_data(conn)
    conn.close()
    print(f"  {len(data['memories'])} memories, {len(data['patterns'])} patterns")
    print(f"  {sum(len(v) for v in data['associations'].values())} association links")

    # Generate scenarios
    print(f"\nGenerating {args.num_scenarios} scenarios...")
    scenarios = []
    attempts = 0
    while len(scenarios) < args.num_scenarios and attempts < args.num_scenarios * 5:
        scenario = build_scenario(data)
        if scenario:
            scenarios.append(scenario)
        attempts += 1
    print(f"  {len(scenarios)} valid scenarios from {attempts} attempts")

    if args.dry_run:
        print("\n[DRY RUN] Sample scenario:")
        if scenarios:
            s = scenarios[0]
            print(f"  Query: {s['query']}")
            print(f"  Initial memories: {len(s['initial_memories'])}")
            print(f"  Hidden memories: {len(s['hidden_memories'])}")
            prompt = build_synthesis_prompt(s)
            print(f"  Prompt length: {len(prompt)} chars")
            print(f"\n  Prompt preview:\n{prompt[:500]}...")
        print(f"\n  Would generate {len(scenarios)} examples (requires LLM_API_KEY)")
        return

    # Generate training examples
    output_path = Path(args.output)
    output_path.parent.mkdir(parents=True, exist_ok=True)

    examples = []
    tool_use_count = 0

    for i, scenario in enumerate(scenarios):
        print(f"  [{i+1}/{len(scenarios)}] {scenario['query'][:60]}...", end=" ", flush=True)

        example = generate_training_example(
            scenario, api_key, args.max_tool_rounds
        )

        if example:
            examples.append(example)
            if example["has_tool_calls"]:
                tool_use_count += 1
            print(f"OK (tools: {example['tool_rounds']})")
        else:
            print("SKIP")

        # Rate limiting
        time.sleep(0.5)

    # Save
    with open(output_path, "w") as f:
        for ex in examples:
            f.write(json.dumps(ex) + "\n")

    print(f"\nGenerated {len(examples)} examples ({tool_use_count} with tool calls)")
    print(f"Saved to {args.output}")

    # Stats
    rounds = [ex["tool_rounds"] for ex in examples]
    if rounds:
        print(f"Tool rounds: min={min(rounds)}, max={max(rounds)}, "
              f"mean={sum(rounds)/len(rounds):.1f}")


if __name__ == "__main__":
    main()
