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
	// Store SQL NULL for empty/nil flags, JSON array for non-empty
	var flagsVal any
	if len(flags) > 0 {
		flagsJSON, err := json.Marshal(flags)
		if err != nil {
			return fmt.Errorf("marshaling flags: %w", err)
		}
		flagsVal = string(flagsJSON)
	}

	_, err := s.db.ExecContext(ctx,
		`UPDATE memories SET encoding_epr = ?, encoding_fr = ?, encoding_flags = ? WHERE id = ?`,
		epr, fr, flagsVal, memoryID,
	)
	if err != nil {
		return fmt.Errorf("writing verification result for %s: %w", memoryID, err)
	}
	return nil
}

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
	defer func() { _ = rows.Close() }()

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
	defer func() { _ = rows.Close() }()

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
		    COALESCE(SUM(CASE WHEN encoding_flags IS NOT NULL AND encoding_flags NOT IN ('[]', 'null') AND encoding_flags != '' THEN 1.0 ELSE 0.0 END) / MAX(COUNT(*), 1), 0),
		    COUNT(*)
		 FROM (SELECT encoding_epr, encoding_flags FROM memories WHERE encoding_epr IS NOT NULL ORDER BY created_at DESC LIMIT ?)`,
		windowSize,
	)
	if err := row.Scan(&w.MeanEPR, &w.TEDRate, &w.FlaggedRate, &w.SampleCount); err != nil {
		return w, fmt.Errorf("getting encoding quality window: %w", err)
	}
	return w, nil
}

// --- Phase B: Curriculum generation ---

func (s *SQLiteStore) UpdateExperienceCorrectedOutput(ctx context.Context, entryID string, output string, epr float64, fr float64, source string) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE experience_buffer
		 SET corrected_output = ?, corrected_epr = ?, corrected_fr = ?,
		     correction_source = ?, corrected_at = ?, updated_at = ?
		 WHERE id = ?`,
		output, epr, fr, source, now, now, entryID,
	)
	if err != nil {
		return fmt.Errorf("updating corrected output for entry %s: %w", entryID, err)
	}
	return nil
}

func (s *SQLiteStore) ListNeedsImprovement(ctx context.Context, limit int) ([]store.ExperienceEntry, error) {
	// Return needs_improvement entries that haven't been corrected yet
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, raw_id, memory_id, encoding_epr, encoding_fr, encoding_flags,
		        recall_score, recall_count, category, used_in_training, created_at, updated_at
		 FROM experience_buffer
		 WHERE category = 'needs_improvement' AND corrected_output IS NULL
		 ORDER BY encoding_epr ASC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing needs_improvement entries: %w", err)
	}
	defer func() { _ = rows.Close() }()

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

func (s *SQLiteStore) WriteCurriculumRun(ctx context.Context, run store.CurriculumRun) error {
	if run.ID == "" {
		run.ID = uuid.New().String()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO curriculum_runs (id, started_at, completed_at, corrections_attempted, corrections_passed,
		     corrections_failed, entries_reclassified, training_batch_path, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.StartedAt, run.CompletedAt,
		run.CorrectionsAttempted, run.CorrectionsPassed, run.CorrectionsFailed,
		run.EntriesReclassified, run.TrainingBatchPath, run.Status, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("writing curriculum run %s: %w", run.ID, err)
	}
	return nil
}

func (s *SQLiteStore) UpdateCurriculumRun(ctx context.Context, run store.CurriculumRun) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE curriculum_runs
		 SET completed_at = ?, corrections_attempted = ?, corrections_passed = ?,
		     corrections_failed = ?, entries_reclassified = ?, training_batch_path = ?, status = ?
		 WHERE id = ?`,
		run.CompletedAt, run.CorrectionsAttempted, run.CorrectionsPassed,
		run.CorrectionsFailed, run.EntriesReclassified, run.TrainingBatchPath, run.Status, run.ID,
	)
	if err != nil {
		return fmt.Errorf("updating curriculum run %s: %w", run.ID, err)
	}
	return nil
}

func (s *SQLiteStore) GetLastCurriculumRunTime(ctx context.Context) (time.Time, error) {
	var raw *string
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(started_at) FROM curriculum_runs WHERE status = 'completed'`,
	).Scan(&raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("getting last curriculum run time: %w", err)
	}
	if raw == nil || *raw == "" {
		return time.Time{}, nil
	}
	// Try multiple time formats — SQLite + Go's time.Time.String() output
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05-07:00",
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05 -0700 MST",
	}
	var t time.Time
	var parseErr error
	for _, f := range formats {
		t, parseErr = time.Parse(f, *raw)
		if parseErr == nil {
			break
		}
	}
	if parseErr != nil {
		return time.Time{}, fmt.Errorf("parsing curriculum run time %q: %w", *raw, parseErr)
	}
	return t, nil
}

func (s *SQLiteStore) ListRecentEncodingQuality(ctx context.Context, limit int) ([]store.EncodingQualityEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT m.id, COALESCE(m.summary, ''), COALESCE(m.source, ''),
		        COALESCE(m.encoding_epr, 0), COALESCE(m.encoding_fr, 0),
		        COALESCE(m.encoding_flags, ''), COALESCE(m.salience, 0), m.created_at
		 FROM memories m
		 WHERE m.encoding_epr IS NOT NULL
		 ORDER BY m.created_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing recent encoding quality: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []store.EncodingQualityEntry
	for rows.Next() {
		var e store.EncodingQualityEntry
		var flagsStr string
		if err := rows.Scan(&e.MemoryID, &e.Summary, &e.Source,
			&e.EPR, &e.FR, &flagsStr, &e.Salience, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning encoding quality entry: %w", err)
		}
		if flagsStr != "" && flagsStr != "null" {
			_ = json.Unmarshal([]byte(flagsStr), &e.Flags)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
