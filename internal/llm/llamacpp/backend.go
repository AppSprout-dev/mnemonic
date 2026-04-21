//go:build llamacpp

package llamacpp

/*
#cgo CFLAGS: -I${SRCDIR}/csrc
#cgo LDFLAGS: ${SRCDIR}/csrc/bridge.o -L${SRCDIR}/../../../third_party/llama.cpp/build/src -L${SRCDIR}/../../../third_party/llama.cpp/build/ggml/src -lllama -lggml -lggml-base -lggml-cpu -lm -lstdc++ -lpthread -fopenmp
#include "csrc/bridge.h"
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/appsprout-dev/mnemonic/internal/llm"
)

// Backend implements llm.Backend using llama.cpp via CGo.
// All inference calls are serialized via a mutex because llama.cpp
// contexts are not thread-safe.
type Backend struct {
	mu    sync.Mutex
	model *C.mnm_model
}

// NewBackend creates a new llama.cpp backend instance.
func NewBackend() llm.Backend {
	return &Backend{}
}

func (b *Backend) LoadModel(path string, opts llm.BackendOptions) error {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	params := C.mnm_model_params{
		context_size: C.int(opts.ContextSize),
		gpu_layers:   C.int(opts.GPULayers),
		threads:      C.int(opts.Threads),
		batch_size:   C.int(opts.BatchSize),
	}

	b.model = C.mnm_load_model(cpath, params)
	if b.model == nil {
		return fmt.Errorf("failed to load model: %s", path)
	}
	return nil
}

func (b *Backend) Complete(ctx context.Context, req llm.BackendCompletionRequest) (llm.BackendCompletionResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.model == nil {
		return llm.BackendCompletionResponse{}, fmt.Errorf("model not loaded")
	}

	// Ablate envelope (EXP-039 / Feature #4). When AblateLayers is non-empty
	// we snapshot those layers' gate_biases, zero them, run the completion,
	// and restore the prior values — all under the single b.mu acquisition
	// so concurrent traffic never observes the zeroed state.
	if len(req.AblateLayers) > 0 {
		acc := unlockedGateAccessor{b: b}
		resp, err := llm.ApplyAblation(acc, req.AblateLayers, func() (llm.BackendCompletionResponse, error) {
			return b.completeLocked(ctx, req)
		})
		if err != nil {
			return llm.BackendCompletionResponse{}, err
		}
		resp.AblatedLayers = append([]int(nil), req.AblateLayers...)
		return resp, nil
	}

	return b.completeLocked(ctx, req)
}

// completeLocked runs a completion against the loaded model. Caller must
// hold b.mu. Extracted from Complete so the ablate envelope can wrap it.
func (b *Backend) completeLocked(_ context.Context, req llm.BackendCompletionRequest) (llm.BackendCompletionResponse, error) {
	cprompt := C.CString(req.Prompt)
	defer C.free(unsafe.Pointer(cprompt))

	var cgrammar *C.char
	if req.Grammar != "" {
		cgrammar = C.CString(req.Grammar)
		defer C.free(unsafe.Pointer(cgrammar))
	}

	// Build stop sequences
	var cstop **C.char
	nStop := len(req.Stop)
	if nStop > 0 {
		cstopSlice := make([]*C.char, nStop)
		for i, s := range req.Stop {
			cstopSlice[i] = C.CString(s)
			defer C.free(unsafe.Pointer(cstopSlice[i]))
		}
		cstop = (**C.char)(unsafe.Pointer(&cstopSlice[0]))
	}

	result := C.mnm_complete(
		b.model,
		cprompt,
		C.int(req.MaxTokens),
		C.float(req.Temperature),
		C.float(req.TopP),
		cgrammar,
		cstop,
		C.int(nStop),
	)

	var text string
	if result.text != nil {
		text = C.GoString(result.text)
		C.mnm_free_string(result.text)
	}

	return llm.BackendCompletionResponse{
		Text:             text,
		PromptTokens:     int(result.prompt_tokens),
		CompletionTokens: int(result.completion_tokens),
		MeanProb:         float32(result.mean_prob),
		MinProb:          float32(result.min_prob),
	}, nil
}

func (b *Backend) Embed(_ context.Context, text string) ([]float32, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.model == nil {
		return nil, fmt.Errorf("model not loaded")
	}

	ctext := C.CString(text)
	defer C.free(unsafe.Pointer(ctext))

	result := C.mnm_embed(b.model, ctext)
	if result.data == nil {
		return nil, fmt.Errorf("embedding extraction failed (model may not support embeddings)")
	}
	defer C.mnm_free_floats(result.data)

	dims := int(result.dims)
	embedding := make([]float32, dims)
	cSlice := unsafe.Slice((*float32)(unsafe.Pointer(result.data)), dims)
	copy(embedding, cSlice)

	return embedding, nil
}

func (b *Backend) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		emb, err := b.Embed(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("embedding text %d: %w", i, err)
		}
		results[i] = emb
	}
	return results, nil
}

func (b *Backend) Close() error {
	if b.model != nil {
		C.mnm_free_model(b.model)
		b.model = nil
	}
	return nil
}

// --- SPLICE: Spoke tensor hot-swap ---

// SetSpokeGateBias sets the gate bias for a single spoke layer.
func (b *Backend) SetSpokeGateBias(layer int, value float32) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.setSpokeGateBiasLocked(layer, value)
}

// setSpokeGateBiasLocked mutates the gate bias with b.mu already held.
// Used by the ablate envelope inside Complete.
func (b *Backend) setSpokeGateBiasLocked(layer int, value float32) error {
	if b.model == nil {
		return fmt.Errorf("model not loaded")
	}
	rc := C.mnm_set_spoke_gate_bias(b.model, C.int(layer), C.float(value))
	if rc != 0 {
		return fmt.Errorf("set spoke gate bias failed (layer=%d, rc=%d)", layer, rc)
	}
	return nil
}

// SetSpokeTensor sets arbitrary tensor data by name (raw bytes, must match tensor size).
func (b *Backend) SetSpokeTensor(name string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.model == nil {
		return fmt.Errorf("model not loaded")
	}

	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	rc := C.mnm_set_spoke_tensor(b.model, cname, unsafe.Pointer(&data[0]), C.int(len(data)))
	if rc != 0 {
		return fmt.Errorf("set spoke tensor failed (name=%s, rc=%d)", name, rc)
	}
	return nil
}

// SetSpokeTensorF32 sets tensor data from F32 floats with automatic quantization.
// The data is quantized to the tensor's native type (F16, RQ4, etc.) before writing.
// Pins the goroutine to the current OS thread for ROCm/HIP context affinity.
func (b *Backend) SetSpokeTensorF32(name string, data []float32) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.model == nil {
		return fmt.Errorf("model not loaded")
	}

	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	rc := C.mnm_set_spoke_tensor_f32(b.model, cname, (*C.float)(unsafe.Pointer(&data[0])), C.int(len(data)))
	if rc != 0 {
		return fmt.Errorf("set spoke tensor f32 failed (name=%s, rc=%d)", name, rc)
	}
	return nil
}

// GetSpokeGateBias reads the current gate bias for a spoke layer.
func (b *Backend) GetSpokeGateBias(layer int) (float32, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.getSpokeGateBiasLocked(layer)
}

// getSpokeGateBiasLocked reads the gate bias with b.mu already held.
// Used by the ablate envelope inside Complete.
func (b *Backend) getSpokeGateBiasLocked(layer int) (float32, error) {
	if b.model == nil {
		return 0, fmt.Errorf("model not loaded")
	}

	name := fmt.Sprintf("blk.%d.spoke.gate_bias", layer)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var value float32
	rc := C.mnm_get_spoke_tensor(b.model, cname, unsafe.Pointer(&value), C.int(4))
	if rc != 0 {
		return 0, fmt.Errorf("get spoke gate bias failed (layer=%d, rc=%d)", layer, int(rc))
	}
	return value, nil
}

// unlockedGateAccessor adapts Backend's *locked helpers to llm.GateBiasAccessor.
// The ablate envelope passes this in while already holding b.mu so the whole
// snapshot / zero / complete / restore sequence is one critical section.
type unlockedGateAccessor struct {
	b *Backend
}

func (a unlockedGateAccessor) GetGateBias(layer int) (float32, error) {
	return a.b.getSpokeGateBiasLocked(layer)
}

func (a unlockedGateAccessor) SetGateBias(layer int, value float32) error {
	return a.b.setSpokeGateBiasLocked(layer, value)
}
