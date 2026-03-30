package embedding

import (
	"math"
	"math/rand"
	"testing"
)

func TestPackBits(t *testing.T) {
	signs := make([]bool, 128)
	// Set every other bit.
	for i := 0; i < 128; i += 2 {
		signs[i] = true
	}
	packed := packBits(signs)
	if len(packed) != 2 {
		t.Fatalf("expected 2 uint64s for 128 bits, got %d", len(packed))
	}
	// Every other bit set = 0x5555555555555555
	for i, word := range packed {
		if word != 0x5555555555555555 {
			t.Errorf("packed[%d] = %016x, want 0x5555555555555555", i, word)
		}
	}
}

func TestPackBitsRoundTrip(t *testing.T) {
	signs := []bool{true, false, true, true, false, false, true, false}
	packed := packBits(signs)
	// Verify each bit.
	for i, want := range signs {
		got := (packed[i/64]>>uint(i%64))&1 == 1
		if got != want {
			t.Errorf("bit %d: got %v, want %v", i, got, want)
		}
	}
}

func TestQuantizeKnownVector(t *testing.T) {
	dims := 64
	q := NewQuantizer(dims, 42)

	vec := make([]float32, dims)
	for i := range vec {
		vec[i] = float32(i) / float32(dims)
	}
	qv := q.Quantize(vec)

	if qv.Dims != dims {
		t.Errorf("Dims = %d, want %d", qv.Dims, dims)
	}
	if qv.Norm <= 0 {
		t.Error("Norm should be positive for non-zero vector")
	}

	// Verify bit packing length.
	expectedWords := (dims + 63) / 64
	if len(qv.Bits) != expectedWords {
		t.Errorf("Bits length = %d, want %d", len(qv.Bits), expectedWords)
	}
}

func TestSimilarityIdenticalVectors(t *testing.T) {
	dims := 384
	q := NewQuantizer(dims, 42)

	vec := makeUnitVector(dims, 99)
	a := q.Quantize(vec)
	b := q.Quantize(vec)

	sim := Similarity(a, b)
	if sim < 0.99 {
		t.Errorf("identical vectors: similarity = %f, want ~1.0", sim)
	}
}

func TestSimilarityOrthogonalVectors(t *testing.T) {
	dims := 384
	q := NewQuantizer(dims, 42)

	// Create two orthogonal vectors: one in first half, one in second half.
	a := make([]float32, dims)
	b := make([]float32, dims)
	for i := 0; i < dims/2; i++ {
		a[i] = 1.0
	}
	for i := dims / 2; i < dims; i++ {
		b[i] = 1.0
	}
	normalize(a)
	normalize(b)

	qa := q.Quantize(a)
	qb := q.Quantize(b)

	sim := Similarity(qa, qb)
	if math.Abs(float64(sim)) > 0.15 {
		t.Errorf("orthogonal vectors: similarity = %f, want ~0.0 (tolerance 0.15)", sim)
	}
}

func TestSimilarityPreservesOrdering(t *testing.T) {
	dims := 384
	q := NewQuantizer(dims, 42)

	// Use structured vectors with clear similarity differences rather than
	// random vectors which tend to be near-orthogonal in high dimensions.
	// Anchor is a random unit vector; close vectors share most components,
	// far vectors share few.
	rng := rand.New(rand.NewSource(123))
	anchor := makeUnitVector(dims, 0)

	// Generate vectors at controlled distances from anchor by blending.
	type testVec struct {
		vec   []float32
		blend float32 // higher = more similar to anchor
	}
	blends := []float32{0.9, 0.7, 0.5, 0.3, 0.1}
	tvecs := make([]testVec, len(blends))
	for i, blend := range blends {
		noise := make([]float32, dims)
		for j := range noise {
			noise[j] = float32(rng.NormFloat64())
		}
		normalize(noise)
		vec := make([]float32, dims)
		for j := range vec {
			vec[j] = blend*anchor[j] + (1-blend)*noise[j]
		}
		normalize(vec)
		tvecs[i] = testVec{vec: vec, blend: blend}
	}

	// Verify that higher-blend vectors are rated more similar.
	violations := 0
	comparisons := 0
	qa := q.Quantize(anchor)
	for i := 0; i < len(tvecs); i++ {
		for j := i + 1; j < len(tvecs); j++ {
			trueSim1 := cosineSim(anchor, tvecs[i].vec)
			trueSim2 := cosineSim(anchor, tvecs[j].vec)
			if math.Abs(float64(trueSim1-trueSim2)) < 0.1 {
				continue // skip near-ties
			}

			q1 := q.Quantize(tvecs[i].vec)
			q2 := q.Quantize(tvecs[j].vec)
			tqSim1 := Similarity(qa, q1)
			tqSim2 := Similarity(qa, q2)

			comparisons++
			if (trueSim1 > trueSim2) != (tqSim1 > tqSim2) {
				violations++
			}
		}
	}

	if comparisons == 0 {
		t.Fatal("no valid comparisons made")
	}
	violationRate := float64(violations) / float64(comparisons)
	t.Logf("ordering violations: %d/%d (%.1f%%)", violations, comparisons, violationRate*100)
	if violationRate > 0.2 {
		t.Errorf("ordering violation rate %.1f%% exceeds 20%% threshold", violationRate*100)
	}
}

func TestCompressionRatio(t *testing.T) {
	dims := 384
	originalBytes := dims * 4 // float32 = 4 bytes

	// QuantizedVector storage: ceil(384/64) * 8 bytes for bits + 4 bytes for norm
	bitsWords := (dims + 63) / 64
	compressedBytes := bitsWords*8 + 4 // uint64 = 8 bytes each, plus float32 norm

	ratio := float64(originalBytes) / float64(compressedBytes)
	t.Logf("original: %d bytes, compressed: %d bytes, ratio: %.1fx", originalBytes, compressedBytes, ratio)

	if compressedBytes > 60 {
		t.Errorf("compressed size %d bytes exceeds 60 byte budget", compressedBytes)
	}
	if ratio < 20 {
		t.Errorf("compression ratio %.1fx is below 20x minimum", ratio)
	}
}

func TestDeterminism(t *testing.T) {
	dims := 384
	seed := int64(42)

	q1 := NewQuantizer(dims, seed)
	q2 := NewQuantizer(dims, seed)

	// Projection matrices should be identical.
	for i := range q1.projMatrix {
		if q1.projMatrix[i] != q2.projMatrix[i] {
			t.Fatalf("projection matrix differs at index %d: %f vs %f", i, q1.projMatrix[i], q2.projMatrix[i])
		}
	}

	// Quantization should be identical.
	vec := makeUnitVector(dims, 99)
	a := q1.Quantize(vec)
	b := q2.Quantize(vec)

	if a.Norm != b.Norm {
		t.Errorf("norms differ: %f vs %f", a.Norm, b.Norm)
	}
	for i := range a.Bits {
		if a.Bits[i] != b.Bits[i] {
			t.Errorf("bits differ at word %d", i)
		}
	}
}

func TestDifferentSeedsDifferentMatrices(t *testing.T) {
	dims := 128
	q1 := NewQuantizer(dims, 1)
	q2 := NewQuantizer(dims, 2)

	same := 0
	for i := range q1.projMatrix {
		if q1.projMatrix[i] == q2.projMatrix[i] {
			same++
		}
	}
	// With different seeds, virtually no entries should match.
	if float64(same)/float64(len(q1.projMatrix)) > 0.01 {
		t.Errorf("different seeds produced %.1f%% matching entries", float64(same)/float64(len(q1.projMatrix))*100)
	}
}

func TestBitAgreementEdgeCases(t *testing.T) {
	// All bits agree.
	a := []uint64{0xFFFFFFFFFFFFFFFF, 0xFFFFFFFFFFFFFFFF}
	b := []uint64{0xFFFFFFFFFFFFFFFF, 0xFFFFFFFFFFFFFFFF}
	if got := bitAgreement(a, b, 128); got != 128 {
		t.Errorf("all-ones agreement = %d, want 128", got)
	}

	// No bits agree.
	c := []uint64{0xFFFFFFFFFFFFFFFF}
	d := []uint64{0x0000000000000000}
	if got := bitAgreement(c, d, 64); got != 0 {
		t.Errorf("none agreement = %d, want 0", got)
	}

	// Partial word (e.g., 70 bits = 1 full word + 6 trailing).
	e := []uint64{0xFFFFFFFFFFFFFFFF, 0x3F} // all ones in first 70 bits
	f := []uint64{0xFFFFFFFFFFFFFFFF, 0x3F}
	if got := bitAgreement(e, f, 70); got != 70 {
		t.Errorf("partial word agreement = %d, want 70", got)
	}
}

func BenchmarkSimilarity(b *testing.B) {
	dims := 384
	q := NewQuantizer(dims, 42)
	v1 := makeUnitVector(dims, 1)
	v2 := makeUnitVector(dims, 2)
	qv1 := q.Quantize(v1)
	qv2 := q.Quantize(v2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Similarity(qv1, qv2)
	}
}

func BenchmarkQuantize(b *testing.B) {
	dims := 384
	q := NewQuantizer(dims, 42)
	vec := makeUnitVector(dims, 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Quantize(vec)
	}
}

// --- helpers ---

func makeUnitVector(dims int, seed int64) []float32 {
	rng := rand.New(rand.NewSource(seed))
	vec := make([]float32, dims)
	for i := range vec {
		vec[i] = float32(rng.NormFloat64())
	}
	normalize(vec)
	return vec
}

func normalize(vec []float32) {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	norm := math.Sqrt(sum)
	if norm > 0 {
		for i := range vec {
			vec[i] = float32(float64(vec[i]) / norm)
		}
	}
}

func cosineSim(a, b []float32) float32 {
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
