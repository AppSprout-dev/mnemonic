"""Canonical constants for mnemonic training data.

Single source of truth for field definitions, enum values, and system prompts.
All training scripts import from here — no duplicate definitions.
"""

# --- Required Fields ---

REQUIRED_FIELDS = frozenset({
    "gist", "summary", "content", "narrative", "concepts",
    "structured_concepts", "significance", "emotional_tone",
    "outcome", "salience",
})

MINIMAL_REQUIRED_FIELDS = frozenset({"summary", "concepts", "salience"})

# --- Enum Values ---
# These match the Gemini system prompt used to generate training data.
# validate.py enforces these; generation scripts produce them.

VALID_SIGNIFICANCE = frozenset({
    "critical", "important", "notable", "routine", "trivial",
})

VALID_EMOTIONAL_TONE = frozenset({
    "positive", "negative", "neutral", "frustrated",
    "excited", "analytical", "reflective",
})

# outcome is FREE TEXT, not an enum.
# The system prompt says: "Brief description of the result or status"
# Do not constrain it to a fixed set of values.

# --- System Prompt ---
# The canonical encoding system prompt. Used by:
# - enrich_and_generate.py (data generation)
# - batch_encode.py (batch data generation)
# - stress_test_hallucination.py (evaluation)
# - serve_spokes.py (inference)
# - eval_qwen_encoding.py (novel input evaluation)

ENCODING_SYSTEM_PROMPT = (
    "You are a memory encoding agent for Mnemonic, a semantic memory system. "
    "You receive raw events (text observations from a developer's work) and output structured JSON.\n\n"
    "Your output MUST be a single JSON object with exactly these 10 fields:\n"
    "- gist: One-line summary, under 80 characters\n"
    "- summary: 2-3 sentence summary of the key information\n"
    "- content: Preserved detail — the important facts, decisions, and context. "
    "Preserve exact file paths with line numbers, person names, version numbers, "
    "and specific metrics verbatim. Do not paraphrase technical identifiers.\n"
    "- narrative: A paragraph providing broader context and significance\n"
    "- concepts: Array of 3-8 keyword strings (lowercase, no phrases longer than 3 words)\n"
    "- structured_concepts: Object with 4 arrays:\n"
    "    - topics: [{label, path}] — what domains this touches\n"
    "    - entities: [{name, type, context}] — people, tools, systems mentioned\n"
    "    - actions: [{verb, object, details}] — what was done\n"
    "    - causality: [{relation, description}] — cause/effect relationships\n"
    "- significance: One of \"critical\", \"important\", \"notable\", \"routine\", \"trivial\"\n"
    "- emotional_tone: One of \"positive\", \"negative\", \"neutral\", \"frustrated\", "
    "\"excited\", \"analytical\", \"reflective\"\n"
    "- outcome: Brief description of the result or status\n"
    "- salience: Float 0.0-1.0 (how important is this to remember long-term)\n\n"
    "Output ONLY the JSON object. No markdown fences, no explanation, no preamble."
)

# Shorter version for eval/stress test (no generation instructions, just schema)
ENCODING_SYSTEM_PROMPT_SHORT = (
    "You are a memory encoding agent. You receive raw events and output structured JSON "
    "with these required fields: gist (one-line summary), summary (2-3 sentences), "
    "content (preserved detail), narrative (context paragraph), concepts (keyword array), "
    "structured_concepts (object with topics, entities, actions, causality arrays), "
    "significance (importance level), emotional_tone (mood), outcome (result), "
    "salience (0.0-1.0 float). Never explain, never apologize. Output only valid JSON."
)

# --- Placeholder Detection ---

PLACEHOLDER_GISTS = frozenset({
    "user did something",
    "something happened",
    "file changed",
    "event occurred",
    "unknown event",
    "observation",
})
