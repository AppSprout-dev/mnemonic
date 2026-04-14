package llm

// GBNF (GGML BNF) grammars for constrained decoding with llama.cpp.
// These grammars restrict model output to valid structures during sampling,
// eliminating JSON parse failures without post-processing.
//
// Reference: https://github.com/ggerganov/llama.cpp/blob/master/grammars/README.md

// GBNFJSONObject constrains output to a valid JSON object.
// This is the fallback grammar for json_object response format.
const GBNFJSONObject = `root   ::= object
value  ::= object | array | string | number | "true" | "false" | "null"

object ::=
  "{" ws "}" |
  "{" ws member ("," ws member)* ws "}"

member ::= string ws ":" ws value

array  ::=
  "[" ws "]" |
  "[" ws value ("," ws value)* ws "]"

string ::=
  "\"" (
    [^\\"\x00-\x1f] |
    "\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
  )* "\""

number ::= "-"? ("0" | [1-9] [0-9]*) ("." [0-9]+)? ([eE] [-+]? [0-9]+)?

ws     ::= ([ \t\n] ws)?
`

// GBNFJSONArray constrains output to a valid JSON array.
const GBNFJSONArray = `root   ::= array
value  ::= object | array | string | number | "true" | "false" | "null"

object ::=
  "{" ws "}" |
  "{" ws member ("," ws member)* ws "}"

member ::= string ws ":" ws value

array  ::=
  "[" ws "]" |
  "[" ws value ("," ws value)* ws "]"

string ::=
  "\"" (
    [^\\"\x00-\x1f] |
    "\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
  )* "\""

number ::= "-"? ("0" | [1-9] [0-9]*) ("." [0-9]+)? ([eE] [-+]? [0-9]+)?

ws     ::= ([ \t\n] ws)?
`

// GBNFEpisodeSynthesis constrains output to the simplified 4-field episode schema.
// Fixed key order and typed values prevent type mismatches. Salience is constrained
// to 0.X or 0.XX format to prevent string values like "high".
const GBNFEpisodeSynthesis = `root ::= "{" ws title-kv "," ws summary-kv "," ws concepts-kv "," ws salience-kv ws "}"

title-kv    ::= "\"title\"" ws ":" ws string
summary-kv  ::= "\"summary\"" ws ":" ws string
concepts-kv ::= "\"concepts\"" ws ":" ws string-array
salience-kv ::= "\"salience\"" ws ":" ws salience-val

string-array ::= "[" ws "]" | "[" ws string ("," ws string)* ws "]"
salience-val ::= "0." [0-9] [0-9]?

string ::=
  "\"" (
    [^\\"\x00-\x1f] |
    "\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
  )* "\""

ws     ::= ([ \t\n] ws)?
`

// GBNFEncodingResponse constrains output to the mnemonic encoding response schema.
// Fixed key order eliminates ambiguity for small models and enforces all required fields.
const GBNFEncodingResponse = `root ::= "{" ws gist-kv "," ws summary-kv "," ws content-kv "," ws narrative-kv "," ws concepts-kv "," ws structured-concepts-kv "," ws significance-kv "," ws emotional-tone-kv "," ws outcome-kv "," ws salience-kv ws "}"

gist-kv              ::= "\"gist\"" ws ":" ws string
summary-kv           ::= "\"summary\"" ws ":" ws string
content-kv           ::= "\"content\"" ws ":" ws string
narrative-kv         ::= "\"narrative\"" ws ":" ws string
concepts-kv          ::= "\"concepts\"" ws ":" ws string-array
structured-concepts-kv ::= "\"structured_concepts\"" ws ":" ws sc-object
significance-kv      ::= "\"significance\"" ws ":" ws string
emotional-tone-kv    ::= "\"emotional_tone\"" ws ":" ws string
outcome-kv           ::= "\"outcome\"" ws ":" ws string
salience-kv          ::= "\"salience\"" ws ":" ws number

string-array ::= "[" ws "]" | "[" ws string ("," ws string)* ws "]"

sc-object    ::= "{" ws topics-kv "," ws entities-kv "," ws actions-kv "," ws causality-kv ws "}"
topics-kv    ::= "\"topics\"" ws ":" ws topic-array
entities-kv  ::= "\"entities\"" ws ":" ws entity-array
actions-kv   ::= "\"actions\"" ws ":" ws action-array
causality-kv ::= "\"causality\"" ws ":" ws causality-array

topic-array     ::= "[" ws "]" | "[" ws topic-obj ("," ws topic-obj)* ws "]"
topic-obj       ::= "{" ws "\"label\"" ws ":" ws string "," ws "\"path\"" ws ":" ws string ws "}"

entity-array    ::= "[" ws "]" | "[" ws entity-obj ("," ws entity-obj)* ws "]"
entity-obj      ::= "{" ws "\"name\"" ws ":" ws string "," ws "\"type\"" ws ":" ws string "," ws "\"context\"" ws ":" ws string ws "}"

action-array    ::= "[" ws "]" | "[" ws action-obj ("," ws action-obj)* ws "]"
action-obj      ::= "{" ws "\"verb\"" ws ":" ws string "," ws "\"object\"" ws ":" ws string "," ws "\"details\"" ws ":" ws string ws "}"

causality-array ::= "[" ws "]" | "[" ws causality-obj ("," ws causality-obj)* ws "]"
causality-obj   ::= "{" ws "\"relation\"" ws ":" ws string "," ws "\"description\"" ws ":" ws string ws "}"

string ::=
  "\"" (
    [^\\"\x00-\x1f] |
    "\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
  )* "\""

number ::= "-"? ("0" | [1-9] [0-9]*) ("." [0-9]+)? ([eE] [-+]? [0-9]+)?

ws     ::= ([ \t\n] ws)?
`
