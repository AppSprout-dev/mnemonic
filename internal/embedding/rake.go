package embedding

import (
	"sort"
	"strings"
	"unicode"
)

// rakeStopWords is the set of words used to split text into candidate phrases.
// Based on the Fox stop word list with additions for technical content.
var rakeStopWords = map[string]bool{
	// Articles & determiners
	"a": true, "an": true, "the": true, "this": true, "that": true, "these": true, "those": true,
	// Pronouns
	"i": true, "me": true, "my": true, "we": true, "our": true, "you": true, "your": true,
	"he": true, "she": true, "it": true, "they": true, "them": true, "its": true, "his": true, "her": true,
	// Prepositions
	"in": true, "on": true, "at": true, "to": true, "for": true, "of": true, "with": true,
	"by": true, "from": true, "into": true, "through": true, "during": true, "before": true,
	"after": true, "above": true, "below": true, "between": true, "under": true, "over": true,
	"about": true, "against": true, "along": true, "around": true, "among": true,
	// Conjunctions
	"and": true, "but": true, "or": true, "nor": true, "so": true, "yet": true,
	// Common verbs
	"is": true, "are": true, "was": true, "were": true, "be": true, "been": true, "being": true,
	"have": true, "has": true, "had": true, "having": true,
	"do": true, "does": true, "did": true, "doing": true,
	"will": true, "would": true, "shall": true, "should": true,
	"can": true, "could": true, "may": true, "might": true, "must": true,
	"get": true, "got": true, "getting": true,
	// Other common words
	"not": true, "no": true, "just": true, "also": true, "very": true, "often": true,
	"if": true, "then": true, "than": true, "when": true, "where": true, "how": true, "what": true,
	"which": true, "who": true, "whom": true, "why": true,
	"all": true, "each": true, "every": true, "both": true, "few": true, "more": true,
	"most": true, "other": true, "some": true, "such": true, "only": true,
	"same": true, "own": true, "too": true, "here": true, "there": true,
	"up": true, "out": true, "off": true, "down": true, "once": true,
	"as": true, "while": true, "because": true, "since": true, "until": true,
	"any": true, "new": true, "now": true, "way": true, "well": true,
	"like": true, "use": true, "used": true, "using": true, "uses": true,
	"one": true, "two": true, "first": true, "second": true,
	// Filler
	"etc": true, "e": true, "g": true, "ie": true, "eg": true,
	"re": true, "vs": true, "via": true,
}

// RakeKeyword represents a keyword or phrase extracted by RAKE.
type RakeKeyword struct {
	Phrase string
	Score  float64
}

// ExtractKeywords uses RAKE (Rapid Automatic Keyword Extraction) to extract
// ranked keyword phrases from text. Unlike vocabulary-based extraction, RAKE
// discovers multi-word phrases and works with any domain vocabulary.
func ExtractKeywords(text string, n int) []string {
	phrases := extractCandidatePhrases(text)
	if len(phrases) == 0 {
		return nil
	}

	// Calculate word scores: score(w) = degree(w) / frequency(w)
	wordFreq := make(map[string]int)
	wordDegree := make(map[string]int)

	for _, phrase := range phrases {
		words := splitPhraseWords(phrase)
		for _, w := range words {
			wordFreq[w]++
			wordDegree[w] += len(words) // degree = number of co-occurring words
		}
	}

	wordScore := make(map[string]float64)
	for w, freq := range wordFreq {
		wordScore[w] = float64(wordDegree[w]) / float64(freq)
	}

	// Score each phrase: sum of its word scores
	type phraseScore struct {
		phrase string
		score  float64
	}
	var scored []phraseScore
	seen := make(map[string]bool)

	for _, phrase := range phrases {
		if seen[phrase] {
			continue
		}
		seen[phrase] = true

		words := splitPhraseWords(phrase)
		var score float64
		for _, w := range words {
			score += wordScore[w]
		}
		scored = append(scored, phraseScore{phrase, score})
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].phrase < scored[j].phrase // stable tie-break
	})

	// Return top N
	result := make([]string, 0, n)
	for i := 0; i < n && i < len(scored); i++ {
		result = append(result, scored[i].phrase)
	}
	return result
}

// extractCandidatePhrases splits text on stop words and punctuation to find
// candidate keyword phrases. Returns lowercased, trimmed phrases.
func extractCandidatePhrases(text string) []string {
	// Normalize: lowercase, replace punctuation with spaces (keep hyphens and underscores)
	lower := strings.ToLower(text)

	// Split into words, treating stop words and punctuation as delimiters
	var phrases []string
	var current []string

	for _, word := range tokenize(lower) {
		if rakeStopWords[word] || len(word) <= 1 {
			// Stop word or single char — flush current phrase
			if len(current) > 0 {
				phrase := strings.Join(current, " ")
				if len(phrase) > 1 {
					phrases = append(phrases, phrase)
				}
				current = current[:0]
			}
		} else {
			current = append(current, word)
		}
	}
	// Flush remaining
	if len(current) > 0 {
		phrase := strings.Join(current, " ")
		if len(phrase) > 1 {
			phrases = append(phrases, phrase)
		}
	}

	return phrases
}

// tokenize splits text into words on whitespace and punctuation boundaries.
// Preserves hyphens and underscores within words (e.g., "context-aware", "max_tokens").
func tokenize(text string) []string {
	var words []string
	var current strings.Builder

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				w := strings.Trim(current.String(), "-_")
				if w != "" {
					words = append(words, w)
				}
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		w := strings.Trim(current.String(), "-_")
		if w != "" {
			words = append(words, w)
		}
	}

	return words
}

// splitPhraseWords splits a phrase into constituent words.
func splitPhraseWords(phrase string) []string {
	return strings.Fields(phrase)
}
