# RQ4 Inference 200 tok/s Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reach 200+ tok/s autoregressive generation with BetaQ RQ4 on RX 7800 XT (RDNA 3, gfx1101)

**Architecture:** Eliminate HIP dispatch overhead through kernel fusion — the GPU is idle 69% of the time due to 1,542 kernel launches per token at 14us average gap. The mmvq compute itself (3.01ms/tok) implies 332 tok/s potential. Closing the gap requires fusing the 1,281 non-matmul dispatches into fewer, larger kernels.

**Tech Stack:** HIP/ROCm 7.2.1, C++/CUDA kernel code in llama.cpp fork (ggml-cuda/), gfx1101 ISA, dp4a SIMD, BetaQ codebook

**Current State:** 100.6 tok/s (after get_rows fix + nwarps whitelist + clock lock)

**Profiling Baseline (rocprofv3, 2026-04-13):**

| Per-Token Budget | Dispatches | ms/tok | % of Total |
|-----------------|-----------|--------|-----------|
| mmvq unfused (Q,K,V,O,down) | 261 | 3.01 | 28% |
| mmvq fused (gate+up+GLU) | 33 | 1.91 | 18% |
| quantize_q8_1 | 342 | 0.65 | 6% |
| rms_norm | 301 | 0.84 | 8% |
| flash_attn | 46 | 1.09 | 10% |
| elementwise (mul/add/repeat) | 192 | 0.39 | 4% |
| activations (gelu/sigmoid/silu) | 119 | 0.21 | 2% |
| rope | 54 | 0.16 | 1% |
| scale/softcap | 43 | 0.07 | 1% |
| rows (get/set) | 37 | 0.08 | 1% |
| memops | 63 | 0.16 | 1% |
| **dispatch gaps (idle GPU)** | -- | **~4.0** | **~37%** |
| **Total** | **~1,542** | **~10.7** | **100%** |

**Target Budget at 200 tok/s (5.0 ms/tok):**

| Component | Current | Target | Savings |
|-----------|---------|--------|---------|
| mmvq compute | 4.92 | 4.92 | 0 |
| flash_attn | 1.09 | 1.09 | 0 |
| Other GPU compute | 2.55 | 0.80 | 1.75 (fusion) |
| Dispatch gaps | ~4.0 | ~0.5 | 3.5 (fewer launches) |
| **Total** | **~10.7** | **~5.0** | **~5.7** |

**Key Insight:** The dispatch gaps and non-matmul compute overlap — eliminating dispatches via fusion simultaneously eliminates both the gap time AND the small-kernel compute time. Each fusion that removes N dispatches saves approximately N × 18us (14us gap + 4us avg kernel).

---

## Phase 1: Fuse quantize_q8_1 into mmvq for RQ4

**Impact:** Eliminate 261 dispatches/token → save ~4.7ms/tok → **~70 tok/s gain** (to ~130)
**Effort:** Medium (new mmvq variant, RQ4-specific)
**Risk:** Low — isolated to RQ4 path, existing types unaffected

### Rationale

Every unfused mmvq dispatch is preceded by a quantize_q8_1 dispatch that converts F32 activations to Q8_1 format. For single-token decode with a 1536-element hidden state, this quantizes 48 blocks of 32 floats each — trivial work that currently costs a full kernel launch + 14us gap.

The fused kernel would accept F32 activations directly. Each warp reads its slice of the F32 activation vector, quantizes to Q8_1 in registers, then performs the dot product. The Q8_1 intermediate never touches global memory.

### Task 1: Write F32-accepting vec_dot for RQ4

**Files:**
- Modify: `third_party/llama.cpp/ggml/src/ggml-cuda/vecdotq.cuh:1249-1287`
- No test file (GPU kernel — tested via integration benchmark)

The existing `vec_dot_rq4_q8_1` takes pre-quantized `block_q8_1`. Write a new `vec_dot_rq4_f32` that reads F32 activations, quantizes per-block in registers, then does the dp4a dot product.

- [ ] **Step 1: Study the Q8_1 quantization math**

The quantize_q8_1 kernel (quantize.cu:5-48) does per-block:
```
amax = warp_reduce_max(|xi|)    // absmax across 32 elements
d = amax / 127.0f               // scale
q = round(xi / d)               // quantize to int8
ds = half2(d, sum)              // pack scale + sum
```

The vec_dot_rq4_q8_1 reads `bq8_1->qs` (int8 values) and `bq8_1->ds` (scale+sum). For the fused version, we compute these in-register from F32 input.

- [ ] **Step 2: Write `vec_dot_rq4_f32` in vecdotq.cuh**

```cpp
// After vec_dot_rq4_q8_1, add:
static __device__ __forceinline__ float vec_dot_rq4_f32_inline(
    const void * __restrict__ vbq, const float * __restrict__ y,
    const int & kbx, const int & iqs, const int ncols_y) {

    const block_rq4 * bq4 = (const block_rq4 *) vbq + kbx;
    
    // Read 8 F32 activation values for this iqs position
    // iqs iterates in steps of VDR (2), processing 8 elements per iteration
    float sumi = 0.0f;

    #pragma unroll
    for (int l = 0; l < VDR_RQ4_Q8_1_MMVQ; ++l) {
        const int aux_q4 = get_int_b2(bq4->qs, iqs + l);

        // Dequant RQ4 weights to float via codebook
        const uint8_t q0 = (aux_q4 >>  0) & 0xF;
        const uint8_t q1 = (aux_q4 >>  4) & 0xF;
        const uint8_t q2 = (aux_q4 >>  8) & 0xF;
        const uint8_t q3 = (aux_q4 >> 12) & 0xF;
        const uint8_t q4 = (aux_q4 >> 16) & 0xF;
        const uint8_t q5 = (aux_q4 >> 20) & 0xF;
        const uint8_t q6 = (aux_q4 >> 24) & 0xF;
        const uint8_t q7 = (aux_q4 >> 28) & 0xF;

        // Read activation values at consecutive positions
        const int y_offset = (iqs + l) * 8;  // 8 elements per iqs step
        sumi += rq4_codebook_gpu[q0] * y[y_offset + 0];
        sumi += rq4_codebook_gpu[q1] * y[y_offset + 1];
        sumi += rq4_codebook_gpu[q2] * y[y_offset + 2];
        sumi += rq4_codebook_gpu[q3] * y[y_offset + 3];
        sumi += rq4_codebook_gpu[q4] * y[y_offset + 4];
        sumi += rq4_codebook_gpu[q5] * y[y_offset + 5];
        sumi += rq4_codebook_gpu[q6] * y[y_offset + 6];
        sumi += rq4_codebook_gpu[q7] * y[y_offset + 7];
    }

    return __half2float(bq4->d) * sumi;
}
```

Note: This trades dp4a integer SIMD for 8 float MADs. On RDNA 3, INT8 dp4a and FP32 MAD have the same throughput (512 ops/CU/clock), so the compute cost is equal. The win is eliminating the Q8_1 quantization kernel entirely.

- [ ] **Step 3: Write a new mmvq kernel variant that uses F32 activations**

Create `mul_mat_vec_q_f32` in mmvq.cu that mirrors `mul_mat_vec_q` but takes `const float * vy` instead of `const void * vy` (Q8_1). The inner loop calls `vec_dot_rq4_f32_inline` instead of `vec_dot_rq4_q8_1`.

This kernel is RQ4-specific — other quant types keep using the Q8_1 path.

- [ ] **Step 4: Add dispatch path in `ggml_cuda_mul_mat_vec_q`**

In mmvq.cu:1108-1115, the current code allocates a Q8_1 buffer and calls `quantize_row_q8_1_cuda`. For RQ4, bypass this and dispatch the F32 variant directly:

```cpp
if (src0->type == GGML_TYPE_RQ4) {
    // Fused path: skip Q8_1 quantization, pass F32 activations directly
    mul_mat_vec_q_f32_rq4(src0->data, src1_d, ids_d, fusion_local, dst_d, ...);
    return;
}
// Existing Q8_1 path for all other types
ggml_cuda_pool_alloc<char> src1_q8_1(...);
quantize_row_q8_1_cuda(...);
```

- [ ] **Step 5: Benchmark the fused kernel**

```bash
# Compare with and without the fusion
# Baseline: ~100.6 tok/s
# Expected: ~115-130 tok/s (eliminating 261 dispatch gaps × ~18us)
```

- [ ] **Step 6: Verify correctness**

Run the 25 gold probes against the all-RQ4 model with the fused kernel. Compare JSON output with the unfused version. Check for numerical drift — the float path may differ slightly from the dp4a+Q8 path.

- [ ] **Step 7: Commit**

```bash
git add ggml/src/ggml-cuda/vecdotq.cuh ggml/src/ggml-cuda/mmvq.cu
git commit -m "perf: fuse quantize_q8_1 into mmvq for RQ4 — eliminate 261 dispatches/token"
```

### Task 2: Alternative approach — shared memory Q8_1 quantization

If Task 1's float path causes numerical issues or register pressure problems, the alternative is to quantize the activation vector in shared memory within the mmvq kernel:

**Files:**
- Modify: `third_party/llama.cpp/ggml/src/ggml-cuda/mmvq.cu:404-480`

- [ ] **Step 1: Add shared memory Q8_1 buffer to mmvq kernel**

At the top of `mul_mat_vec_q`, before the main loop, have the first warp cooperatively quantize the F32 activation vector to shared memory Q8_1 format:

```cpp
// 1536 elements / 32 per block = 48 Q8_1 blocks = 48 * 34 bytes = 1,632 bytes shared
extern __shared__ char smem_q8_1[];
block_q8_1 * shared_q8 = (block_q8_1 *) smem_q8_1;

// Cooperative quantization: each warp handles some blocks
const int blocks_total = ncols_x / QK8_1;
for (int b = threadIdx.y; b < blocks_total; b += nwarps) {
    // Each thread in the warp handles one element
    float xi = (threadIdx.x < QK8_1) ? vy_f32[b * QK8_1 + threadIdx.x] : 0.0f;
    float amax = warp_reduce_max<QK8_1>(fabsf(xi));
    float d = amax / 127.0f;
    int8_t q = (amax == 0.0f) ? 0 : __float2int_rn(xi / d);
    shared_q8[b].qs[threadIdx.x] = q;
    if (threadIdx.x == 0) {
        float sum = warp_reduce_sum<QK8_1>(xi);
        shared_q8[b].ds = make_half2(d, sum);
    }
}
__syncthreads();
// Now use shared_q8 instead of vy in the dot product loop
```

This preserves the dp4a integer SIMD path while eliminating the separate kernel launch.

- [ ] **Step 2: Benchmark and compare with Task 1**

The shared memory approach has higher register pressure but preserves the fast dp4a path. The float approach (Task 1) uses fewer registers but more FP32 ops. Benchmark both.

---

## Phase 2: Fuse RMSNorm into matmul prologue/epilogue

**Impact:** Eliminate ~300 dispatches/token → save ~5.4ms/tok → **~50 tok/s gain** (to ~150-180)
**Effort:** High (touches multiple kernel files, needs graph pattern matching)
**Risk:** Medium — RMSNorm fusion affects all model types, needs careful testing

### Rationale

RMSNorm dispatches 301 times per token (8.6 per layer), each averaging 2.8us of compute. The dispatch gap (14us) is 5x the compute cost. These norms are always adjacent to matmuls in the graph — either normalizing the input to a matmul or scaling the output.

### Task 3: Identify RMSNorm fusion patterns in the Gemma 4 graph

**Files:**
- Read: `third_party/llama.cpp/src/models/gemma4.cpp` (model graph construction)
- Read: `third_party/llama.cpp/ggml/src/ggml-cuda/ggml-cuda.cu:3800+` (fusion pattern matching)

- [ ] **Step 1: Trace the Gemma 4 graph to identify RMSNorm positions**

Each transformer layer has this pattern:
```
rms_norm(hidden) → [Q,K,V projections] → attention → rms_norm(attn_out) → [FFN] → add
```

The 301 norms/token = ~8.6 per layer × 35 layers. The breakdown is:
- Pre-attention norm: 1/layer = 35
- Pre-FFN norm: 1/layer = 35
- Per-head norms (Gemma 4 has per-head RMSNorm): varies
- Post-norm scaling: varies

- [ ] **Step 2: Extend the mmvq fusion infrastructure to include RMSNorm**

The existing `ggml_cuda_mm_fusion_args_host` struct supports bias and gate fusion. Add an `rms_norm_weight` field:

```cpp
struct ggml_cuda_mm_fusion_args_host {
    const ggml_tensor * x_bias;
    const ggml_tensor * gate;
    const ggml_tensor * gate_bias;
    ggml_glu_op glu_op;
    const ggml_tensor * rms_norm_weight;  // NEW: fuse input normalization
    float rms_norm_eps;                    // NEW
};
```

- [ ] **Step 3: Add RMSNorm computation to the mmvq kernel prologue**

Before the dot product loop, compute RMSNorm on the activation vector:
```
1. Read F32 activation into registers
2. Compute sum of squares (warp reduction)
3. Normalize: x_i = x_i * rsqrt(mean_sq + eps) * weight_i
4. Quantize to Q8_1 (or use float path from Phase 1)
5. Proceed with dot product
```

This replaces: `rms_norm_kernel → quantize_q8_1_kernel → mmvq_kernel` with a single `mmvq_with_rms_norm_kernel`.

- [ ] **Step 4: Add graph pattern matching for RMSNorm + matmul**

In `ggml_cuda_graph_evaluate_and_capture`, add a pattern detector:
```
if (node->op == GGML_OP_MUL_MAT && node->src[1]->src[0]->op == GGML_OP_RMS_NORM) {
    // Fuse RMSNorm into matmul prologue
}
```

- [ ] **Step 5: Benchmark**

Expected: eliminate ~300 dispatch gaps, saving ~5.4ms/token. Combined with Phase 1: ~10ms improvement → ~150-180 tok/s.

- [ ] **Step 6: Commit**

---

## Phase 3: Fuse elementwise chains (SiLU-gated FFN)

**Impact:** Eliminate ~115 dispatches/token → save ~2ms/tok → **~20 tok/s gain**
**Effort:** Medium (the FFN gate+up is already fused, this handles the remaining chains)
**Risk:** Low — isolated to specific patterns

### Rationale

The FFN gate+up+GLU fusion is already active (33 fused calls/token). But 119 activation dispatches + 192 elementwise dispatches remain. These are:
- Residual connections (add): ~70/token
- Attention scaling (mul/repeat): ~80/token
- Remaining activations after attention: ~40/token

### Task 4: Fuse attention output scaling chain

**Files:**
- Modify: `third_party/llama.cpp/ggml/src/ggml-cuda/ggml-cuda.cu` (pattern matching)
- Modify: `third_party/llama.cpp/ggml/src/ggml-cuda/mmvq.cu` (kernel epilogue)

- [ ] **Step 1: Identify the attention output chain**

After the O-projection matmul, the typical pattern is:
```
mmvq(O) → scale → add(residual)
```

Fuse `scale + add_residual` into the mmvq epilogue using the existing `x_bias` fusion slot (treating the residual as a bias).

- [ ] **Step 2: Implement residual-add fusion in mmvq epilogue**

The existing fusion code (mmvq.cu:448-481) already handles bias addition. Extend it to add a residual input:

```cpp
if constexpr (has_fusion) {
    if (use_bias) {
        partial_sum += x_biases[j];  // existing bias
    }
    if (use_residual) {
        partial_sum += residual[row0 + threadIdx.x];  // NEW
    }
}
```

- [ ] **Step 3: Benchmark**

- [ ] **Step 4: Commit**

---

## Phase 4: mmvq Bandwidth Optimization

**Impact:** Improve mmvq from 41% to 60%+ bandwidth utilization → save ~1ms/tok → **~20 tok/s gain**
**Effort:** High (ISA-level optimization, profiling-driven)
**Risk:** Medium — may be limited by RDNA 3 architecture

### Rationale

The mmvq kernel achieves 3.01ms per token for ~2.5GB of weight reads = 830 GB/s... wait, that's above theoretical. Let me recalculate: 9,132 decode mmvq calls averaging 11.54us each. The average matrix is ~3-5MB. At 11.54us for 3MB = 260 GB/s (42% of 624). For the larger 56MB embedding matrix: 850us for 56MB = 66 GB/s (11%).

### Task 5: Profile mmvq occupancy and memory access patterns

**Files:**
- Read: `third_party/llama.cpp/ggml/src/ggml-cuda/vecdotq.cuh:1249-1287`
- Read: `third_party/llama.cpp/ggml/src/ggml-cuda/mmvq.cu:402-480`

- [ ] **Step 1: Use rocprofv3 hardware counters to measure actual bandwidth**

```bash
rocprofv3 --pmc SQ_INSTS_VALU,SQ_INSTS_SMEM,TCP_TCC_READ_REQ_sum,TCP_TCC_WRITE_REQ_sum \
  -- llama-server -m model.gguf ...
```

Measure: L2 hit rate, actual VRAM reads, VALU utilization.

- [ ] **Step 2: Check for coalescing issues with 18-byte RQ4 blocks**

The block_rq4 is 18 bytes (2B scale + 16B data). This is not power-of-2 aligned. Consecutive blocks start at offsets 0, 18, 36, 54... Thread access patterns may cause suboptimal cache line utilization.

If confirmed: pad blocks to 32 bytes in the GGUF (wastes 44% space but might double bandwidth). Test with a custom padded GGUF.

- [ ] **Step 3: Evaluate wider loads**

The current kernel reads 2 bytes (`get_int_b2`) at a time from the weight tensor. On RDNA 3, the optimal load width is 128 bits (16 bytes). Check if wider loads (reading multiple blocks per thread) improve bandwidth.

- [ ] **Step 4: Benchmark improvements**

---

## Stretch Goal: Custom Fused Transformer Kernel

**Impact:** Potentially 200+ tok/s
**Effort:** Very High (custom ISA-level kernel)
**Risk:** High — essentially writing a new inference engine

If Phases 1-4 don't reach 200 tok/s, the remaining path is a single fused kernel that does an entire transformer layer decode step: RMSNorm → Q/K/V projection → RoPE → attention → O projection → RMSNorm → FFN gate+up → GLU → FFN down → residual add.

This eliminates ALL dispatch overhead within a layer (reducing from ~44 dispatches/layer to 1). The cost is a massive kernel with high register pressure and complex control flow.

Frameworks like FlashInfer and AMD Composable Kernel (CK) provide building blocks. A custom CK-based decoder kernel for Gemma 4 + RQ4 would be the ultimate optimization but is a multi-week project.

---

## Summary: Expected Progression

| Phase | Optimization | Est. tok/s | Delta | Status |
|-------|-------------|-----------|-------|--------|
| Done | get_rows + nwarps + clocks | 100.6 | baseline | Done |
| 1 | Fuse quantize_q8 into mmvq | ~130 | +30 | **Dead end** — see post-mortem |
| 2 | Fuse RMSNorm into matmul | ~170 | +40 | Blocked (same root cause) |
| 3 | Fuse elementwise chains | ~185 | +15 | Blocked (same root cause) |
| 4 | mmvq bandwidth optimization | ~200 | +15 | Open |
| Stretch | Custom fused decoder kernel | 220+ | +20 | Open |

## Phase 1 Post-Mortem (2026-04-13)

Phase 1 was implemented and benchmarked in two variants:

**Variant A: F32 activation path** — `mul_mat_vec_rq4_f32` kernel that reads F32 activations directly, using float codebook lookups instead of dp4a. Result: +1.5% (101-102 tok/s). The F32 codebook path (8 table reads + 8 FMAs per 8 elements) is slower than dp4a (2 ops per 8 elements), eating the dispatch savings.

**Variant B: Shared-memory Q8_1 path** — `mul_mat_vec_rq4_smem` kernel that cooperatively quantizes F32 → Q8_1 in shared memory, then uses the dp4a dot product. V1 (1 row/block): 49.8 tok/s catastrophic regression. V2 (8 rows/block): 92.5 tok/s, still 8.5% slower than baseline.

**Root cause: HIP dispatch gaps are systemic, not per-kernel-pair.** Removing `quantize_q8_1` from the kernel stream doesn't shrink dispatch gaps — the same ~14us gap appears before the next kernel in the queue. The 69% GPU idle time is a property of the HIP runtime's dispatch pipeline on RDNA 3, not of any specific kernel pairing. This invalidates the premise of Phases 1-3, which all assumed that reducing kernel count would reduce idle time proportionally.

**Additional finding:** nwarps for ncols_dst=2 (Gemma 4 decode path) has zero effect. Tested nwarps=1, 4, and 8 for RQ4 on RDNA 3 — all give identical 101.1 tok/s. The mmvq kernel is not compute-bound; it's already fast enough. The bottleneck is the ~6ms of fixed overhead per token (dispatch scheduling, non-matmul GPU ops, CPU graph evaluation).

**Hardware counter profiling (FETCH_SIZE):** Small matmuls (256 rows, K/V projections) are served from L2 cache at >1000 GB/s. Large matmuls (12,288 rows, FFN down) achieve 361 GB/s (58% of 624 GB/s theoretical). The mmvq kernel itself has reasonable bandwidth efficiency.

**Revised path to 200 tok/s:** Requires cutting 6ms of fixed overhead to 1ms. Viable approaches:
1. **HIP Graph capture** — capture static portions of the decode graph, replay each token
2. **Persistent kernel** — single kernel launch that processes the entire forward pass
3. **AMD Composable Kernel** — custom fused decoder layer (1 dispatch per layer instead of ~44)

All three require architectural changes beyond kernel-level optimization.

## Related Issues

- #403 — RQ4 mmvq bandwidth utilization analysis
- #404 — Kernel fusion opportunities with inter-kernel gap analysis

## Profiling Databases

- `training/data/rocprof-rq4-allquant-2026-04-13.db` — dispatch trace (rocprofv3 format)
- `/tmp/mmvq_bw_results.db` — hardware counter profile (FETCH_SIZE per kernel)

Key tables: `kernels` (dispatch trace with timing), `kernel_symbols` (demangled names).

Query template:
```sql
SELECT name, COUNT(*), ROUND(SUM(duration)/1e6, 1) as ms
FROM kernels GROUP BY name ORDER BY ms DESC LIMIT 20;
```
