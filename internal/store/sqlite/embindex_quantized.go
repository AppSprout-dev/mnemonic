package sqlite

import (
	"math"
	"sort"
	"sync"

	"github.com/appsprout-dev/mnemonic/internal/embedding"
)

// quantizedIndex is an in-memory index that uses TurboQuant 1-bit compression
// for fast approximate nearest neighbor search. It maintains quantized copies
// of embeddings alongside the float32 originals for two-stage retrieval:
// 1. Fast pre-filter using XNOR + popcount on quantized vectors
// 2. Exact cosine re-ranking on float32 vectors for top candidates
type quantizedIndex struct {
	mu        sync.RWMutex
	quantizer *embedding.Quantizer
	entries   map[string]quantizedEntry
	dims      int // expected embedding dimension (0 = not yet determined)
}

type quantizedEntry struct {
	qvec      embedding.QuantizedVector
	embedding []float32 // original for exact re-ranking
	norm      float32   // precomputed L2 norm
}

// newQuantizedIndex creates a quantized index. The quantizer is initialized
// lazily on the first Add call, since we don't know the embedding dimension
// until then. Seed 42 is used for deterministic projection matrix generation.
func newQuantizedIndex() *quantizedIndex {
	return &quantizedIndex{
		entries: make(map[string]quantizedEntry, 256),
	}
}

func (qi *quantizedIndex) initQuantizer(dims int) {
	qi.dims = dims
	qi.quantizer = embedding.NewQuantizer(dims, 42) // fixed seed for reproducibility
}

// Add inserts or replaces an embedding in the quantized index.
func (qi *quantizedIndex) Add(id string, emb []float32) {
	if len(emb) == 0 {
		return
	}

	norm := l2norm(emb)
	if norm == 0 {
		return
	}

	qi.mu.Lock()
	defer qi.mu.Unlock()

	// Initialize quantizer on first vector
	if qi.quantizer == nil {
		qi.initQuantizer(len(emb))
	}

	// Skip vectors with wrong dimensions
	if len(emb) != qi.dims {
		return
	}

	qi.entries[id] = quantizedEntry{
		qvec:      qi.quantizer.Quantize(emb),
		embedding: emb,
		norm:      norm,
	}
}

// Remove removes an entry from the index.
func (qi *quantizedIndex) Remove(id string) {
	qi.mu.Lock()
	defer qi.mu.Unlock()
	delete(qi.entries, id)
}

// Search finds the top-k most similar embeddings using two-stage retrieval:
// Stage 1: TurboQuant approximate similarity on ALL vectors (very fast)
// Stage 2: Exact cosine similarity on top candidates (accurate)
//
// The candidateMultiplier controls how many candidates pass stage 1.
// Default: 4x the requested k (e.g., k=10 → 40 candidates for re-ranking).
func (qi *quantizedIndex) Search(query []float32, k int) []searchResult {
	if len(query) == 0 || k <= 0 {
		return nil
	}

	queryNorm := l2norm(query)
	if queryNorm == 0 {
		return nil
	}

	qi.mu.RLock()
	defer qi.mu.RUnlock()

	if qi.quantizer == nil || len(qi.entries) == 0 {
		return nil
	}

	// Skip if query dimensions don't match
	if len(query) != qi.dims {
		return nil
	}

	// Stage 1: Quantize query and do fast approximate search
	qquery := qi.quantizer.Quantize(query)
	candidateLimit := k * 20
	if candidateLimit < 100 {
		candidateLimit = 100
	}

	type approxResult struct {
		id    string
		score float32
	}
	approx := make([]approxResult, 0, len(qi.entries))
	for id, entry := range qi.entries {
		sim := embedding.Similarity(qquery, entry.qvec)
		approx = append(approx, approxResult{id: id, score: sim})
	}

	// Partial sort: only need top candidateLimit
	sort.Slice(approx, func(i, j int) bool {
		return approx[i].score > approx[j].score
	})
	if len(approx) > candidateLimit {
		approx = approx[:candidateLimit]
	}

	// Stage 2: Exact cosine similarity on candidates
	results := make([]searchResult, 0, len(approx))
	for _, candidate := range approx {
		entry := qi.entries[candidate.id]

		// Exact cosine similarity
		var dot float32
		for j := range query {
			dot += query[j] * entry.embedding[j]
		}
		exactSim := dot / (queryNorm * entry.norm)

		results = append(results, searchResult{id: candidate.id, score: exactSim})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})
	if len(results) > k {
		results = results[:k]
	}

	return results
}

// Len returns the number of entries.
func (qi *quantizedIndex) Len() int {
	qi.mu.RLock()
	defer qi.mu.RUnlock()
	return len(qi.entries)
}

// Stats returns compression statistics.
func (qi *quantizedIndex) Stats() (count int, dims int, origBytes int, quantBytes int) {
	qi.mu.RLock()
	defer qi.mu.RUnlock()

	count = len(qi.entries)
	dims = qi.dims
	if count > 0 && dims > 0 {
		origBytes = count * dims * 4 // float32 = 4 bytes
		bitsPerVec := (dims + 63) / 64 * 8
		quantBytes = count * (bitsPerVec + 4) // bits + norm
	}
	return
}

// cosineSimilarity computes exact cosine similarity (used as fallback).
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return float32(dot / denom)
}
