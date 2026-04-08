package llm

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Backend is the interface for in-process LLM inference engines.
// The llama.cpp CGo implementation will satisfy this interface.
type Backend interface {
	// LoadModel loads a GGUF model file into memory.
	LoadModel(path string, opts BackendOptions) error

	// Complete runs text generation on the loaded model.
	Complete(ctx context.Context, req BackendCompletionRequest) (BackendCompletionResponse, error)

	// Embed generates an embedding vector for the given text.
	Embed(ctx context.Context, text string) ([]float32, error)

	// BatchEmbed generates embedding vectors for multiple texts.
	BatchEmbed(ctx context.Context, texts []string) ([][]float32, error)

	// Close releases all resources held by the backend.
	Close() error
}

// BackendOptions configures model loading for a Backend.
type BackendOptions struct {
	ContextSize int // context window size in tokens
	GPULayers   int // layers to offload to GPU (-1 = all, 0 = CPU only)
	Threads     int // CPU threads for inference (0 = auto-detect)
	BatchSize   int // prompt processing batch size
}

// BackendCompletionRequest is the input for a backend completion call.
type BackendCompletionRequest struct {
	Prompt      string   // formatted prompt text
	MaxTokens   int      // maximum tokens to generate
	Temperature float32  // sampling temperature
	TopP        float32  // nucleus sampling threshold
	Stop        []string // stop sequences
	Grammar     string   // GBNF grammar string for constrained decoding (empty = unconstrained)
}

// BackendCompletionResponse is the output of a backend completion call.
type BackendCompletionResponse struct {
	Text             string  // generated text
	PromptTokens     int     // tokens in the prompt
	CompletionTokens int     // tokens generated
	MeanProb         float32 // mean probability of chosen tokens (0-1)
	MinProb          float32 // minimum probability of any chosen token (0-1)
}

// AvailableModel describes a GGUF model available for loading.
type AvailableModel struct {
	Filename string `json:"filename"`
	Path     string `json:"path"`
	SizeMB   int64  `json:"size_mb"`
	Role     string `json:"role,omitempty"`     // "chat" or "embedding"
	Version  string `json:"version,omitempty"`  // model version
	Quantize string `json:"quantize,omitempty"` // quantization type
}

// ModelStatus reports the currently loaded model state.
type ModelStatus struct {
	ChatModel  string `json:"chat_model"`
	EmbedModel string `json:"embed_model"`
	Loaded     bool   `json:"loaded"`
	ModelsDir  string `json:"models_dir"`
	Mode       string `json:"mode,omitempty"`      // "embedded" or "api"
	APIModel   string `json:"api_model,omitempty"` // cloud model name when in API mode
}

// EmbeddedProvider implements the Provider interface using in-process inference
// via a Backend (llama.cpp CGo bindings). This allows mnemonic to run its own
// GGUF models without an external API server.
type EmbeddedProvider struct {
	modelsDir      string
	chatModelFile  string
	embedModelFile string
	opts           BackendOptions
	maxTokens      int
	temperature    float32

	mu             sync.RWMutex
	chatBackend    Backend
	embedBackend   Backend
	sem            chan struct{}
	backendFactory func() Backend
}

// EmbeddedProviderConfig holds the configuration for creating an EmbeddedProvider.
type EmbeddedProviderConfig struct {
	ModelsDir      string
	ChatModelFile  string
	EmbedModelFile string
	ContextSize    int
	GPULayers      int
	Threads        int
	BatchSize      int
	MaxTokens      int
	Temperature    float32
	MaxConcurrent  int
}

// NewEmbeddedProvider creates a new in-process inference provider.
// The provider is created in an unloaded state — call LoadModels to load GGUF files.
func NewEmbeddedProvider(cfg EmbeddedProviderConfig) *EmbeddedProvider {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 1
	}
	if cfg.ContextSize <= 0 {
		cfg.ContextSize = 2048
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 512
	}
	return &EmbeddedProvider{
		modelsDir:      cfg.ModelsDir,
		chatModelFile:  cfg.ChatModelFile,
		embedModelFile: cfg.EmbedModelFile,
		opts: BackendOptions{
			ContextSize: cfg.ContextSize,
			GPULayers:   cfg.GPULayers,
			Threads:     cfg.Threads,
			BatchSize:   cfg.BatchSize,
		},
		maxTokens:   cfg.MaxTokens,
		temperature: cfg.Temperature,
		sem:         make(chan struct{}, cfg.MaxConcurrent),
	}
}

// LoadModels loads the configured GGUF model files using the given backend factory.
// backendFactory creates a new Backend instance for each model.
// The factory is retained for later hot-swap operations.
func (p *EmbeddedProvider) LoadModels(backendFactory func() Backend) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.backendFactory = backendFactory

	// Load chat model
	chatPath := filepath.Join(p.modelsDir, p.chatModelFile)
	if _, err := os.Stat(chatPath); err != nil {
		return fmt.Errorf("chat model not found at %s: %w", chatPath, err)
	}

	chatBackend := backendFactory()
	if err := chatBackend.LoadModel(chatPath, p.opts); err != nil {
		return fmt.Errorf("loading chat model %s: %w", chatPath, err)
	}
	p.chatBackend = chatBackend
	slog.Info("loaded embedded chat model", "path", chatPath)

	// Load embedding model if configured
	if p.embedModelFile != "" {
		embedPath := filepath.Join(p.modelsDir, p.embedModelFile)
		if _, err := os.Stat(embedPath); err != nil {
			return fmt.Errorf("embedding model not found at %s: %w", embedPath, err)
		}

		embedBackend := backendFactory()
		if err := embedBackend.LoadModel(embedPath, p.opts); err != nil {
			return fmt.Errorf("loading embedding model %s: %w", embedPath, err)
		}
		p.embedBackend = embedBackend
		slog.Info("loaded embedded embedding model", "path", embedPath)
	}

	return nil
}

// acquire blocks until a concurrency slot is available or ctx is cancelled.
func (p *EmbeddedProvider) acquire(ctx context.Context) error {
	select {
	case p.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release frees a concurrency slot.
func (p *EmbeddedProvider) release() {
	<-p.sem
}

// formatPrompt converts a slice of Messages into a prompt string.
// Uses ChatML format (Qwen 3.5, Gemma-it, etc.):
//
//	<|im_start|>system\n...<|im_end|>\n<|im_start|>user\n...<|im_end|>\n<|im_start|>assistant\n
//
// Appends /no_think to the system message to disable Qwen's thinking mode,
// which interferes with GBNF grammar-constrained generation.
func formatPrompt(messages []Message) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString("<|im_start|>")
		b.WriteString(msg.Role)
		b.WriteByte('\n')
		b.WriteString(msg.Content)
		if msg.Role == "system" {
			b.WriteString(" /no_think")
		}
		b.WriteString("<|im_end|>\n")
	}
	b.WriteString("<|im_start|>assistant\n")
	return b.String()
}

// Complete sends a completion request to the in-process backend.
func (p *EmbeddedProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	if err := p.acquire(ctx); err != nil {
		return CompletionResponse{}, fmt.Errorf("embedded provider busy: %w", err)
	}
	defer p.release()

	p.mu.RLock()
	backend := p.chatBackend
	p.mu.RUnlock()

	if backend == nil {
		return CompletionResponse{}, &ErrProviderUnavailable{
			Endpoint: "embedded",
			Cause:    fmt.Errorf("chat model not loaded — call LoadModels first"),
		}
	}

	// Determine generation parameters
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = p.maxTokens
	}
	temp := req.Temperature
	if temp == 0 {
		temp = p.temperature
	}

	// Convert response format to GBNF grammar if applicable
	grammar := ""
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_object" {
		grammar = GBNFJSONObject
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_schema" && req.ResponseFormat.JSONSchema != nil {
		if req.ResponseFormat.JSONSchema.Name == "encoding_response" {
			grammar = GBNFEncodingResponse
		} else {
			grammar = GBNFJSONObject
		}
	}

	// Warn if prompt likely exceeds context window (bridge will hard-truncate)
	prompt := formatPrompt(req.Messages)
	if estimatedTokens := len(prompt) / 4; estimatedTokens > p.opts.ContextSize-maxTokens {
		slog.Warn("prompt likely exceeds context window, bridge will truncate",
			"estimated_tokens", estimatedTokens,
			"context_size", p.opts.ContextSize,
			"max_tokens", maxTokens,
			"prompt_chars", len(prompt))
	}

	// Ensure <|im_end|> is a stop sequence so the model stops at turn boundary.
	stop := req.Stop
	hasIMEnd := false
	for _, s := range stop {
		if s == "<|im_end|>" {
			hasIMEnd = true
			break
		}
	}
	if !hasIMEnd {
		stop = append(stop, "<|im_end|>")
	}

	backendReq := BackendCompletionRequest{
		Prompt:      prompt,
		MaxTokens:   maxTokens,
		Temperature: temp,
		TopP:        req.TopP,
		Stop:        stop,
		Grammar:     grammar,
	}

	backendResp, err := backend.Complete(ctx, backendReq)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("embedded completion: %w", err)
	}

	// Strip Qwen-style <think>...</think> wrapper if present.
	content := stripThinkingTokens(backendResp.Text)

	return CompletionResponse{
		Content:          content,
		StopReason:       "stop",
		TokensUsed:       backendResp.PromptTokens + backendResp.CompletionTokens,
		PromptTokens:     backendResp.PromptTokens,
		CompletionTokens: backendResp.CompletionTokens,
		MeanProb:         backendResp.MeanProb,
		MinProb:          backendResp.MinProb,
	}, nil
}

// Embed generates a single embedding for the given text.
func (p *EmbeddedProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := p.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}
	return embeddings[0], nil
}

// BatchEmbed generates embeddings for multiple texts.
func (p *EmbeddedProvider) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	if err := p.acquire(ctx); err != nil {
		return nil, fmt.Errorf("embedded provider busy: %w", err)
	}
	defer p.release()

	p.mu.RLock()
	backend := p.embedBackend
	// Fall back to chat backend for embeddings if no dedicated embedding model.
	// The llama.cpp bridge creates a separate embedding context with mean pooling.
	if backend == nil {
		backend = p.chatBackend
	}
	p.mu.RUnlock()

	if backend == nil {
		return nil, &ErrProviderUnavailable{
			Endpoint: "embedded",
			Cause:    fmt.Errorf("no model loaded for embeddings — call LoadModels first"),
		}
	}

	return backend.BatchEmbed(ctx, texts)
}

// Health checks if the embedded models are loaded and ready.
func (p *EmbeddedProvider) Health(ctx context.Context) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.chatBackend == nil {
		return &ErrProviderUnavailable{
			Endpoint: "embedded",
			Cause:    fmt.Errorf("chat model not loaded"),
		}
	}
	return nil
}

// ModelInfo returns metadata about the loaded embedded model.
func (p *EmbeddedProvider) ModelInfo(ctx context.Context) (ModelMetadata, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.chatBackend == nil {
		return ModelMetadata{}, &ErrProviderUnavailable{
			Endpoint: "embedded",
			Cause:    fmt.Errorf("chat model not loaded"),
		}
	}

	return ModelMetadata{
		Name:              p.chatModelFile,
		ContextWindow:     p.opts.ContextSize,
		SupportsEmbedding: p.embedBackend != nil || p.embedModelFile == "",
		MaxTokens:         p.maxTokens,
	}, nil
}

// ListAvailableModels returns models registered in models.json.
// Only curated, production-ready models appear — not every GGUF file on disk.
func (p *EmbeddedProvider) ListAvailableModels() ([]AvailableModel, error) {
	p.mu.RLock()
	dir := p.modelsDir
	p.mu.RUnlock()

	manifest, err := LoadManifest(dir)
	if err != nil {
		return nil, fmt.Errorf("loading model manifest: %w", err)
	}

	var models []AvailableModel
	for _, entry := range manifest.Models {
		models = append(models, AvailableModel{
			Filename: entry.Filename,
			Path:     filepath.Join(dir, entry.Filename),
			SizeMB:   entry.SizeBytes / (1024 * 1024),
			Role:     entry.Role,
			Version:  entry.Version,
			Quantize: entry.Quantize,
		})
	}
	return models, nil
}

// ActiveModel returns the currently loaded model status.
func (p *EmbeddedProvider) ActiveModel() ModelStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return ModelStatus{
		ChatModel:  p.chatModelFile,
		EmbedModel: p.embedModelFile,
		Loaded:     p.chatBackend != nil,
		ModelsDir:  p.modelsDir,
	}
}

// SwapChatModel hot-swaps the chat model to a different GGUF file.
// The old backend is closed after the new one is loaded successfully.
func (p *EmbeddedProvider) SwapChatModel(filename string) error {
	p.mu.RLock()
	factory := p.backendFactory
	dir := p.modelsDir
	opts := p.opts
	p.mu.RUnlock()

	if factory == nil {
		return fmt.Errorf("no backend factory configured — cannot swap models")
	}

	modelPath := filepath.Join(dir, filename)
	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("model not found at %s: %w", modelPath, err)
	}

	// Load new model before acquiring write lock
	newBackend := factory()
	if err := newBackend.LoadModel(modelPath, opts); err != nil {
		return fmt.Errorf("loading new chat model %s: %w", filename, err)
	}
	slog.Info("loaded new chat model for swap", "path", modelPath)

	// Swap under write lock
	p.mu.Lock()
	oldBackend := p.chatBackend
	p.chatBackend = newBackend
	p.chatModelFile = filename
	p.mu.Unlock()

	// Close old backend outside the lock
	if oldBackend != nil {
		if err := oldBackend.Close(); err != nil {
			slog.Warn("error closing old chat backend during swap", "error", err)
		}
	}

	slog.Info("chat model swapped", "model", filename)
	return nil
}

// SwapEmbedModel hot-swaps the embedding model to a different GGUF file.
// Pass empty string to clear the dedicated embedding model (falls back to chat backend).
func (p *EmbeddedProvider) SwapEmbedModel(filename string) error {
	if filename == "" {
		p.mu.Lock()
		oldBackend := p.embedBackend
		p.embedBackend = nil
		p.embedModelFile = ""
		p.mu.Unlock()
		if oldBackend != nil {
			if err := oldBackend.Close(); err != nil {
				slog.Warn("error closing old embed backend during swap", "error", err)
			}
		}
		slog.Info("embed model cleared, using chat backend for embeddings")
		return nil
	}

	p.mu.RLock()
	factory := p.backendFactory
	dir := p.modelsDir
	opts := p.opts
	p.mu.RUnlock()

	if factory == nil {
		return fmt.Errorf("no backend factory configured — cannot swap models")
	}

	modelPath := filepath.Join(dir, filename)
	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("embed model not found at %s: %w", modelPath, err)
	}

	newBackend := factory()
	if err := newBackend.LoadModel(modelPath, opts); err != nil {
		return fmt.Errorf("loading new embed model %s: %w", filename, err)
	}

	p.mu.Lock()
	oldBackend := p.embedBackend
	p.embedBackend = newBackend
	p.embedModelFile = filename
	p.mu.Unlock()

	if oldBackend != nil {
		if err := oldBackend.Close(); err != nil {
			slog.Warn("error closing old embed backend during swap", "error", err)
		}
	}

	slog.Info("embed model swapped", "model", filename)
	return nil
}

// Unload releases all backend resources without destroying the provider config.
// The provider can be reloaded later with Reload().
func (p *EmbeddedProvider) Unload() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.chatBackend != nil {
		if err := p.chatBackend.Close(); err != nil {
			slog.Warn("error closing chat backend during unload", "error", err)
		}
		p.chatBackend = nil
		slog.Info("unloaded chat model", "model", p.chatModelFile)
	}
	if p.embedBackend != nil {
		if err := p.embedBackend.Close(); err != nil {
			slog.Warn("error closing embed backend during unload", "error", err)
		}
		p.embedBackend = nil
		slog.Info("unloaded embed model", "model", p.embedModelFile)
	}
}

// Reload reloads models using the stored backend factory.
// Called after Unload() to restore embedded inference.
func (p *EmbeddedProvider) Reload() error {
	p.mu.RLock()
	factory := p.backendFactory
	p.mu.RUnlock()

	if factory == nil {
		return fmt.Errorf("no backend factory configured — cannot reload")
	}
	return p.LoadModels(factory)
}

// SetProviderMode on a bare EmbeddedProvider only supports "embedded".
func (p *EmbeddedProvider) SetProviderMode(mode string) error {
	if mode == "embedded" {
		return nil
	}
	return fmt.Errorf("API provider not configured — only embedded mode available")
}

// ProviderMode always returns "embedded" for a bare EmbeddedProvider.
func (p *EmbeddedProvider) ProviderMode() string {
	return "embedded"
}

// stripThinkingTokens removes <think>...</think> blocks from model output.
// Qwen 3.5 and similar models prepend reasoning tokens before the actual response.
func stripThinkingTokens(text string) string {
	const openTag = "<think>"
	const closeTag = "</think>"

	for {
		start := strings.Index(text, openTag)
		if start == -1 {
			break
		}
		end := strings.Index(text[start:], closeTag)
		if end == -1 {
			// Unclosed think tag — strip from start to end of text
			text = strings.TrimSpace(text[:start])
			break
		}
		text = text[:start] + text[start+end+len(closeTag):]
	}
	return strings.TrimSpace(text)
}

// Close releases all backend resources.
func (p *EmbeddedProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	var errs []error
	if p.chatBackend != nil {
		if err := p.chatBackend.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing chat backend: %w", err))
		}
		p.chatBackend = nil
	}
	if p.embedBackend != nil {
		if err := p.embedBackend.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing embed backend: %w", err))
		}
		p.embedBackend = nil
	}

	if len(errs) > 0 {
		return fmt.Errorf("closing embedded provider: %v", errs)
	}
	return nil
}
