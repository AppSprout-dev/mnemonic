package main

import (
	"context"
	"fmt"
	"time"
)

// Phase represents a single lifecycle simulation phase.
type Phase interface {
	Name() string
	Run(ctx context.Context, h *Harness, verbose bool) (*PhaseResult, error)
}

// Assertion is a single pass/fail check within a phase.
type Assertion struct {
	Name     string
	Passed   bool
	Expected string
	Actual   string
}

// PhaseResult holds the outcome of a single phase.
type PhaseResult struct {
	Name       string
	Duration   time.Duration
	Assertions []Assertion
	Metrics    map[string]float64
}

// Assert adds a pass/fail assertion to the result.
func (r *PhaseResult) Assert(name string, passed bool, expected, actual string) {
	r.Assertions = append(r.Assertions, Assertion{
		Name:     name,
		Passed:   passed,
		Expected: expected,
		Actual:   actual,
	})
}

// AssertGE adds an assertion that actual >= expected.
func (r *PhaseResult) AssertGE(name string, actual, expected int) {
	r.Assert(name, actual >= expected,
		fmt.Sprintf(">= %d", expected),
		fmt.Sprintf("%d", actual))
}

// AssertEQ adds an assertion that actual == expected.
func (r *PhaseResult) AssertEQ(name string, actual, expected int) {
	r.Assert(name, actual == expected,
		fmt.Sprintf("%d", expected),
		fmt.Sprintf("%d", actual))
}

// AssertGT adds an assertion that actual > expected.
func (r *PhaseResult) AssertGT(name string, actual, expected int) {
	r.Assert(name, actual > expected,
		fmt.Sprintf("> %d", expected),
		fmt.Sprintf("%d", actual))
}

// AssertFloatGE adds an assertion that actual >= expected for float64.
func (r *PhaseResult) AssertFloatGE(name string, actual, expected float64) {
	r.Assert(name, actual >= expected,
		fmt.Sprintf(">= %.2f", expected),
		fmt.Sprintf("%.2f", actual))
}

// AssertLT adds an assertion that actual < expected.
func (r *PhaseResult) AssertLT(name string, actual, expected int) {
	r.Assert(name, actual < expected,
		fmt.Sprintf("< %d", expected),
		fmt.Sprintf("%d", actual))
}

// Passed returns true if all assertions passed.
func (r *PhaseResult) Passed() bool {
	for _, a := range r.Assertions {
		if !a.Passed {
			return false
		}
	}
	return true
}

// RunPhases executes the phases according to the given filters.
// If phaseFlag is set, only that phase runs (prerequisites are auto-seeded).
// Phases in skipSet are skipped.
func RunPhases(ctx context.Context, h *Harness, phases []Phase, phaseFlag string, skipSet map[string]bool, verbose bool) ([]*PhaseResult, error) {
	var results []*PhaseResult

	for _, p := range phases {
		if phaseFlag != "" && p.Name() != phaseFlag {
			// If targeting a specific phase, silently run prerequisites.
			if !isPrerequisiteOf(p.Name(), phaseFlag, phases) {
				continue
			}
		}
		if skipSet[p.Name()] {
			if verbose {
				fmt.Printf("  [SKIP] %s\n", p.Name())
			}
			continue
		}

		fmt.Printf("  [....] %s", p.Name())
		start := time.Now()
		result, err := p.Run(ctx, h, verbose)
		if err != nil {
			fmt.Printf("\r  [FAIL] %s (%v)\n", p.Name(), err)
			return results, fmt.Errorf("phase %s failed: %w", p.Name(), err)
		}
		result.Duration = time.Since(start)

		status := "PASS"
		if !result.Passed() {
			status = "FAIL"
		}
		fmt.Printf("\r  [%s] %s (%dms)\n", status, p.Name(), result.Duration.Milliseconds())

		results = append(results, result)
	}

	return results, nil
}

// isPrerequisiteOf returns true if candidate comes before target in the phase list.
func isPrerequisiteOf(candidate, target string, phases []Phase) bool {
	candidateIdx := -1
	targetIdx := -1
	for i, p := range phases {
		if p.Name() == candidate {
			candidateIdx = i
		}
		if p.Name() == target {
			targetIdx = i
		}
	}
	return candidateIdx >= 0 && targetIdx >= 0 && candidateIdx < targetIdx
}
