package llm

import (
	"context"
	"fmt"
	"sync"
)

// SwitchableProvider wraps an embedded provider and an API provider,
// allowing runtime switching between local inference and cloud API.
// All agents hold references to this provider, so switching takes effect
// immediately across the entire daemon.
type SwitchableProvider struct {
	mu       sync.RWMutex
	embedded *EmbeddedProvider
	api      Provider
	useAPI   bool
	apiModel string // model name for display (e.g. "gemini-3-flash-preview")
}

// NewSwitchableProvider creates a provider that can toggle between embedded and API.
// Starts in embedded mode.
func NewSwitchableProvider(embedded *EmbeddedProvider, api Provider, apiModel string) *SwitchableProvider {
	return &SwitchableProvider{
		embedded: embedded,
		api:      api,
		apiModel: apiModel,
	}
}

func (s *SwitchableProvider) active() Provider {
	if s.useAPI {
		return s.api
	}
	return s.embedded
}

// Complete delegates to the active provider.
func (s *SwitchableProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	s.mu.RLock()
	p := s.active()
	s.mu.RUnlock()
	return p.Complete(ctx, req)
}

// Embed delegates to the active provider.
func (s *SwitchableProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	s.mu.RLock()
	p := s.active()
	s.mu.RUnlock()
	return p.Embed(ctx, text)
}

// BatchEmbed delegates to the active provider.
func (s *SwitchableProvider) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	s.mu.RLock()
	p := s.active()
	s.mu.RUnlock()
	return p.BatchEmbed(ctx, texts)
}

// Health delegates to the active provider.
func (s *SwitchableProvider) Health(ctx context.Context) error {
	s.mu.RLock()
	p := s.active()
	s.mu.RUnlock()
	return p.Health(ctx)
}

// ModelInfo delegates to the active provider.
func (s *SwitchableProvider) ModelInfo(ctx context.Context) (ModelMetadata, error) {
	s.mu.RLock()
	p := s.active()
	s.mu.RUnlock()
	return p.ModelInfo(ctx)
}

// --- ModelManager implementation ---

// ListAvailableModels delegates to the embedded provider.
func (s *SwitchableProvider) ListAvailableModels() ([]AvailableModel, error) {
	return s.embedded.ListAvailableModels()
}

// ActiveModel returns the current model status including provider mode.
func (s *SwitchableProvider) ActiveModel() ModelStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.embedded.ActiveModel()
	if s.useAPI {
		status.Mode = "api"
		status.APIModel = s.apiModel
	} else {
		status.Mode = "embedded"
	}
	return status
}

// SwapChatModel delegates to the embedded provider.
func (s *SwitchableProvider) SwapChatModel(filename string) error {
	return s.embedded.SwapChatModel(filename)
}

// SwapEmbedModel delegates to the embedded provider.
func (s *SwitchableProvider) SwapEmbedModel(filename string) error {
	return s.embedded.SwapEmbedModel(filename)
}

// SetProviderMode switches between "embedded" and "api" at runtime.
// Switching to API unloads embedded models to free VRAM.
// Switching back to embedded reloads them.
func (s *SwitchableProvider) SetProviderMode(mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch mode {
	case "embedded":
		if !s.useAPI {
			return nil // already in embedded mode
		}
		// Reload models
		if err := s.embedded.Reload(); err != nil {
			return fmt.Errorf("reloading embedded models: %w", err)
		}
		s.useAPI = false
	case "api":
		if s.api == nil {
			return fmt.Errorf("API provider not configured")
		}
		if s.useAPI {
			return nil // already in API mode
		}
		s.useAPI = true
		// Unload models to free VRAM
		s.embedded.Unload()
	default:
		return fmt.Errorf("unknown provider mode: %q (use \"embedded\" or \"api\")", mode)
	}
	return nil
}

// ProviderMode returns the current mode ("embedded" or "api").
func (s *SwitchableProvider) ProviderMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.useAPI {
		return "api"
	}
	return "embedded"
}

// SpokeEditor delegates to the embedded provider's spoke editor.
func (s *SwitchableProvider) SpokeEditor() SpokeEditor {
	return s.embedded.SpokeEditor()
}
