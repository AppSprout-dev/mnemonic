package llm

import (
	"errors"
	"fmt"
	"testing"
)

// fakeGateBiasAccessor is a map-backed GateBiasAccessor with optional
// injected failure modes per operation. Used to test ApplyAblation in
// isolation of any Backend / CGo.
type fakeGateBiasAccessor struct {
	biases map[int]float32

	// Optional: return this error from GetGateBias for the given layer.
	getErr map[int]error
	// Optional: return this error from SetGateBias for the given layer.
	// Matches on (layer, intended value) so tests can fail only the zeroing
	// step or only the restore step.
	setErr func(layer int, value float32) error

	// Trace of every call for assertions.
	calls []string
}

func newFake(initial map[int]float32) *fakeGateBiasAccessor {
	f := &fakeGateBiasAccessor{biases: map[int]float32{}}
	for k, v := range initial {
		f.biases[k] = v
	}
	return f
}

func (f *fakeGateBiasAccessor) GetGateBias(layer int) (float32, error) {
	f.calls = append(f.calls, fmt.Sprintf("get(%d)", layer))
	if err, ok := f.getErr[layer]; ok {
		return 0, err
	}
	return f.biases[layer], nil
}

func (f *fakeGateBiasAccessor) SetGateBias(layer int, value float32) error {
	f.calls = append(f.calls, fmt.Sprintf("set(%d,%g)", layer, value))
	if f.setErr != nil {
		if err := f.setErr(layer, value); err != nil {
			return err
		}
	}
	f.biases[layer] = value
	return nil
}

func TestApplyAblation_HappyPath(t *testing.T) {
	f := newFake(map[int]float32{4: 1.0, 5: 0.75, 6: 0.5})

	got, err := ApplyAblation(f, []int{4, 5, 6}, func() (string, error) {
		// Inside fn, the biases should be zero.
		for _, l := range []int{4, 5, 6} {
			if f.biases[l] != 0 {
				t.Fatalf("layer %d not zeroed during fn: got %v", l, f.biases[l])
			}
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("fn result mismatch: got %q", got)
	}

	// After: biases fully restored.
	expected := map[int]float32{4: 1.0, 5: 0.75, 6: 0.5}
	for l, want := range expected {
		if f.biases[l] != want {
			t.Fatalf("layer %d not restored: want %v, got %v", l, want, f.biases[l])
		}
	}
}

func TestApplyAblation_EmptyLayersSkipsEnvelope(t *testing.T) {
	f := newFake(map[int]float32{4: 1.0})
	called := false
	got, err := ApplyAblation(f, nil, func() (int, error) {
		called = true
		return 42, nil
	})
	if err != nil || got != 42 {
		t.Fatalf("unexpected: err=%v got=%v", err, got)
	}
	if !called {
		t.Fatal("fn should still run with empty layers")
	}
	// No gate bias touched.
	if len(f.calls) != 0 {
		t.Fatalf("expected no accessor calls, got %v", f.calls)
	}
	if f.biases[4] != 1.0 {
		t.Fatalf("unrelated layer mutated: %v", f.biases[4])
	}
}

func TestApplyAblation_FnErrorStillRestores(t *testing.T) {
	f := newFake(map[int]float32{4: 1.0, 5: 0.75})
	sentinel := errors.New("fn blew up")

	_, err := ApplyAblation(f, []int{4, 5}, func() (int, error) {
		// Verify zeroed mid-fn.
		if f.biases[4] != 0 || f.biases[5] != 0 {
			t.Fatal("biases not zeroed during fn")
		}
		return 0, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}

	// Biases restored even though fn failed.
	if f.biases[4] != 1.0 || f.biases[5] != 0.75 {
		t.Fatalf("biases not restored after fn error: %v", f.biases)
	}
}

func TestApplyAblation_SnapshotErrorAborts(t *testing.T) {
	f := newFake(map[int]float32{4: 1.0, 5: 0.75})
	f.getErr = map[int]error{5: errors.New("read failure")}

	fnCalled := false
	_, err := ApplyAblation(f, []int{4, 5}, func() (int, error) {
		fnCalled = true
		return 0, nil
	})
	if err == nil {
		t.Fatal("expected snapshot error to propagate")
	}
	if fnCalled {
		t.Fatal("fn should not run when snapshot fails")
	}
	// Original biases untouched — we never started writing.
	if f.biases[4] != 1.0 || f.biases[5] != 0.75 {
		t.Fatalf("biases mutated despite snapshot error: %v", f.biases)
	}
}

func TestApplyAblation_ZeroFailureRollsBack(t *testing.T) {
	f := newFake(map[int]float32{4: 1.0, 5: 0.75, 6: 0.5})
	// Fail the second zero-write (layer 5 → 0). The first (layer 4 → 0)
	// should succeed, and then get rolled back to 1.0 on the failure.
	f.setErr = func(layer int, value float32) error {
		if layer == 5 && value == 0 {
			return errors.New("bridge rejected set")
		}
		return nil
	}

	_, err := ApplyAblation(f, []int{4, 5, 6}, func() (int, error) {
		t.Fatal("fn should not run when zeroing fails")
		return 0, nil
	})
	if err == nil {
		t.Fatal("expected zeroing error to propagate")
	}

	// Layer 4 should have been rolled back to its original 1.0.
	if f.biases[4] != 1.0 {
		t.Fatalf("layer 4 not rolled back: %v", f.biases[4])
	}
	// Layer 5 never zeroed (set failed); layer 6 never touched.
	if f.biases[5] != 0.75 {
		t.Fatalf("layer 5 mutated despite failed write: %v", f.biases[5])
	}
	if f.biases[6] != 0.5 {
		t.Fatalf("layer 6 touched before failure: %v", f.biases[6])
	}
}

func TestApplyAblation_RestoreFailureSurfaced(t *testing.T) {
	f := newFake(map[int]float32{4: 1.0, 5: 0.75})
	// Zeroing succeeds, fn succeeds, then layer-5 restore fails.
	f.setErr = func(layer int, value float32) error {
		if layer == 5 && value == 0.75 {
			return errors.New("restore failed")
		}
		return nil
	}

	_, err := ApplyAblation(f, []int{4, 5}, func() (int, error) {
		return 0, nil
	})
	if err == nil {
		t.Fatal("expected restore error to propagate")
	}
	if !contains(err.Error(), "restore") {
		t.Fatalf("error missing 'restore' hint: %v", err)
	}
}

func TestApplyAblation_FnAndRestoreBothFail_FnWins(t *testing.T) {
	f := newFake(map[int]float32{4: 1.0})
	fnErr := errors.New("fn boom")
	f.setErr = func(layer int, value float32) error {
		if layer == 4 && value == 1.0 {
			return errors.New("restore boom")
		}
		return nil
	}

	_, err := ApplyAblation(f, []int{4}, func() (int, error) {
		return 0, fnErr
	})
	if err == nil {
		t.Fatal("expected combined error")
	}
	// fn error must appear in the message (it is the substantive failure).
	if !errors.Is(err, fnErr) {
		t.Fatalf("expected fn error to be wrapped, got %v", err)
	}
	if !contains(err.Error(), "restore") {
		t.Fatalf("error should also mention restore, got %v", err)
	}
}

func TestApplyAblation_DoesNotRezeroAlreadyZeroLayers(t *testing.T) {
	// A layer with bias=0 is idempotent: we zero it, run, restore to 0.
	// Nothing weird should happen.
	f := newFake(map[int]float32{4: 0.0, 5: 1.0})

	_, err := ApplyAblation(f, []int{4, 5}, func() (int, error) {
		return 0, nil
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if f.biases[4] != 0 || f.biases[5] != 1.0 {
		t.Fatalf("post-state wrong: %v", f.biases)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
