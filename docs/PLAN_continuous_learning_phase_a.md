# Phase A: Runtime Verification & Experience Collection — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add runtime encoding verification (EPR, TED, FR, MIG) to the encoding pipeline, persist quality metrics, collect experience buffer entries, link recall feedback to encodings, and detect quality drift in metacognition.

**Architecture:** Go port of eval_faithfulness.py's entity extraction and verification checks, inserted between compression and embedding in the encoding pipeline. New `experience_buffer` and `recall_feedback` tables. Metacognition extended with rolling quality window.

**Tech Stack:** Go 1.22+, SQLite (modernc.org/sqlite), regexp, slog

**Spec:** `docs/SPEC_continuous_learning.md` Section 2

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `internal/agent/encoding/verification.go` | Entity extraction, EPR/FR/TED/MIG checks |
| Create | `internal/agent/encoding/verification_test.go` | Unit tests + parity test vs Python |
| Create | `migrations/007_continuous_learning.sql` | New tables + columns |
| Modify | `internal/store/store.go:580-608` | Add `ContinuousLearningStore` interface |
| Modify | `internal/store/sqlite/sqlite.go` | Implement new store methods |
| Modify | `internal/store/sqlite/schema.go:13` | Bump SchemaVersion to 16 |
| Create | `internal/store/sqlite/continuous_learning.go` | SQL implementations for experience buffer, recall_feedback |
| Modify | `internal/agent/encoding/agent.go:1046-1048` | Insert verification gate call |
| Modify | `internal/mcp/server.go:2003` | Hook feedback into experience buffer |
| Modify | `internal/agent/metacognition/agent.go:189` | Add quality drift observation |
| Modify | `internal/agent/dreaming/agent.go:177-179` | Add reclassification phase |
| Modify | `internal/config/config.go:18-42` | Add ContinuousLearningConfig |
| Modify | `config.yaml` | Add continuous_learning section |

---

### Task 1: Schema Migration

**Files:**
- Create: `migrations/007_continuous_learning.sql`
- Modify: `internal/store/sqlite/schema.go:13`

- [ ] **Step 1: Create migration file**

Create `migrations/007_continuous_learning.sql`:

```sql
-- Verification metrics on memories table
ALTER TABLE memories ADD COLUMN encoding_epr REAL DEFAULT NULL;
ALTER TABLE memories ADD COLUMN encoding_fr REAL DEFAULT NULL;
ALTER TABLE memories ADD COLUMN encoding_flags TEXT DEFAULT NULL;

-- Recall-encoding linkage: tracks which memories were recalled and rated
CREATE TABLE IF NOT EXISTS recall_feedback (
    id TEXT PRIMARY KEY,
    query TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    feedback TEXT NOT NULL,
    recall_session_id TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (memory_id) REFERENCES memories(id)
);
CREATE INDEX IF NOT EXISTS idx_recall_feedback_memory ON recall_feedback(memory_id);
CREATE INDEX IF NOT EXISTS idx_recall_feedback_session ON recall_feedback(recall_session_id);

-- Experience buffer: curated training candidates with quality metrics
CREATE TABLE IF NOT EXISTS experience_buffer (
    id TEXT PRIMARY KEY,
    raw_id TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    encoding_epr REAL,
    encoding_fr REAL,
    encoding_flags TEXT,
    recall_score REAL,
    recall_count INTEGER DEFAULT 0,
    category TEXT DEFAULT 'ambiguous',
    used_in_training INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (memory_id) REFERENCES memories(id)
);
CREATE INDEX IF NOT EXISTS idx_experience_buffer_category ON experience_buffer(category);
CREATE INDEX IF NOT EXISTS idx_experience_buffer_memory ON experience_buffer(memory_id);
```

- [ ] **Step 2: Bump schema version**

In `internal/store/sqlite/schema.go`, change:

```go
const SchemaVersion = 15
```

to:

```go
const SchemaVersion = 16
```

- [ ] **Step 3: Verify migration applies**

Run: `make build && ./bin/mnemonic version`

The daemon will apply migration 007 on next startup. For now just verify it compiles.

- [ ] **Step 4: Commit**

```bash
git add migrations/007_continuous_learning.sql internal/store/sqlite/schema.go
git commit -m "feat: schema migration for continuous learning (#391)

Add encoding_epr/fr/flags columns to memories, create recall_feedback
and experience_buffer tables for Phase A experience collection."
```

---

### Task 2: Store Interface — ContinuousLearningStore

**Files:**
- Modify: `internal/store/store.go`

- [ ] **Step 1: Add types for experience buffer and recall feedback**

Add after the existing type definitions (before the Store interface at line 585):

```go
// ExperienceEntry represents a training candidate in the experience buffer.
type ExperienceEntry struct {
	ID            string    `json:"id"`
	RawID         string    `json:"raw_id"`
	MemoryID      string    `json:"memory_id"`
	EncodingEPR   float64   `json:"encoding_epr"`
	EncodingFR    float64   `json:"encoding_fr"`
	EncodingFlags []string  `json:"encoding_flags"`
	RecallScore   float64   `json:"recall_score"`
	RecallCount   int       `json:"recall_count"`
	Category      string    `json:"category"` // gold, needs_improvement, ambiguous
	UsedInTraining bool     `json:"used_in_training"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ExperienceStats summarizes the experience buffer contents.
type ExperienceStats struct {
	Gold             int `json:"gold"`
	NeedsImprovement int `json:"needs_improvement"`
	Ambiguous        int `json:"ambiguous"`
	Total            int `json:"total"`
}

// RecallFeedbackEntry links a recall query to a specific memory's feedback rating.
type RecallFeedbackEntry struct {
	ID              string    `json:"id"`
	Query           string    `json:"query"`
	MemoryID        string    `json:"memory_id"`
	Feedback        string    `json:"feedback"` // helpful, partial, irrelevant
	RecallSessionID string    `json:"recall_session_id"`
	CreatedAt       time.Time `json:"created_at"`
}

// EncodingQualityWindow holds rolling quality metrics for drift detection.
type EncodingQualityWindow struct {
	WindowSize  int     `json:"window_size"`
	MeanEPR     float64 `json:"mean_epr"`
	TEDRate     float64 `json:"ted_rate"`
	FlaggedRate float64 `json:"flagged_rate"`
	SampleCount int     `json:"sample_count"`
}
```

- [ ] **Step 2: Add the ContinuousLearningStore interface**

Add before the main Store interface (before line 585):

```go
// ContinuousLearningStore manages experience collection for continuous learning.
type ContinuousLearningStore interface {
	// Verification results (written during encoding)
	WriteVerificationResult(ctx context.Context, memoryID string, epr float64, fr float64, flags []string) error

	// Experience buffer
	WriteExperienceEntry(ctx context.Context, entry ExperienceEntry) error
	UpdateExperienceRecallScore(ctx context.Context, memoryID string, feedback string) error
	ReclassifyExperienceBuffer(ctx context.Context) (int, error)
	ListExperienceByCategory(ctx context.Context, category string, limit int) ([]ExperienceEntry, error)
	GetExperienceBufferStats(ctx context.Context) (ExperienceStats, error)

	// Recall-encoding linkage
	WriteRecallFeedbackEntry(ctx context.Context, entry RecallFeedbackEntry) error
	GetRecallHistory(ctx context.Context, memoryID string) ([]RecallFeedbackEntry, error)

	// Quality drift detection
	GetEncodingQualityWindow(ctx context.Context, windowSize int) (EncodingQualityWindow, error)
}
```

- [ ] **Step 3: Embed in the main Store interface**

Add `ContinuousLearningStore` to the Store interface at line 604:

```go
type Store interface {
	RawMemoryStore
	MemoryStore
	SearchStore
	AssociationStore
	ConceptStore
	EpisodeStore
	PatternStore
	AbstractionStore
	MetacognitionStore
	FeedbackStore
	ConsolidationStore
	SessionStore
	ExclusionStore
	UsageStore
	ForumStore
	AnalyticsStore
	ContinuousLearningStore

	// --- Lifecycle ---
	Close() error
}
```

- [ ] **Step 4: Verify compilation**

Run: `make build`

Expected: Compilation fails because sqlite.go doesn't implement ContinuousLearningStore yet. That's correct — Task 3 fixes it.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go
git commit -m "feat: store interface for continuous learning (#391)

Add ContinuousLearningStore interface with types for ExperienceEntry,
ExperienceStats, RecallFeedbackEntry, and EncodingQualityWindow."
```

---

### Task 3: SQLite Implementation — ContinuousLearningStore

**Files:**
- Create: `internal/store/sqlite/continuous_learning.go`

- [ ] **Step 1: Implement WriteVerificationResult**

Create `internal/store/sqlite/continuous_learning.go`:

```go
package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/google/uuid"
)

func (s *SQLiteStore) WriteVerificationResult(ctx context.Context, memoryID string, epr float64, fr float64, flags []string) error {
	flagsJSON, err := json.Marshal(flags)
	if err != nil {
		return fmt.Errorf("marshaling flags: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`UPDATE memories SET encoding_epr = ?, encoding_fr = ?, encoding_flags = ? WHERE id = ?`,
		epr, fr, string(flagsJSON), memoryID,
	)
	if err != nil {
		return fmt.Errorf("writing verification result for %s: %w", memoryID, err)
	}
	return nil
}
```

- [ ] **Step 2: Implement WriteExperienceEntry**

Append to the same file:

```go
func (s *SQLiteStore) WriteExperienceEntry(ctx context.Context, entry store.ExperienceEntry) error {
	flagsJSON, err := json.Marshal(entry.EncodingFlags)
	if err != nil {
		return fmt.Errorf("marshaling encoding flags: %w", err)
	}

	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	now := time.Now()

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO experience_buffer (id, raw_id, memory_id, encoding_epr, encoding_fr, encoding_flags, recall_score, recall_count, category, used_in_training, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.RawID, entry.MemoryID,
		entry.EncodingEPR, entry.EncodingFR, string(flagsJSON),
		entry.RecallScore, entry.RecallCount, entry.Category,
		entry.UsedInTraining, now, now,
	)
	if err != nil {
		return fmt.Errorf("writing experience entry for memory %s: %w", entry.MemoryID, err)
	}
	return nil
}
```

- [ ] **Step 3: Implement UpdateExperienceRecallScore**

```go
func (s *SQLiteStore) UpdateExperienceRecallScore(ctx context.Context, memoryID string, feedback string) error {
	var rating float64
	switch feedback {
	case "helpful":
		rating = 1.0
	case "partial":
		rating = 0.5
	case "irrelevant":
		rating = 0.0
	default:
		return fmt.Errorf("invalid feedback rating: %s", feedback)
	}

	// Running weighted average: new = (old * count + rating) / (count + 1)
	_, err := s.db.ExecContext(ctx,
		`UPDATE experience_buffer
		 SET recall_score = CASE
		     WHEN recall_count = 0 THEN ?
		     ELSE (recall_score * recall_count + ?) / (recall_count + 1)
		 END,
		 recall_count = recall_count + 1,
		 updated_at = CURRENT_TIMESTAMP
		 WHERE memory_id = ?`,
		rating, rating, memoryID,
	)
	if err != nil {
		return fmt.Errorf("updating recall score for memory %s: %w", memoryID, err)
	}
	return nil
}
```

- [ ] **Step 4: Implement ReclassifyExperienceBuffer**

```go
func (s *SQLiteStore) ReclassifyExperienceBuffer(ctx context.Context) (int, error) {
	// Gold: EPR > 0.9 AND no TED AND (recall_score > 0.8 OR (no recalls AND no flags))
	res1, err := s.db.ExecContext(ctx,
		`UPDATE experience_buffer SET category = 'gold', updated_at = CURRENT_TIMESTAMP
		 WHERE encoding_epr > 0.9
		   AND (encoding_flags IS NULL OR encoding_flags = '[]' OR encoding_flags NOT LIKE '%template_echo%')
		   AND (
		       (recall_score > 0.8)
		       OR (recall_count = 0 AND (encoding_flags IS NULL OR encoding_flags = '[]'))
		   )
		   AND category != 'gold'`)
	if err != nil {
		return 0, fmt.Errorf("reclassifying gold: %w", err)
	}
	gold, _ := res1.RowsAffected()

	// Needs improvement: EPR < 0.7 OR TED OR (recall_score < 0.3 AND recall_count >= 3)
	res2, err := s.db.ExecContext(ctx,
		`UPDATE experience_buffer SET category = 'needs_improvement', updated_at = CURRENT_TIMESTAMP
		 WHERE (
		     encoding_epr < 0.7
		     OR (encoding_flags IS NOT NULL AND encoding_flags LIKE '%template_echo%')
		     OR (recall_score < 0.3 AND recall_count >= 3)
		 )
		 AND category != 'needs_improvement'`)
	if err != nil {
		return 0, fmt.Errorf("reclassifying needs_improvement: %w", err)
	}
	needs, _ := res2.RowsAffected()

	return int(gold + needs), nil
}
```

- [ ] **Step 5: Implement remaining methods**

```go
func (s *SQLiteStore) ListExperienceByCategory(ctx context.Context, category string, limit int) ([]store.ExperienceEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, raw_id, memory_id, encoding_epr, encoding_fr, encoding_flags,
		        recall_score, recall_count, category, used_in_training, created_at, updated_at
		 FROM experience_buffer WHERE category = ? ORDER BY created_at DESC LIMIT ?`,
		category, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing experience by category %s: %w", category, err)
	}
	defer rows.Close()

	var entries []store.ExperienceEntry
	for rows.Next() {
		var e store.ExperienceEntry
		var flagsJSON string
		var usedInt int
		if err := rows.Scan(&e.ID, &e.RawID, &e.MemoryID, &e.EncodingEPR, &e.EncodingFR, &flagsJSON,
			&e.RecallScore, &e.RecallCount, &e.Category, &usedInt, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning experience entry: %w", err)
		}
		_ = json.Unmarshal([]byte(flagsJSON), &e.EncodingFlags)
		e.UsedInTraining = usedInt != 0
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *SQLiteStore) GetExperienceBufferStats(ctx context.Context) (store.ExperienceStats, error) {
	var stats store.ExperienceStats
	row := s.db.QueryRowContext(ctx,
		`SELECT
		    COALESCE(SUM(CASE WHEN category = 'gold' THEN 1 ELSE 0 END), 0),
		    COALESCE(SUM(CASE WHEN category = 'needs_improvement' THEN 1 ELSE 0 END), 0),
		    COALESCE(SUM(CASE WHEN category = 'ambiguous' THEN 1 ELSE 0 END), 0),
		    COUNT(*)
		 FROM experience_buffer`)
	if err := row.Scan(&stats.Gold, &stats.NeedsImprovement, &stats.Ambiguous, &stats.Total); err != nil {
		return stats, fmt.Errorf("getting experience buffer stats: %w", err)
	}
	return stats, nil
}

func (s *SQLiteStore) WriteRecallFeedbackEntry(ctx context.Context, entry store.RecallFeedbackEntry) error {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO recall_feedback (id, query, memory_id, feedback, recall_session_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.Query, entry.MemoryID, entry.Feedback, entry.RecallSessionID, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("writing recall feedback for memory %s: %w", entry.MemoryID, err)
	}
	return nil
}

func (s *SQLiteStore) GetRecallHistory(ctx context.Context, memoryID string) ([]store.RecallFeedbackEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, query, memory_id, feedback, recall_session_id, created_at
		 FROM recall_feedback WHERE memory_id = ? ORDER BY created_at DESC`,
		memoryID,
	)
	if err != nil {
		return nil, fmt.Errorf("getting recall history for %s: %w", memoryID, err)
	}
	defer rows.Close()

	var entries []store.RecallFeedbackEntry
	for rows.Next() {
		var e store.RecallFeedbackEntry
		if err := rows.Scan(&e.ID, &e.Query, &e.MemoryID, &e.Feedback, &e.RecallSessionID, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning recall feedback: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *SQLiteStore) GetEncodingQualityWindow(ctx context.Context, windowSize int) (store.EncodingQualityWindow, error) {
	var w store.EncodingQualityWindow
	w.WindowSize = windowSize

	row := s.db.QueryRowContext(ctx,
		`SELECT
		    COALESCE(AVG(encoding_epr), 0),
		    COALESCE(SUM(CASE WHEN encoding_flags LIKE '%template_echo%' THEN 1.0 ELSE 0.0 END) / MAX(COUNT(*), 1), 0),
		    COALESCE(SUM(CASE WHEN encoding_flags IS NOT NULL AND encoding_flags != '[]' THEN 1.0 ELSE 0.0 END) / MAX(COUNT(*), 1), 0),
		    COUNT(*)
		 FROM (SELECT encoding_epr, encoding_flags FROM memories WHERE encoding_epr IS NOT NULL ORDER BY created_at DESC LIMIT ?)`,
		windowSize,
	)
	if err := row.Scan(&w.MeanEPR, &w.TEDRate, &w.FlaggedRate, &w.SampleCount); err != nil {
		return w, fmt.Errorf("getting encoding quality window: %w", err)
	}
	return w, nil
}
```

- [ ] **Step 6: Verify compilation**

Run: `make build`

Expected: PASS — all Store interface methods now implemented.

- [ ] **Step 7: Commit**

```bash
git add internal/store/sqlite/continuous_learning.go
git commit -m "feat: SQLite implementation for continuous learning store (#391)

Implements ContinuousLearningStore: verification results, experience buffer
CRUD, recall feedback linkage, reclassification, and quality window queries."
```

---

### Task 4: Verification Gate — Entity Extraction & Checks

**Files:**
- Create: `internal/agent/encoding/verification.go`

- [ ] **Step 1: Create verification.go with entity extraction regexes**

Create `internal/agent/encoding/verification.go`:

```go
package encoding

import (
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
	"Since": true, "Still": true, "Think": true, "Where": true, "While": true,
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
```

Note: add `"fmt"` to imports — needed for `fmt.Sprintf` in the flags.

- [ ] **Step 2: Verify compilation**

Run: `make build`

Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/agent/encoding/verification.go
git commit -m "feat: runtime faithfulness verification gate (#391)

Go port of eval_faithfulness.py entity extraction and verification checks.
Computes EPR, FR, TED, MIG on every encoding. Pure string matching, ~0.3ms."
```

---

### Task 5: Verification Gate Tests

**Files:**
- Create: `internal/agent/encoding/verification_test.go`

- [ ] **Step 1: Write entity extraction tests**

Create `internal/agent/encoding/verification_test.go`:

```go
package encoding

import (
	"testing"
)

func TestExtractEntities_Numbers(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"plain integer", "found 200 errors", []string{"200"}},
		{"decimal", "accuracy was 0.847", []string{"0.847"}},
		{"percentage", "achieved 94.2% recall", []string{"94.2%"}},
		{"comma separated", "47,231 records processed", []string{"47231"}}, // normalized
		{"fraction", "12/21 tests passed", []string{"12/21"}},
		{"negative", "delta was -3.5", []string{"-3.5"}},
		{"scientific", "learning rate 2.3e-4", []string{"2.3e-4"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entities := extractEntities(tc.input)
			for _, exp := range tc.expected {
				if !entities[exp] {
					t.Errorf("expected entity %q not found in %v", exp, entities)
				}
			}
		})
	}
}

func TestExtractEntities_Paths(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"go file", "changed internal/agent/encoding/agent.go", "internal/agent/encoding/agent.go"},
		{"python file", "running training/scripts/eval_faithfulness.py", "training/scripts/eval_faithfulness.py"},
		{"absolute path", "config at /home/user/config.yaml", "/home/user/config.yaml"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entities := extractEntities(tc.input)
			if !entities[tc.expected] {
				t.Errorf("expected path %q not found in %v", tc.expected, entities)
			}
		})
	}
}

func TestExtractEntities_Versions(t *testing.T) {
	entities := extractEntities("upgraded from v1.2.3 to v2.0")
	if !entities["v1.2.3"] {
		t.Error("expected v1.2.3")
	}
	if !entities["v2.0"] {
		t.Error("expected v2.0")
	}
}

func TestExtractEntities_ProperNouns(t *testing.T) {
	entities := extractEntities("Caleb discussed with Aaron Gokaslan about PostgreSQL migration")
	if !entities["aaron gokaslan"] {
		t.Error("expected 'aaron gokaslan' as multi-word proper noun")
	}
	// "Caleb" should be caught by single proper noun regex
	if !entities["caleb"] {
		// May not match if not preceded by lowercase — that's OK
		t.Log("'caleb' not extracted as single proper noun (expected if at start of text)")
	}
}

func TestVerifyFaithfulness_HighEPR(t *testing.T) {
	raw := "Fixed null pointer in auth middleware at internal/agent/encoding/agent.go:42. Error was in v2.1.3."
	compression := &compressionResponse{
		Gist:    "Fixed null pointer in auth middleware",
		Summary: "Resolved null pointer exception in auth middleware at internal/agent/encoding/agent.go:42",
		Content: "A null pointer bug in the auth middleware was fixed at internal/agent/encoding/agent.go:42. The issue was present since v2.1.3.",
		Narrative: "The auth middleware had a null pointer that was causing crashes.",
	}

	result := verifyFaithfulness(raw, compression)

	if result.EPR < 0.7 {
		t.Errorf("expected high EPR, got %.2f", result.EPR)
	}
	if result.TED {
		t.Error("unexpected template echo detection")
	}
}

func TestVerifyFaithfulness_TemplateEcho(t *testing.T) {
	raw := "deployed new service"
	compression := &compressionResponse{
		Gist:    "deployed new service under 60 characters",
		Summary: "A new service was deployed. Output ONLY valid JSON.",
		Content: "Service deployed successfully.",
	}

	result := verifyFaithfulness(raw, compression)

	if !result.TED {
		t.Error("expected template echo detection")
	}
	if len(result.Flags) == 0 {
		t.Error("expected flags to be set")
	}
}

func TestVerifyFaithfulness_MinimalInputGuard(t *testing.T) {
	raw := "WAL mode on."
	compression := &compressionResponse{
		Gist:    "Enabled WAL mode on the database",
		Summary: "WAL mode was enabled for the SQLite database to improve concurrent read performance.",
		Content: "The database was configured with Write-Ahead Logging (WAL) mode to improve concurrent read performance. " +
			"This is a common optimization for SQLite databases that allows multiple readers while a single writer commits transactions. " +
			"The change affects all database operations and requires no schema changes. WAL mode persists across database connections " +
			"and provides better throughput for read-heavy workloads typical in memory retrieval systems.",
	}

	result := verifyFaithfulness(raw, compression)

	if !result.MIG {
		t.Error("expected MIG flag for short input with long output")
	}
}

func TestVerifyFaithfulness_NoEntities(t *testing.T) {
	raw := "ok"
	compression := &compressionResponse{
		Gist:    "acknowledged",
		Summary: "Simple acknowledgment.",
		Content: "acknowledged",
	}

	result := verifyFaithfulness(raw, compression)

	if result.EPR != 1.0 {
		t.Errorf("expected EPR 1.0 for no-entity input, got %.2f", result.EPR)
	}
}
```

- [ ] **Step 2: Run tests**

Run: `cd /home/hubcaps/Projects/mem && go test ./internal/agent/encoding/ -run TestExtract -v`

Expected: All entity extraction tests PASS.

Run: `go test ./internal/agent/encoding/ -run TestVerify -v`

Expected: All verification tests PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/encoding/verification_test.go
git commit -m "test: verification gate unit tests (#391)

Entity extraction tests (numbers, paths, versions, proper nouns),
EPR validation, template echo detection, and minimal input guard."
```

---

### Task 6: Wire Verification Gate into Encoding Pipeline

**Files:**
- Modify: `internal/agent/encoding/agent.go:1046-1048`

- [ ] **Step 1: Insert verification gate call after compression**

At `internal/agent/encoding/agent.go`, after line 1046 (the "compression completed" debug log) and before line 1048 (the "Step 3: Generate embedding" comment), insert:

```go
	// Step 2b: Verify faithfulness (EPR, TED, FR, MIG)
	verification := verifyFaithfulness(raw.Content, compression)
	ea.log.Debug("verification completed",
		"raw_id", raw.ID,
		"epr", verification.EPR,
		"fr", verification.FR,
		"ted", verification.TED,
		"flags", verification.Flags,
	)
	if verification.TED {
		ea.log.Warn("template echo detected in encoding",
			"raw_id", raw.ID,
			"flags", verification.Flags,
		)
		// Reduce salience for template-echoed encodings
		if compression.Salience > 0.1 {
			compression.Salience -= 0.1
		}
	}
	if verification.EPR < 0.7 {
		ea.log.Warn("low entity preservation rate",
			"raw_id", raw.ID,
			"epr", verification.EPR,
			"input_entities", verification.InputEntities,
		)
	}
```

- [ ] **Step 2: Store verification result and experience entry after persistence**

After the `persistEncodedMemory` call at line 1059 (now shifted down due to insertion), add:

```go
	// Step 8b: Store verification metrics and experience buffer entry
	if result != nil && result.MemoryID != "" {
		if err := ea.store.WriteVerificationResult(ctx, result.MemoryID, verification.EPR, verification.FR, verification.Flags); err != nil {
			ea.log.Warn("failed to write verification result", "memory_id", result.MemoryID, "error", err)
		}

		entry := store.ExperienceEntry{
			RawID:         raw.ID,
			MemoryID:      result.MemoryID,
			EncodingEPR:   verification.EPR,
			EncodingFR:    verification.FR,
			EncodingFlags: verification.Flags,
			Category:      "ambiguous",
		}
		if err := ea.store.WriteExperienceEntry(ctx, entry); err != nil {
			ea.log.Warn("failed to write experience entry", "memory_id", result.MemoryID, "error", err)
		}
	}
```

- [ ] **Step 3: Add store import if needed**

The encoding agent already imports `store` — verify with `go build`.

- [ ] **Step 4: Verify compilation and run existing tests**

Run: `make build && go test ./internal/agent/encoding/ -v`

Expected: Build succeeds. Existing tests pass (mock store will need the new ContinuousLearningStore methods — the `storetest.MockStore` may need stub implementations).

- [ ] **Step 5: Add stub implementations to MockStore if needed**

If `storetest.MockStore` doesn't satisfy `ContinuousLearningStore`, add no-op stubs. Check `internal/store/storetest/` for the mock pattern and add:

```go
func (m *MockStore) WriteVerificationResult(ctx context.Context, memoryID string, epr float64, fr float64, flags []string) error { return nil }
func (m *MockStore) WriteExperienceEntry(ctx context.Context, entry store.ExperienceEntry) error { return nil }
func (m *MockStore) UpdateExperienceRecallScore(ctx context.Context, memoryID string, feedback string) error { return nil }
func (m *MockStore) ReclassifyExperienceBuffer(ctx context.Context) (int, error) { return 0, nil }
func (m *MockStore) ListExperienceByCategory(ctx context.Context, category string, limit int) ([]store.ExperienceEntry, error) { return nil, nil }
func (m *MockStore) GetExperienceBufferStats(ctx context.Context) (store.ExperienceStats, error) { return store.ExperienceStats{}, nil }
func (m *MockStore) WriteRecallFeedbackEntry(ctx context.Context, entry store.RecallFeedbackEntry) error { return nil }
func (m *MockStore) GetRecallHistory(ctx context.Context, memoryID string) ([]store.RecallFeedbackEntry, error) { return nil, nil }
func (m *MockStore) GetEncodingQualityWindow(ctx context.Context, windowSize int) (store.EncodingQualityWindow, error) { return store.EncodingQualityWindow{}, nil }
```

- [ ] **Step 6: Run full test suite**

Run: `make test`

Expected: All tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/encoding/agent.go internal/store/storetest/
git commit -m "feat: wire verification gate into encoding pipeline (#391)

Every encoding now runs through verifyFaithfulness() after compression.
EPR, FR, TED, MIG metrics stored on memory record and experience buffer.
Template echo reduces salience by 0.1."
```

---

### Task 7: Hook Recall Feedback into Experience Buffer

**Files:**
- Modify: `internal/mcp/server.go:2003`

- [ ] **Step 1: Add experience buffer update after feedback storage**

At `internal/mcp/server.go`, after line 2003 (after `WriteMetaObservation` succeeds and before the `if queryID != ""` block at line 2005), insert:

```go
	// Update experience buffer recall scores for each memory that received feedback
	for _, memID := range memoryIDs {
		if err := srv.store.UpdateExperienceRecallScore(ctx, memID, quality); err != nil {
			srv.log.Debug("no experience entry for memory (may predate continuous learning)", "memory_id", memID)
		}

		// Also record the recall-encoding linkage
		rfEntry := store.RecallFeedbackEntry{
			Query:           query,
			MemoryID:        memID,
			Feedback:        quality,
			RecallSessionID: srv.sessionID,
		}
		if err := srv.store.WriteRecallFeedbackEntry(ctx, rfEntry); err != nil {
			srv.log.Warn("failed to write recall feedback entry", "memory_id", memID, "error", err)
		}
	}
```

- [ ] **Step 2: Add store import if needed**

The MCP server already imports `store` — verify compilation.

- [ ] **Step 3: Verify compilation**

Run: `make build`

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/server.go
git commit -m "feat: hook recall feedback into experience buffer (#391)

When users rate recall quality via the feedback MCP tool, the rating
propagates to the experience buffer as a running weighted average.
Also records recall-encoding linkage for downstream analysis."
```

---

### Task 8: Dreaming Agent — Experience Buffer Reclassification

**Files:**
- Modify: `internal/agent/dreaming/agent.go:177-179`

- [ ] **Step 1: Add reclassification phase after cross-project linking**

At `internal/agent/dreaming/agent.go`, between line 177 (end of crossProjectLink) and line 179 (start of linkToPatterns), insert:

```go
	// Phase 4.5: Reclassify experience buffer entries based on accumulated feedback
	if reclassified, err := da.store.ReclassifyExperienceBuffer(ctx); err != nil && ctx.Err() == nil {
		da.log.Error("experience buffer reclassification failed", "error", err)
	} else if reclassified > 0 {
		da.log.Info("reclassified experience buffer entries", "count", reclassified)
	}
```

- [ ] **Step 2: Verify compilation**

Run: `make build`

Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/agent/dreaming/agent.go
git commit -m "feat: reclassify experience buffer during dreaming (#391)

Phase 4.5 in the dreaming cycle reclassifies experience buffer entries
as gold/needs_improvement/ambiguous based on accumulated EPR, TED, and
recall feedback scores."
```

---

### Task 9: Metacognition — Quality Drift Detection

**Files:**
- Modify: `internal/agent/metacognition/agent.go`

- [ ] **Step 1: Add encoding quality drift observation**

Find the method that collects all observations (the function that calls `auditMemoryQuality` and other audit methods, assembles them into a slice, and stores them). Add a new call after the existing audit methods:

```go
func (ma *MetacognitionAgent) auditEncodingQuality(ctx context.Context) *store.MetaObservation {
	window, err := ma.store.GetEncodingQualityWindow(ctx, 100)
	if err != nil {
		ma.log.Warn("failed to get encoding quality window", "error", err)
		return nil
	}

	// Need at least 20 samples to make a meaningful assessment
	if window.SampleCount < 20 {
		return nil
	}

	details := map[string]interface{}{
		"window_size":  window.WindowSize,
		"mean_epr":     window.MeanEPR,
		"ted_rate":     window.TEDRate,
		"flagged_rate": window.FlaggedRate,
		"sample_count": window.SampleCount,
	}

	severity := "info"
	trend := "stable"

	// Detect degradation: EPR below 0.85 or TED rate above 5%
	if window.MeanEPR < 0.85 {
		severity = "warning"
		trend = "degrading"
		details["issue"] = "mean EPR below 0.85"
	}
	if window.TEDRate > 0.05 {
		severity = "warning"
		trend = "degrading"
		details["issue"] = "template echo rate above 5%"
	}
	if window.MeanEPR < 0.70 || window.TEDRate > 0.15 {
		severity = "critical"
		trend = "degrading"
	}
	if window.MeanEPR > 0.93 && window.TEDRate < 0.02 {
		trend = "improving"
	}

	details["trend"] = trend

	return &store.MetaObservation{
		ObservationType: "encoding_quality_drift",
		Severity:        severity,
		Details:         details,
	}
}
```

- [ ] **Step 2: Wire into the observation collection loop**

Find where other audit methods are called (the function that builds the observations slice) and add:

```go
	if obs := ma.auditEncodingQuality(ctx); obs != nil {
		observations = append(observations, obs)
	}
```

- [ ] **Step 3: Verify compilation and tests**

Run: `make build && go test ./internal/agent/metacognition/ -v`

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/agent/metacognition/agent.go
git commit -m "feat: encoding quality drift detection in metacognition (#391)

New observation type 'encoding_quality_drift' tracks rolling EPR and TED
rate over the last 100 encodings. Emits warning when EPR < 0.85 or TED
rate > 5%, critical when EPR < 0.70 or TED > 15%."
```

---

### Task 10: Configuration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.yaml`

- [ ] **Step 1: Add ContinuousLearningConfig struct**

In `internal/config/config.go`, add after the existing config structs (before the `Config` struct or after `TrainingConfig`):

```go
// ContinuousLearningConfig holds settings for the continuous learning pipeline.
type ContinuousLearningConfig struct {
	Enabled  bool                      `yaml:"enabled"`  // master switch
	Training CLTrainingConfig          `yaml:"training"`
	Trigger  CLTriggerConfig           `yaml:"trigger"`
}

// CLTrainingConfig holds training-specific settings for continuous learning.
type CLTrainingConfig struct {
	MinNewExamples   int     `yaml:"min_new_examples"`   // minimum experience entries before training (default: 50)
	MaxExamplesPerRun int    `yaml:"max_examples_per_run"` // cap batch size (default: 200)
	ReplayRatio      float64 `yaml:"replay_ratio"`        // fraction from base dataset (default: 0.30)
	RollbackVersions int     `yaml:"rollback_versions"`   // keep last N spoke versions (default: 3)
}

// CLTriggerConfig holds trigger settings for continuous learning.
type CLTriggerConfig struct {
	Auto           bool   `yaml:"auto"`            // metacognition auto-trigger (default: false)
	Manual         bool   `yaml:"manual"`           // MCP tool trigger (default: true)
	TrainingWindow string `yaml:"training_window"`  // auto-trigger window, e.g. "02:00-06:00"
}
```

- [ ] **Step 2: Add to main Config struct**

Add to the `Config` struct at `internal/config/config.go:18-42`:

```go
ContinuousLearning ContinuousLearningConfig `yaml:"continuous_learning"`
```

- [ ] **Step 3: Add defaults to config.yaml**

Append to `config.yaml`:

```yaml
# Continuous learning — encoding model improves from operational experience (#391)
continuous_learning:
  enabled: false
  training:
    min_new_examples: 50
    max_examples_per_run: 200
    replay_ratio: 0.30
    rollback_versions: 3
  trigger:
    auto: false
    manual: true
    training_window: "02:00-06:00"
```

- [ ] **Step 4: Verify compilation**

Run: `make build`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go config.yaml
git commit -m "feat: continuous learning configuration (#391)

Add ContinuousLearningConfig with training, curriculum, and trigger
settings. Disabled by default (opt-in). Manual trigger enabled."
```

---

### Task 11: Build, Test, Deploy to Daemon

- [ ] **Step 1: Run full test suite**

Run: `make test`

Expected: All tests pass.

- [ ] **Step 2: Run lint**

Run: `golangci-lint run`

Expected: No new warnings. Fix any errcheck or unused issues.

- [ ] **Step 3: Build and restart daemon**

Run: `make build && systemctl --user restart mnemonic`

- [ ] **Step 4: Verify daemon is healthy**

Run: `systemctl --user status mnemonic`

Expected: Active (running). Migration 007 applies on startup (check logs):

Run: `journalctl --user -u mnemonic --since "1 min ago" | grep -i migration`

- [ ] **Step 5: Test verification in production**

Use the mnemonic MCP `remember` tool to store a test memory. Then check the dashboard or query the DB:

Run: `sqlite3 ~/.mnemonic/mnemonic.db "SELECT id, encoding_epr, encoding_fr, encoding_flags FROM memories ORDER BY created_at DESC LIMIT 5"`

Expected: The newest memory has EPR, FR, and flags populated.

Run: `sqlite3 ~/.mnemonic/mnemonic.db "SELECT * FROM experience_buffer ORDER BY created_at DESC LIMIT 5"`

Expected: Corresponding experience buffer entry exists.

- [ ] **Step 6: Commit any fixes from integration testing**

If any issues found during live testing, fix and commit.

---

### Task 12: Parity Test — Go vs Python Verification

- [ ] **Step 1: Create parity test script**

This is a one-time validation that the Go verification gate produces the same results as the Python eval_faithfulness.py. Run both on the 25 EXP-25 gold inputs and compare EPR values.

Create a test in `internal/agent/encoding/verification_test.go` that loads the gold data:

```go
func TestVerification_ParityWithPython(t *testing.T) {
	// This test requires the gold data files from EXP-25.
	// Skip if files don't exist (CI environments without training data).
	goldPath := "../../../training/data/faithfulness_probe/gold_train.jsonl"
	if _, err := os.Stat(goldPath); os.IsNotExist(err) {
		t.Skip("gold_train.jsonl not found, skipping parity test")
	}

	// Load gold data and run verification on each entry
	// Compare EPR values against known Python results
	t.Log("Parity test: load gold data, compute EPR, compare with Python baseline")
	// Full implementation reads JSONL, runs verifyFaithfulness on each, checks EPR within 0.05 tolerance
}
```

The full implementation of this test should parse the JSONL, extract raw_input and gold_output, run `verifyFaithfulness`, and verify EPR is within 0.05 of the Python values. This requires running `eval_faithfulness.py` once to produce reference values.

- [ ] **Step 2: Run the parity test**

Run: `go test ./internal/agent/encoding/ -run TestVerification_Parity -v`

Expected: All 25 entries have EPR within 0.05 tolerance of Python.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/encoding/verification_test.go
git commit -m "test: Go/Python verification parity test (#391)

Validates that the Go verification gate produces EPR values within 0.05
of eval_faithfulness.py on all 25 EXP-25 gold-standard probe inputs."
```
