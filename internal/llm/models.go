package llm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ModelManifest tracks installed GGUF models in the models directory.
// Stored as models.json alongside the GGUF files.
type ModelManifest struct {
	Models []ModelEntry `json:"models"`
}

// ModelEntry describes a single installed GGUF model.
type ModelEntry struct {
	Filename    string    `json:"filename"`     // e.g. "mnemonic-lm-100m-q8_0.gguf"
	Role        string    `json:"role"`         // "chat" or "embedding"
	Version     string    `json:"version"`      // semantic version of the model weights
	Quantize    string    `json:"quantize"`     // quantization type, e.g. "Q8_0", "Q4_K_M"
	SizeBytes   int64     `json:"size_bytes"`   // file size
	SHA256      string    `json:"sha256"`       // integrity hash
	InstalledAt time.Time `json:"installed_at"` // when the model was added
}

// manifestPath returns the path to models.json in the given models directory.
func manifestPath(modelsDir string) string {
	return filepath.Join(modelsDir, "models.json")
}

// LoadManifest reads the model manifest from the models directory.
// Returns an empty manifest if the file does not exist.
func LoadManifest(modelsDir string) (*ModelManifest, error) {
	path := manifestPath(modelsDir)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &ModelManifest{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading model manifest: %w", err)
	}

	var m ModelManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing model manifest: %w", err)
	}
	return &m, nil
}

// SaveManifest writes the model manifest to the models directory.
func SaveManifest(modelsDir string, m *ModelManifest) error {
	if err := os.MkdirAll(modelsDir, 0700); err != nil {
		return fmt.Errorf("creating models directory: %w", err)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling model manifest: %w", err)
	}

	path := manifestPath(modelsDir)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing model manifest: %w", err)
	}
	return nil
}

// FindModel looks up a model entry by filename.
func (m *ModelManifest) FindModel(filename string) *ModelEntry {
	for i := range m.Models {
		if m.Models[i].Filename == filename {
			return &m.Models[i]
		}
	}
	return nil
}

// FindByRole returns the first model entry with the given role.
func (m *ModelManifest) FindByRole(role string) *ModelEntry {
	for i := range m.Models {
		if m.Models[i].Role == role {
			return &m.Models[i]
		}
	}
	return nil
}

// AddModel adds or updates a model entry in the manifest.
func (m *ModelManifest) AddModel(entry ModelEntry) {
	for i := range m.Models {
		if m.Models[i].Filename == entry.Filename {
			m.Models[i] = entry
			return
		}
	}
	m.Models = append(m.Models, entry)
}

// DiscoverModels scans the models directory for .gguf files and returns
// any that are not yet in the manifest. This helps detect manually placed models.
func DiscoverModels(modelsDir string) ([]string, error) {
	entries, err := os.ReadDir(modelsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading models directory: %w", err)
	}

	var found []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".gguf" {
			found = append(found, e.Name())
		}
	}
	return found, nil
}

// EnsureModelsDir creates the models directory if it doesn't exist.
func EnsureModelsDir(modelsDir string) error {
	if err := os.MkdirAll(modelsDir, 0700); err != nil {
		return fmt.Errorf("creating models directory %s: %w", modelsDir, err)
	}
	return nil
}

// ValidateModelFile checks that a GGUF file exists and is readable.
func ValidateModelFile(modelsDir, filename string) error {
	path := filepath.Join(modelsDir, filename)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("model file %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("model file %s is a directory", path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("model file %s is empty", path)
	}
	return nil
}
