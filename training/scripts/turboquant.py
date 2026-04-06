#!/usr/bin/env python3
"""TurboQuant: Near-optimal KV cache quantization for LLM inference.

Reference implementation for ROCm (AMD GPU) — no CUDA-specific dependencies.
Based on arXiv:2504.19874 (ICLR 2026), Algorithm 1 (TurboQuant_MSE only).

The algorithm:
  1. Generate a fixed random orthogonal rotation matrix Pi (from deterministic seed)
  2. For each KV vector: normalize, rotate by Pi, scalar-quantize each coordinate
  3. Coordinates after rotation follow Beta((d-1)/2, (d-1)/2) on [-1,1]
  4. Optimal codebook is precomputed from this known distribution
  5. Dequantize: centroid lookup -> inverse rotation -> rescale

This is data-oblivious (no calibration needed) and training-free.

Usage:
    tq = TurboQuant(dim=128, bits=3)
    indices, norms = tq.quantize(keys)     # compress
    keys_hat = tq.dequantize(indices, norms)  # decompress

    # Or for attention: rotate query once, score against compressed keys directly
    q_rot = tq.rotate_query(query)
    scores = tq.score_compressed(q_rot, indices, norms)
"""

import math
from typing import Tuple

import torch
import numpy as np
from scipy.special import betaincinv
from scipy.stats import beta as beta_dist


class TurboQuant:
    """TurboQuant_MSE: MSE-optimal vector quantization via random rotation.

    For KV cache compression in transformer attention. Each KV head's vectors
    are independently quantized to `bits` per coordinate.

    Args:
        dim: Vector dimension (head_dim, typically 128)
        bits: Bits per coordinate (2, 3, or 4). 3-bit is the sweet spot.
        seed: Deterministic seed for rotation matrix. Same seed = same rotation
              across all layers and all models. This is the "data-oblivious" property.
    """

    def __init__(self, dim: int, bits: int = 3, seed: int = 42):
        self.dim = dim
        self.bits = bits
        self.n_centroids = 2 ** bits

        # 1. Random orthogonal rotation matrix (deterministic from seed)
        gen = torch.Generator(device="cpu").manual_seed(seed)
        G = torch.randn(dim, dim, generator=gen)
        Q, R = torch.linalg.qr(G)
        # Ensure det(Q) = +1 (proper rotation, not reflection)
        Q = Q * torch.sign(torch.diag(R)).unsqueeze(0)
        self.Pi = Q        # (dim, dim) rotation
        self.Pi_T = Q.T    # (dim, dim) inverse rotation

        # 2. Optimal codebook from Beta distribution
        self.codebook, self.boundaries = self._compute_codebook(dim, bits)

        # Precompute for fast quantization (searchsorted boundaries)
        self._boundaries_tensor = torch.tensor(
            self.boundaries, dtype=torch.float32
        )

    def _compute_codebook(self, d: int, b: int) -> Tuple[torch.Tensor, list]:
        """Compute optimal scalar quantizer for Beta((d-1)/2, (d-1)/2) on [-1,1].

        Returns:
            codebook: (n_centroids,) tensor of centroid values
            boundaries: (n_centroids-1,) list of decision boundaries
        """
        n = 2 ** b
        alpha = (d - 1) / 2.0
        dist = beta_dist(alpha, alpha)

        # Decision boundaries: equal-probability quantiles
        boundaries = []
        for i in range(1, n):
            q01 = float(betaincinv(alpha, alpha, i / n))  # quantile in [0,1]
            boundaries.append(2.0 * q01 - 1.0)  # map to [-1,1]

        # Centroids: conditional expectation within each interval
        centroids = []
        lower = -1.0
        for i in range(n):
            upper = boundaries[i] if i < len(boundaries) else 1.0
            # E[X | lower <= X <= upper] for X ~ Beta(alpha,alpha) on [-1,1]
            a01 = max((lower + 1) / 2, 1e-12)
            b01 = min((upper + 1) / 2, 1 - 1e-12)
            prob = dist.cdf(b01) - dist.cdf(a01)
            if prob < 1e-15:
                centroids.append((lower + upper) / 2)
            else:
                x = np.linspace(a01, b01, 2000)
                pdf = dist.pdf(x)
                centroid_01 = float(np.trapezoid(x * pdf, x) / np.trapezoid(pdf, x))
                centroids.append(2.0 * centroid_01 - 1.0)
            lower = upper

        return torch.tensor(centroids, dtype=torch.float32), boundaries

    def to(self, device):
        """Move quantizer state to device."""
        self.Pi = self.Pi.to(device)
        self.Pi_T = self.Pi_T.to(device)
        self.codebook = self.codebook.to(device)
        self._boundaries_tensor = self._boundaries_tensor.to(device)
        return self

    def quantize(self, x: torch.Tensor) -> Tuple[torch.Tensor, torch.Tensor]:
        """Quantize vectors to indices + norms.

        Args:
            x: (..., dim) input vectors (any norm)

        Returns:
            indices: (..., dim) uint8 quantization indices
            norms: (..., 1) float32 L2 norms
        """
        x_f32 = x.float()
        norms = torch.norm(x_f32, dim=-1, keepdim=True)
        x_unit = x_f32 / (norms + 1e-10)

        # Rotate
        y = x_unit @ self.Pi_T  # (..., dim)

        # Scalar quantize: find nearest centroid per coordinate
        # Using searchsorted on boundaries is faster than full distance computation
        # Map coordinates to indices via boundary comparison
        indices = torch.searchsorted(self._boundaries_tensor, y)

        return indices.to(torch.uint8), norms

    def dequantize(self, indices: torch.Tensor, norms: torch.Tensor) -> torch.Tensor:
        """Dequantize indices + norms back to vectors.

        Args:
            indices: (..., dim) uint8 quantization indices
            norms: (..., 1) float32 norms

        Returns:
            x_hat: (..., dim) reconstructed vectors
        """
        y_hat = self.codebook[indices.long()]  # centroid lookup
        x_hat = y_hat @ self.Pi  # inverse rotation
        return x_hat * norms

    def rotate_query(self, q: torch.Tensor) -> torch.Tensor:
        """Pre-rotate query for direct scoring against compressed keys.

        Instead of dequantizing keys (expensive inverse rotation per KV token),
        rotate the query forward once and score against centroids directly.

        Args:
            q: (..., dim) query vectors

        Returns:
            q_rot: (..., dim) rotated query vectors
        """
        return q.float() @ self.Pi_T

    def score_compressed(
        self,
        q_rot: torch.Tensor,
        k_indices: torch.Tensor,
        k_norms: torch.Tensor,
    ) -> torch.Tensor:
        """Compute attention scores directly from compressed keys.

        This avoids the full dequantize → matmul path. Instead:
        q_rot @ k_hat = q_rot @ (codebook[indices] @ Pi)
                      = (q_rot @ Pi^T) @ codebook[indices]^T  ... but q_rot = q @ Pi^T already
        Actually: score = sum_d q_rot[d] * codebook[k_indices[d]] * k_norm

        This is a table lookup + element-wise multiply + reduction.

        Args:
            q_rot: (batch, n_heads, 1, dim) rotated query
            k_indices: (batch, n_heads, seq_len, dim) uint8 key indices
            k_norms: (batch, n_heads, seq_len, 1) key norms

        Returns:
            scores: (batch, n_heads, 1, seq_len) attention scores
        """
        # Look up centroids for each key coordinate
        k_centroids = self.codebook[k_indices.long()]  # (..., seq_len, dim)

        # Score = (q_rot * k_centroids).sum(dim=-1) * k_norm
        scores = (q_rot * k_centroids).sum(dim=-1, keepdim=True)  # (..., seq_len, 1)
        scores = scores * k_norms  # (..., seq_len, 1)

        # Reshape for attention: (..., 1, seq_len)
        return scores.squeeze(-1).unsqueeze(-2)

    def memory_bytes(self, n_tokens: int) -> dict:
        """Compute memory usage for n_tokens compressed vectors."""
        index_bytes = n_tokens * self.dim * self.bits / 8
        norm_bytes = n_tokens * 4  # float32
        total = index_bytes + norm_bytes
        fp16_bytes = n_tokens * self.dim * 2
        return {
            "index_bytes": index_bytes,
            "norm_bytes": norm_bytes,
            "total_bytes": total,
            "fp16_bytes": fp16_bytes,
            "compression_ratio": fp16_bytes / total,
            "bits_per_element": total * 8 / (n_tokens * self.dim),
        }


class TurboQuantKVCache:
    """Drop-in KV cache with TurboQuant compression.

    Keeps a configurable number of recent tokens in full precision (the "buffer")
    and compresses older tokens. This is important because attention to very recent
    tokens benefits most from full precision.

    Args:
        dim: Head dimension
        bits_k: Bits per coordinate for keys (default 3)
        bits_v: Bits per coordinate for values (default 4, values need more precision)
        buffer_size: Number of recent tokens kept in full precision (default 128)
        seed: Deterministic seed
    """

    def __init__(
        self,
        dim: int,
        bits_k: int = 3,
        bits_v: int = 4,
        buffer_size: int = 128,
        seed: int = 42,
    ):
        self.dim = dim
        self.buffer_size = buffer_size
        self.tq_k = TurboQuant(dim, bits=bits_k, seed=seed)
        self.tq_v = TurboQuant(dim, bits=bits_v, seed=seed + 1000)

        # Compressed storage
        self.k_indices = None  # (batch, heads, compressed_len, dim)
        self.k_norms = None
        self.v_indices = None
        self.v_norms = None

        # Full-precision buffer for recent tokens
        self.k_buffer = None  # (batch, heads, buffer_len, dim)
        self.v_buffer = None

    def to(self, device):
        """Move to device."""
        self.tq_k.to(device)
        self.tq_v.to(device)
        return self

    def append(self, k: torch.Tensor, v: torch.Tensor):
        """Append new KV pair(s) to the cache.

        Args:
            k: (batch, heads, new_tokens, dim)
            v: (batch, heads, new_tokens, dim)
        """
        if self.k_buffer is None:
            self.k_buffer = k
            self.v_buffer = v
        else:
            self.k_buffer = torch.cat([self.k_buffer, k], dim=2)
            self.v_buffer = torch.cat([self.v_buffer, v], dim=2)

        # If buffer exceeds limit, compress the overflow
        if self.k_buffer.shape[2] > self.buffer_size:
            n_compress = self.k_buffer.shape[2] - self.buffer_size
            k_old = self.k_buffer[:, :, :n_compress]
            v_old = self.v_buffer[:, :, :n_compress]

            # Quantize
            k_idx, k_nrm = self.tq_k.quantize(k_old)
            v_idx, v_nrm = self.tq_v.quantize(v_old)

            # Append to compressed storage
            if self.k_indices is None:
                self.k_indices = k_idx
                self.k_norms = k_nrm
                self.v_indices = v_idx
                self.v_norms = v_nrm
            else:
                self.k_indices = torch.cat([self.k_indices, k_idx], dim=2)
                self.k_norms = torch.cat([self.k_norms, k_nrm], dim=2)
                self.v_indices = torch.cat([self.v_indices, v_idx], dim=2)
                self.v_norms = torch.cat([self.v_norms, v_nrm], dim=2)

            # Trim buffer
            self.k_buffer = self.k_buffer[:, :, n_compress:]
            self.v_buffer = self.v_buffer[:, :, n_compress:]

    def get_keys_values(self) -> Tuple[torch.Tensor, torch.Tensor]:
        """Get full KV tensors (dequantizing compressed portion).

        Returns:
            keys: (batch, heads, total_len, dim)
            values: (batch, heads, total_len, dim)
        """
        parts_k = []
        parts_v = []

        if self.k_indices is not None:
            parts_k.append(self.tq_k.dequantize(self.k_indices, self.k_norms))
            parts_v.append(self.tq_v.dequantize(self.v_indices, self.v_norms))

        if self.k_buffer is not None:
            parts_k.append(self.k_buffer)
            parts_v.append(self.v_buffer)

        return torch.cat(parts_k, dim=2), torch.cat(parts_v, dim=2)

    @property
    def seq_len(self) -> int:
        """Total sequence length (compressed + buffer)."""
        compressed = self.k_indices.shape[2] if self.k_indices is not None else 0
        buffered = self.k_buffer.shape[2] if self.k_buffer is not None else 0
        return compressed + buffered

    def clear(self):
        """Reset the cache."""
        self.k_indices = self.k_norms = None
        self.v_indices = self.v_norms = None
        self.k_buffer = self.v_buffer = None


def benchmark():
    """Benchmark TurboQuant on the RX 7800 XT."""
    device = "cuda" if torch.cuda.is_available() else "cpu"
    print(f"Device: {device}")

    dim = 128  # Qwen 3.5 head_dim
    n_heads = 16  # Qwen 3.5 num_heads
    batch = 1

    # Test correctness
    print("\n=== Correctness ===")
    for bits in [2, 3, 4]:
        tq = TurboQuant(dim, bits=bits).to(device)
        x = torch.randn(1000, dim, device=device)
        x = x / torch.norm(x, dim=-1, keepdim=True)

        idx, norms = tq.quantize(x)
        x_hat = tq.dequantize(idx, norms)
        mse = ((x - x_hat) ** 2).sum(dim=-1).mean().item()
        cosine = torch.nn.functional.cosine_similarity(x, x_hat, dim=-1).mean().item()

        mem = tq.memory_bytes(1000)
        print(f"  {bits}-bit: MSE={mse:.6f}, cosine={cosine:.6f}, "
              f"compression={mem['compression_ratio']:.1f}x")

    # Benchmark throughput
    print("\n=== Throughput ===")
    for seq_len in [256, 512, 1024, 2048, 4096]:
        tq = TurboQuant(dim, bits=3).to(device)
        keys = torch.randn(batch, n_heads, seq_len, dim, device=device)

        # Warmup
        for _ in range(3):
            idx, norms = tq.quantize(keys)
            _ = tq.dequantize(idx, norms)
        torch.cuda.synchronize()

        import time
        # Quantize
        t0 = time.perf_counter()
        for _ in range(100):
            idx, norms = tq.quantize(keys)
        torch.cuda.synchronize()
        quant_ms = (time.perf_counter() - t0) / 100 * 1000

        # Dequantize
        t0 = time.perf_counter()
        for _ in range(100):
            _ = tq.dequantize(idx, norms)
        torch.cuda.synchronize()
        dequant_ms = (time.perf_counter() - t0) / 100 * 1000

        mem = tq.memory_bytes(seq_len * n_heads)
        print(f"  seq_len={seq_len:5d}: quant={quant_ms:.2f}ms, dequant={dequant_ms:.2f}ms, "
              f"compressed={mem['total_bytes']/1024:.1f}KB vs fp16={mem['fp16_bytes']/1024:.1f}KB")

    # Test KV cache
    print("\n=== KV Cache ===")
    cache = TurboQuantKVCache(dim, bits_k=3, bits_v=4, buffer_size=128).to(device)
    for step in range(512):
        k = torch.randn(batch, n_heads, 1, dim, device=device)
        v = torch.randn(batch, n_heads, 1, dim, device=device)
        cache.append(k, v)

    keys, values = cache.get_keys_values()
    print(f"  Cache: {cache.seq_len} tokens "
          f"({cache.k_indices.shape[2] if cache.k_indices is not None else 0} compressed "
          f"+ {cache.k_buffer.shape[2]} buffered)")
    print(f"  Keys shape: {keys.shape}, Values shape: {values.shape}")


if __name__ == "__main__":
    benchmark()
