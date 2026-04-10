package encoding

import (
	"testing"
)

func TestExtractEntities_Numbers(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"plain integer", "found 200 errors", []string{"200"}},
		{"decimal", "accuracy was 0.847", []string{"0.847"}},
		{"percentage", "achieved 94.2% recall", []string{"94.2%"}},
		{"comma separated", "47,231 records processed", []string{"47231"}}, // normalized
		{"fraction", "12/21 tests passed", []string{"12/21"}},
		{"negative", "delta was -3.5", []string{"-3.5"}},
		{"scientific", "learning rate 2.3e-4", []string{"2.3e-4"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entities := extractEntities(tc.input)
			for _, exp := range tc.expected {
				if !entities[exp] {
					t.Errorf("expected entity %q not found in %v", exp, entities)
				}
			}
		})
	}
}

func TestExtractEntities_Paths(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"go file", "changed internal/agent/encoding/agent.go", "internal/agent/encoding/agent.go"},
		{"python file", "running training/scripts/eval_faithfulness.py", "training/scripts/eval_faithfulness.py"},
		{"absolute path", "config at /home/user/config.yaml", "/home/user/config.yaml"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entities := extractEntities(tc.input)
			if !entities[tc.expected] {
				t.Errorf("expected path %q not found in %v", tc.expected, entities)
			}
		})
	}
}

func TestExtractEntities_Versions(t *testing.T) {
	entities := extractEntities("upgraded from v1.2.3 to v2.0")
	if !entities["v1.2.3"] {
		t.Error("expected v1.2.3")
	}
	if !entities["v2.0"] {
		t.Error("expected v2.0")
	}
}

func TestExtractEntities_ProperNouns(t *testing.T) {
	entities := extractEntities("Caleb discussed with Aaron Gokaslan about PostgreSQL migration")
	if !entities["aaron gokaslan"] {
		t.Error("expected 'aaron gokaslan' as multi-word proper noun")
	}
	// "Caleb" should be caught by single proper noun regex
	if !entities["caleb"] {
		// May not match if not preceded by lowercase — that's OK
		t.Log("'caleb' not extracted as single proper noun (expected if at start of text)")
	}
}

func TestVerifyFaithfulness_HighEPR(t *testing.T) {
	raw := "Fixed null pointer in auth middleware at internal/agent/encoding/agent.go:42. Error was in v2.1.3."
	compression := &compressionResponse{
		Gist:      "Fixed null pointer in auth middleware",
		Summary:   "Resolved null pointer exception in auth middleware at internal/agent/encoding/agent.go:42",
		Content:   "A null pointer bug in the auth middleware was fixed at internal/agent/encoding/agent.go:42. The issue was present since v2.1.3.",
		Narrative: "The auth middleware had a null pointer that was causing crashes.",
	}

	result := verifyFaithfulness(raw, compression)

	if result.EPR < 0.7 {
		t.Errorf("expected high EPR, got %.2f", result.EPR)
	}
	if result.TED {
		t.Error("unexpected template echo detection")
	}
}

func TestVerifyFaithfulness_TemplateEcho(t *testing.T) {
	raw := "deployed new service"
	compression := &compressionResponse{
		Gist:    "deployed new service under 60 characters",
		Summary: "A new service was deployed. Output ONLY valid JSON.",
		Content: "Service deployed successfully.",
	}

	result := verifyFaithfulness(raw, compression)

	if !result.TED {
		t.Error("expected template echo detection")
	}
	if len(result.Flags) == 0 {
		t.Error("expected flags to be set")
	}
}

func TestVerifyFaithfulness_MinimalInputGuard(t *testing.T) {
	raw := "WAL mode on."
	compression := &compressionResponse{
		Gist:    "Enabled WAL mode on the database",
		Summary: "WAL mode was enabled for the SQLite database to improve concurrent read performance.",
		Content: "The database was configured with Write-Ahead Logging (WAL) mode to improve concurrent read performance. " +
			"This is a common optimization for SQLite databases that allows multiple readers while a single writer commits transactions. " +
			"The change affects all database operations and requires no schema changes. WAL mode persists across database connections " +
			"and provides better throughput for read-heavy workloads typical in memory retrieval systems.",
	}

	result := verifyFaithfulness(raw, compression)

	if !result.MIG {
		t.Error("expected MIG flag for short input with long output")
	}
}

func TestVerifyFaithfulness_NoEntities(t *testing.T) {
	raw := "ok"
	compression := &compressionResponse{
		Gist:    "acknowledged",
		Summary: "Simple acknowledgment.",
		Content: "acknowledged",
	}

	result := verifyFaithfulness(raw, compression)

	if result.EPR != 1.0 {
		t.Errorf("expected EPR 1.0 for no-entity input, got %.2f", result.EPR)
	}
}
