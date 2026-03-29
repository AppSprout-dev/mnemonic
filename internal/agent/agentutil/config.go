package agentutil

// IntOr returns val if non-zero, else fallback.
func IntOr(val, fallback int) int {
	if val != 0 {
		return val
	}
	return fallback
}

// Float32Or returns val if non-zero, else fallback.
func Float32Or(val, fallback float32) float32 {
	if val != 0 {
		return val
	}
	return fallback
}

// Float64Or returns val if non-zero, else fallback.
func Float64Or(val, fallback float64) float64 {
	if val != 0 {
		return val
	}
	return fallback
}
