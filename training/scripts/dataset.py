#!/usr/bin/env python3
"""MixedPretrainDataset — stochastic multi-source token shard reader.

Reads pre-tokenized .bin shards from multiple source directories,
samples sources by configured weights, and yields (input_ids, targets)
sequences for causal LM training.

Usage:
    from dataset import MixedPretrainDataset
    ds = MixedPretrainDataset("configs/pretrain_mix.yaml", seq_len=2048)
    for input_ids, targets in ds:
        ...  # input_ids and targets are np.ndarray of shape (seq_len,)
"""

import json
import sys
from pathlib import Path

import numpy as np
import yaml

sys.path.insert(0, str(Path(__file__).parent))
from utils import DTYPE


class ShardReader:
    """Streaming memory-mapped reader for a single source's .bin shard files.

    Reads one shard at a time via mmap (no concatenation), keeping memory
    usage proportional to one shard (~200MB) instead of all shards (~GB).
    """

    def __init__(self, source_dir: Path, seed: int = 42):
        self.source_dir = source_dir
        self.shard_files = sorted(source_dir.glob("shard_*.bin"))
        if not self.shard_files:
            raise FileNotFoundError(f"No shard files in {source_dir}")

        # Load manifest for metadata
        manifest_path = source_dir / "manifest.json"
        if manifest_path.exists():
            with open(manifest_path) as f:
                self.manifest = json.load(f)
            self.total_tokens = self.manifest["total_tokens"]
        else:
            self.total_tokens = sum(f.stat().st_size // 2 for f in self.shard_files)
            self.manifest = {"total_tokens": self.total_tokens, "shard_count": len(self.shard_files)}

        self.rng = np.random.RandomState(seed)
        self._shard_order = list(range(len(self.shard_files)))
        self._shard_idx = 0
        self._current_mmap = None
        self._pos_in_shard = 0
        self._load_current_shard()

    def _load_current_shard(self):
        """Memory-map the current shard."""
        if self._shard_idx < len(self._shard_order):
            sf = self.shard_files[self._shard_order[self._shard_idx]]
            self._current_mmap = np.memmap(sf, dtype=DTYPE, mode="r")
            self._pos_in_shard = 0
        else:
            self._current_mmap = None

    def get_sequence(self, seq_len: int) -> np.ndarray | None:
        """Get the next seq_len+1 tokens (for input/target split).

        Returns None if all shards exhausted (call reset() for new epoch).
        """
        need = seq_len + 1

        while self._current_mmap is not None:
            remaining = len(self._current_mmap) - self._pos_in_shard
            if remaining >= need:
                seq = self._current_mmap[self._pos_in_shard:self._pos_in_shard + need].copy()
                self._pos_in_shard += seq_len
                return seq

            # Not enough tokens in this shard, move to next
            self._shard_idx += 1
            self._load_current_shard()

        return None

    def reset(self, shuffle: bool = True):
        """Reset to beginning. Optionally shuffle shard order."""
        if shuffle:
            self.rng.shuffle(self._shard_order)
        self._shard_idx = 0
        self._pos_in_shard = 0
        self._load_current_shard()


class MixedPretrainDataset:
    """Stochastic multi-source dataset for pretraining.

    Samples from sources according to configured weights.
    Yields (input_ids, targets) tuples where targets = input_ids shifted by 1.
    """

    def __init__(
        self,
        config_path: str,
        seq_len: int | None = None,
        seed: int = 42,
        tokenized_dir: str | None = None,
    ):
        with open(config_path) as f:
            self.config = yaml.safe_load(f)

        self.seq_len = seq_len or self.config.get("seq_len", 2048)
        self.seed = seed
        self.rng = np.random.RandomState(seed)

        # Resolve tokenized data directory
        if tokenized_dir:
            self.tokenized_dir = Path(tokenized_dir)
        else:
            # Default: data/pretrain/tokenized relative to config
            self.tokenized_dir = Path(config_path).parent.parent / "data" / "pretrain" / "tokenized"

        # Load sources with weights
        self.sources = {}
        self.weights = {}
        sources_config = self.config.get("sources", {})

        for name, src_cfg in sources_config.items():
            source_dir = self.tokenized_dir / name
            if not source_dir.exists() or not list(source_dir.glob("shard_*.bin")):
                print(f"  Skipping {name} (no tokenized shards)")
                continue
            try:
                reader = ShardReader(source_dir, seed=seed)
                self.sources[name] = reader
                self.weights[name] = src_cfg.get("weight", 0.0)
            except FileNotFoundError:
                print(f"  Skipping {name} (no shard files)")

        if not self.sources:
            raise RuntimeError(f"No tokenized sources found in {self.tokenized_dir}")

        # Normalize weights to sum to 1.0
        total_weight = sum(self.weights.values())
        self.weight_array = np.array([self.weights[n] / total_weight for n in self.sources])
        self.source_names = list(self.sources.keys())

        self._epoch = 0
        self._steps = 0

        print(f"MixedPretrainDataset initialized:")
        print(f"  Sources: {len(self.sources)}")
        print(f"  Seq len: {self.seq_len}")
        for name in self.source_names:
            reader = self.sources[name]
            pct = self.weights[name] / total_weight * 100
            print(f"    {name}: {reader.total_tokens:,} tokens, {pct:.1f}% weight")
        total_tokens = sum(r.total_tokens for r in self.sources.values())
        print(f"  Total tokens: {total_tokens:,}")

    def __iter__(self):
        return self

    def __next__(self) -> tuple[np.ndarray, np.ndarray]:
        """Yield next (input_ids, targets) pair.

        Stochastically selects a source based on weights,
        gets a sequence, and splits into input/target.
        """
        # Try up to len(sources) times to find a non-exhausted source
        for _ in range(len(self.sources)):
            # Weighted random source selection
            idx = self.rng.choice(len(self.source_names), p=self.weight_array)
            name = self.source_names[idx]
            reader = self.sources[name]

            seq = reader.get_sequence(self.seq_len)
            if seq is not None:
                self._steps += 1
                input_ids = seq[:-1].astype(np.int64)
                targets = seq[1:].astype(np.int64)
                return input_ids, targets

            # Source exhausted — reset it (new epoch for this source)
            reader.reset()
            seq = reader.get_sequence(self.seq_len)
            if seq is not None:
                self._steps += 1
                input_ids = seq[:-1].astype(np.int64)
                targets = seq[1:].astype(np.int64)
                return input_ids, targets

        # All sources exhausted even after reset — shouldn't happen
        raise StopIteration

    def state_dict(self) -> dict:
        """Return state for checkpointing."""
        return {
            "epoch": self._epoch,
            "steps": self._steps,
            "positions": {name: self.sources[name]._pos for name in self.source_names},
        }


def main():
    """Quick test: iterate a few batches and print stats."""
    import argparse
    import time

    parser = argparse.ArgumentParser(description="Test MixedPretrainDataset")
    parser.add_argument("--config", default="configs/pretrain_mix.yaml")
    parser.add_argument("--tokenized-dir", default=None)
    parser.add_argument("--seq-len", type=int, default=2048)
    parser.add_argument("--steps", type=int, default=100, help="Number of steps to test")
    args = parser.parse_args()

    ds = MixedPretrainDataset(args.config, seq_len=args.seq_len, tokenized_dir=args.tokenized_dir)

    print(f"\nRunning {args.steps} steps...")
    start = time.time()

    for i, (input_ids, targets) in enumerate(ds):
        if i >= args.steps:
            break

        # Track which source was sampled (by checking first token against each reader's position)
        assert input_ids.shape == (args.seq_len,), f"Bad input shape: {input_ids.shape}"
        assert targets.shape == (args.seq_len,), f"Bad target shape: {targets.shape}"
        assert input_ids.dtype == np.int64
        assert (targets[:-1] == input_ids[1:]).all(), "Targets should be input shifted by 1"

    elapsed = time.time() - start
    print(f"\n  {args.steps} steps in {elapsed:.1f}s ({args.steps / elapsed:.0f} steps/s)")
    print(f"  Sequence shape: ({args.seq_len},)")
    print(f"  Token range: [{input_ids.min()}, {input_ids.max()}]")
    print(f"  All assertions passed.")


if __name__ == "__main__":
    main()
