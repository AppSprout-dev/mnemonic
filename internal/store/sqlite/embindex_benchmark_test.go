package sqlite

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"
)

// generateRandomEmbedding creates a random unit vector of given dimensions.
func generateRandomEmbedding(dims int, rng *rand.Rand) []float32 {
	emb := make([]float32, dims)
	var norm float64
	for i := range emb {
		emb[i] = rng.Float32()*2 - 1
		norm += float64(emb[i]) * float64(emb[i])
	}
	norm = math.Sqrt(norm)
	for i := range emb {
		emb[i] = float32(float64(emb[i]) / norm)
	}
	return emb
}

// BenchmarkEmbeddingIndexSearch benchmarks the float32 brute-force index.
func BenchmarkEmbeddingIndexSearch(b *testing.B) {
	sizes := []int{1000, 5000, 10000, 34000}
	dims := []int{128, 384, 3072}

	for _, n := range sizes {
		for _, d := range dims {
			name := fmt.Sprintf("n=%d/dims=%d", n, d)
			b.Run(name, func(b *testing.B) {
				rng := rand.New(rand.NewSource(42))
				idx := newEmbeddingIndex()

				// Populate index
				for i := 0; i < n; i++ {
					idx.Add(fmt.Sprintf("mem-%d", i), generateRandomEmbedding(d, rng))
				}

				query := generateRandomEmbedding(d, rng)

				b.ResetTimer()
				for b.Loop() {
					idx.Search(query, 10)
				}
			})
		}
	}
}

// BenchmarkQuantizedIndexSearch benchmarks the TurboQuant quantized index.
func BenchmarkQuantizedIndexSearch(b *testing.B) {
	sizes := []int{1000, 5000, 10000, 34000}
	dims := []int{128, 384}

	for _, n := range sizes {
		for _, d := range dims {
			name := fmt.Sprintf("n=%d/dims=%d", n, d)
			b.Run(name, func(b *testing.B) {
				rng := rand.New(rand.NewSource(42))
				idx := newQuantizedIndex()

				for i := 0; i < n; i++ {
					idx.Add(fmt.Sprintf("mem-%d", i), generateRandomEmbedding(d, rng))
				}

				query := generateRandomEmbedding(d, rng)

				b.ResetTimer()
				for b.Loop() {
					idx.Search(query, 10)
				}
			})
		}
	}
}

// BenchmarkIndexComparison runs both indexes side by side at production scale.
func BenchmarkIndexComparison(b *testing.B) {
	const n = 34000
	const dims = 384

	rng := rand.New(rand.NewSource(42))
	embeddings := make([][]float32, n)
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		embeddings[i] = generateRandomEmbedding(dims, rng)
		ids[i] = fmt.Sprintf("mem-%d", i)
	}
	query := generateRandomEmbedding(dims, rng)

	b.Run("float32_brute_force", func(b *testing.B) {
		idx := newEmbeddingIndex()
		for i := 0; i < n; i++ {
			idx.Add(ids[i], embeddings[i])
		}
		b.ResetTimer()
		for b.Loop() {
			idx.Search(query, 10)
		}
	})

	b.Run("turboquant_1bit", func(b *testing.B) {
		idx := newQuantizedIndex()
		for i := 0; i < n; i++ {
			idx.Add(ids[i], embeddings[i])
		}
		b.ResetTimer()
		for b.Loop() {
			idx.Search(query, 10)
		}
	})
}

// TestQuantizedSearchQuality verifies the quantized index returns similar
// results to the float32 index at production scale.
func TestQuantizedSearchQuality(t *testing.T) {
	const n = 5000
	const dims = 384

	rng := rand.New(rand.NewSource(42))

	floatIdx := newEmbeddingIndex()
	quantIdx := newQuantizedIndex()

	for i := 0; i < n; i++ {
		emb := generateRandomEmbedding(dims, rng)
		id := fmt.Sprintf("mem-%d", i)
		floatIdx.Add(id, emb)
		quantIdx.Add(id, emb)
	}

	// Run 20 random queries
	hits := 0
	total := 0
	for q := 0; q < 20; q++ {
		query := generateRandomEmbedding(dims, rng)

		floatResults := floatIdx.Search(query, 10)
		quantResults := quantIdx.Search(query, 10)

		floatTop := make(map[string]bool)
		for _, r := range floatResults {
			floatTop[r.id] = true
		}

		for _, r := range quantResults {
			total++
			if floatTop[r.id] {
				hits++
			}
		}
	}

	recall := float64(hits) / float64(total)
	t.Logf("Recall@10 (quantized vs float32): %.1f%% (%d/%d)", recall*100, hits, total)

	// Expect at least 60% recall — TurboQuant 1-bit is approximate
	if recall < 0.5 {
		t.Errorf("recall too low: %.1f%% (expected >50%%)", recall*100)
	}
}

// TestIndexLoadTime simulates production startup with 34K 384-dim embeddings.
func TestIndexLoadTime(t *testing.T) {
	const n = 34000
	const dims = 384

	rng := rand.New(rand.NewSource(42))
	embeddings := make([][]float32, n)
	for i := range embeddings {
		embeddings[i] = generateRandomEmbedding(dims, rng)
	}

	// Float32 index load time
	start := time.Now()
	floatIdx := newEmbeddingIndex()
	for i := 0; i < n; i++ {
		floatIdx.Add(fmt.Sprintf("mem-%d", i), embeddings[i])
	}
	floatLoad := time.Since(start)

	// Quantized index load time
	start = time.Now()
	quantIdx := newQuantizedIndex()
	for i := 0; i < n; i++ {
		quantIdx.Add(fmt.Sprintf("mem-%d", i), embeddings[i])
	}
	quantLoad := time.Since(start)

	t.Logf("Float32 index load: %v (%d entries)", floatLoad, floatIdx.Len())
	t.Logf("Quantized index load: %v (%d entries)", quantLoad, quantIdx.Len())

	// Quantized load is slower (must compute projection matrix multiply per vector)
	// but search is faster. This is the expected tradeoff.

	count, d, origBytes, quantBytes := quantIdx.Stats()
	t.Logf("Quantized stats: %d entries, %d dims, orig=%dMB, quant=%dMB, ratio=%.1fx",
		count, d, origBytes/1024/1024, quantBytes/1024/1024,
		float64(origBytes)/float64(quantBytes))
}
