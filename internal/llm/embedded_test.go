package llm

import (
	"context"
	"os"
	"sync"
	"testing"
)

// mockBackend is a test Backend implementation.
type mockBackend struct {
	loaded       bool
	modelPath    string
	opts         BackendOptions
	completeFunc func(ctx context.Context, req BackendCompletionRequest) (BackendCompletionResponse, error)
	embedFunc    func(ctx context.Context, text string) ([]float32, error)
}

func (m *mockBackend) LoadModel(path string, opts BackendOptions) error {
	m.loaded = true
	m.modelPath = path
	m.opts = opts
	return nil
}

func (m *mockBackend) Complete(ctx context.Context, req BackendCompletionRequest) (BackendCompletionResponse, error) {
	if m.completeFunc != nil {
		return m.completeFunc(ctx, req)
	}
	return BackendCompletionResponse{
		Text:             `{"summary": "test"}`,
		PromptTokens:     10,
		CompletionTokens: 5,
	}, nil
}

func (m *mockBackend) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.embedFunc != nil {
		return m.embedFunc(ctx, text)
	}
	return []float32{0.1, 0.2, 0.3}, nil
}

func (m *mockBackend) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, t := range texts {
		emb, err := m.Embed(ctx, t)
		if err != nil {
			return nil, err
		}
		result[i] = emb
	}
	return result, nil
}

func (m *mockBackend) Close() error {
	m.loaded = false
	return nil
}

func TestEmbeddedProviderUnloaded(t *testing.T) {
	p := NewEmbeddedProvider(EmbeddedProviderConfig{
		ModelsDir:     "/tmp/models",
		ChatModelFile: "test.gguf",
		MaxTokens:     256,
		Temperature:   0.3,
	})

	ctx := context.Background()

	// Health should fail when no model is loaded
	if err := p.Health(ctx); err == nil {
		t.Fatal("expected Health to fail when no model loaded")
	}

	// Complete should fail when no model is loaded
	_, err := p.Complete(ctx, CompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected Complete to fail when no model loaded")
	}

	// Embed should fail when no model is loaded
	_, err = p.Embed(ctx, "test")
	if err == nil {
		t.Fatal("expected Embed to fail when no model loaded")
	}

	// ModelInfo should fail when no model is loaded
	_, err = p.ModelInfo(ctx)
	if err == nil {
		t.Fatal("expected ModelInfo to fail when no model loaded")
	}
}

func TestEmbeddedProviderWithMockBackend(t *testing.T) {
	// Create a temp dir with a fake GGUF file
	dir := t.TempDir()
	chatFile := "chat.gguf"
	embedFile := "embed.gguf"

	// Create fake model files
	for _, f := range []string{chatFile, embedFile} {
		if err := writeTestFile(dir, f); err != nil {
			t.Fatalf("creating test file: %v", err)
		}
	}

	p := NewEmbeddedProvider(EmbeddedProviderConfig{
		ModelsDir:      dir,
		ChatModelFile:  chatFile,
		EmbedModelFile: embedFile,
		ContextSize:    1024,
		GPULayers:      0,
		MaxTokens:      256,
		Temperature:    0.3,
		MaxConcurrent:  1,
	})

	// Load models with mock backend
	err := p.LoadModels(func() Backend { return &mockBackend{} })
	if err != nil {
		t.Fatalf("LoadModels failed: %v", err)
	}

	ctx := context.Background()

	// Health should pass
	if err := p.Health(ctx); err != nil {
		t.Fatalf("Health failed: %v", err)
	}

	// Complete should work
	resp, err := p.Complete(ctx, CompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if resp.Content != `{"summary": "test"}` {
		t.Errorf("unexpected content: %s", resp.Content)
	}
	if resp.PromptTokens != 10 {
		t.Errorf("expected 10 prompt tokens, got %d", resp.PromptTokens)
	}
	if resp.CompletionTokens != 5 {
		t.Errorf("expected 5 completion tokens, got %d", resp.CompletionTokens)
	}

	// Embed should work
	emb, err := p.Embed(ctx, "test")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(emb) != 3 {
		t.Errorf("expected 3-dim embedding, got %d", len(emb))
	}

	// BatchEmbed should work
	embs, err := p.BatchEmbed(ctx, []string{"a", "b"})
	if err != nil {
		t.Fatalf("BatchEmbed failed: %v", err)
	}
	if len(embs) != 2 {
		t.Errorf("expected 2 embeddings, got %d", len(embs))
	}

	// ModelInfo should work
	info, err := p.ModelInfo(ctx)
	if err != nil {
		t.Fatalf("ModelInfo failed: %v", err)
	}
	if info.Name != chatFile {
		t.Errorf("expected model name %s, got %s", chatFile, info.Name)
	}
	if info.ContextWindow != 1024 {
		t.Errorf("expected context window 1024, got %d", info.ContextWindow)
	}

	// Close should work
	if err := p.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// After close, health should fail
	if err := p.Health(ctx); err == nil {
		t.Fatal("expected Health to fail after Close")
	}
}

func TestEmbeddedProviderGrammarRouting(t *testing.T) {
	dir := t.TempDir()
	chatFile := "chat.gguf"
	if err := writeTestFile(dir, chatFile); err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	var capturedGrammar string
	p := NewEmbeddedProvider(EmbeddedProviderConfig{
		ModelsDir:     dir,
		ChatModelFile: chatFile,
		MaxTokens:     256,
		Temperature:   0.3,
		MaxConcurrent: 1,
	})

	err := p.LoadModels(func() Backend {
		return &mockBackend{
			completeFunc: func(_ context.Context, req BackendCompletionRequest) (BackendCompletionResponse, error) {
				capturedGrammar = req.Grammar
				return BackendCompletionResponse{Text: "{}", PromptTokens: 1, CompletionTokens: 1}, nil
			},
		}
	})
	if err != nil {
		t.Fatalf("LoadModels failed: %v", err)
	}

	ctx := context.Background()

	// No response format — no grammar
	_, err = p.Complete(ctx, CompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if capturedGrammar != "" {
		t.Errorf("expected no grammar for plain request, got %q", capturedGrammar)
	}

	// json_object format — should use GBNF grammar
	_, err = p.Complete(ctx, CompletionRequest{
		Messages:       []Message{{Role: "user", Content: "hello"}},
		ResponseFormat: &ResponseFormat{Type: "json_object"},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if capturedGrammar != GBNFJSONObject {
		t.Errorf("expected GBNF JSON grammar for json_object request")
	}

	// json_schema with encoding_response name — should use encoding-specific grammar
	_, err = p.Complete(ctx, CompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
		ResponseFormat: &ResponseFormat{
			Type: "json_schema",
			JSONSchema: &JSONSchema{
				Name:   "encoding_response",
				Strict: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if capturedGrammar != GBNFEncodingResponse {
		t.Errorf("expected encoding-specific GBNF grammar for encoding_response schema, got generic")
	}

	// json_schema with episode_synthesis name — should use episode-specific grammar
	_, err = p.Complete(ctx, CompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
		ResponseFormat: &ResponseFormat{
			Type: "json_schema",
			JSONSchema: &JSONSchema{
				Name:   "episode_synthesis",
				Strict: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if capturedGrammar != GBNFEpisodeSynthesis {
		t.Errorf("expected episode-specific GBNF grammar for episode_synthesis schema, got generic")
	}

	// json_schema with other name — should fall back to generic JSON grammar
	_, err = p.Complete(ctx, CompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
		ResponseFormat: &ResponseFormat{
			Type: "json_schema",
			JSONSchema: &JSONSchema{
				Name:   "other_schema",
				Strict: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if capturedGrammar != GBNFJSONObject {
		t.Errorf("expected generic GBNF JSON grammar for non-encoding schema")
	}
}

func TestFormatPrompt(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
	}
	got := formatPrompt(messages, "chatml")
	// ChatML format with /no_think for Qwen 3.5 thinking mode suppression
	expected := "<|im_start|>system\nYou are helpful. /no_think<|im_end|>\n<|im_start|>user\nHello<|im_end|>\n<|im_start|>assistant\n"
	if got != expected {
		t.Errorf("formatPrompt(chatml) mismatch:\ngot:  %q\nwant: %q", got, expected)
	}

	// Gemma format — system role mapped to user turn
	gotGemma := formatPrompt(messages, "gemma")
	expectedGemma := "<start_of_turn>user\nYou are helpful.<end_of_turn>\n<start_of_turn>user\nHello<end_of_turn>\n<start_of_turn>model\n"
	if gotGemma != expectedGemma {
		t.Errorf("formatPrompt(gemma) mismatch:\ngot:  %q\nwant: %q", gotGemma, expectedGemma)
	}
}

func TestEmbeddedProviderBatchEmbedEmpty(t *testing.T) {
	p := NewEmbeddedProvider(EmbeddedProviderConfig{
		ModelsDir:     "/tmp/models",
		ChatModelFile: "test.gguf",
	})

	ctx := context.Background()
	result, err := p.BatchEmbed(ctx, []string{})
	if err != nil {
		t.Fatalf("BatchEmbed with empty input failed: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d", len(result))
	}
}

func TestDeferLoadDoesNotLoadImmediately(t *testing.T) {
	dir := t.TempDir()
	chatFile := "chat.gguf"
	if err := writeTestFile(dir, chatFile); err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	loadCount := 0
	p := NewEmbeddedProvider(EmbeddedProviderConfig{
		ModelsDir:     dir,
		ChatModelFile: chatFile,
		MaxTokens:     256,
		Temperature:   0.3,
		MaxConcurrent: 1,
	})

	p.DeferLoad(func() Backend {
		loadCount++
		return &mockBackend{}
	})

	// After DeferLoad, no backend should be loaded
	if loadCount != 0 {
		t.Fatalf("DeferLoad triggered %d model loads, expected 0", loadCount)
	}

	// Health should report not ready (no model loaded yet)
	ctx := context.Background()
	if err := p.Health(ctx); err == nil {
		t.Fatal("expected Health to fail before first use with DeferLoad")
	}

	// ActiveModel should show not loaded
	status := p.ActiveModel()
	if status.Loaded {
		t.Fatal("ActiveModel.Loaded should be false before first use")
	}
}

func TestDeferLoadTriggersOnFirstComplete(t *testing.T) {
	dir := t.TempDir()
	chatFile := "chat.gguf"
	if err := writeTestFile(dir, chatFile); err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	loadCount := 0
	p := NewEmbeddedProvider(EmbeddedProviderConfig{
		ModelsDir:     dir,
		ChatModelFile: chatFile,
		MaxTokens:     256,
		Temperature:   0.3,
		MaxConcurrent: 1,
	})

	p.DeferLoad(func() Backend {
		loadCount++
		return &mockBackend{}
	})

	ctx := context.Background()

	// First Complete() triggers lazy load
	resp, err := p.Complete(ctx, CompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if resp.Content != `{"summary": "test"}` {
		t.Errorf("unexpected content: %s", resp.Content)
	}
	if loadCount != 1 {
		t.Fatalf("expected exactly 1 model load on first Complete, got %d", loadCount)
	}

	// Second Complete() should NOT trigger another load
	_, err = p.Complete(ctx, CompletionRequest{
		Messages: []Message{{Role: "user", Content: "again"}},
	})
	if err != nil {
		t.Fatalf("second Complete failed: %v", err)
	}
	if loadCount != 1 {
		t.Fatalf("expected still 1 model load after second Complete, got %d", loadCount)
	}
}

func TestDeferLoadTriggersOnFirstEmbed(t *testing.T) {
	dir := t.TempDir()
	chatFile := "chat.gguf"
	if err := writeTestFile(dir, chatFile); err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	loadCount := 0
	p := NewEmbeddedProvider(EmbeddedProviderConfig{
		ModelsDir:     dir,
		ChatModelFile: chatFile,
		MaxTokens:     256,
		Temperature:   0.3,
		MaxConcurrent: 1,
	})

	p.DeferLoad(func() Backend {
		loadCount++
		return &mockBackend{}
	})

	ctx := context.Background()

	// First Embed() triggers lazy load
	emb, err := p.Embed(ctx, "test")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(emb) != 3 {
		t.Errorf("expected 3-dim embedding, got %d", len(emb))
	}
	if loadCount != 1 {
		t.Fatalf("expected exactly 1 model load on first Embed, got %d", loadCount)
	}
}

func TestDeferLoadConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	chatFile := "chat.gguf"
	if err := writeTestFile(dir, chatFile); err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	var mu sync.Mutex
	loadCount := 0
	p := NewEmbeddedProvider(EmbeddedProviderConfig{
		ModelsDir:     dir,
		ChatModelFile: chatFile,
		MaxTokens:     256,
		Temperature:   0.3,
		MaxConcurrent: 10, // allow concurrent access
	})

	p.DeferLoad(func() Backend {
		mu.Lock()
		loadCount++
		mu.Unlock()
		return &mockBackend{}
	})

	ctx := context.Background()

	// Launch 10 goroutines that all call Complete concurrently
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.Complete(ctx, CompletionRequest{
				Messages: []Message{{Role: "user", Content: "hello"}},
			})
			if err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent Complete failed: %v", err)
	}

	// sync.Once guarantees exactly 1 load despite 10 concurrent callers
	mu.Lock()
	got := loadCount
	mu.Unlock()
	if got != 1 {
		t.Fatalf("expected exactly 1 model load with 10 concurrent callers, got %d", got)
	}
}

func TestEagerLoadSkipsEnsureLoaded(t *testing.T) {
	dir := t.TempDir()
	chatFile := "chat.gguf"
	if err := writeTestFile(dir, chatFile); err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	p := NewEmbeddedProvider(EmbeddedProviderConfig{
		ModelsDir:     dir,
		ChatModelFile: chatFile,
		MaxTokens:     256,
		Temperature:   0.3,
		MaxConcurrent: 1,
	})

	// Eager load (the daemon path)
	err := p.LoadModels(func() Backend { return &mockBackend{} })
	if err != nil {
		t.Fatalf("LoadModels failed: %v", err)
	}

	// deferredFactory should be nil — ensureLoaded is a no-op
	if p.deferredFactory != nil {
		t.Fatal("deferredFactory should be nil after eager LoadModels")
	}

	ctx := context.Background()
	resp, err := p.Complete(ctx, CompletionRequest{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if resp.Content != `{"summary": "test"}` {
		t.Errorf("unexpected content: %s", resp.Content)
	}
}

func writeTestFile(dir, name string) error {
	return os.WriteFile(dir+"/"+name, []byte("fake gguf data"), 0600)
}
