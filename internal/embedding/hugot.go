package embedding

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
)

const (
	// DefaultModel is the HuggingFace model used for embeddings.
	DefaultModel = "KnightsAnalytics/all-MiniLM-L6-v2"

	// DefaultOnnxFile is the ONNX model filename within the model directory.
	DefaultOnnxFile = "model.onnx"
)

// HugotProvider implements embedding.Provider using the hugot library
// with a pure Go inference backend (GoMLX simplego). No CGo, no shared
// libraries — produces 384-dim MiniLM-L6-v2 embeddings in a single binary.
type HugotProvider struct {
	session  *hugot.Session
	pipeline *pipelines.FeatureExtractionPipeline
	mu       sync.Mutex // hugot pipelines are not documented as thread-safe
	log      *slog.Logger
}

// HugotConfig configures the hugot embedding provider.
type HugotConfig struct {
	// ModelDir is the path to the downloaded model directory.
	// If empty, defaults to ~/.mnemonic/models/all-MiniLM-L6-v2.
	ModelDir string

	// AutoDownload controls whether to download the model if not present.
	// Default: true.
	AutoDownload bool
}

// NewHugotProvider creates a new hugot-based embedding provider.
// It loads the MiniLM-L6-v2 model using the pure Go backend.
func NewHugotProvider(cfg HugotConfig, log *slog.Logger) (*HugotProvider, error) {
	if log == nil {
		log = slog.Default()
	}

	modelDir := cfg.ModelDir
	if modelDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot determine home dir: %w", err)
		}
		modelDir = filepath.Join(home, ".mnemonic", "models", "all-MiniLM-L6-v2")
	}

	// Check if model exists, download if needed
	onnxPath := filepath.Join(modelDir, DefaultOnnxFile)
	if _, err := os.Stat(onnxPath); os.IsNotExist(err) {
		if !cfg.AutoDownload {
			return nil, fmt.Errorf("model not found at %s and auto-download is disabled", onnxPath)
		}
		log.Info("downloading embedding model", "model", DefaultModel, "dest", modelDir)
		opts := hugot.NewDownloadOptions()
		downloadedPath, err := hugot.DownloadModel(DefaultModel, filepath.Dir(modelDir), opts)
		if err != nil {
			return nil, fmt.Errorf("failed to download model %s: %w", DefaultModel, err)
		}
		modelDir = downloadedPath
		log.Info("model downloaded", "path", modelDir)
	}

	// Create pure Go session
	session, err := hugot.NewGoSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create hugot session: %w", err)
	}

	// Load feature extraction pipeline
	config := hugot.FeatureExtractionConfig{
		ModelPath:    modelDir,
		Name:         "mnemonic-embed",
		OnnxFilename: DefaultOnnxFile,
	}
	pipeline, err := hugot.NewPipeline(session, config)
	if err != nil {
		_ = session.Destroy()
		return nil, fmt.Errorf("failed to load embedding pipeline: %w", err)
	}

	log.Info("hugot embedding provider ready",
		"model", DefaultModel,
		"path", modelDir,
		"dims", 384,
	)

	return &HugotProvider{
		session:  session,
		pipeline: pipeline,
		log:      log,
	}, nil
}

// Embed generates an embedding for a single text.
func (p *HugotProvider) Embed(_ context.Context, text string) ([]float32, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	result, err := p.pipeline.RunPipeline([]string{text})
	if err != nil {
		return nil, fmt.Errorf("embedding failed: %w", err)
	}
	if len(result.Embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return result.Embeddings[0], nil
}

// BatchEmbed generates embeddings for multiple texts.
func (p *HugotProvider) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	result, err := p.pipeline.RunPipeline(texts)
	if err != nil {
		return nil, fmt.Errorf("batch embedding failed: %w", err)
	}
	return result.Embeddings, nil
}

// Health checks if the pipeline is loaded and ready.
func (p *HugotProvider) Health(_ context.Context) error {
	if p.pipeline == nil {
		return fmt.Errorf("hugot pipeline not loaded")
	}
	return nil
}

// Close releases the hugot session resources.
func (p *HugotProvider) Close() error {
	if p.session != nil {
		return p.session.Destroy()
	}
	return nil
}
