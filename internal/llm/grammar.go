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
