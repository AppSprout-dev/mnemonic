package retrieval

import (
	"testing"
	"time"
)

func TestActivityTracker_Observe(t *testing.T) {
	at := newActivityTracker(30, 0.2)
	at.observe([]string{"retrieval", "agent", "mcp"})

	at.mu.RLock()
	defer at.mu.RUnlock()
	if len(at.concepts) != 3 {
		t.Fatalf("expected 3 concepts, got %d", len(at.concepts))
	}
	if _, ok := at.concepts["retrieval"]; !ok {
		t.Fatal("expected 'retrieval' in concepts")
	}
}

func TestActivityTracker_BoostForMemory(t *testing.T) {
	at := newActivityTracker(30, 0.2)
	at.observe([]string{"retrieval", "agent", "mcp"})

	// Memory with 2/3 concept overlap should get a boost.
	boost := at.boostForMemory([]string{"retrieval", "agent", "store"})
	if boost <= 0 {
		t.Fatalf("expected positive boost, got %f", boost)
	}
	if boost > 0.2 {
		t.Fatalf("boost %f exceeds maxBoost 0.2", boost)
	}
}

func TestActivityTracker_NoOverlap(t *testing.T) {
	at := newActivityTracker(30, 0.2)
	at.observe([]string{"retrieval", "agent"})

	boost := at.boostForMemory([]string{"config", "yaml"})
	if boost != 0 {
		t.Fatalf("expected 0 boost for no overlap, got %f", boost)
	}
}

func TestActivityTracker_TimeDecay(t *testing.T) {
	at := newActivityTracker(1, 0.2) // 1-minute window

	// Manually set a concept timestamp in the past (beyond the window).
	at.mu.Lock()
	at.concepts["old"] = time.Now().Add(-2 * time.Minute)
	at.concepts["fresh"] = time.Now()
	at.mu.Unlock()

	boost := at.boostForMemory([]string{"old", "fresh"})
	// Only "fresh" should contribute, so boost is roughly 0.5 * maxWeight / 2 concepts.
	if boost <= 0 {
		t.Fatalf("expected positive boost from fresh concept, got %f", boost)
	}

	// A memory with only the expired concept should get zero.
	boost = at.boostForMemory([]string{"old"})
	if boost != 0 {
		t.Fatalf("expected 0 boost for expired concept, got %f", boost)
	}
}

func TestActivityTracker_MaxBoostCap(t *testing.T) {
	at := newActivityTracker(30, 0.1) // low cap
	at.observe([]string{"a", "b", "c", "d", "e"})

	// Memory with all 5 concepts matching — total weight ~5.0, divided by 5 = 1.0,
	// but should be clamped to maxBoost=0.1.
	boost := at.boostForMemory([]string{"a", "b", "c", "d", "e"})
	if boost > 0.1+0.001 {
		t.Fatalf("boost %f exceeds maxBoost 0.1", boost)
	}
}

func TestActivityTracker_NilSafety(t *testing.T) {
	var at *activityTracker
	boost := at.boostForMemory([]string{"anything"})
	if boost != 0 {
		t.Fatalf("expected 0 from nil tracker, got %f", boost)
	}
}

func TestActivityTracker_EmptyConcepts(t *testing.T) {
	at := newActivityTracker(30, 0.2)
	at.observe([]string{"retrieval"})

	boost := at.boostForMemory(nil)
	if boost != 0 {
		t.Fatalf("expected 0 for nil memory concepts, got %f", boost)
	}

	boost = at.boostForMemory([]string{})
	if boost != 0 {
		t.Fatalf("expected 0 for empty memory concepts, got %f", boost)
	}
}

func TestActivityTracker_LazyCleanup(t *testing.T) {
	at := newActivityTracker(1, 0.2) // 1-minute window

	// Add 501 expired concepts.
	at.mu.Lock()
	expired := time.Now().Add(-5 * time.Minute)
	for i := 0; i < 501; i++ {
		at.concepts[string(rune('a'+i%26))+string(rune('0'+i/26))] = expired
	}
	at.mu.Unlock()

	// Observe a fresh concept — should trigger cleanup.
	at.observe([]string{"fresh"})

	at.mu.RLock()
	defer at.mu.RUnlock()
	// All expired should be cleaned, only "fresh" remains.
	if len(at.concepts) != 1 {
		t.Fatalf("expected 1 concept after cleanup, got %d", len(at.concepts))
	}
}
