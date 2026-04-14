//go:build sqlite_fts5

package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/store"
)

// writeMemoryForExperience creates the prerequisite memory + raw_memory rows
// that the experience_buffer FK requires.
func writeMemoryForExperience(t *testing.T, s *SQLiteStore, id string) {
	t.Helper()
	rawID := "raw-" + id
	writeRawForMemory(t, s, rawID)
	mem := store.Memory{
		ID:        "mem-" + id,
		RawID:     rawID,
		Summary:   "test memory " + id,
		CreatedAt: time.Now(),
	}
	if err := s.WriteMemory(context.Background(), mem); err != nil {
		t.Fatalf("writing prerequisite memory %s: %v", id, err)
	}
}

func TestListNeedsImprovement(t *testing.T) {
	s := createTestStore(t)
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	ids := []string{"a", "b", "c", "d"}
	cats := []string{"gold", "needs_improvement", "needs_improvement", "ambiguous"}

	for i, id := range ids {
		writeMemoryForExperience(t, s, id)
		entry := store.ExperienceEntry{
			ID:          "entry-" + id,
			RawID:       "raw-" + id,
			MemoryID:    "mem-" + id,
			EncodingEPR: float64(i) * 0.2,
			Category:    cats[i],
		}
		if err := s.WriteExperienceEntry(ctx, entry); err != nil {
			t.Fatalf("writing entry %s: %v", id, err)
		}
	}

	entries, err := s.ListNeedsImprovement(ctx, 10)
	if err != nil {
		t.Fatalf("ListNeedsImprovement: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 needs_improvement entries, got %d", len(entries))
	}
	if entries[0].EncodingEPR > entries[1].EncodingEPR {
		t.Errorf("expected ascending EPR order, got %.2f then %.2f", entries[0].EncodingEPR, entries[1].EncodingEPR)
	}
}

func TestListNeedsImprovement_ExcludesCorrected(t *testing.T) {
	s := createTestStore(t)
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	for _, id := range []string{"a", "b"} {
		writeMemoryForExperience(t, s, id)
		entry := store.ExperienceEntry{
			ID:       "entry-" + id,
			RawID:    "raw-" + id,
			MemoryID: "mem-" + id,
			Category: "needs_improvement",
		}
		if err := s.WriteExperienceEntry(ctx, entry); err != nil {
			t.Fatalf("writing entry %s: %v", id, err)
		}
	}

	if err := s.UpdateExperienceCorrectedOutput(ctx, "entry-a", `{"summary":"corrected"}`, 0.95, 1.0, "gemini"); err != nil {
		t.Fatalf("UpdateExperienceCorrectedOutput: %v", err)
	}

	entries, err := s.ListNeedsImprovement(ctx, 10)
	if err != nil {
		t.Fatalf("ListNeedsImprovement: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 uncorrected entry, got %d", len(entries))
	}
	if entries[0].ID != "entry-b" {
		t.Errorf("expected entry-b, got %s", entries[0].ID)
	}
}

func TestUpdateExperienceCorrectedOutput(t *testing.T) {
	s := createTestStore(t)
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	writeMemoryForExperience(t, s, "1")
	entry := store.ExperienceEntry{
		ID:       "entry-1",
		RawID:    "raw-1",
		MemoryID: "mem-1",
		Category: "needs_improvement",
	}
	if err := s.WriteExperienceEntry(ctx, entry); err != nil {
		t.Fatalf("writing entry: %v", err)
	}

	if err := s.UpdateExperienceCorrectedOutput(ctx, "entry-1", `{"summary":"better"}`, 0.92, 1.0, "gemini"); err != nil {
		t.Fatalf("UpdateExperienceCorrectedOutput: %v", err)
	}

	var output string
	var epr float64
	var source string
	err := s.db.QueryRow(`SELECT corrected_output, corrected_epr, correction_source FROM experience_buffer WHERE id = ?`, "entry-1").
		Scan(&output, &epr, &source)
	if err != nil {
		t.Fatalf("querying corrected output: %v", err)
	}
	if output != `{"summary":"better"}` {
		t.Errorf("expected corrected output, got %s", output)
	}
	if epr != 0.92 {
		t.Errorf("expected corrected EPR 0.92, got %.2f", epr)
	}
	if source != "gemini" {
		t.Errorf("expected source 'gemini', got %s", source)
	}
}

func TestCurriculumRunLifecycle(t *testing.T) {
	s := createTestStore(t)
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	// No runs yet — should return zero time
	lastRun, err := s.GetLastCurriculumRunTime(ctx)
	if err != nil {
		t.Fatalf("GetLastCurriculumRunTime: %v", err)
	}
	if !lastRun.IsZero() {
		t.Errorf("expected zero time for no runs, got %v", lastRun)
	}

	// Write a completed run
	now := time.Now().Truncate(time.Second)
	run := store.CurriculumRun{
		ID:                   "run-1",
		StartedAt:            now,
		CompletedAt:          &now,
		CorrectionsAttempted: 10,
		CorrectionsPassed:    7,
		CorrectionsFailed:    3,
		Status:               "completed",
	}
	if err := s.WriteCurriculumRun(ctx, run); err != nil {
		t.Fatalf("WriteCurriculumRun: %v", err)
	}

	lastRun, err = s.GetLastCurriculumRunTime(ctx)
	if err != nil {
		t.Fatalf("GetLastCurriculumRunTime after write: %v", err)
	}
	if lastRun.Before(now.Add(-time.Second)) || lastRun.After(now.Add(time.Second)) {
		t.Errorf("expected last run near %v, got %v", now, lastRun)
	}

	// Update the run
	later := now.Add(time.Minute)
	run.CompletedAt = &later
	run.CorrectionsPassed = 8
	if err := s.UpdateCurriculumRun(ctx, run); err != nil {
		t.Fatalf("UpdateCurriculumRun: %v", err)
	}

	// Verify update took
	var passed int
	err = s.db.QueryRow(`SELECT corrections_passed FROM curriculum_runs WHERE id = ?`, "run-1").Scan(&passed)
	if err != nil {
		t.Fatalf("querying updated run: %v", err)
	}
	if passed != 8 {
		t.Errorf("expected corrections_passed=8, got %d", passed)
	}
}

func TestListNeedsImprovement_RespectsLimit(t *testing.T) {
	s := createTestStore(t)
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("%d", i)
		writeMemoryForExperience(t, s, id)
		entry := store.ExperienceEntry{
			ID:       "entry-" + id,
			RawID:    "raw-" + id,
			MemoryID: "mem-" + id,
			Category: "needs_improvement",
		}
		if err := s.WriteExperienceEntry(ctx, entry); err != nil {
			t.Fatalf("writing entry %d: %v", i, err)
		}
	}

	entries, err := s.ListNeedsImprovement(ctx, 3)
	if err != nil {
		t.Fatalf("ListNeedsImprovement: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected limit of 3, got %d", len(entries))
	}
}
