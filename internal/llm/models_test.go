package llm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()

	manifest := &ModelManifest{
		Models: []ModelEntry{
			{
				Filename:    "mnemonic-100m-q8.gguf",
				Role:        "chat",
				Version:     "0.1.0",
				Quantize:    "Q8_0",
				SizeBytes:   100_000_000,
				SHA256:      "abc123",
				InstalledAt: time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	if err := SaveManifest(dir, manifest); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}

	loaded, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}

	if len(loaded.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(loaded.Models))
	}
	if loaded.Models[0].Filename != "mnemonic-100m-q8.gguf" {
		t.Errorf("unexpected filename: %s", loaded.Models[0].Filename)
	}
	if loaded.Models[0].Role != "chat" {
		t.Errorf("unexpected role: %s", loaded.Models[0].Role)
	}
}

func TestManifestMissing(t *testing.T) {
	dir := t.TempDir()

	manifest, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest on empty dir: %v", err)
	}
	if len(manifest.Models) != 0 {
		t.Errorf("expected empty manifest, got %d models", len(manifest.Models))
	}
}

func TestManifestFindModel(t *testing.T) {
	m := &ModelManifest{
		Models: []ModelEntry{
			{Filename: "a.gguf", Role: "chat"},
			{Filename: "b.gguf", Role: "embedding"},
		},
	}

	if got := m.FindModel("a.gguf"); got == nil {
		t.Error("expected to find a.gguf")
	}
	if got := m.FindModel("c.gguf"); got != nil {
		t.Error("expected nil for missing model")
	}
}

func TestManifestFindByRole(t *testing.T) {
	m := &ModelManifest{
		Models: []ModelEntry{
			{Filename: "a.gguf", Role: "chat"},
			{Filename: "b.gguf", Role: "embedding"},
		},
	}

	if got := m.FindByRole("embedding"); got == nil || got.Filename != "b.gguf" {
		t.Error("expected to find embedding model b.gguf")
	}
	if got := m.FindByRole("other"); got != nil {
		t.Error("expected nil for missing role")
	}
}

func TestManifestAddModel(t *testing.T) {
	m := &ModelManifest{}

	m.AddModel(ModelEntry{Filename: "a.gguf", Role: "chat", Version: "1.0"})
	if len(m.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(m.Models))
	}

	// Update existing
	m.AddModel(ModelEntry{Filename: "a.gguf", Role: "chat", Version: "2.0"})
	if len(m.Models) != 1 {
		t.Fatalf("expected 1 model after update, got %d", len(m.Models))
	}
	if m.Models[0].Version != "2.0" {
		t.Errorf("expected version 2.0, got %s", m.Models[0].Version)
	}

	// Add new
	m.AddModel(ModelEntry{Filename: "b.gguf", Role: "embedding"})
	if len(m.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(m.Models))
	}
}

func TestDiscoverModels(t *testing.T) {
	dir := t.TempDir()

	// Create some files
	for _, name := range []string{"model.gguf", "other.gguf", "readme.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0600); err != nil {
			t.Fatalf("creating file: %v", err)
		}
	}

	found, err := DiscoverModels(dir)
	if err != nil {
		t.Fatalf("DiscoverModels: %v", err)
	}
	if len(found) != 2 {
		t.Errorf("expected 2 gguf files, got %d: %v", len(found), found)
	}
}

func TestDiscoverModelsMissingDir(t *testing.T) {
	found, err := DiscoverModels("/nonexistent/dir")
	if err != nil {
		t.Fatalf("DiscoverModels on missing dir: %v", err)
	}
	if found != nil {
		t.Errorf("expected nil, got %v", found)
	}
}

func TestValidateModelFile(t *testing.T) {
	dir := t.TempDir()

	// Missing file
	if err := ValidateModelFile(dir, "missing.gguf"); err == nil {
		t.Error("expected error for missing file")
	}

	// Empty file
	if err := os.WriteFile(filepath.Join(dir, "empty.gguf"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateModelFile(dir, "empty.gguf"); err == nil {
		t.Error("expected error for empty file")
	}

	// Valid file
	if err := os.WriteFile(filepath.Join(dir, "valid.gguf"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateModelFile(dir, "valid.gguf"); err != nil {
		t.Errorf("unexpected error for valid file: %v", err)
	}
}
