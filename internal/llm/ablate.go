package llm

import (
	"errors"
	"fmt"
)

// GateBiasAccessor is the minimal interface the ablate envelope needs:
// read the current gate bias for a layer, and overwrite it. Implementations
// typically close over an already-acquired mutex so the envelope is atomic.
//
// Feature #4 (EXP-039, CRISPR-LM). Zeroing gate biases for the duration of
// a single /complete call is how we probe whether the spoke stack altered
// an answer that would otherwise be the same as the base model.
type GateBiasAccessor interface {
	// GetGateBias returns the current gate bias for the given spoke layer.
	GetGateBias(layer int) (float32, error)

	// SetGateBias overwrites the gate bias for the given spoke layer.
	SetGateBias(layer int, value float32) error
}

// ApplyAblation snapshots the current gate biases for ``layers``, zeroes
// them, runs ``fn``, and restores the saved values — even when ``fn``
// returns an error.
//
// Errors are reported along the following priority:
//   - If snapshot or zeroing fails, the function aborts with that error
//     and attempts a best-effort rollback for any layers already zeroed.
//   - If ``fn`` returns an error, the restore still runs; the ``fn`` error
//     is returned.
//   - If the restore itself fails (in one or more layers), the error is
//     wrapped and returned. ``fn``'s error (if any) takes precedence.
//
// The caller is responsible for providing a ``GateBiasAccessor`` whose
// operations are atomic with ``fn`` under some shared lock — typically
// the Backend's own mutex. ``ApplyAblation`` performs no locking itself.
func ApplyAblation[T any](
	snap GateBiasAccessor,
	layers []int,
	fn func() (T, error),
) (T, error) {
	var zero T
	if len(layers) == 0 {
		return fn()
	}

	saved := make([]float32, len(layers))
	for i, l := range layers {
		v, err := snap.GetGateBias(l)
		if err != nil {
			return zero, fmt.Errorf("ablate: snapshot layer %d: %w", l, err)
		}
		saved[i] = v
	}

	// Zero each layer. If one fails, roll back the ones already zeroed
	// using the snapshot values so we don't leave a partial ablation.
	for i, l := range layers {
		if err := snap.SetGateBias(l, 0); err != nil {
			rollbackErrs := restorePartial(snap, layers[:i], saved[:i])
			if rollbackErrs != nil {
				return zero, fmt.Errorf("ablate: zero layer %d: %w (rollback: %v)", l, err, rollbackErrs)
			}
			return zero, fmt.Errorf("ablate: zero layer %d: %w", l, err)
		}
	}

	// Run fn, always restore afterwards.
	out, fnErr := fn()
	restoreErrs := restorePartial(snap, layers, saved)

	if fnErr != nil {
		if restoreErrs != nil {
			return zero, fmt.Errorf("ablate: fn: %w (restore also failed: %v)", fnErr, restoreErrs)
		}
		return zero, fnErr
	}
	if restoreErrs != nil {
		return zero, fmt.Errorf("ablate: restore gate biases: %w", restoreErrs)
	}
	return out, nil
}

// restorePartial writes each saved[i] back to layers[i]. Collects all
// per-layer errors and joins them with ``errors.Join`` so callers can
// see every failure, not just the first.
func restorePartial(snap GateBiasAccessor, layers []int, saved []float32) error {
	var errs []error
	for i, l := range layers {
		if err := snap.SetGateBias(l, saved[i]); err != nil {
			errs = append(errs, fmt.Errorf("layer %d: %w", l, err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}
