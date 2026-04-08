#!/usr/bin/env python3
"""Compute RQ3 (3-bit RotorQ) codebook from Beta distribution.

Uses the same math as RQ4 but with 8 centroids instead of 16.
Outputs C constants for llama.cpp integration.
"""

import numpy as np
from scipy.special import betaincinv
from scipy.stats import beta as beta_dist


def compute_codebook(dim, bits):
    a = b = (dim - 1) / 2.0
    n = 1 << bits
    edges_01 = np.concatenate([[0], betaincinv(a, b, np.arange(1, n) / n), [1]])
    edges = 2 * edges_01 - 1
    centroids = []
    for i in range(n):
        lo, hi = edges[i], edges[i + 1]
        x = np.linspace(lo, hi, 2000)
        x01 = (x + 1) / 2
        pdf = beta_dist.pdf(x01, a, b) / 2
        num = np.trapezoid(x * pdf, x)
        den = np.trapezoid(pdf, x)
        centroids.append(num / den if den > 1e-15 else (lo + hi) / 2)
    return centroids


# Find which dim produces RQ4's known codebook
print("=== Finding RQ4's dim parameter ===")
for dim in [32, 64, 128, 256, 512]:
    cb = compute_codebook(dim, 4)
    max_c = max(cb)
    match = " <-- MATCH" if abs(max_c - 0.12281943) < 0.001 else ""
    print(f"  dim={dim:4d}: max_centroid={max_c:.8f}{match}")

# Generate codebooks for matching dims
for dim in [128, 256]:
    print(f"\n=== dim={dim}, 4-bit (16 centroids) — reference ===")
    cb4 = compute_codebook(dim, 4)
    for i, c in enumerate(cb4):
        print(f"  [{i:2d}] {c:+.10f}")

    print(f"\n=== dim={dim}, 3-bit (8 centroids) — RQ3 ===")
    cb3 = compute_codebook(dim, 3)
    for i, c in enumerate(cb3):
        print(f"  [{i}] {c:+.10f}")

    max_c = max(abs(c) for c in cb3)
    int8_vals = [round(c / max_c * 127) for c in cb3]
    print(f"\n  Float codebook max: {max_c:.10f}")
    print(f"  Int8 (for dp4a):    {int8_vals}")

    # C format
    vals = ", ".join(f"{c:.10f}f" for c in cb3)
    print(f"\n  C float array: {{{vals}}}")

    ivals = ", ".join(str(v) for v in int8_vals)
    print(f"  C int8 array:  {{{ivals}}}")

    # Compression ratio
    rq4_bytes = 18  # 2 (scale) + 16 (indices) per 32 elements
    rq3_bytes = 14  # 2 (scale) + 12 (indices) per 32 elements
    print(f"\n  Block size: RQ4={rq4_bytes}B, RQ3={rq3_bytes}B per 32 elements")
    print(f"  Compression vs RQ4: {rq4_bytes/rq3_bytes:.2f}x smaller")
    print(f"  Compression vs F16: {64/rq3_bytes:.2f}x smaller")
