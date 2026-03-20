// Package concepts provides shared concept extraction functions for
// deriving meaningful tokens from file paths, terminal commands, and
// watcher event types. Used by both the MCP server (for get_context
// theme generation) and the retrieval agent (for activity-based
// recall boosting).
package concepts

import (
	"path/filepath"
	"strings"
)

// FromPath extracts meaningful concept tokens from a file path.
// Splits on separators, filters short/noisy segments, and deduplicates.
// e.g. "internal/agent/retrieval/agent.go" → ["agent", "retrieval"].
func FromPath(path string) []string {
	// Strip extension and split into directory/file segments.
	path = strings.TrimSuffix(path, filepath.Ext(path))
	// Normalize separators and split.
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\' || r == '_' || r == '-' || r == '.'
	})

	// Filter short/noisy segments.
	skip := map[string]bool{
		"internal": true, "cmd": true, "pkg": true, "src": true,
		"lib": true, "bin": true, "tmp": true, "test": true,
		"main": true, "index": true, "mod": true, "sum": true,
		"go": true, "the": true, "and": true, "for": true,
	}

	seen := make(map[string]bool)
	var concepts []string
	for _, seg := range parts {
		seg = strings.ToLower(seg)
		if len(seg) <= 2 || skip[seg] || seen[seg] {
			continue
		}
		seen[seg] = true
		concepts = append(concepts, seg)
	}
	return concepts
}

// FromEventType extracts a meaningful action verb from a watcher event type.
// e.g. "file_created" → "created", "file_modified" → "modified".
// Returns empty string for generic types like "dir_activity".
func FromEventType(eventType string) string {
	if strings.HasPrefix(eventType, "file_") {
		action := strings.TrimPrefix(eventType, "file_")
		if action != "" {
			return action
		}
	}
	return ""
}

// FromCommand extracts concepts from a terminal command string.
// Returns the command name and subcommand for compound commands (git, docker, etc.).
// e.g. "git commit -m 'fix'" → ["git", "commit"], "ls -la" → ["ls"].
func FromCommand(content string) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}

	tokens := strings.Fields(content)
	if len(tokens) == 0 {
		return nil
	}

	// First non-flag token is the command.
	command := strings.ToLower(tokens[0])
	concepts := []string{command}

	// Compound commands where the subcommand carries meaning.
	compound := map[string]bool{
		"git": true, "docker": true, "kubectl": true,
		"npm": true, "go": true, "cargo": true,
		"pip": true, "yarn": true, "make": true,
		"systemctl": true, "brew": true, "apt": true,
	}

	if compound[command] && len(tokens) > 1 {
		// Find the first non-flag token after the command.
		for _, t := range tokens[1:] {
			if !strings.HasPrefix(t, "-") {
				sub := strings.ToLower(t)
				if sub != "" {
					concepts = append(concepts, sub)
				}
				break
			}
		}
	}

	return concepts
}
