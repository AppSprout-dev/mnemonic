package abstraction

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/appsprout-dev/mnemonic/internal/store/storetest"
)

// silentLogger returns a logger that discards output. Used so tests do not
// spam stderr with the INFO-level dedup logs from findSimilarAbstraction.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestFindSimilarAbstraction_ConceptGateRejectsAttractor verifies that an
// existing abstraction whose embedding is nearly identical to the new one
// but whose concepts do not overlap is NOT returned as a duplicate — the
// attractor behavior that caused consolidation patterns to be absorbed into
// one dominant pattern before PRs #412/#414. Same fix pattern applied here.
func TestFindSimilarAbstraction_ConceptGateRejectsAttractor(t *testing.T) {
	attractor := store.Abstraction{
		ID:        "attractor",
		Title:     "Building a Self-Contained LLM Architecture",
		State:     "active",
		Embedding: []float32{1, 0, 0, 0},
		Concepts:  []string{"llm", "architecture", "workflow"},
	}
	existing := []store.Abstraction{attractor}

	// New abstraction: same embedding, zero shared concepts.
	match := findSimilarAbstraction(existing,
		[]string{"crispr-lm", "splice", "api"},
		[]float32{1, 0, 0, 0},
		"Splice Tensor API for CRISPR-LM",
		0.85, 2, silentLogger())

	if match != nil {
		t.Errorf("expected concept gate to reject attractor (no shared concepts), got match=%s", match.ID)
	}
}

// TestFindSimilarAbstraction_ConceptGateAcceptsGenuineDup verifies that when
// both similarity AND concept overlap hold, the function returns the match.
func TestFindSimilarAbstraction_ConceptGateAcceptsGenuineDup(t *testing.T) {
	original := store.Abstraction{
		ID:        "real-dup",
		Title:     "Defensive Nil Guarding in Go Event Loops",
		State:     "active",
		Embedding: []float32{0.9, 0.1, 0, 0},
		Concepts:  []string{"go", "nil-guard", "event-bus"},
	}
	existing := []store.Abstraction{original}

	match := findSimilarAbstraction(existing,
		[]string{"go", "nil-guard", "event-bus", "panic"},
		[]float32{0.92, 0.08, 0, 0}, // cosine ~= 1.0
		"Event-Bus Nil Guards Prevent Go Panics",
		0.85, 2, silentLogger())

	if match == nil {
		t.Fatal("expected concept-matched duplicate, got nil")
	}
	if match.ID != "real-dup" {
		t.Errorf("expected real-dup match, got %s", match.ID)
	}
}

// TestFindSimilarAbstraction_ArchivedIsSkipped verifies that abstractions in
// state other than active/fading are skipped regardless of similarity. This
// is existing behavior — the test documents it.
func TestFindSimilarAbstraction_ArchivedIsSkipped(t *testing.T) {
	archived := store.Abstraction{
		ID:        "archived",
		Title:     "Old Principle",
		State:     "archived",
		Embedding: []float32{1, 0, 0, 0},
		Concepts:  []string{"go", "nil-guard"},
	}
	existing := []store.Abstraction{archived}

	match := findSimilarAbstraction(existing,
		[]string{"go", "nil-guard"},
		[]float32{1, 0, 0, 0},
		"Old Principle",
		0.85, 2, silentLogger())

	if match != nil {
		t.Errorf("expected archived abstraction to be skipped, got match=%s", match.ID)
	}
}

// TestFindSimilarAbstraction_TitleMatchStillNeedsConcepts is the sharp-edges
// test: the title-Jaccard fallback path (titleSim >= 0.6) must also pass the
// concept gate. Without this, a principle with a near-identical title but
// different topic could still be merged. Previously the title fallback was an
// escape hatch from any concept discipline.
func TestFindSimilarAbstraction_TitleMatchStillNeedsConcepts(t *testing.T) {
	existing := []store.Abstraction{{
		ID:        "title-clone",
		Title:     "Recurring Optimization Workflow",
		State:     "active",
		Embedding: []float32{0.1, 0.9, 0, 0}, // low embedding similarity
		Concepts:  []string{"quant", "gpu", "benchmark"},
	}}

	// Near-identical title (Jaccard > 0.6) but zero shared concepts.
	match := findSimilarAbstraction(existing,
		[]string{"auth", "session", "cookies"},
		[]float32{0.9, 0.1, 0, 0},
		"Recurring Optimization Workflow",
		0.85, 2, silentLogger())

	if match != nil {
		t.Errorf("expected concept gate to reject title-only match, got %s", match.ID)
	}
}

// groundingMockStore fakes ListAbstractions / GetMemory / GetPattern /
// UpdateAbstraction so we can exercise verifyGrounding without a real
// database. Memories are returned as "archived" by default (grounding ratio
// drops to 0). Memories whose ID matches activeMemoryID are returned as
// "active" — letting a test dial groundingRatio via the source-memory list
// size. The updates map captures the resulting abstraction state after the
// cycle.
type groundingMockStore struct {
	storetest.MockStore
	abstractions   []store.Abstraction
	activeMemoryID string
	updates        map[string]store.Abstraction
}

func (m *groundingMockStore) ListAbstractions(_ context.Context, level, _ int) ([]store.Abstraction, error) {
	var out []store.Abstraction
	for _, a := range m.abstractions {
		if a.Level == level {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *groundingMockStore) GetMemory(_ context.Context, id string) (store.Memory, error) {
	if m.activeMemoryID != "" && id == m.activeMemoryID {
		return store.Memory{State: "active"}, nil
	}
	return store.Memory{State: "archived"}, nil
}

func (m *groundingMockStore) GetPattern(_ context.Context, _ string) (store.Pattern, error) {
	return store.Pattern{State: "archived"}, nil
}

// GetAbstraction exposes the mock's stored abstractions by ID so level-3 axioms
// (whose SourcePatternIDs point at principles) can have their grounding computed
// against the principles' live state.
func (m *groundingMockStore) GetAbstraction(_ context.Context, id string) (store.Abstraction, error) {
	for _, a := range m.abstractions {
		if a.ID == id {
			return a, nil
		}
	}
	return store.Abstraction{}, store.ErrNotFound
}

func (m *groundingMockStore) UpdateAbstraction(_ context.Context, a store.Abstraction) error {
	if m.updates == nil {
		m.updates = map[string]store.Abstraction{}
	}
	m.updates[a.ID] = a
	return nil
}

// standardStreakConfig is the archival-relevant config used by the streak tests.
func standardStreakConfig() AbstractionConfig {
	return AbstractionConfig{
		ConfidenceModerateDecay:    0.9,
		ConfidenceSignificantDecay: 0.7,
		ConfidenceSevereDecay:      0.5,
		GroundingFloor:             0.5,
		ArchiveDecayConfidence:     0.2,
		ArchiveDemotionStreak:      3,
	}
}

// TestVerifyGrounding_IncrementsStreakOnDemote verifies that a cycle where
// grounding is below the healthy threshold bumps DemotionStreak by 1 without
// (yet) archiving, when the streak is still below the archive threshold.
func TestVerifyGrounding_IncrementsStreakOnDemote(t *testing.T) {
	a := store.Abstraction{
		ID:              "streak-1",
		Level:           2,
		State:           "active",
		Confidence:      0.4,
		DemotionStreak:  0,
		CreatedAt:       time.Now().Add(-30 * 24 * time.Hour),
		SourceMemoryIDs: []string{"mem-a", "mem-b"}, // all archived → ratio 0 (severe)
	}

	ms := &groundingMockStore{abstractions: []store.Abstraction{a}}
	agent := NewAbstractionAgent(ms, nil, standardStreakConfig(), silentLogger())

	report := &CycleReport{}
	if err := agent.verifyGrounding(context.Background(), report); err != nil {
		t.Fatalf("verifyGrounding failed: %v", err)
	}

	got, ok := ms.updates[a.ID]
	if !ok {
		t.Fatalf("expected abstraction update, got none")
	}
	if got.DemotionStreak != 1 {
		t.Errorf("expected DemotionStreak=1, got %d", got.DemotionStreak)
	}
	if got.State != "active" {
		t.Errorf("expected state=active (streak below threshold), got %s", got.State)
	}
	if report.AbstractionsArchived != 0 {
		t.Errorf("expected AbstractionsArchived=0, got %d", report.AbstractionsArchived)
	}
}

// TestVerifyGrounding_ResetsStreakOnHealthy verifies that when an abstraction
// is healthy (ratio >= 0.5), a previously accumulated streak is reset to 0 and
// the abstraction is left in state=active. The reset must be persisted.
func TestVerifyGrounding_ResetsStreakOnHealthy(t *testing.T) {
	a := store.Abstraction{
		ID:              "streak-reset",
		Level:           2,
		State:           "active",
		Confidence:      0.4,
		DemotionStreak:  5, // previously accumulated
		CreatedAt:       time.Now().Add(-30 * 24 * time.Hour),
		SourceMemoryIDs: []string{"mem-active"}, // 1/1 active → ratio 1.0 (healthy)
	}

	ms := &groundingMockStore{
		abstractions:   []store.Abstraction{a},
		activeMemoryID: "mem-active",
	}
	agent := NewAbstractionAgent(ms, nil, standardStreakConfig(), silentLogger())

	report := &CycleReport{}
	if err := agent.verifyGrounding(context.Background(), report); err != nil {
		t.Fatalf("verifyGrounding failed: %v", err)
	}

	got, ok := ms.updates[a.ID]
	if !ok {
		t.Fatalf("expected abstraction update (streak reset must persist), got none")
	}
	if got.DemotionStreak != 0 {
		t.Errorf("expected DemotionStreak=0 after healthy cycle, got %d", got.DemotionStreak)
	}
	if got.State != "active" {
		t.Errorf("expected state=active, got %s", got.State)
	}
}

// TestVerifyGrounding_ArchivesWhenStreakAndConfidenceThresholdsMet verifies
// the escape hatch fires when an abstraction is old, below the confidence
// threshold, and at/past the streak threshold. This is the fix for the stuck
// "abstractions_demoted=8" loop.
func TestVerifyGrounding_ArchivesWhenStreakAndConfidenceThresholdsMet(t *testing.T) {
	a := store.Abstraction{
		ID:              "ready-to-archive",
		Level:           2,
		State:           "active",
		Confidence:      0.25, // 0.25 * 0.7 = 0.175 < 0.2 after this cycle's decay
		DemotionStreak:  2,    // +1 this cycle = 3, matches ArchiveDemotionStreak
		CreatedAt:       time.Now().Add(-30 * 24 * time.Hour),
		SourceMemoryIDs: []string{"mem-a", "mem-b", "mem-c", "mem-d", "mem-e"}, // 1/5 active → 0.2 ratio (significant)
	}

	ms := &groundingMockStore{
		abstractions:   []store.Abstraction{a},
		activeMemoryID: "mem-a",
	}
	agent := NewAbstractionAgent(ms, nil, standardStreakConfig(), silentLogger())

	report := &CycleReport{}
	if err := agent.verifyGrounding(context.Background(), report); err != nil {
		t.Fatalf("verifyGrounding failed: %v", err)
	}

	got, ok := ms.updates[a.ID]
	if !ok {
		t.Fatalf("expected abstraction update, got none")
	}
	if got.State != "archived" {
		t.Errorf("expected state=archived (streak=%d, conf=%v), got %s", got.DemotionStreak, got.Confidence, got.State)
	}
	if report.AbstractionsArchived != 1 {
		t.Errorf("expected AbstractionsArchived=1, got %d", report.AbstractionsArchived)
	}
	if report.AbstractionsDemoted != 0 {
		t.Errorf("expected AbstractionsDemoted=0 (archive replaces demote), got %d", report.AbstractionsDemoted)
	}
}

// TestVerifyGrounding_YoungAbstractionNotArchived verifies the isYoung
// grace period (< 7 days): even with a high streak and low confidence, a
// young abstraction must not be archived. Young abstractions get a
// GroundingFloor on confidence.
func TestVerifyGrounding_YoungAbstractionNotArchived(t *testing.T) {
	young := store.Abstraction{
		ID:              "young",
		Level:           2,
		State:           "active",
		Confidence:      0.15,
		DemotionStreak:  99,                                  // way past threshold
		CreatedAt:       time.Now().Add(-2 * 24 * time.Hour), // 2 days — isYoung
		SourceMemoryIDs: []string{"mem-a", "mem-b", "mem-c"},
	}

	ms := &groundingMockStore{abstractions: []store.Abstraction{young}}
	agent := NewAbstractionAgent(ms, nil, standardStreakConfig(), silentLogger())

	report := &CycleReport{}
	if err := agent.verifyGrounding(context.Background(), report); err != nil {
		t.Fatalf("verifyGrounding failed: %v", err)
	}

	got, ok := ms.updates[young.ID]
	if !ok {
		t.Fatalf("expected abstraction update, got none")
	}
	if got.State == "archived" {
		t.Errorf("expected young abstraction NOT to be archived, got state=%s", got.State)
	}
	if report.AbstractionsArchived != 0 {
		t.Errorf("expected AbstractionsArchived=0, got %d", report.AbstractionsArchived)
	}
}

// TestVerifyGrounding_AxiomResolvesSourcePrinciples verifies the fix for the
// overnight bug where level-3 axioms always computed grounding_ratio=0. The
// axiom's SourcePatternIDs field holds principle IDs (abs-*), not pattern IDs,
// so routing them through GetPattern (which searches the patterns table) always
// failed and the axiom was stuck in severe-decay forever. Level>=3 must route
// through GetAbstraction instead.
func TestVerifyGrounding_AxiomResolvesSourcePrinciples(t *testing.T) {
	principleA := store.Abstraction{ID: "abs-A", Level: 2, State: "active"}
	principleB := store.Abstraction{ID: "abs-B", Level: 2, State: "active"}
	principleC := store.Abstraction{ID: "abs-C", Level: 2, State: "active"}
	axiom := store.Abstraction{
		ID:               "axm-live",
		Level:            3,
		State:            "active",
		Confidence:       0.8,
		DemotionStreak:   2,
		CreatedAt:        time.Now().Add(-30 * 24 * time.Hour),
		SourcePatternIDs: []string{"abs-A", "abs-B", "abs-C"},
	}

	ms := &groundingMockStore{abstractions: []store.Abstraction{principleA, principleB, principleC, axiom}}
	agent := NewAbstractionAgent(ms, nil, standardStreakConfig(), silentLogger())

	report := &CycleReport{}
	if err := agent.verifyGrounding(context.Background(), report); err != nil {
		t.Fatalf("verifyGrounding failed: %v", err)
	}

	got, ok := ms.updates[axiom.ID]
	if !ok {
		t.Fatalf("expected axiom update, got none")
	}
	if got.DemotionStreak != 0 {
		t.Errorf("axiom with 3/3 active principles should reset streak, got DemotionStreak=%d", got.DemotionStreak)
	}
}

// TestVerifyGrounding_AxiomDecaysWhenPrinciplesArchived verifies the axiom
// fix in the inverse direction: when its source principles are archived, the
// axiom actually decays instead of silently resetting every cycle.
func TestVerifyGrounding_AxiomDecaysWhenPrinciplesArchived(t *testing.T) {
	archivedA := store.Abstraction{ID: "abs-A", Level: 2, State: "archived"}
	archivedB := store.Abstraction{ID: "abs-B", Level: 2, State: "archived"}
	axiom := store.Abstraction{
		ID:               "axm-dead",
		Level:            3,
		State:            "active",
		Confidence:       0.3,
		DemotionStreak:   0,
		CreatedAt:        time.Now().Add(-30 * 24 * time.Hour),
		SourcePatternIDs: []string{"abs-A", "abs-B"},
	}

	ms := &groundingMockStore{abstractions: []store.Abstraction{archivedA, archivedB, axiom}}
	agent := NewAbstractionAgent(ms, nil, standardStreakConfig(), silentLogger())

	report := &CycleReport{}
	if err := agent.verifyGrounding(context.Background(), report); err != nil {
		t.Fatalf("verifyGrounding failed: %v", err)
	}

	got, ok := ms.updates[axiom.ID]
	if !ok {
		t.Fatalf("expected axiom update, got none")
	}
	if got.DemotionStreak != 1 {
		t.Errorf("axiom with 0/2 active principles should demote, got DemotionStreak=%d", got.DemotionStreak)
	}
}

// TestFindSimilarAbstraction_StrongTitleMatchBypassesConceptGate verifies the
// title+embedding short-circuit: when titleSim and embSim are both very high,
// the concept gate is bypassed. LLM-extracted concepts drift run-to-run even
// for the same principle, which was creating duplicate principles with
// identical titles overnight.
func TestFindSimilarAbstraction_StrongTitleMatchBypassesConceptGate(t *testing.T) {
	existing := []store.Abstraction{{
		ID:        "strong-title",
		Title:     "Iterative LLM Architecture Development",
		State:     "active",
		Embedding: []float32{1, 0, 0, 0},
		Concepts:  []string{"llm", "architecture"},
	}}

	match := findSimilarAbstraction(existing,
		[]string{"retrieval", "orchestration"}, // zero concept overlap
		[]float32{0.99, 0.01, 0, 0},            // embSim >= 0.9
		"Iterative LLM Architecture Development",
		0.85, 2, silentLogger())

	if match == nil || match.ID != "strong-title" {
		t.Errorf("expected strong title+embedding match to bypass concept gate, got %+v", match)
	}
}

// patternMockStore is a minimal ListPatterns/ListAbstractions stub. The
// fingerprint-gating tests only need to control what the two list calls
// return; everything else falls through to MockStore's zero-value methods.
type patternMockStore struct {
	storetest.MockStore
	patterns     []store.Pattern
	abstractions []store.Abstraction
}

func (m *patternMockStore) ListPatterns(context.Context, string, int) ([]store.Pattern, error) {
	return m.patterns, nil
}

func (m *patternMockStore) ListAbstractions(_ context.Context, level, _ int) ([]store.Abstraction, error) {
	var out []store.Abstraction
	for _, a := range m.abstractions {
		if a.Level == level {
			out = append(out, a)
		}
	}
	return out, nil
}

// TestFingerprintPatterns_StableAndSensitive verifies fingerprintPatterns is
// deterministic under reordering and float-wobble-below-quantum, and changes
// when a pattern's strength crosses the 0.01 quantum or an ID shifts.
func TestFingerprintPatterns_StableAndSensitive(t *testing.T) {
	base := []store.Pattern{
		{ID: "p1", Strength: 0.80},
		{ID: "p2", Strength: 0.72},
		{ID: "p3", Strength: 0.65},
	}
	fpBase := fingerprintPatterns(base)

	reordered := []store.Pattern{base[2], base[0], base[1]}
	if got := fingerprintPatterns(reordered); got != fpBase {
		t.Errorf("fingerprint should be order-independent, got %x want %x", got, fpBase)
	}

	wobble := []store.Pattern{
		{ID: "p1", Strength: 0.803}, // still 0.80 at 2dp
		{ID: "p2", Strength: 0.724},
		{ID: "p3", Strength: 0.651},
	}
	if got := fingerprintPatterns(wobble); got != fpBase {
		t.Errorf("sub-quantum wobble must not change fingerprint, got %x want %x", got, fpBase)
	}

	strengthened := []store.Pattern{
		{ID: "p1", Strength: 0.82}, // crosses 0.01 quantum
		{ID: "p2", Strength: 0.72},
		{ID: "p3", Strength: 0.65},
	}
	if got := fingerprintPatterns(strengthened); got == fpBase {
		t.Errorf("strength change of 0.02 must change fingerprint, got %x == baseline", got)
	}

	added := append([]store.Pattern{}, base...)
	added = append(added, store.Pattern{ID: "p4", Strength: 0.70})
	if got := fingerprintPatterns(added); got == fpBase {
		t.Errorf("adding a pattern must change fingerprint, got %x == baseline", got)
	}
}

// TestSynthesizePrinciples_SkipsWhenSubstrateUnchanged verifies that a second
// call on an identical strong-pattern set short-circuits before clustering.
// Proxy for skipping: report.PrinciplesSkippedNoChange must be true on the
// second call and false on the first.
func TestSynthesizePrinciples_SkipsWhenSubstrateUnchanged(t *testing.T) {
	// Orthogonal embeddings across patterns → no cluster of 2+ forms →
	// cluster loop skips all entries → no LLM call needed (nil provider is fine).
	patterns := []store.Pattern{
		{ID: "p1", Strength: 0.9, State: "active", Embedding: []float32{1, 0, 0}},
		{ID: "p2", Strength: 0.9, State: "active", Embedding: []float32{0, 1, 0}},
		{ID: "p3", Strength: 0.9, State: "active", Embedding: []float32{0, 0, 1}},
	}
	ms := &patternMockStore{patterns: patterns}
	cfg := AbstractionConfig{MinStrength: 0.5, MaxLLMCalls: 0} // LLM budget 0 so no LLM call is attempted
	agent := NewAbstractionAgent(ms, nil, cfg, silentLogger())

	r1 := &CycleReport{}
	if err := agent.synthesizePrinciples(context.Background(), r1); err != nil {
		t.Fatalf("first cycle: %v", err)
	}
	if r1.PrinciplesSkippedNoChange {
		t.Errorf("first cycle should NOT be skipped (empty baseline fingerprint)")
	}
	if r1.PatternsEvaluated != 3 {
		t.Errorf("first cycle should evaluate 3 patterns, got %d", r1.PatternsEvaluated)
	}

	r2 := &CycleReport{}
	if err := agent.synthesizePrinciples(context.Background(), r2); err != nil {
		t.Fatalf("second cycle: %v", err)
	}
	if !r2.PrinciplesSkippedNoChange {
		t.Errorf("second cycle should be skipped (substrate unchanged)")
	}
}

// TestSynthesizePrinciples_GateOpensOnStrengthChange verifies that mutating a
// pattern's strength between calls invalidates the fingerprint and re-runs.
func TestSynthesizePrinciples_GateOpensOnStrengthChange(t *testing.T) {
	ms := &patternMockStore{patterns: []store.Pattern{
		{ID: "p1", Strength: 0.9, State: "active", Embedding: []float32{1, 0, 0}},
		{ID: "p2", Strength: 0.9, State: "active", Embedding: []float32{0, 1, 0}},
	}}
	cfg := AbstractionConfig{MinStrength: 0.5, MaxLLMCalls: 0}
	agent := NewAbstractionAgent(ms, nil, cfg, silentLogger())

	r1 := &CycleReport{}
	_ = agent.synthesizePrinciples(context.Background(), r1)

	ms.patterns[0].Strength = 0.75 // mutate — fingerprint must change

	r2 := &CycleReport{}
	if err := agent.synthesizePrinciples(context.Background(), r2); err != nil {
		t.Fatalf("second cycle: %v", err)
	}
	if r2.PrinciplesSkippedNoChange {
		t.Errorf("second cycle should NOT be skipped after strength change")
	}
}

// TestSynthesizeAxioms_SkipsWhenSubstrateUnchanged is the level-2 analogue.
func TestSynthesizeAxioms_SkipsWhenSubstrateUnchanged(t *testing.T) {
	principles := []store.Abstraction{
		{ID: "a1", Level: 2, State: "active", Confidence: 0.8, Embedding: []float32{1, 0, 0}},
		{ID: "a2", Level: 2, State: "active", Confidence: 0.8, Embedding: []float32{0, 1, 0}},
	}
	ms := &patternMockStore{abstractions: principles}
	cfg := AbstractionConfig{MaxLLMCalls: 0}
	agent := NewAbstractionAgent(ms, nil, cfg, silentLogger())

	r1 := &CycleReport{}
	_ = agent.synthesizeAxioms(context.Background(), r1)
	if r1.AxiomsSkippedNoChange {
		t.Errorf("first cycle should NOT be skipped")
	}

	r2 := &CycleReport{}
	_ = agent.synthesizeAxioms(context.Background(), r2)
	if !r2.AxiomsSkippedNoChange {
		t.Errorf("second cycle should be skipped (principle substrate unchanged)")
	}
}
