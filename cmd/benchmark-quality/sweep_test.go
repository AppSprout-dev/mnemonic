package main

import (
	"context"
	"log/slog"
	"os"
	"testing"
)

func TestRunSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("sweep test requires SQLite")
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ctx := context.Background()

	// Use a minimal sweep: one param, two values, only first scenario.
	scenarios := allScenarios()[:1]
	def := SweepDefinition{
		Sweeps: []ParamSweep{
			{
				Param:  "retrieval.decay_factor",
				Values: []float64{0.5, 0.7, 0.9},
			},
		},
		Cycles: 2,
	}

	report, err := runSweep(ctx, def, scenarios, 2, false, log)
	if err != nil {
		t.Fatalf("runSweep failed: %v", err)
	}

	// Baseline should have scores.
	if report.Baseline.AvgPrecision == 0 && report.Baseline.AvgMRR == 0 {
		t.Error("baseline has zero scores — something is wrong")
	}

	// Should have results for the one param.
	results, ok := report.ParamResults["retrieval.decay_factor"]
	if !ok {
		t.Fatal("missing results for retrieval.decay_factor")
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 sweep results, got %d", len(results))
	}

	// The value 0.7 should be marked as default.
	foundDefault := false
	for _, r := range results {
		if r.IsDefault {
			foundDefault = true
			if r.Value != 0.7 {
				t.Errorf("default marked on value %f, expected 0.7", r.Value)
			}
		}
		// Every result should have delta entries.
		if len(r.Delta) != 5 {
			t.Errorf("expected 5 delta entries, got %d", len(r.Delta))
		}
	}
	if !foundDefault {
		t.Error("no result marked as default")
	}
}

func TestCompositeScore(t *testing.T) {
	delta := map[string]float64{
		"precision": 0.10,
		"mrr":       0.10,
		"ndcg":      0.10,
		"noise":     0.10,
		"signal":    0.10,
	}
	// 0.10*(0.3+0.2+0.2+0.15+0.15) = 0.10*1.0 = 0.10
	got := compositeScore(delta)
	if got < 0.099 || got > 0.101 {
		t.Errorf("compositeScore = %f, want ~0.10", got)
	}
}

func TestComputeDelta(t *testing.T) {
	baseline := aggregateResult{
		AvgPrecision:        0.50,
		AvgMRR:              0.40,
		AvgNDCG:             0.45,
		AvgNoiseSuppression: 0.80,
		AvgSignalRetention:  0.90,
	}
	current := aggregateResult{
		AvgPrecision:        0.60,
		AvgMRR:              0.35,
		AvgNDCG:             0.50,
		AvgNoiseSuppression: 0.85,
		AvgSignalRetention:  0.85,
	}
	delta := computeDelta(baseline, current)

	check := func(key string, want float64) {
		got := delta[key]
		diff := got - want
		if diff < 0 {
			diff = -diff
		}
		if diff > 0.001 {
			t.Errorf("delta[%s] = %f, want %f", key, got, want)
		}
	}

	check("precision", 0.10)
	check("mrr", -0.05)
	check("ndcg", 0.05)
	check("noise", 0.05)
	check("signal", -0.05)
}

func TestLoadSweepDefinition(t *testing.T) {
	content := `
sweeps:
  - param: retrieval.decay_factor
    values: [0.5, 0.7, 0.9]
  - param: consolidation.decay_rate
    values: [0.90, 0.95]
cycles: 3
`
	tmp := t.TempDir()
	path := tmp + "/test-sweep.yaml"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	def, err := loadSweepDefinition(path)
	if err != nil {
		t.Fatalf("loadSweepDefinition: %v", err)
	}

	if def.Cycles != 3 {
		t.Errorf("Cycles = %d, want 3", def.Cycles)
	}
	if len(def.Sweeps) != 2 {
		t.Fatalf("len(Sweeps) = %d, want 2", len(def.Sweeps))
	}
	if def.Sweeps[0].Param != "retrieval.decay_factor" {
		t.Errorf("Sweeps[0].Param = %q", def.Sweeps[0].Param)
	}
	if len(def.Sweeps[0].Values) != 3 {
		t.Errorf("Sweeps[0].Values has %d entries", len(def.Sweeps[0].Values))
	}
}
