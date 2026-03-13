package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"gopkg.in/yaml.v3"
)

// loadSweepDefinition reads a sweep YAML file.
func loadSweepDefinition(path string) (SweepDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SweepDefinition{}, fmt.Errorf("reading sweep file: %w", err)
	}
	var def SweepDefinition
	if err := yaml.Unmarshal(data, &def); err != nil {
		return SweepDefinition{}, fmt.Errorf("parsing sweep file: %w", err)
	}
	return def, nil
}

// runSweep executes a full parameter sweep: baseline + one-at-a-time variations.
func runSweep(
	ctx context.Context,
	def SweepDefinition,
	scenarios []scenario,
	cycles int,
	verbose bool,
	log *slog.Logger,
) (SweepReport, error) {
	report := SweepReport{
		ParamResults: make(map[string][]SweepResult),
	}

	// Use sweep-defined cycles if set, otherwise use CLI default.
	if def.Cycles > 0 {
		cycles = def.Cycles
	}

	// Phase 1: Run baseline with default config.
	fmt.Println("  SWEEP: Running baseline (all defaults)...")
	baselineCfg := defaultBenchConfig()
	baselineResults, err := runAllScenarios(ctx, scenarios, baselineCfg, cycles, verbose, log)
	if err != nil {
		return report, fmt.Errorf("baseline run: %w", err)
	}
	report.Baseline = aggregateResults(baselineResults)

	if verbose {
		fmt.Printf("    Baseline: P@5=%.2f  MRR=%.2f  nDCG=%.2f  Noise=%.2f  Signal=%.2f\n",
			report.Baseline.AvgPrecision, report.Baseline.AvgMRR, report.Baseline.AvgNDCG,
			report.Baseline.AvgNoiseSuppression, report.Baseline.AvgSignalRetention)
	}

	// Phase 2: Sweep each parameter independently.
	for _, sweep := range def.Sweeps {
		fmt.Printf("  SWEEP: %s (%d values)\n", sweep.Param, len(sweep.Values))

		for _, val := range sweep.Values {
			cfg := defaultBenchConfig()
			if err := applyOverrides(&cfg, map[string]float64{sweep.Param: val}); err != nil {
				return report, fmt.Errorf("applying override %s=%f: %w", sweep.Param, val, err)
			}

			results, err := runAllScenarios(ctx, scenarios, cfg, cycles, verbose, log)
			if err != nil {
				return report, fmt.Errorf("sweep %s=%f: %w", sweep.Param, val, err)
			}

			agg := aggregateResults(results)
			sr := SweepResult{
				Param:     sweep.Param,
				Value:     val,
				Scores:    agg,
				Delta:     computeDelta(report.Baseline, agg),
				IsDefault: isDefaultValue(sweep.Param, val),
			}

			report.ParamResults[sweep.Param] = append(report.ParamResults[sweep.Param], sr)

			if verbose {
				marker := ""
				if sr.IsDefault {
					marker = " <- default"
				}
				fmt.Printf("    %s=%.4g  P@5=%.2f (%+.2f)  MRR=%.2f (%+.2f)  nDCG=%.2f (%+.2f)%s\n",
					sweep.Param, val,
					agg.AvgPrecision, sr.Delta["precision"],
					agg.AvgMRR, sr.Delta["mrr"],
					agg.AvgNDCG, sr.Delta["ndcg"],
					marker)
			}
		}
	}

	return report, nil
}

// runAllScenarios runs every scenario with a given config and returns the results.
func runAllScenarios(
	ctx context.Context,
	scenarios []scenario,
	cfg benchConfig,
	cycles int,
	verbose bool,
	log *slog.Logger,
) ([]scenarioResult, error) {
	var results []scenarioResult
	for _, sc := range scenarios {
		result, err := runScenario(ctx, sc, cfg, cycles, verbose, log)
		if err != nil {
			return nil, fmt.Errorf("scenario %q: %w", sc.Name, err)
		}
		results = append(results, result)
	}
	return results, nil
}

// computeDelta calculates the difference between a sweep result and the baseline.
func computeDelta(baseline, current aggregateResult) map[string]float64 {
	return map[string]float64{
		"precision": current.AvgPrecision - baseline.AvgPrecision,
		"mrr":       current.AvgMRR - baseline.AvgMRR,
		"ndcg":      current.AvgNDCG - baseline.AvgNDCG,
		"noise":     current.AvgNoiseSuppression - baseline.AvgNoiseSuppression,
		"signal":    current.AvgSignalRetention - baseline.AvgSignalRetention,
	}
}

// compositeScore computes a single composite improvement score from deltas.
// Weights: precision 0.3, MRR 0.2, nDCG 0.2, noise 0.15, signal 0.15.
func compositeScore(delta map[string]float64) float64 {
	return delta["precision"]*0.3 +
		delta["mrr"]*0.2 +
		delta["ndcg"]*0.2 +
		delta["noise"]*0.15 +
		delta["signal"]*0.15
}

// printSweepReport prints the sweep results to stdout.
func printSweepReport(report SweepReport) {
	fmt.Println()
	fmt.Println("  SWEEP RESULTS")
	fmt.Println("  =============")
	fmt.Printf("  Baseline: P@5=%.2f  MRR=%.2f  nDCG=%.2f  Noise=%.2f  Signal=%.2f  [%s]\n",
		report.Baseline.AvgPrecision, report.Baseline.AvgMRR, report.Baseline.AvgNDCG,
		report.Baseline.AvgNoiseSuppression, report.Baseline.AvgSignalRetention,
		report.Baseline.Overall)
	fmt.Println()

	for param, results := range report.ParamResults {
		fmt.Printf("  PARAM: %s\n", param)
		fmt.Printf("    %-10s  %6s  %6s  %6s  %6s  %6s  %8s\n",
			"Value", "P@5", "MRR", "nDCG", "Noise", "Signal", "Composite")

		bestIdx := 0
		bestComp := compositeScore(results[0].Delta)

		for i, sr := range results {
			comp := compositeScore(sr.Delta)
			if comp > bestComp {
				bestComp = comp
				bestIdx = i
			}

			marker := ""
			if sr.IsDefault {
				marker = " <- default"
			}
			fmt.Printf("    %-10.4g  %+.3f  %+.3f  %+.3f  %+.3f  %+.3f  %+8.4f%s\n",
				sr.Value,
				sr.Delta["precision"], sr.Delta["mrr"], sr.Delta["ndcg"],
				sr.Delta["noise"], sr.Delta["signal"],
				comp, marker)
		}

		best := results[bestIdx]
		if !best.IsDefault {
			fmt.Printf("    RECOMMENDED: %s = %.4g  (composite %+.4f vs default)\n", param, best.Value, bestComp)
		} else {
			fmt.Printf("    RECOMMENDED: keep default (%.4g)\n", best.Value)
		}
		fmt.Println()
	}
}
