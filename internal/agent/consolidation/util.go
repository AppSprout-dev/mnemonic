package consolidation

import (
	"encoding/json"
	"strings"
)

// extractJSONFromResponse tries to find a JSON object in an LLM response
// that may contain surrounding prose.
func extractJSONFromResponse(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 0 && s[0] == '{' {
		return s
	}

	// Look for JSON between ```json ... ``` fences
	if idx := strings.Index(s, "```json"); idx != -1 {
		start := idx + 7
		end := strings.Index(s[start:], "```")
		if end != -1 {
			return strings.TrimSpace(s[start : start+end])
		}
	}

	// Look for JSON between ``` ... ``` fences
	if idx := strings.Index(s, "```"); idx != -1 {
		start := idx + 3
		if start < len(s) && s[start] == '\n' {
			start++
		}
		end := strings.Index(s[start:], "```")
		if end != -1 {
			candidate := strings.TrimSpace(s[start : start+end])
			if len(candidate) > 0 && candidate[0] == '{' {
				return candidate
			}
		}
	}

	// Find outermost JSON object
	first := strings.Index(s, "{")
	last := strings.LastIndex(s, "}")
	if first != -1 && last > first {
		return s[first : last+1]
	}

	return s
}

// parseJSON is a helper that unmarshals a JSON string into the target.
func parseJSON(jsonStr string, target interface{}) error {
	return json.Unmarshal([]byte(jsonStr), target)
}
