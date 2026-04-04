package agentutil

import "github.com/appsprout-dev/mnemonic/internal/mathutil"

// CosineSimilarity computes cosine similarity between two embedding vectors.
// Returns 0 if vectors are different lengths, empty, or have zero magnitude.
func CosineSimilarity(a, b []float32) float32 {
	return mathutil.CosineSimilarity(a, b)
}

// AverageVectors computes the element-wise average of a set of float32 vectors.
// All vectors must have the same dimension; mismatched vectors are skipped.
// Returns nil if the input is empty.
func AverageVectors(vecs [][]float32) []float32 {
	if len(vecs) == 0 {
		return nil
	}
	dim := len(vecs[0])
	avg := make([]float32, dim)
	for _, v := range vecs {
		if len(v) != dim {
			continue
		}
		for i, val := range v {
			avg[i] += val
		}
	}
	n := float32(len(vecs))
	for i := range avg {
		avg[i] /= n
	}
	return avg
}
