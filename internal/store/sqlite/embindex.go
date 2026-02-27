package sqlite

import (
	"math"
	"sort"
	"sync"
)

// embEntry holds a memory ID and its embedding vector for fast similarity search.
type embEntry struct {
	id        string
	embedding []float32
	norm      float32 // precomputed L2 norm
}

// embeddingIndex is an in-memory index of embedding vectors for fast cosine similarity search.
// It avoids the O(n) full-table-scan + decode overhead of the naive approach by keeping
// only (id, embedding, precomputed-norm) in memory.
//
// Thread-safe via sync.RWMutex.
type embeddingIndex struct {
	mu      sync.RWMutex
	entries []embEntry
	byID    map[string]int // maps memory ID -> index in entries
}

// newEmbeddingIndex creates a new empty embedding index.
func newEmbeddingIndex() *embeddingIndex {
	return &embeddingIndex{
		entries: make([]embEntry, 0, 256),
		byID:    make(map[string]int, 256),
	}
}

// l2norm computes the L2 norm of a vector.
func l2norm(v []float32) float32 {
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	return float32(math.Sqrt(float64(sum)))
}

// Add inserts or replaces an embedding for the given memory ID.
func (idx *embeddingIndex) Add(id string, embedding []float32) {
	if len(embedding) == 0 {
		return
	}

	norm := l2norm(embedding)
	if norm == 0 {
		return
	}

	entry := embEntry{
		id:        id,
		embedding: embedding,
		norm:      norm,
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	if pos, exists := idx.byID[id]; exists {
		// Replace existing entry
		idx.entries[pos] = entry
	} else {
		// Append new entry
		idx.byID[id] = len(idx.entries)
		idx.entries = append(idx.entries, entry)
	}
}

// Remove removes an embedding by memory ID.
func (idx *embeddingIndex) Remove(id string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	pos, exists := idx.byID[id]
	if !exists {
		return
	}

	// Swap with last element for O(1) removal
	last := len(idx.entries) - 1
	if pos != last {
		idx.entries[pos] = idx.entries[last]
		idx.byID[idx.entries[pos].id] = pos
	}
	idx.entries = idx.entries[:last]
	delete(idx.byID, id)
}

// searchResult holds a memory ID and its cosine similarity score.
type searchResult struct {
	id    string
	score float32
}

// Search finds the top-k most similar embeddings to the query vector.
// Returns results sorted by descending similarity score.
func (idx *embeddingIndex) Search(query []float32, k int) []searchResult {
	if len(query) == 0 || k <= 0 {
		return nil
	}

	queryNorm := l2norm(query)
	if queryNorm == 0 {
		return nil
	}

	idx.mu.RLock()
	n := len(idx.entries)
	if n == 0 {
		idx.mu.RUnlock()
		return nil
	}

	// Compute cosine similarity against all entries
	type scored struct {
		id    string
		score float32
	}
	results := make([]scored, 0, n)
	queryDim := len(query)

	for i := 0; i < n; i++ {
		e := &idx.entries[i]
		if len(e.embedding) != queryDim {
			continue
		}

		// Inline cosine similarity with precomputed norms
		var dot float32
		for j := 0; j < queryDim; j++ {
			dot += query[j] * e.embedding[j]
		}
		sim := dot / (queryNorm * e.norm)

		results = append(results, scored{id: e.id, score: sim})
	}
	idx.mu.RUnlock()

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Limit to k
	if len(results) > k {
		results = results[:k]
	}

	out := make([]searchResult, len(results))
	for i, r := range results {
		out[i] = searchResult(r)
	}
	return out
}

// Len returns the number of entries in the index.
func (idx *embeddingIndex) Len() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.entries)
}
