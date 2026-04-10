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

# --- Production Encoding Prompt ---
# Matches the daemon's buildCompressionPrompt() output in
# internal/agent/encoding/agent.go. Training data MUST use this format
# so the model sees the same prompt structure it will encounter in production.

# The concept vocabulary from DefaultConceptVocabulary in agent.go
DEFAULT_CONCEPT_VOCABULARY = [
    # Languages & runtimes
    "go", "python", "javascript", "typescript", "sql", "bash", "html", "css",
    # Infrastructure & tooling
    "docker", "git", "linux", "macos", "systemd", "build", "ci", "deployment",
    # Dev activities
    "debugging", "testing", "refactoring", "configuration", "migration",
    "documentation", "review",
    # Code domains
    "api", "database", "filesystem", "networking", "security", "authentication",
    "performance", "logging", "ui", "cli",
    # AI & systems
    "memory", "llm",
    # Project context
    "fix", "research", "dependency", "schema", "config",
]


def build_production_prompt(
    content: str,
    source: str = "mcp",
    mem_type: str = "general",
    episode_ctx: str = "",
    coaching_instructions: str = "",
    concept_vocabulary: list[str] | None = None,
) -> str:
    """Build the production encoding prompt matching the daemon's format.

    This is a Python port of buildCompressionPrompt() from
    internal/agent/encoding/agent.go.
    """
    if concept_vocabulary is None:
        concept_vocabulary = DEFAULT_CONCEPT_VOCABULARY

    parts = []

    if source == "ingest":
        parts.append(
            "Catalog this source code file. Describe what the file IS and DOES.\n\n"
            "Fill in every JSON field based on the actual file content below:\n"
            "- gist: What this file is in under 60 characters.\n"
            "- summary: The file's purpose in under 100 characters.\n"
            "- content: A compressed description of what the file contains and how it works.\n"
            "- narrative: The file's role in the project architecture and why it matters.\n"
            "- concepts: 3-5 keywords describing the file's domain. PREFER exact terms from the vocabulary list below; only use new terms if no vocabulary term fits.\n"
            "- structured_concepts: Extract topics, entities, actions, and causal relationships. Keep each array to 3-5 items max. Use short strings, not sentences.\n"
            "- significance: One of routine, notable, important, or critical.\n"
            "- emotional_tone: neutral.\n"
            "- outcome: success.\n"
            "- salience: 0.7+ for core implementation, 0.5 for tests/utilities, 0.3 for generated files.\n\n"
        )
    else:
        parts.append(
            "Encode this event into memory. Read the content below and summarize what actually happened.\n\n"
            "Fill in every JSON field based on the actual event content below:\n"
            "- gist: What happened in under 60 characters.\n"
            "- summary: What happened and why it matters in under 100 characters.\n"
            "- content: The key details someone would need to understand this event later.\n"
            "- narrative: The story of what happened including context and meaning.\n"
            "- concepts: 3-5 keywords about the event. PREFER exact terms from the vocabulary list below; only use new terms if no vocabulary term fits.\n"
            "- structured_concepts: Extract topics, entities, actions, and causal relationships. Keep each array to 3-5 items max. Use short strings, not sentences.\n"
            "- significance: One of routine, notable, important, or critical.\n"
            "- emotional_tone: One of neutral, satisfying, frustrating, exciting, or concerning.\n"
            "- outcome: One of success, failure, ongoing, or unknown.\n"
            "- salience: 0.7+ for decisions/errors/insights, 0.5 for notable activity, 0.3 for routine file saves.\n\n"
        )

    if concept_vocabulary:
        parts.append(
            "IMPORTANT: Extract concepts from the CONTENT of the memory, not from what kind of memory it is. "
            "A decision about database indexing should have concepts like 'database', 'performance' — NOT 'decision'. "
            "Do NOT use metadata as concepts (e.g., 'source:mcp', 'type:insight', project names).\n\n"
        )
        parts.append(
            "CONCEPT VOCABULARY — prefer terms from this list when they match the content topic. "
            "Invent a new term if no vocabulary term fits the actual subject matter:\n"
        )
        parts.append(", ".join(concept_vocabulary))
        parts.append("\n\n")

    if episode_ctx:
        parts.append(episode_ctx)
    if coaching_instructions:
        parts.append(coaching_instructions)
        parts.append("\n\n")

    parts.append(f"SOURCE: {source}\n")
    parts.append(f"TYPE: {mem_type}\n")
    parts.append(f"CONTENT:\n{content}\n")

    return "".join(parts)


# --- Prompt Ablation Variants (EXP-29) ---
# Three alternative prompt strategies to test the LLMStructBench finding that
# prompting strategy matters more than model size for structured extraction.

PROMPT_VARIANT_MINIMAL = (
    "Output a JSON object encoding this event. Required fields: "
    "gist (str), summary (str), content (str), narrative (str), "
    "concepts (str[]), structured_concepts ({topics, entities, actions, causality}), "
    "significance (routine|notable|important|critical), "
    "emotional_tone (neutral|satisfying|frustrating|exciting|concerning), "
    "outcome (success|failure|ongoing|unknown), salience (0.0-1.0). "
    "Output ONLY valid JSON.\n\n"
)


PROMPT_VARIANT_FIELD_BY_FIELD = (
    "You are a memory encoder. Read the input below, then fill in each field:\n\n"
    "1. gist — What happened, under 60 chars\n"
    "2. summary — 2-3 sentences, under 100 chars\n"
    "3. content — Key facts, decisions, metrics. Copy names/numbers EXACTLY from input.\n"
    "4. narrative — Context paragraph explaining why this matters\n"
    "5. concepts — 3-5 keyword strings\n"
    "6. structured_concepts — Extract {topics: [{label,path}], entities: [{name,type,context}], "
    "actions: [{verb,object,details}], causality: [{relation,description}]}\n"
    "7. significance — routine / notable / important / critical\n"
    "8. emotional_tone — neutral / satisfying / frustrating / exciting / concerning\n"
    "9. outcome — success / failure / ongoing / unknown\n"
    "10. salience — float 0.0-1.0\n\n"
    "CRITICAL: Every name, number, file path, and metric in the input MUST appear "
    "in the output. Do NOT add information that isn't in the input.\n\n"
    "Output a single JSON object with all 10 fields.\n\n"
)


PROMPT_VARIANT_FAITHFUL = (
    "TASK: Compress the following observation into structured JSON.\n\n"
    "RULES:\n"
    "- FAITHFULNESS: Every fact in your output must come from the input. "
    "Do not infer, speculate, or add context from your training data.\n"
    "- PRESERVATION: Copy all proper nouns, numbers, file paths, version strings, "
    "and technical identifiers VERBATIM from the input.\n"
    "- MINIMALITY: If the input is short, the output should be short. "
    "Do not pad with generic filler.\n\n"
    "SCHEMA: {gist, summary, content, narrative, concepts, structured_concepts, "
    "significance, emotional_tone, outcome, salience}\n"
    "- significance: routine | notable | important | critical\n"
    "- emotional_tone: neutral | satisfying | frustrating | exciting | concerning\n"
    "- outcome: success | failure | ongoing | unknown\n"
    "- salience: float 0.0-1.0\n\n"
    "Output ONLY the JSON object. No explanation.\n\n"
)


def build_prompt_variant(
    content: str,
    variant: str = "production",
    source: str = "mcp",
    mem_type: str = "general",
) -> str:
    """Build encoding prompt using a specified variant.

    Variants:
        production — full daemon prompt (build_production_prompt)
        minimal — compressed schema-only instructions
        field_by_field — numbered field list with faithfulness emphasis
        faithful — rules-first prompt emphasizing no hallucination
    """
    if variant == "production":
        return build_production_prompt(content, source=source, mem_type=mem_type)

    prompt_map = {
        "minimal": PROMPT_VARIANT_MINIMAL,
        "field_by_field": PROMPT_VARIANT_FIELD_BY_FIELD,
        "faithful": PROMPT_VARIANT_FAITHFUL,
    }
    prefix = prompt_map.get(variant)
    if prefix is None:
        raise ValueError(f"Unknown variant: {variant}")

    return f"{prefix}SOURCE: {source}\nTYPE: {mem_type}\nCONTENT:\n{content}\n"


# --- Placeholder Detection ---

PLACEHOLDER_GISTS = frozenset({
    "user did something",
    "something happened",
    "file changed",
    "event occurred",
    "unknown event",
    "observation",
})
