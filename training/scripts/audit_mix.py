#!/usr/bin/env python3
"""Audit the pretraining data mix — ratios, coverage, and sequence quality.

Validates that tokenized shards match configured weights, checks domain
term coverage in decoded samples, and profiles sequence quality.

Usage:
    python audit_mix.py [--config PATH] [--tokenized-dir PATH] [--samples N]
"""

import argparse
import json
import sys
from pathlib import Path

import numpy as np
import yaml

sys.path.insert(0, str(Path(__file__).parent))
from utils import DTYPE

# Domain terms that should appear frequently in our data
DOMAIN_TERMS = {
    "neuroscience": [
        "hippocampus", "consolidation", "episodic", "retrieval",
        "synaptic", "plasticity", "encoding", "metacognition",
        "salience", "prefrontal", "consciousness",
    ],
    "code": [
        "func", "struct", "return", "import", "class", "def",
        "async", "await", "interface",
    ],
    "json": [
        '{"', '"}', '":', 'null', 'true', 'false',
    ],
}


def audit_ratios(config: dict, tokenized_dir: Path):
    """Compare actual token counts against configured weights."""
    print("=== Token Ratios ===\n")

    sources = config.get("sources", {})
    target_tokens = config.get("target_tokens", 10_000_000_000)

    actual = {}
    for name in sources:
        source_dir = tokenized_dir / name
        if not source_dir.exists():
            actual[name] = 0
            continue
        total = sum(f.stat().st_size // 2 for f in source_dir.glob("shard_*.bin"))
        actual[name] = total

    total_actual = sum(actual.values())

    print(f"  {'Source':<20} {'Config %':>10} {'Actual %':>10} {'Tokens':>15} {'Delta':>8}")
    print(f"  {'-'*20} {'-'*10} {'-'*10} {'-'*15} {'-'*8}")

    for name, src_cfg in sources.items():
        weight = src_cfg.get("weight", 0)
        config_pct = weight * 100
        actual_tokens = actual.get(name, 0)
        actual_pct = (actual_tokens / total_actual * 100) if total_actual else 0
        delta = actual_pct - config_pct
        flag = "!!" if abs(delta) > 5 else "  "
        print(f"{flag}{name:<20} {config_pct:>9.1f}% {actual_pct:>9.1f}% {actual_tokens:>15,} {delta:>+7.1f}%")

    print(f"\n  Total: {total_actual:,} tokens ({total_actual / 1e9:.2f}B)")
    print(f"  Target: {target_tokens:,} ({target_tokens / 1e9:.0f}B)")
    coverage = total_actual / target_tokens * 100
    print(f"  Coverage: {coverage:.1f}% of target")

    if total_actual < target_tokens:
        epochs = target_tokens / total_actual
        print(f"  Need {epochs:.1f} epochs to reach target")

    return total_actual


def audit_domain_coverage(tokenized_dir: Path, tokenizer_path: Path, n_samples: int = 50):
    """Decode random sequences and check domain term presence."""
    print("\n=== Domain Term Coverage ===\n")

    from tokenizers import Tokenizer
    tok_file = tokenizer_path / "tokenizer.json"
    if not tok_file.exists():
        print("  Tokenizer not found, skipping coverage audit.")
        return
    tok = Tokenizer.from_file(str(tok_file))

    for source_name in ["pes2o_neuro", "code", "json_structured"]:
        source_dir = tokenized_dir / source_name
        if not source_dir.exists():
            continue

        shard_files = sorted(source_dir.glob("shard_*.bin"))
        if not shard_files:
            continue

        # Read a sample from the first shard
        mmap = np.memmap(shard_files[0], dtype=DTYPE, mode="r")
        n_tokens = len(mmap)

        # Decode random 2048-token windows
        term_hits = {t: 0 for terms in DOMAIN_TERMS.values() for t in terms}
        rng = np.random.RandomState(42)

        for _ in range(n_samples):
            start = rng.randint(0, max(1, n_tokens - 2048))
            tokens = mmap[start:start + 2048].tolist()
            text = tok.decode(tokens).lower()

            for term in term_hits:
                if term.lower() in text:
                    term_hits[term] += 1

        # Report by category
        category = "neuroscience" if "neuro" in source_name else "code" if source_name == "code" else "json"
        terms = DOMAIN_TERMS.get(category, [])
        print(f"  {source_name} ({n_samples} samples):")
        for term in terms:
            hits = term_hits.get(term, 0)
            pct = hits / n_samples * 100
            marker = "OK" if pct > 10 else "LOW" if pct > 0 else "MISS"
            print(f"    [{marker:>4}] {term}: {hits}/{n_samples} ({pct:.0f}%)")
        print()


def audit_sequence_quality(tokenized_dir: Path, n_samples: int = 100):
    """Check for degenerate sequences (all same token, all padding, etc)."""
    print("=== Sequence Quality ===\n")

    rng = np.random.RandomState(42)
    seq_len = 2048
    issues = 0

    for source_dir in sorted(tokenized_dir.iterdir()):
        if not source_dir.is_dir():
            continue
        shard_files = sorted(source_dir.glob("shard_*.bin"))
        if not shard_files:
            continue

        name = source_dir.name
        source_issues = 0

        # Sample from first shard
        mmap = np.memmap(shard_files[0], dtype=DTYPE, mode="r")
        n_tokens = len(mmap)

        for _ in range(n_samples):
            start = rng.randint(0, max(1, n_tokens - seq_len))
            seq = mmap[start:start + seq_len]

            # Check for degenerate patterns
            unique = len(np.unique(seq))
            if unique < 10:
                source_issues += 1
            elif unique < 50:
                source_issues += 0.5

        quality = (n_samples - source_issues) / n_samples * 100
        status = "OK" if quality > 95 else "WARN" if quality > 80 else "BAD"
        print(f"  [{status:>4}] {name}: {quality:.0f}% quality ({source_issues:.0f} issues in {n_samples} samples)")
        issues += source_issues

    print(f"\n  Total issues: {issues:.0f}")


def main():
    parser = argparse.ArgumentParser(description="Audit pretraining data mix")
    parser.add_argument("--config", default="configs/pretrain_mix.yaml")
    parser.add_argument("--tokenized-dir", default="data/pretrain/tokenized")
    parser.add_argument("--tokenizer-path", default="tokenizer")
    parser.add_argument("--samples", type=int, default=50)
    args = parser.parse_args()

    config_path = Path(args.config)
    tokenized_dir = Path(args.tokenized_dir)
    tokenizer_path = Path(args.tokenizer_path)

    with open(config_path) as f:
        config = yaml.safe_load(f)

    audit_ratios(config, tokenized_dir)
    audit_domain_coverage(tokenized_dir, tokenizer_path, n_samples=args.samples)
    audit_sequence_quality(tokenized_dir, n_samples=args.samples)

    print("=== Audit Complete ===")


if __name__ == "__main__":
    main()
