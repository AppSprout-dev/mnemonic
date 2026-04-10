package encoding

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// VerificationResult holds the output of the faithfulness verification gate.
type VerificationResult struct {
	EPR            float64  // Entity Preservation Rate (0.0-1.0)
	FR             float64  // Fabrication Rate (0.0-1.0), monitoring only
	TED            bool     // Template Echo Detected
	MIG            bool     // Minimal Input Guard triggered
	Flags          []string // Human-readable issue descriptions
	InputEntities  int      // Count of entities extracted from raw input
	OutputEntities int      // Count of entities extracted from compression
}

// --- Entity extraction regexes (ported from eval_faithfulness.py) ---

var (
	// Numbers: integers, decimals, percentages, fractions, scientific notation, comma-separated
	numberRE = regexp.MustCompile(
		`-?\d{1,3}(?:,\d{3})+(?:\.\d+)?` + `|` + // comma-separated: 47,231
			`-?\d+\.\d+[eE][+-]?\d+` + `|` + // scientific: 2.3e-4
			`-?\d+\.\d+%` + `|` + // decimal percentage: 94.2%
			`-?\d+%` + `|` + // integer percentage: 80%
			`-?\d+\.\d+` + `|` + // decimal: 0.847
			`\d+/\d+` + `|` + // fraction: 12/21
			`\d+`, // plain integer: 200
	)

	// File paths with common extensions
	pathRE = regexp.MustCompile(
		`[a-zA-Z_~/][\w/~.-]+\.(?:go|py|js|ts|html|css|yaml|yml|json|jsonl|toml|md|sh|sql|gguf|db|txt|log|patch|cuh|cpp|c|h)\b` + `|` +
			`/(?:home|usr|etc|var|tmp|opt|api|static)[\w./~-]+`,
	)

	// Version strings: v1.2.3, v2.0
	versionRE = regexp.MustCompile(`v\d+\.\d+(?:\.\d+)?`)

	// Proper nouns: multi-word capitalized phrases
	multiWordProperRE = regexp.MustCompile(`\b([A-Z][a-z]+(?:\s+[A-Z][a-z]+)+)\b`)

	// Single capitalized words after lowercase context
	singleProperRE = regexp.MustCompile(`(?:[a-z,;]\s)([A-Z][a-z]{2,})\b`)

	// @mentions
	mentionRE = regexp.MustCompile(`@(\w+)`)

	// CamelCase identifiers
	camelCaseRE = regexp.MustCompile(`\b([A-Z][a-z]+[A-Z]\w+)\b`)
)

// Common words to filter from proper noun detection
var commonWords = map[string]bool{
	"The": true, "This": true, "That": true, "These": true, "Those": true,
	"When": true, "Where": true, "What": true, "Which": true, "Who": true,
	"How": true, "Why": true, "And": true, "But": true, "For": true,
	"Not": true, "You": true, "All": true, "Can": true, "Had": true,
	"Her": true, "Was": true, "One": true, "Our": true, "Out": true,
	"Are": true, "Has": true, "His": true, "Its": true, "May": true,
	"New": true, "Now": true, "Old": true, "See": true, "Way": true,
	"Day": true, "Did": true, "Get": true, "Let": true, "Say": true,
	"She": true, "Too": true, "Use": true, "After": true, "Also": true,
	"Into": true, "Just": true, "Like": true, "Long": true, "Make": true,
	"Many": true, "Most": true, "Only": true, "Over": true, "Such": true,
	"Take": true, "Than": true, "Them": true, "Then": true, "Very": true,
	"With": true, "Been": true, "Call": true, "Come": true, "Each": true,
	"From": true, "Have": true, "Here": true, "High": true, "More": true,
	"Part": true, "Some": true, "Time": true, "Will": true, "About": true,
	"Could": true, "First": true, "Other": true, "Their": true, "There": true,
	"Would": true, "Being": true, "Every": true, "Great": true, "Never": true,
	"Since": true, "Still": true, "Think": true, "While": true,
	"Should": true, "Before": true, "Between": true, "During": true,
	"Output": true, "Input": true, "Based": true, "Given": true,
	"Using": true, "Brief": true, "Under": true, "Memory": true,
}

// templateEchoPhrases are instruction fragments that should never appear in output.
var templateEchoPhrases = []string{
	"under 60 characters",
	"under 80 characters",
	"under 100 characters",
	"2-3 sentence summary",
	"key information",
	"broader context",
	"3-8 keyword strings",
	"cause/effect relationships",
	"how important is this",
	"no markdown fences",
	"no explanation",
	"no preamble",
	"output ONLY",
	"single JSON object",
	"output only valid json",
	"no phrases longer than",
	"fill in every json field",
	"encode this event into memory",
}

// extractEntities extracts identifiable entities from text.
// Returns a deduplicated set of entity strings (lowercased for comparison).
func extractEntities(text string) map[string]bool {
	entities := make(map[string]bool)

	for _, m := range numberRE.FindAllString(text, -1) {
		// Normalize: strip commas for comparison
		normalized := strings.ReplaceAll(m, ",", "")
		entities[normalized] = true
	}
	for _, m := range pathRE.FindAllString(text, -1) {
		entities[strings.ToLower(m)] = true
	}
	for _, m := range versionRE.FindAllString(text, -1) {
		entities[strings.ToLower(m)] = true
	}
	for _, m := range multiWordProperRE.FindAllString(text, -1) {
		if !commonWords[m] {
			entities[strings.ToLower(m)] = true
		}
	}
	for _, matches := range singleProperRE.FindAllStringSubmatch(text, -1) {
		if len(matches) > 1 && !commonWords[matches[1]] {
			entities[strings.ToLower(matches[1])] = true
		}
	}
	for _, matches := range mentionRE.FindAllStringSubmatch(text, -1) {
		if len(matches) > 1 {
			entities[strings.ToLower(matches[1])] = true
		}
	}
	for _, m := range camelCaseRE.FindAllString(text, -1) {
		entities[strings.ToLower(m)] = true
	}

	return entities
}

// contentFields extracts text from the content-bearing fields of a compression response.
func contentFields(cr *compressionResponse) string {
	var b strings.Builder
	b.WriteString(cr.Gist)
	b.WriteByte(' ')
	b.WriteString(cr.Summary)
	b.WriteByte(' ')
	b.WriteString(cr.Content)
	b.WriteByte(' ')
	b.WriteString(cr.Narrative)
	b.WriteByte(' ')
	b.WriteString(cr.Outcome)
	return b.String()
}

// verifyFaithfulness runs the post-compression verification gate.
// Returns a VerificationResult with EPR, FR, TED, MIG, and human-readable flags.
func verifyFaithfulness(rawText string, compression *compressionResponse) VerificationResult {
	result := VerificationResult{}

	inputEntities := extractEntities(rawText)
	outputText := contentFields(compression)
	outputEntities := extractEntities(outputText)

	result.InputEntities = len(inputEntities)
	result.OutputEntities = len(outputEntities)

	// EPR: fraction of input entities found in output
	if len(inputEntities) > 0 {
		preserved := 0
		for entity := range inputEntities {
			if strings.Contains(strings.ToLower(outputText), entity) {
				preserved++
			}
		}
		result.EPR = float64(preserved) / float64(len(inputEntities))
	} else {
		result.EPR = 1.0 // No entities to preserve = perfect preservation
	}

	// FR: fraction of output entities not in input (monitoring only)
	if len(outputEntities) > 0 {
		fabricated := 0
		inputLower := strings.ToLower(rawText)
		for entity := range outputEntities {
			if !strings.Contains(inputLower, entity) {
				fabricated++
			}
		}
		result.FR = float64(fabricated) / float64(len(outputEntities))
	}

	// TED: template echo detection
	outputLower := strings.ToLower(outputText)
	for _, phrase := range templateEchoPhrases {
		if strings.Contains(outputLower, phrase) {
			result.TED = true
			result.Flags = append(result.Flags, "template_echo:"+phrase)
			break // One is enough to flag
		}
	}

	// MIG: minimal input guard
	rawTrimmed := strings.TrimSpace(rawText)
	if countNonWhitespace(rawTrimmed) < 50 && len(compression.Content) > 300 {
		result.MIG = true
		result.Flags = append(result.Flags, "minimal_input_padded")
	}

	// Build summary flags
	if result.EPR < 0.7 {
		result.Flags = append(result.Flags, fmt.Sprintf("low_epr:%.2f", result.EPR))
	}
	if result.FR > 0.3 {
		result.Flags = append(result.Flags, fmt.Sprintf("high_fr:%.2f", result.FR))
	}

	return result
}

// countNonWhitespace returns the count of non-whitespace characters.
func countNonWhitespace(s string) int {
	count := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			count++
		}
	}
	return count
}
