package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
)

// PrintReport prints the lifecycle results to the terminal.
func PrintReport(results []*PhaseResult, verbose bool) {
	fmt.Println()
	fmt.Println("  ── Results ──────────────────────────────────────────")
	fmt.Println()

	totalAssertions := 0
	totalPassed := 0
	totalFailed := 0

	for _, r := range results {
		passed := 0
		failed := 0
		for _, a := range r.Assertions {
			if a.Passed {
				passed++
			} else {
				failed++
			}
		}
		totalAssertions += len(r.Assertions)
		totalPassed += passed
		totalFailed += failed

		status := fmt.Sprintf("%s✓%s", colorGreen, colorReset)
		if failed > 0 {
			status = fmt.Sprintf("%s✗%s", colorRed, colorReset)
		}

		fmt.Printf("  %s %-20s  %d/%d assertions  %dms\n",
			status, r.Name, passed, len(r.Assertions), r.Duration.Milliseconds())

		// Show failures.
		for _, a := range r.Assertions {
			if !a.Passed {
				fmt.Printf("      %s✗ %s: expected %s, got %s%s\n",
					colorRed, a.Name, a.Expected, a.Actual, colorReset)
			}
		}

		// Show key metrics if verbose.
		if verbose && len(r.Metrics) > 0 {
			keys := sortedKeys(r.Metrics)
			for _, k := range keys {
				fmt.Printf("      %s = %.1f\n", k, r.Metrics[k])
			}
		}
	}

	fmt.Println()
	fmt.Println("  ── Summary ─────────────────────────────────────────")
	fmt.Printf("  Phases: %d  |  Assertions: %d passed, %d failed  |  Total: %d\n",
		len(results), totalPassed, totalFailed, totalAssertions)

	if totalFailed == 0 {
		fmt.Printf("\n  %s  ALL PASSED  %s\n\n", colorGreen, colorReset)
	} else {
		fmt.Printf("\n  %s  %d FAILURES  %s\n\n", colorRed, totalFailed, colorReset)
	}
}

// WriteMarkdownReport writes a markdown report to the given path.
func WriteMarkdownReport(results []*PhaseResult, path string) error {
	var sb strings.Builder

	sb.WriteString("# Mnemonic Lifecycle Simulation Results\n\n")

	// Summary table.
	sb.WriteString("## Summary\n\n")
	sb.WriteString("| Phase | Assertions | Duration | Status |\n")
	sb.WriteString("|-------|-----------|----------|--------|\n")

	totalPassed := 0
	totalFailed := 0

	for _, r := range results {
		passed := 0
		failed := 0
		for _, a := range r.Assertions {
			if a.Passed {
				passed++
			} else {
				failed++
			}
		}
		totalPassed += passed
		totalFailed += failed

		status := "PASS"
		if failed > 0 {
			status = "FAIL"
		}

		fmt.Fprintf(&sb, "| %s | %d/%d | %dms | %s |\n",
			r.Name, passed, len(r.Assertions), r.Duration.Milliseconds(), status)
	}

	fmt.Fprintf(&sb, "\n**Total: %d passed, %d failed**\n\n", totalPassed, totalFailed)

	// Phase details.
	sb.WriteString("## Phase Details\n\n")

	for _, r := range results {
		fmt.Fprintf(&sb, "### %s\n\n", r.Name)

		// Assertions.
		for _, a := range r.Assertions {
			mark := "x"
			if !a.Passed {
				mark = " "
			}
			fmt.Fprintf(&sb, "- [%s] %s (expected: %s, actual: %s)\n",
				mark, a.Name, a.Expected, a.Actual)
		}

		// Metrics.
		if len(r.Metrics) > 0 {
			sb.WriteString("\n**Metrics:**\n\n")
			keys := sortedKeys(r.Metrics)
			for _, k := range keys {
				fmt.Fprintf(&sb, "- %s: %.2f\n", k, r.Metrics[k])
			}
		}
		sb.WriteString("\n")
	}

	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
