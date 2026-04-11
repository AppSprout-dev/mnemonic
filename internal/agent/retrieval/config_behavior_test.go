package retrieval

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/llm"
	"github.com/appsprout-dev/mnemonic/internal/store"
)

// ---------------------------------------------------------------------------
// Config Behavioral Tests — verify each config param affects behavior
// ---------------------------------------------------------------------------

func TestConfigMaxResultsLimitsOutput(t *testing.T) {
	now := time.Now()

	// Return 10 FTS results
	memories := make([]store.Memory, 10)
	for i := range memories {
		memories[i] = store.Memory{
			ID:           fmt.Sprintf("m%d", i),
			Summary:      fmt.Sprintf("memory %d", i),
			Salience:     0.9 - float32(i)*0.05,
			LastAccessed: now,
		}
	}

	s := &mockStore{
		searchByFullTextFunc: func(_ context.Context, _ string, _ int) ([]store.Memory, error) {
			return memories, nil
		},
		searchByEmbeddingFunc: func(_ context.Context, _ []float32, _ int) ([]store.RetrievalResult, error) {
			return nil, nil
		},
		getAssociationsFunc: func(_ context.Context, _ string) ([]store.Association, error) {
			return nil, nil
		},
		getMemoryFunc: func(_ context.Context, id string) (store.Memory, error) {
			for _, m := range memories {
				if m.ID == id {
					return m, nil
				}
			}
			return store.Memory{ID: id, Salience: 0.5, LastAccessed: now}, nil
		},
	}

	tests := []struct {
		name       string
		maxResults int
		wantAtMost int
	}{
		{"max_results=1", 1, 1},
		{"max_results=3", 3, 3},
		{"max_results=5", 5, 5},
		{"max_results=10", 10, 10},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.MaxResults = tc.maxResults
			agent := NewRetrievalAgent(s, &mockLLMProvider{}, cfg, testLogger(), nil)

			resp, err := agent.Query(context.Background(), QueryRequest{Query: "test"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(resp.Memories) > tc.wantAtMost {
				t.Errorf("config MaxResults=%d but got %d results", tc.maxResults, len(resp.Memories))
			}
		})
	}
}

func TestConfigMaxHopsControlsGraphDepth(t *testing.T) {
	// Chain: m1 → m2 → m3 → m4 (each hop via strong association)
	s := &mockStore{
		getAssociationsFunc: func(_ context.Context, memoryID string) ([]store.Association, error) {
			chains := map[string]string{"m1": "m2", "m2": "m3", "m3": "m4"}
			if target, ok := chains[memoryID]; ok {
				return []store.Association{
					{SourceID: memoryID, TargetID: target, Strength: 0.9, RelationType: "similar"},
				}, nil
			}
			return nil, nil
		},
	}

	tests := []struct {
		name     string
		maxHops  int
		wantIDs  []string
		dontWant []string
	}{
		{"0_hops_entry_only", 0, []string{"m1"}, []string{"m2", "m3", "m4"}},
		{"1_hop_reaches_m2", 1, []string{"m1", "m2"}, []string{"m3", "m4"}},
		{"2_hops_reaches_m3", 2, []string{"m1", "m2", "m3"}, []string{"m4"}},
		{"3_hops_reaches_m4", 3, []string{"m1", "m2", "m3", "m4"}, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := RetrievalConfig{
				MaxHops:             tc.maxHops,
				ActivationThreshold: 0.01,
				DecayFactor:         0.9, // high so activation survives multiple hops
				MaxResults:          10,
			}
			agent := NewRetrievalAgent(s, &mockLLMProvider{}, cfg, testLogger(), nil)

			entryPoints := map[string]float32{"m1": 1.0}
			result, _ := agent.spreadActivation(context.Background(), entryPoints)

			for _, id := range tc.wantIDs {
				if _, ok := result[id]; !ok {
					t.Errorf("expected %s to be activated with maxHops=%d", id, tc.maxHops)
				}
			}
			for _, id := range tc.dontWant {
				if _, ok := result[id]; ok {
					t.Errorf("expected %s NOT to be activated with maxHops=%d", id, tc.maxHops)
				}
			}
		})
	}
}

func TestConfigActivationThresholdPrunesWeak(t *testing.T) {
	// m1 has a weak association to m2 (strength 0.15)
	s := &mockStore{
		getAssociationsFunc: func(_ context.Context, memoryID string) ([]store.Association, error) {
			if memoryID == "m1" {
				return []store.Association{
					{SourceID: "m1", TargetID: "m2", Strength: 0.15, RelationType: "similar"},
				}, nil
			}
			return nil, nil
		},
	}

	tests := []struct {
		name      string
		threshold float32
		expectM2  bool
	}{
		// Propagated: 1.0 * 0.15 * 0.7^1 * 1.0 = 0.105
		{"threshold_0.1_propagates", 0.1, true},
		{"threshold_0.2_prunes", 0.2, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := RetrievalConfig{
				MaxHops:             1,
				ActivationThreshold: tc.threshold,
				DecayFactor:         0.7,
				MaxResults:          10,
			}
			agent := NewRetrievalAgent(s, &mockLLMProvider{}, cfg, testLogger(), nil)

			entryPoints := map[string]float32{"m1": 1.0}
			result, _ := agent.spreadActivation(context.Background(), entryPoints)

			_, hasM2 := result["m2"]
			if hasM2 != tc.expectM2 {
				t.Errorf("threshold=%.2f: expected m2 activated=%v, got %v", tc.threshold, tc.expectM2, hasM2)
			}
		})
	}
}

func TestConfigDecayFactorAffectsActivationMagnitude(t *testing.T) {
	// 2-hop chain: m1 → m2 → m3
	s := &mockStore{
		getAssociationsFunc: func(_ context.Context, memoryID string) ([]store.Association, error) {
			chains := map[string]string{"m1": "m2", "m2": "m3"}
			if target, ok := chains[memoryID]; ok {
				return []store.Association{
					{SourceID: memoryID, TargetID: target, Strength: 1.0, RelationType: "similar"},
				}, nil
			}
			return nil, nil
		},
	}

	tests := []struct {
		name        string
		decayFactor float32
		wantM2Min   float32
		wantM2Max   float32
	}{
		// m2: 1.0 * 1.0 * 0.5^1 * 1.0 = 0.5
		{"decay_0.5_fast", 0.5, 0.49, 0.51},
		// m2: 1.0 * 1.0 * 0.9^1 * 1.0 = 0.9
		{"decay_0.9_slow", 0.9, 0.89, 0.91},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := RetrievalConfig{
				MaxHops:             2,
				ActivationThreshold: 0.01,
				DecayFactor:         tc.decayFactor,
				MaxResults:          10,
			}
			agent := NewRetrievalAgent(s, &mockLLMProvider{}, cfg, testLogger(), nil)

			entryPoints := map[string]float32{"m1": 1.0}
			result, _ := agent.spreadActivation(context.Background(), entryPoints)

			m2 := result["m2"].activation
			if m2 < tc.wantM2Min || m2 > tc.wantM2Max {
				t.Errorf("decay=%.1f: expected m2 activation in [%.2f, %.2f], got %.4f",
					tc.decayFactor, tc.wantM2Min, tc.wantM2Max, m2)
			}
		})
	}
}

func TestConfigMergeAlphaWeightsFTSvsEmbedding(t *testing.T) {
	tests := []struct {
		name       string
		alpha      float32
		wantMinFTS bool // if true, FTS-dominated score should be lower bound
	}{
		{"alpha_0_fts_only", 0.0, true},
		{"alpha_1_embedding_only", 1.0, false},
	}

	fts := []store.Memory{
		{ID: "m1", Salience: 0.8}, // FTS score: 0.7*1.0 + 0.3*0.8 = 0.94 (rank 1)
	}
	emb := []store.RetrievalResult{
		{Memory: store.Memory{ID: "m1"}, Score: 0.3}, // embedding score: 0.3
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.MergeAlpha = tc.alpha
			cfg.DualHitBonus = 0 // isolate alpha effect
			agent := NewRetrievalAgent(&mockStore{}, &mockLLMProvider{}, cfg, testLogger(), nil)

			result := agent.mergeEntryPoints(fts, emb)

			score := result["m1"]
			// FTS score for rank 1 with salience 0.8: 0.7*1.0 + 0.3*0.8 = 0.94
			// alpha=0: score = 0*0.3 + 1*0.94 + 0 = 0.94 (FTS dominated)
			// alpha=1: score = 1*0.3 + 0*0.94 + 0 = 0.30 (embedding dominated)
			if tc.alpha == 0.0 {
				expected := float32(0.94)
				if abs32(score-expected) > 0.01 {
					t.Errorf("alpha=0: expected score ~%.2f (FTS dominated), got %.4f", expected, score)
				}
			} else {
				expected := float32(0.3)
				if abs32(score-expected) > 0.01 {
					t.Errorf("alpha=1: expected score ~%.2f (embedding dominated), got %.4f", expected, score)
				}
			}
		})
	}
}

func TestConfigDualHitBonusAddsToScore(t *testing.T) {
	fts := []store.Memory{{ID: "m1", Salience: 0.5}}
	emb := []store.RetrievalResult{{Memory: store.Memory{ID: "m1"}, Score: 0.5}}

	tests := []struct {
		name  string
		bonus float32
	}{
		{"bonus_0.0", 0.0},
		{"bonus_0.15", 0.15},
		{"bonus_0.5", 0.5},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.DualHitBonus = tc.bonus
			agent := NewRetrievalAgent(&mockStore{}, &mockLLMProvider{}, cfg, testLogger(), nil)

			result := agent.mergeEntryPoints(fts, emb)

			// Score = alpha*emb + (1-alpha)*fts + bonus
			// FTS rank 1 with salience 0.5: 0.7*1.0 + 0.3*0.5 = 0.85
			ftsScore := float32(0.7*1.0 + 0.3*0.5) // 0.85
			expected := cfg.MergeAlpha*0.5 + (1-cfg.MergeAlpha)*ftsScore + tc.bonus
			score := result["m1"]
			if abs32(score-expected) > 0.001 {
				t.Errorf("bonus=%.2f: expected score %.4f, got %.4f", tc.bonus, expected, score)
			}
		})
	}
}

func TestConfigSynthesisMaxTokensPassedToLLM(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		maxTokens int
	}{
		{"tokens_256", 256},
		{"tokens_2048", 2048},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var capturedMaxTokens int

			s := &mockStore{
				searchByFullTextFunc: func(_ context.Context, _ string, _ int) ([]store.Memory, error) {
					return []store.Memory{
						{ID: "m1", Summary: "test", Salience: 0.8, LastAccessed: now},
					}, nil
				},
				searchByEmbeddingFunc: func(_ context.Context, _ []float32, _ int) ([]store.RetrievalResult, error) {
					return nil, nil
				},
				getAssociationsFunc: func(_ context.Context, _ string) ([]store.Association, error) {
					return nil, nil
				},
				getMemoryFunc: func(_ context.Context, id string) (store.Memory, error) {
					return store.Memory{ID: id, Summary: "test", Salience: 0.8, LastAccessed: now}, nil
				},
			}

			p := &mockLLMProvider{
				completeFunc: func(_ context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
					capturedMaxTokens = req.MaxTokens
					return llm.CompletionResponse{Content: "synthesis result", TokensUsed: 10}, nil
				},
			}

			cfg := DefaultConfig()
			cfg.SynthesisMaxTokens = tc.maxTokens
			agent := NewRetrievalAgent(s, p, cfg, testLogger(), nil)

			_, err := agent.Query(context.Background(), QueryRequest{
				Query:      "test",
				Synthesize: true,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if capturedMaxTokens != tc.maxTokens {
				t.Errorf("expected MaxTokens=%d in LLM request, got %d", tc.maxTokens, capturedMaxTokens)
			}
		})
	}
}

func TestConfigTypeFilterRecencyBoostsRecent(t *testing.T) {
	// Scenario: two handoff memories with identical salience.
	// m_old was created 7 days ago and has more associations (higher base activation).
	// m_new was created 30 minutes ago.
	// With the type-filter recency boost (weight 0.5, half-life 7 days), the new
	// handoff must rank above the old one despite the old one's association advantage.

	now := time.Now()
	mNew := store.Memory{
		ID:        "m_new",
		Summary:   "session handoff 2026-04-11",
		Content:   "recent handoff content",
		Salience:  0.95,
		CreatedAt: now.Add(-30 * time.Minute),
		Source:    "mcp",
		Type:      "handoff",
	}
	mOld := store.Memory{
		ID:        "m_old",
		Summary:   "session handoff 2026-04-04",
		Content:   "old handoff content",
		Salience:  0.95,
		CreatedAt: now.Add(-7 * 24 * time.Hour),
		Source:    "mcp",
		Type:      "handoff",
	}

	s := &mockStore{
		searchByFullTextFunc: func(_ context.Context, _ string, _ int) ([]store.Memory, error) {
			return nil, nil
		},
		searchByEmbeddingFunc: func(_ context.Context, _ []float32, _ int) ([]store.RetrievalResult, error) {
			return nil, nil
		},
		searchByTypeFunc: func(_ context.Context, _ []string, _ int) ([]store.Memory, error) {
			return []store.Memory{mNew, mOld}, nil
		},
		getAssociationsFunc: func(_ context.Context, memoryID string) ([]store.Association, error) {
			// Old memory has more associations — simulates richer graph
			if memoryID == "m_old" {
				return []store.Association{
					{SourceID: "m_old", TargetID: "m_other1", Strength: 0.8, RelationType: "temporal", ActivationCount: 5},
					{SourceID: "m_old", TargetID: "m_other2", Strength: 0.7, RelationType: "similar", ActivationCount: 3},
				}, nil
			}
			return nil, nil
		},
		getMemoryFunc: func(_ context.Context, id string) (store.Memory, error) {
			switch id {
			case "m_new":
				return mNew, nil
			case "m_old":
				return mOld, nil
			default:
				return store.Memory{ID: id, Salience: 0.5, CreatedAt: now.Add(-14 * 24 * time.Hour)}, nil
			}
		},
	}

	cfg := DefaultConfig()
	agent := NewRetrievalAgent(s, &mockLLMProvider{}, cfg, testLogger(), nil)

	resp, err := agent.Query(context.Background(), QueryRequest{
		Query: "session handoff",
		Type:  "handoff",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Memories) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(resp.Memories))
	}

	// The recent handoff must rank first
	if resp.Memories[0].Memory.ID != "m_new" {
		t.Errorf("expected m_new (recent) to rank first, but got %s (scores: %v)",
			resp.Memories[0].Memory.ID,
			func() []string {
				var s []string
				for _, m := range resp.Memories {
					s = append(s, fmt.Sprintf("%s=%.4f", m.Memory.ID, m.Score))
				}
				return s
			}())
	}
}

func TestConfigTypeFilterRecencyParamsUsed(t *testing.T) {
	// Verify that the type-filter recency params are actually applied (not the
	// general ones) by using extreme values and checking the ranking effect.
	now := time.Now()

	mRecent := store.Memory{
		ID:        "m_recent",
		Summary:   "recent decision",
		Salience:  0.5, // lower salience
		CreatedAt: now.Add(-1 * time.Hour),
		Source:    "mcp",
		Type:      "decision",
	}
	mOld := store.Memory{
		ID:        "m_old",
		Summary:   "old decision",
		Salience:  0.9, // higher salience
		CreatedAt: now.Add(-30 * 24 * time.Hour),
		Source:    "mcp",
		Type:      "decision",
	}

	s := &mockStore{
		searchByFullTextFunc: func(_ context.Context, _ string, _ int) ([]store.Memory, error) {
			return nil, nil
		},
		searchByEmbeddingFunc: func(_ context.Context, _ []float32, _ int) ([]store.RetrievalResult, error) {
			return nil, nil
		},
		searchByTypeFunc: func(_ context.Context, _ []string, _ int) ([]store.Memory, error) {
			return []store.Memory{mRecent, mOld}, nil
		},
		getAssociationsFunc: func(_ context.Context, _ string) ([]store.Association, error) {
			return nil, nil
		},
		getMemoryFunc: func(_ context.Context, id string) (store.Memory, error) {
			switch id {
			case "m_recent":
				return mRecent, nil
			case "m_old":
				return mOld, nil
			default:
				return store.Memory{ID: id, Salience: 0.5, CreatedAt: now}, nil
			}
		},
	}

	// Use aggressive type-filter recency: weight=1.0, half-life=1 day
	cfg := DefaultConfig()
	cfg.TypeFilterRecencyWeight = 1.0
	cfg.TypeFilterRecencyHalfLife = 1.0
	agent := NewRetrievalAgent(s, &mockLLMProvider{}, cfg, testLogger(), nil)

	resp, err := agent.Query(context.Background(), QueryRequest{
		Query: "decision",
		Type:  "decision",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Memories) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(resp.Memories))
	}

	// With weight=1.0 and half-life=1 day:
	//   m_recent (1 hour old): bonus = 1.0 * exp(-0.04/1) ≈ 0.96
	//   m_old (30 days old):   bonus = 1.0 * exp(-30/1) ≈ 0.0
	// Even though m_old has higher salience, the recency must dominate
	if resp.Memories[0].Memory.ID != "m_recent" {
		t.Errorf("expected m_recent to rank first with aggressive type-filter recency, got %s",
			resp.Memories[0].Memory.ID)
	}
}

func TestConfigMaxToolCallsLimitsSynthesisTools(t *testing.T) {
	now := time.Now()

	s := &mockStore{
		searchByFullTextFunc: func(_ context.Context, _ string, _ int) ([]store.Memory, error) {
			return []store.Memory{
				{ID: "m1", Summary: "test", Salience: 0.8, LastAccessed: now},
			}, nil
		},
		searchByEmbeddingFunc: func(_ context.Context, _ []float32, _ int) ([]store.RetrievalResult, error) {
			return nil, nil
		},
		getAssociationsFunc: func(_ context.Context, _ string) ([]store.Association, error) {
			return nil, nil
		},
		getMemoryFunc: func(_ context.Context, id string) (store.Memory, error) {
			return store.Memory{ID: id, Summary: "test", Salience: 0.8, LastAccessed: now}, nil
		},
	}

	tests := []struct {
		name         string
		maxToolCalls int
		wantCalls    int // expected total Complete() calls: 1 per tool round + 1 final
	}{
		// maxToolCalls=0: first call gets no tools, must produce text immediately → 1 call
		{"max_tool_calls_0", 0, 1},
		// maxToolCalls=2: up to 2 rounds of tool use + 1 final = 3 max calls
		{"max_tool_calls_2", 2, 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			callCount := 0

			p := &mockLLMProvider{
				completeFunc: func(_ context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
					callCount++
					// If tools are available, make a tool call; otherwise return text
					if len(req.Tools) > 0 {
						return llm.CompletionResponse{
							ToolCalls: []llm.ToolCall{
								{
									ID: "call1",
									Function: llm.ToolCallFunction{
										Name:      "search_memories",
										Arguments: `{"query": "test"}`,
									},
								},
							},
						}, nil
					}
					return llm.CompletionResponse{Content: "final synthesis", TokensUsed: 10}, nil
				},
			}

			cfg := DefaultConfig()
			cfg.MaxToolCalls = tc.maxToolCalls
			agent := NewRetrievalAgent(s, p, cfg, testLogger(), nil)

			_, err := agent.Query(context.Background(), QueryRequest{
				Query:      "test",
				Synthesize: true,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if callCount > tc.wantCalls {
				t.Errorf("maxToolCalls=%d: expected at most %d Complete() calls, got %d",
					tc.maxToolCalls, tc.wantCalls, callCount)
			}
		})
	}
}
