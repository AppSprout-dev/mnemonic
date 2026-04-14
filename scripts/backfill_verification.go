// Backfill encoding verification metrics (EPR, FR, TED, MIG) for all memories
// that were encoded before Phase A went live.
//
// Usage: go run scripts/backfill_verification.go [--db path] [--dry-run]
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	_ "modernc.org/sqlite"
)

// Verification types — minimal port of internal/agent/encoding/verification.go
type verificationResult struct {
	EPR   float64
	FR    float64
	Flags []string
}

var (
	numberRE       = regexp.MustCompile(`-?\d{1,3}(?:,\d{3})+(?:\.\d+)?|-?\d+\.\d+[eE][+-]?\d+|-?\d+\.\d+%|-?\d+%|-?\d+\.\d+|\d+/\d+|\d+`)
	pathRE         = regexp.MustCompile(`[a-zA-Z_~/][\w/~.-]+\.(?:go|py|js|ts|html|css|yaml|yml|json|jsonl|toml|md|sh|sql|gguf|db|txt|log|patch|cuh|cpp|c|h)\b|/(?:home|usr|etc|var|tmp|opt|api|static)[\w./~-]+`)
	versionRE      = regexp.MustCompile(`v\d+\.\d+(?:\.\d+)?`)
	multiWordRE    = regexp.MustCompile(`\b([A-Z][a-z]+(?:\s+[A-Z][a-z]+)+)\b`)
	singleProperRE = regexp.MustCompile(`(?:[a-z,;]\s)([A-Z][a-z]{2,})\b`)
	mentionRE      = regexp.MustCompile(`@(\w+)`)
	camelCaseRE    = regexp.MustCompile(`\b([A-Z][a-z]+[A-Z]\w+)\b`)
)

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

var templateEchoPhrases = []string{
	"under 60 characters", "under 80 characters", "under 100 characters",
	"2-3 sentence summary", "key information", "broader context",
	"3-8 keyword strings", "cause/effect relationships", "how important is this",
	"no markdown fences", "no explanation", "no preamble", "output ONLY",
	"single JSON object", "output only valid json", "no phrases longer than",
	"fill in every json field", "encode this event into memory",
}

func extractEntities(text string) map[string]bool {
	entities := make(map[string]bool)
	for _, m := range numberRE.FindAllString(text, -1) {
		entities[strings.ReplaceAll(m, ",", "")] = true
	}
	for _, m := range pathRE.FindAllString(text, -1) {
		entities[strings.ToLower(m)] = true
	}
	for _, m := range versionRE.FindAllString(text, -1) {
		entities[strings.ToLower(m)] = true
	}
	for _, m := range multiWordRE.FindAllString(text, -1) {
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

func verify(rawText, outputText string) verificationResult {
	var result verificationResult

	inputEntities := extractEntities(rawText)
	outputEntities := extractEntities(outputText)

	if len(inputEntities) > 0 {
		preserved := 0
		outLower := strings.ToLower(outputText)
		for entity := range inputEntities {
			if strings.Contains(outLower, entity) {
				preserved++
			}
		}
		result.EPR = float64(preserved) / float64(len(inputEntities))
	} else {
		result.EPR = 1.0
	}

	if len(outputEntities) > 0 {
		fabricated := 0
		inLower := strings.ToLower(rawText)
		for entity := range outputEntities {
			if !strings.Contains(inLower, entity) {
				fabricated++
			}
		}
		result.FR = float64(fabricated) / float64(len(outputEntities))
	}

	outLower := strings.ToLower(outputText)
	for _, phrase := range templateEchoPhrases {
		if strings.Contains(outLower, phrase) {
			result.Flags = append(result.Flags, "template_echo:"+phrase)
			break
		}
	}

	rawTrimmed := strings.TrimSpace(rawText)
	nonWS := 0
	for _, r := range rawTrimmed {
		if !unicode.IsSpace(r) {
			nonWS++
		}
	}
	if nonWS < 50 && len(outputText) > 300 {
		result.Flags = append(result.Flags, "minimal_input_padded")
	}

	if result.EPR < 0.7 {
		result.Flags = append(result.Flags, fmt.Sprintf("low_epr:%.2f", result.EPR))
	}
	if result.FR > 0.3 {
		result.Flags = append(result.Flags, fmt.Sprintf("high_fr:%.2f", result.FR))
	}

	return result
}

func main() {
	home, _ := os.UserHomeDir()
	defaultDB := filepath.Join(home, ".mnemonic", "memory.db")

	dbPath := flag.String("db", defaultDB, "path to mnemonic database")
	dryRun := flag.Bool("dry-run", false, "print results without writing to DB")
	flag.Parse()

	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`
		SELECT m.id, COALESCE(m.summary, ''), COALESCE(m.content, ''),
		       COALESCE(r.content, m.content, '') as raw_content
		FROM memories m
		LEFT JOIN raw_memories r ON m.raw_id = r.id
		WHERE m.encoding_epr IS NULL AND m.state != 'merged'
		ORDER BY m.created_at`)
	if err != nil {
		log.Fatalf("query: %v", err)
	}
	defer func() { _ = rows.Close() }()

	type entry struct {
		id         string
		rawContent string
		outContent string
	}
	var entries []entry
	for rows.Next() {
		var e entry
		var summary, content, rawContent string
		if err := rows.Scan(&e.id, &summary, &content, &rawContent); err != nil {
			log.Fatalf("scan: %v", err)
		}
		e.rawContent = rawContent
		e.outContent = summary + " " + content
		entries = append(entries, e)
	}

	fmt.Printf("Backfilling %d memories...\n", len(entries))

	var updated, flagged int
	for _, e := range entries {
		result := verify(e.rawContent, e.outContent)

		if *dryRun {
			status := "OK"
			if len(result.Flags) > 0 {
				status = strings.Join(result.Flags, ", ")
			}
			fmt.Printf("  %s  EPR=%.2f  FR=%.2f  %s\n", e.id[:8], result.EPR, result.FR, status)
		} else {
			var flagsVal any
			if len(result.Flags) > 0 {
				fj, _ := json.Marshal(result.Flags)
				flagsVal = string(fj)
			}
			_, err := db.Exec(
				`UPDATE memories SET encoding_epr = ?, encoding_fr = ?, encoding_flags = ? WHERE id = ?`,
				result.EPR, result.FR, flagsVal, e.id,
			)
			if err != nil {
				log.Printf("  WARN: %s: %v", e.id[:8], err)
			}
		}
		updated++
		if len(result.Flags) > 0 {
			flagged++
		}
	}

	fmt.Printf("Done: %d verified, %d flagged\n", updated, flagged)
	if *dryRun {
		fmt.Println("(dry run — no changes written)")
	}
}
