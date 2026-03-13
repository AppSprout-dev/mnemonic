package main

import (
	"fmt"
	"strings"
)

// paramDefaults maps parameter names to their default values.
var paramDefaults = map[string]float64{
	"retrieval.max_hops":              3,
	"retrieval.activation_threshold":  0.1,
	"retrieval.decay_factor":          0.7,
	"retrieval.merge_alpha":           0.6,
	"retrieval.dual_hit_bonus":        0.15,
	"consolidation.decay_rate":        0.95,
	"consolidation.fade_threshold":    0.3,
	"consolidation.archive_threshold": 0.1,
	"bench.decay_per_cycle":           0.92,
}

// applyOverrides applies dot-notation config overrides to a benchConfig.
// Returns an error for unknown parameter names.
func applyOverrides(cfg *benchConfig, overrides map[string]float64) error {
	for key, val := range overrides {
		if err := applyOneOverride(cfg, key, val); err != nil {
			return err
		}
	}
	return nil
}

// applyOneOverride applies a single parameter override.
func applyOneOverride(cfg *benchConfig, key string, val float64) error {
	switch key {
	// Retrieval parameters.
	case "retrieval.max_hops":
		cfg.Retrieval.MaxHops = int(val)
	case "retrieval.activation_threshold":
		cfg.Retrieval.ActivationThreshold = float32(val)
	case "retrieval.decay_factor":
		cfg.Retrieval.DecayFactor = float32(val)
	case "retrieval.merge_alpha":
		cfg.Retrieval.MergeAlpha = float32(val)
	case "retrieval.dual_hit_bonus":
		cfg.Retrieval.DualHitBonus = float32(val)

	// Consolidation parameters.
	case "consolidation.decay_rate":
		cfg.Consolidation.DecayRate = val
	case "consolidation.fade_threshold":
		cfg.Consolidation.FadeThreshold = val
	case "consolidation.archive_threshold":
		cfg.Consolidation.ArchiveThreshold = val

	// Benchmark parameters.
	case "bench.decay_per_cycle":
		cfg.BenchDecay = float32(val)

	default:
		return fmt.Errorf("unknown parameter: %q", key)
	}
	return nil
}

// parseSetFlag parses a "key=value" string into key and value.
func parseSetFlag(s string) (string, float64, error) {
	key, valStr, found := strings.Cut(s, "=")
	if !found {
		return "", 0, fmt.Errorf("invalid -set format %q, expected key=value", s)
	}
	var val float64
	_, err := fmt.Sscanf(valStr, "%f", &val)
	if err != nil {
		return "", 0, fmt.Errorf("invalid value %q for key %q: %w", valStr, key, err)
	}
	return key, val, nil
}

// setFlagList implements flag.Value for repeatable -set flags.
type setFlagList []string

func (s *setFlagList) String() string { return fmt.Sprintf("%v", *s) }
func (s *setFlagList) Set(val string) error {
	*s = append(*s, val)
	return nil
}

// isDefaultValue checks if a value matches the default for a parameter.
func isDefaultValue(param string, val float64) bool {
	def, ok := paramDefaults[param]
	if !ok {
		return false
	}
	// Use epsilon comparison for floats.
	diff := def - val
	if diff < 0 {
		diff = -diff
	}
	return diff < 0.0001
}
