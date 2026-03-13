package main

import (
	"testing"
)

func TestApplyOverrides(t *testing.T) {
	t.Run("valid retrieval overrides", func(t *testing.T) {
		cfg := defaultBenchConfig()
		err := applyOverrides(&cfg, map[string]float64{
			"retrieval.max_hops":             5,
			"retrieval.activation_threshold": 0.2,
			"retrieval.decay_factor":         0.8,
			"retrieval.merge_alpha":          0.5,
			"retrieval.dual_hit_bonus":       0.3,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Retrieval.MaxHops != 5 {
			t.Errorf("MaxHops = %d, want 5", cfg.Retrieval.MaxHops)
		}
		if cfg.Retrieval.ActivationThreshold != 0.2 {
			t.Errorf("ActivationThreshold = %f, want 0.2", cfg.Retrieval.ActivationThreshold)
		}
		if cfg.Retrieval.DecayFactor != 0.8 {
			t.Errorf("DecayFactor = %f, want 0.8", cfg.Retrieval.DecayFactor)
		}
		if cfg.Retrieval.MergeAlpha != 0.5 {
			t.Errorf("MergeAlpha = %f, want 0.5", cfg.Retrieval.MergeAlpha)
		}
		if cfg.Retrieval.DualHitBonus != 0.3 {
			t.Errorf("DualHitBonus = %f, want 0.3", cfg.Retrieval.DualHitBonus)
		}
	})

	t.Run("valid consolidation overrides", func(t *testing.T) {
		cfg := defaultBenchConfig()
		err := applyOverrides(&cfg, map[string]float64{
			"consolidation.decay_rate":        0.90,
			"consolidation.fade_threshold":    0.4,
			"consolidation.archive_threshold": 0.15,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Consolidation.DecayRate != 0.90 {
			t.Errorf("DecayRate = %f, want 0.90", cfg.Consolidation.DecayRate)
		}
		if cfg.Consolidation.FadeThreshold != 0.4 {
			t.Errorf("FadeThreshold = %f, want 0.4", cfg.Consolidation.FadeThreshold)
		}
		if cfg.Consolidation.ArchiveThreshold != 0.15 {
			t.Errorf("ArchiveThreshold = %f, want 0.15", cfg.Consolidation.ArchiveThreshold)
		}
	})

	t.Run("bench decay override", func(t *testing.T) {
		cfg := defaultBenchConfig()
		err := applyOverrides(&cfg, map[string]float64{
			"bench.decay_per_cycle": 0.85,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.BenchDecay != 0.85 {
			t.Errorf("BenchDecay = %f, want 0.85", cfg.BenchDecay)
		}
	})

	t.Run("unknown parameter returns error", func(t *testing.T) {
		cfg := defaultBenchConfig()
		err := applyOverrides(&cfg, map[string]float64{
			"unknown.param": 1.0,
		})
		if err == nil {
			t.Fatal("expected error for unknown parameter")
		}
	})
}

func TestParseSetFlag(t *testing.T) {
	tests := []struct {
		input   string
		wantKey string
		wantVal float64
		wantErr bool
	}{
		{"retrieval.decay_factor=0.8", "retrieval.decay_factor", 0.8, false},
		{"bench.decay_per_cycle=0.92", "bench.decay_per_cycle", 0.92, false},
		{"retrieval.max_hops=5", "retrieval.max_hops", 5.0, false},
		{"invalid", "", 0, true},
		{"key=notanumber", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			key, val, err := parseSetFlag(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if key != tt.wantKey {
				t.Errorf("key = %q, want %q", key, tt.wantKey)
			}
			if val != tt.wantVal {
				t.Errorf("val = %f, want %f", val, tt.wantVal)
			}
		})
	}
}

func TestIsDefaultValue(t *testing.T) {
	if !isDefaultValue("retrieval.decay_factor", 0.7) {
		t.Error("expected 0.7 to be default for retrieval.decay_factor")
	}
	if isDefaultValue("retrieval.decay_factor", 0.8) {
		t.Error("expected 0.8 to NOT be default for retrieval.decay_factor")
	}
	if isDefaultValue("unknown.param", 0.5) {
		t.Error("expected unknown param to return false")
	}
}
