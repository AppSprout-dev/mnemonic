package embedding

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"regexp"
	"sort"
	"strings"
)

// BowDims is the dimensionality of the bag-of-words embedding space.
const BowDims = 128

// Vocabulary is the fixed bag-of-words vocabulary. Each word maps to a
// fixed dimension in the embedding space. Texts sharing words produce
// similar embeddings, making retrieval and association scores meaningful.
// Synonyms map to the same dimension for automatic grouping.
var Vocabulary = map[string]int{
	// Languages & runtimes
	"go": 0, "golang": 0, "python": 1, "javascript": 2, "typescript": 3,
	"sql": 4, "bash": 5, "html": 6, "css": 7, "rust": 8, "java": 9,
	// Infrastructure
	"docker": 10, "git": 11, "linux": 12, "macos": 13, "systemd": 14,
	"build": 15, "ci": 16, "deployment": 17, "deploy": 17, "kubernetes": 18,
	// Dev activities
	"debugging": 19, "debug": 19, "testing": 20, "test": 20,
	"refactoring": 21, "refactor": 21, "configuration": 22, "config": 22,
	"migration": 23, "documentation": 24, "review": 25,
	// Code domains
	"api": 26, "database": 27, "db": 27, "sqlite": 27, "postgres": 27, "postgresql": 27,
	"filesystem": 28, "file": 28, "networking": 29, "network": 29, "connection": 29,
	"security": 30, "authentication": 31, "auth": 31, "login": 31, "session": 31,
	"performance": 32, "logging": 33, "log": 33, "ui": 34, "cli": 35,
	"latency": 32, "throughput": 32, "slow": 32, "fast": 32, "speed": 32,
	// Memory system
	"memory": 36, "encoding": 37, "retrieval": 38, "embedding": 39,
	"agent": 40, "llm": 41, "daemon": 42, "mcp": 43, "watcher": 44,
	// Project context — with synonyms
	"decision": 45, "chose": 45, "choose": 45, "selected": 45, "picked": 45, "choice": 45,
	"error": 46, "bug": 46, "issue": 46, "problem": 46, "defect": 46, "incident": 46, "outage": 46,
	"fix": 47, "fixed": 47, "resolve": 47, "resolved": 47, "solution": 47, "repair": 47, "patch": 47, "workaround": 47,
	"insight": 48, "learning": 49, "planning": 50, "research": 51,
	"dependency": 52, "library": 52, "module": 52, "schema": 53, "config_yaml": 54,
	// Common nouns
	"server": 55, "client": 56, "request": 57, "response": 58,
	"cache": 59, "redis": 59, "memcached": 59, "queue": 60, "event": 61, "handler": 62,
	"middleware": 63, "route": 64, "endpoint": 65,
	"function": 66, "method": 67, "interface": 68, "struct": 69,
	"channel": 70, "goroutine": 71, "mutex": 72, "context": 73,
	// Actions
	"create": 74, "read": 75, "update": 76, "delete": 77,
	"query": 78, "search": 79, "filter": 80, "sort": 81,
	"parse": 82, "validate": 83, "transform": 84, "serialize": 85,
	// Qualities — with synonyms
	"nil": 86, "null": 86, "panic": 87, "crash": 87, "failure": 87, "failed": 87, "broken": 87,
	"timeout": 88, "retry": 89, "fallback": 90, "graceful": 91,
	"concurrent": 92, "concurrency": 92, "pool": 92, "async": 93, "sync": 94,
	// Specific to mnemonic
	"spread": 95, "activation": 96, "association": 97, "salience": 98,
	"consolidation": 99, "decay": 100, "dreaming": 101, "abstraction": 102,
	"episoding": 103, "metacognition": 104, "perception": 105,
	"fts5": 106, "bm25": 107, "cosine": 108, "similarity": 109,
	// General — with synonyms
	"pattern": 110, "principle": 111, "rule": 111, "guideline": 111, "axiom": 112,
	"graph": 113, "node": 114, "edge": 115,
	"threshold": 116, "weight": 117, "score": 118,
	"architecture": 119, "design": 120, "tradeoff": 121, "tradeoffs": 121,
	// System noise vocabulary (distinct region)
	"chrome": 122, "browser": 122, "clipboard": 123,
	"desktop": 124, "gnome": 124, "notification": 125,
	"audio": 126, "pipewire": 126, "trash": 127,
}

// wordSplitRe splits text into words for bag-of-words processing.
var wordSplitRe = regexp.MustCompile(`[a-zA-Z][a-z]*|[A-Z]+`)

// BowProvider implements embedding.Provider using bag-of-words embeddings.
// This is a zero-dependency, deterministic embedding provider that requires
// no external model, no GPU, and no network access. It maps words from a
// fixed vocabulary to dimensions in a 128-dim embedding space.
type BowProvider struct{}

// NewBowProvider returns a new bag-of-words embedding provider.
func NewBowProvider() *BowProvider {
	return &BowProvider{}
}

func (p *BowProvider) Embed(_ context.Context, text string) ([]float32, error) {
	return BowEmbedding(text), nil
}

func (p *BowProvider) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, t := range texts {
		results[i] = BowEmbedding(t)
	}
	return results, nil
}

func (p *BowProvider) Health(_ context.Context) error {
	return nil // always healthy — pure CPU, no external deps
}

// BowEmbedding creates a bag-of-words embedding. Words in the vocabulary
// activate their assigned dimension. Unknown words hash into the space
// with a weaker signal. Result is normalized to a unit vector.
func BowEmbedding(text string) []float32 {
	emb := make([]float32, BowDims)
	lower := strings.ToLower(text)
	words := wordSplitRe.FindAllString(lower, -1)

	for _, w := range words {
		if dim, ok := Vocabulary[w]; ok {
			emb[dim] += 1.0
		} else {
			// Hash unknown words into the embedding space.
			h := fnv.New32a()
			_, _ = h.Write([]byte(w))
			dim := int(h.Sum32()) % BowDims
			emb[dim] += 0.3 // weaker signal for unknown words
		}
	}

	// Normalize to unit vector.
	var norm float64
	for _, v := range emb {
		norm += float64(v) * float64(v)
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range emb {
			emb[i] = float32(float64(emb[i]) / norm)
		}
	}
	return emb
}

// ExtractTopConcepts returns the top N vocabulary words found in text,
// ranked by frequency. Synonyms are grouped by dimension.
func ExtractTopConcepts(text string, n int) []string {
	lower := strings.ToLower(text)
	words := wordSplitRe.FindAllString(lower, -1)

	type dimCount struct {
		word  string
		dim   int
		count int
	}
	dimCounts := make(map[int]*dimCount)
	for _, w := range words {
		if dim, ok := Vocabulary[w]; ok {
			if dc, exists := dimCounts[dim]; exists {
				dc.count++
			} else {
				dimCounts[dim] = &dimCount{word: w, dim: dim, count: 1}
			}
		}
	}

	sorted := make([]*dimCount, 0, len(dimCounts))
	for _, dc := range dimCounts {
		sorted = append(sorted, dc)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	result := make([]string, 0, n)
	for i := 0; i < n && i < len(sorted); i++ {
		result = append(result, sorted[i].word)
	}
	return result
}

// ComputeSalience returns a deterministic salience based on vocabulary density.
// Higher ratio of recognized vocabulary words = higher salience.
func ComputeSalience(text string) float32 {
	lower := strings.ToLower(text)
	words := wordSplitRe.FindAllString(lower, -1)
	if len(words) == 0 {
		return 0.3
	}
	vocabHits := 0
	for _, w := range words {
		if _, ok := Vocabulary[w]; ok {
			vocabHits++
		}
	}
	ratio := float32(vocabHits) / float32(len(words))
	sal := 0.3 + ratio*0.6
	if sal > 0.9 {
		sal = 0.9
	}
	return sal
}

// ClassifyRelationship classifies the relationship between two texts
// based on keyword analysis. Returns one of: similar, caused_by, part_of,
// contradicts, temporal, reinforces.
func ClassifyRelationship(text1, text2 string) string {
	combined := strings.ToLower(text1 + " " + text2)

	switch {
	case strings.Contains(combined, "caused") || strings.Contains(combined, "because") ||
		strings.Contains(combined, "led to") || strings.Contains(combined, "result"):
		return "caused_by"
	case strings.Contains(combined, "part of") || strings.Contains(combined, "component") ||
		strings.Contains(combined, "belongs"):
		return "part_of"
	case strings.Contains(combined, "contradict") || strings.Contains(combined, "opposite") ||
		strings.Contains(combined, "however"):
		return "contradicts"
	case strings.Contains(combined, "before") || strings.Contains(combined, "after") ||
		strings.Contains(combined, "then") || strings.Contains(combined, "later"):
		return "temporal"
	case strings.Contains(combined, "reinforce") || strings.Contains(combined, "confirm") ||
		strings.Contains(combined, "support"):
		return "reinforces"
	default:
		return "similar"
	}
}

// GenerateEncodingResponse produces a heuristic encoding for raw memory content.
// This replaces the LLM compression step with deterministic extraction.
// Concepts are extracted using RAKE (multi-word phrases) supplemented by
// vocabulary-based single-word terms for consistent tagging.
func GenerateEncodingResponse(content, source, memType string) EncodingResult {
	concepts := ExtractConcepts(content, 8)
	if len(concepts) == 0 {
		concepts = []string{"general"}
	}

	summary := truncateStr(content, 100)
	salience := ComputeSalience(content)

	// Source-aware salience adjustment
	switch source {
	case "mcp":
		if salience < 0.5 {
			salience = 0.5
		}
	case "terminal":
		salience *= 0.9
	case "filesystem":
		salience *= 0.8
	}

	// Type-aware salience boost
	switch memType {
	case "decision":
		salience += 0.15
	case "error":
		salience += 0.1
	case "insight":
		salience += 0.15
	case "learning":
		salience += 0.1
	}
	if salience > 1.0 {
		salience = 1.0
	}

	significance := "routine"
	if salience > 0.7 {
		significance = "important"
	} else if salience > 0.5 {
		significance = "notable"
	}

	return EncodingResult{
		Summary:      summary,
		Content:      truncateStr(content, 2000),
		Concepts:     concepts,
		Salience:     salience,
		Significance: significance,
		Tone:         "neutral",
		Outcome:      "ongoing",
	}
}

// EncodingResult holds the heuristic encoding of a raw memory.
type EncodingResult struct {
	Summary      string
	Content      string
	Concepts     []string
	Salience     float32
	Significance string
	Tone         string
	Outcome      string
}

// GenerateEpisodeSynthesis produces an algorithmic episode summary.
func GenerateEpisodeSynthesis(eventTexts []string, durationMinutes int) EpisodeResult {
	combined := strings.Join(eventTexts, " ")
	concepts := ExtractTopConcepts(combined, 5)
	if len(concepts) == 0 {
		concepts = []string{"session"}
	}

	title := fmt.Sprintf("Session: %s", strings.Join(concepts, ", "))
	if len(title) > 80 {
		title = title[:80]
	}

	summary := fmt.Sprintf("%d events over %d minutes involving %s",
		len(eventTexts), durationMinutes, strings.Join(concepts, ", "))

	narrative := fmt.Sprintf("During this session, activity was observed related to %s.",
		strings.Join(concepts, ", "))

	// Detect emotional tone from keywords
	tone := "neutral"
	lower := strings.ToLower(combined)
	if strings.ContainsAny(lower, "") {
		// Check for concerning keywords
		for _, kw := range []string{"error", "panic", "fail", "crash", "bug", "broken"} {
			if strings.Contains(lower, kw) {
				tone = "concerning"
				break
			}
		}
	}
	if tone == "neutral" {
		for _, kw := range []string{"deployed", "completed", "working", "success", "passed", "fixed"} {
			if strings.Contains(lower, kw) {
				tone = "satisfying"
				break
			}
		}
	}

	salience := ComputeSalience(combined)

	return EpisodeResult{
		Title:         title,
		Summary:       summary,
		Narrative:     narrative,
		EmotionalTone: tone,
		Outcome:       "ongoing",
		Concepts:      concepts,
		Salience:      salience,
	}
}

// EpisodeResult holds the algorithmic episode synthesis.
type EpisodeResult struct {
	Title         string
	Summary       string
	Narrative     string
	EmotionalTone string
	Outcome       string
	Concepts      []string
	Salience      float32
}

// GenerateInsight produces a heuristic insight from a cluster of memory concepts.
// It looks for concept bridges — shared concepts across otherwise distinct groups.
func GenerateInsight(memoryConcepts [][]string) *InsightResult {
	if len(memoryConcepts) < 3 {
		return nil
	}

	// Count concept frequency across memories
	conceptFreq := make(map[string]int)
	for _, concepts := range memoryConcepts {
		seen := make(map[string]bool)
		for _, c := range concepts {
			if !seen[c] {
				conceptFreq[c]++
				seen[c] = true
			}
		}
	}

	// Find concepts that appear in multiple memories (bridge concepts)
	type conceptCount struct {
		concept string
		count   int
	}
	var bridges []conceptCount
	for c, count := range conceptFreq {
		if count >= 2 {
			bridges = append(bridges, conceptCount{c, count})
		}
	}

	if len(bridges) < 2 {
		return nil
	}

	sort.Slice(bridges, func(i, j int) bool {
		return bridges[i].count > bridges[j].count
	})

	topConcepts := make([]string, 0, 3)
	for i := 0; i < 3 && i < len(bridges); i++ {
		topConcepts = append(topConcepts, bridges[i].concept)
	}

	title := fmt.Sprintf("Connection: %s", strings.Join(topConcepts, " + "))
	insight := fmt.Sprintf("These memories share a pattern around %s, suggesting a recurring theme in the workflow.",
		strings.Join(topConcepts, ", "))

	return &InsightResult{
		Title:      title,
		Insight:    insight,
		Concepts:   topConcepts,
		Confidence: 0.7,
	}
}

// InsightResult holds a heuristic insight.
type InsightResult struct {
	Title      string
	Insight    string
	Concepts   []string
	Confidence float64
}

// GeneratePattern detects a statistical pattern from a cluster of memories.
func GeneratePattern(clusterConcepts [][]string) *PatternResult {
	if len(clusterConcepts) < 3 {
		return nil
	}

	conceptFreq := make(map[string]int)
	for _, concepts := range clusterConcepts {
		seen := make(map[string]bool)
		for _, c := range concepts {
			if !seen[c] {
				conceptFreq[c]++
				seen[c] = true
			}
		}
	}

	threshold := int(math.Ceil(float64(len(clusterConcepts)) * 0.6))
	var patternConcepts []string
	for c, count := range conceptFreq {
		if count >= threshold {
			patternConcepts = append(patternConcepts, c)
		}
	}
	sort.Strings(patternConcepts)

	if len(patternConcepts) < 2 {
		return nil
	}

	title := fmt.Sprintf("Pattern: %s", strings.Join(patternConcepts, " + "))
	description := fmt.Sprintf("Recurring theme across %d memories involving %s.",
		len(clusterConcepts), strings.Join(patternConcepts, ", "))

	// Classify pattern type by keyword
	patternType := "code_practice"
	for _, c := range patternConcepts {
		switch c {
		case "error", "bug", "panic", "crash", "failure":
			patternType = "recurring_error"
		case "deploy", "build", "ci", "deployment":
			patternType = "workflow"
		case "decision", "chose", "choice":
			patternType = "decision_pattern"
		}
	}

	return &PatternResult{
		Title:       title,
		Description: description,
		PatternType: patternType,
		Concepts:    patternConcepts,
	}
}

// PatternResult holds a heuristic pattern detection result.
type PatternResult struct {
	Title       string
	Description string
	PatternType string
	Concepts    []string
}

// GeneratePrinciple synthesizes a principle from a cluster of patterns.
func GeneratePrinciple(patternDescriptions []string) *PrincipleResult {
	combined := strings.Join(patternDescriptions, " ")
	concepts := ExtractTopConcepts(combined, 5)

	if len(concepts) < 2 {
		return nil
	}

	title := fmt.Sprintf("Principle: %s", strings.Join(concepts[:2], " and "))
	principle := fmt.Sprintf("When working with %s, consistent patterns emerge around %s.",
		concepts[0], strings.Join(concepts[1:], " and "))

	return &PrincipleResult{
		Title:      title,
		Principle:  principle,
		Concepts:   concepts,
		Confidence: 0.6,
	}
}

// PrincipleResult holds a heuristic principle.
type PrincipleResult struct {
	Title      string
	Principle  string
	Concepts   []string
	Confidence float64
}

// GenerateAxiom synthesizes an axiom from a cluster of principles.
func GenerateAxiom(principleDescriptions []string) *AxiomResult {
	combined := strings.Join(principleDescriptions, " ")
	concepts := ExtractTopConcepts(combined, 4)

	if len(concepts) < 3 {
		return nil
	}

	title := fmt.Sprintf("Axiom: %s", concepts[0])
	axiom := fmt.Sprintf("Across all observed patterns, %s serves as a fundamental organizing principle.",
		concepts[0])

	return &AxiomResult{
		Title:      title,
		Axiom:      axiom,
		Concepts:   concepts,
		Confidence: 0.5,
	}
}

// AxiomResult holds a heuristic axiom.
type AxiomResult struct {
	Title      string
	Axiom      string
	Concepts   []string
	Confidence float64
}

// ExtractConcepts combines RAKE keyword extraction with vocabulary-based terms.
// RAKE provides multi-word domain phrases; vocabulary provides consistent single-word
// tags for association and pattern detection. Returns up to n unique concepts.
func ExtractConcepts(text string, n int) []string {
	seen := make(map[string]bool)
	var result []string

	// Phase 1: RAKE keywords (multi-word phrases, domain-adaptive)
	rakeResults := ExtractKeywords(text, n)
	for _, kw := range rakeResults {
		if !seen[kw] {
			seen[kw] = true
			result = append(result, kw)
		}
	}

	// Phase 2: Vocabulary terms (single-word, consistent tagging)
	vocabResults := ExtractTopConcepts(text, n)
	for _, v := range vocabResults {
		if !seen[v] && len(result) < n {
			seen[v] = true
			result = append(result, v)
		}
	}

	if len(result) > n {
		result = result[:n]
	}
	return result
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
