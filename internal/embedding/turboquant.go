package embedding

import (
	"math"
	"math/bits"
	"math/rand"
)

// QuantizedVector stores a sign-quantized random projection of an embedding vector.
// This is the QJL (Quantized Johnson-Lindenstrauss) stage of TurboQuant:
// each dimension is reduced to a single sign bit after projection through a
// random Gaussian matrix, preserving angular similarity while compressing
// 384-dim float32 (1536 bytes) down to ~52 bytes (48 bits + 4 norm).
type QuantizedVector struct {
	Bits []uint64 // sign-quantized projected vector, packed as bit array
	Norm float32  // L2 norm of the original vector (for similarity scaling)
	Dims int      // original dimension count (for validation)
}

// Quantizer holds the random projection matrix for QJL quantization.
// After creation via NewQuantizer, it is read-only and safe for concurrent use.
type Quantizer struct {
	projMatrix []float32 // random Gaussian projection matrix (dims x dims), row-major
	dims       int       // vector dimensionality
	seed       int64     // seed used to generate the projection matrix
}

// NewQuantizer creates a Quantizer with a random Gaussian projection matrix
// of size dims x dims. Entries are sampled from N(0, 1/sqrt(dims)) using
// the provided seed for reproducibility.
func NewQuantizer(dims int, seed int64) *Quantizer {
	rng := rand.New(rand.NewSource(seed))
	scale := 1.0 / math.Sqrt(float64(dims))
	matrix := make([]float32, dims*dims)
	for i := range matrix {
		matrix[i] = float32(rng.NormFloat64() * scale)
	}
	return &Quantizer{
		projMatrix: matrix,
		dims:       dims,
		seed:       seed,
	}
}

// Quantize compresses a float32 embedding vector into a QuantizedVector.
// It computes the L2 norm, projects through the random matrix, sign-quantizes
// each projected component, and packs the sign bits into uint64s.
func (q *Quantizer) Quantize(vec []float32) QuantizedVector {
	dims := q.dims

	// Compute L2 norm of the input vector.
	var normSq float64
	for _, v := range vec {
		normSq += float64(v) * float64(v)
	}
	norm := float32(math.Sqrt(normSq))

	// Project: projected[i] = dot(projMatrix[i*dims : (i+1)*dims], vec)
	signs := make([]bool, dims)
	for i := 0; i < dims; i++ {
		var dot float64
		row := q.projMatrix[i*dims : (i+1)*dims]
		for j := 0; j < dims; j++ {
			dot += float64(row[j]) * float64(vec[j])
		}
		signs[i] = dot >= 0
	}

	return QuantizedVector{
		Bits: packBits(signs),
		Norm: norm,
		Dims: dims,
	}
}

// Similarity estimates the cosine similarity between two quantized vectors
// using XNOR + popcount on the packed sign bits. The result approximates
// cosine similarity for unit-normalized input vectors: cos(a,b) ~ 2*(agreement/total) - 1.
func Similarity(a, b QuantizedVector) float32 {
	agreement := bitAgreement(a.Bits, b.Bits, a.Dims)
	totalBits := a.Dims

	// Cosine estimate from sign agreement ratio.
	// For unit vectors: cos(theta) ~ cos(pi * (1 - agreement/total))
	// Linear approximation: 2 * agreement/total - 1
	estimate := 2.0*float32(agreement)/float32(totalBits) - 1.0
	return estimate
}

// packBits packs a slice of booleans into a []uint64 bit array.
// Bit i is stored as bit (i % 64) of element (i / 64).
func packBits(signs []bool) []uint64 {
	n := (len(signs) + 63) / 64
	packed := make([]uint64, n)
	for i, s := range signs {
		if s {
			packed[i/64] |= 1 << uint(i%64)
		}
	}
	return packed
}

// bitAgreement counts how many of the first n sign bits agree between two
// packed bit arrays using XNOR + popcount (hardware-accelerated via OnesCount64).
func bitAgreement(a, b []uint64, n int) int {
	agreement := 0
	fullWords := n / 64
	for i := 0; i < fullWords; i++ {
		xnor := ^(a[i] ^ b[i])
		agreement += bits.OnesCount64(xnor)
	}

	// Handle trailing bits in the last word.
	rem := n % 64
	if rem > 0 {
		xnor := ^(a[fullWords] ^ b[fullWords])
		// Mask off bits beyond the valid range.
		mask := uint64((1 << uint(rem)) - 1)
		agreement += bits.OnesCount64(xnor & mask)
	}
	return agreement
}
