package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// APIProvider implements embedding.Provider using an OpenAI-compatible
// /v1/embeddings HTTP endpoint. This allows using external embedding
// services (LM Studio, Ollama, etc.) without the full llm.Provider.
type APIProvider struct {
	endpoint   string
	model      string
	apiKey     string
	httpClient *http.Client
	sem        chan struct{}
}

// NewAPIProvider creates a new API-based embedding provider.
// endpoint should be the base URL (e.g., "http://localhost:1234/v1").
// model is the embedding model name (e.g., "nomic-embed-text").
func NewAPIProvider(endpoint, model, apiKey string, timeout time.Duration, maxConcurrent int) *APIProvider {
	if maxConcurrent <= 0 {
		maxConcurrent = 2
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &APIProvider{
		endpoint:   endpoint,
		model:      model,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: timeout},
		sem:        make(chan struct{}, maxConcurrent),
	}
}

func (p *APIProvider) acquire(ctx context.Context) error {
	select {
	case p.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *APIProvider) release() {
	<-p.sem
}

func (p *APIProvider) setAuthHeader(req *http.Request) {
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
}

// Embed generates an embedding for a single text.
func (p *APIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := p.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}
	return embeddings[0], nil
}

// BatchEmbed generates embeddings for multiple texts in a single request.
func (p *APIProvider) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	if err := p.acquire(ctx); err != nil {
		return nil, fmt.Errorf("embedding concurrency limit reached: %w", err)
	}
	defer p.release()

	apiReq := embeddingRequest{
		Model: p.model,
		Input: texts,
	}

	reqBody, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embedding request: %w", err)
	}

	url := fmt.Sprintf("%s/embeddings", p.endpoint)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(reqBody)), nil
	}

	httpReq.Header.Set("Content-Type", "application/json")
	p.setAuthHeader(httpReq)

	httpResp, err := p.doWithRetry(httpReq)
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("embedding request returned http %d: %s", httpResp.StatusCode, string(body))
	}

	var apiResp embeddingResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode embedding response: %w", err)
	}

	embeddings := make([][]float32, len(texts))
	for _, embData := range apiResp.Data {
		if embData.Index < 0 || embData.Index >= len(embeddings) {
			return nil, fmt.Errorf("embedding index %d out of bounds", embData.Index)
		}
		embeddings[embData.Index] = embData.Embedding
	}

	return embeddings, nil
}

// Health checks if the embedding endpoint is reachable.
func (p *APIProvider) Health(ctx context.Context) error {
	url := fmt.Sprintf("%s/models", p.endpoint)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create health request: %w", err)
	}
	p.setAuthHeader(httpReq)

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("embedding provider unreachable at %s: %w", url, err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("embedding provider returned http %d", httpResp.StatusCode)
	}

	return nil
}

func (p *APIProvider) doWithRetry(req *http.Request) (*http.Response, error) {
	const maxRetries = 3
	delays := [3]time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			slog.Debug("retrying embedding request", "attempt", attempt, "url", req.URL.String())
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(delays[attempt-1]):
			}
			if req.GetBody != nil {
				body, err := req.GetBody()
				if err != nil {
					return nil, fmt.Errorf("failed to reset request body for retry: %w", err)
				}
				req.Body = body
			}
		}

		resp, err := p.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			continue
		}

		if resp.StatusCode >= 500 && attempt < maxRetries {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("embedding request failed after %d retries: %w", maxRetries, lastErr)
}

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingData struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type embeddingResponse struct {
	Data []embeddingData `json:"data"`
}
