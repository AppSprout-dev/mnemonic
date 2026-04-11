#!/usr/bin/env python3
"""Serve Gemma 4 E2B + Spokes as an OpenAI-compatible API.

Exposes POST /v1/chat/completions, POST /v1/embeddings, and GET /v1/models
so the mnemonic daemon can use the Gemma spoke model as a drop-in replacement
for any OpenAI-compatible LLM provider. Fully air-gapped.

The server loads the base Gemma 4 E2B model (NF4-quantized by default for
16GB VRAM cards) and injects trained Felix spoke adapters. Generation uses
HuggingFace's generate() — the proven path from EXP-30 evaluation.

Architecture notes:
  - Gemma 4 E2B is a conditional generation model (2.3B text params)
  - Spokes are ~27.5M params (~1.2% overhead), injected at all 35 layers
  - NF4 quantization: ~2.5GB base + ~110MB spokes + ~1GB KV cache
  - PLE (Per-Layer Embeddings) offloaded to CPU to save ~4.7GB VRAM
  - Vision/audio towers stripped at load time (text-only task)

Usage:
    source ~/Projects/felixlm/.venv/bin/activate
    python serve_gemma_spokes.py \\
        --spokes ../../checkpoints/exp30_gemma4_v7_faithful/best_spokes.pt

    # Full precision (requires >16GB VRAM):
    python serve_gemma_spokes.py --spokes <path> --no-quantize

    # Without embeddings:
    python serve_gemma_spokes.py --spokes <path> --embedding-model none

Requires: transformers, torch (ROCm or CUDA), bitsandbytes, sentence-transformers
"""

import argparse
import json
import sys
import time
import uuid
from http.server import HTTPServer, BaseHTTPRequestHandler
from pathlib import Path
from threading import Lock

import torch
from transformers import AutoTokenizer

# Add training scripts to path for adapter imports
SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

from gemma_spoke_adapter import GemmaWithSpokes, SpokeConfig  # noqa: E402

# ---------------------------------------------------------------------------
# Global state (loaded once at startup)
# ---------------------------------------------------------------------------
MODEL: GemmaWithSpokes | None = None
TOKENIZER: AutoTokenizer | None = None
DEVICE: torch.device | None = None
EMBED_MODEL = None
GENERATE_LOCK = Lock()
EMBED_LOCK = Lock()

# Model identifier reported in API responses
MODEL_ID = "gemma-4-e2b-spokes"


def load_model(
    base_model: str,
    spoke_path: str,
    device: str,
    embedding_model: str | None = None,
    no_quantize: bool = False,
    no_compile: bool = False,
    no_ple_offload: bool = False,
) -> None:
    """Load base Gemma 4 E2B + spoke weights and optional embedding model.

    Args:
        base_model: HuggingFace model name or local path.
        spoke_path: Path to spoke checkpoint (.pt file from training).
        device: Target device ("auto", "cpu", "cuda").
        embedding_model: Sentence-transformers model for /v1/embeddings.
        no_quantize: If True, load in bf16 instead of NF4.
        no_compile: If True, skip torch.compile.
        no_ple_offload: If True, keep PLE on GPU (needs >16GB VRAM).
    """
    global MODEL, TOKENIZER, DEVICE, EMBED_MODEL

    if device == "auto":
        DEVICE = torch.device("cuda" if torch.cuda.is_available() else "cpu")
    else:
        DEVICE = torch.device(device)

    torch.set_float32_matmul_precision("high")

    # Load tokenizer
    print(f"Loading tokenizer: {base_model}")
    TOKENIZER = AutoTokenizer.from_pretrained(base_model)
    print(f"  Vocab size: {TOKENIZER.vocab_size}")

    # Load spoke config from checkpoint
    print(f"Loading spoke config from: {spoke_path}")
    data = torch.load(spoke_path, weights_only=True, map_location="cpu")
    spoke_config = SpokeConfig(**data["spoke_config"])
    print(f"  Spokes: {spoke_config.num_spokes} x rank {spoke_config.spoke_rank}")

    # Load base model + inject spokes
    # GemmaWithSpokes.from_pretrained handles NF4, PLE offload, tower stripping
    MODEL = GemmaWithSpokes.from_pretrained(
        base_model,
        spoke_config=spoke_config,
        dtype=torch.bfloat16,
        no_quantize=no_quantize,
        offload_ple=not no_ple_offload,
    )
    MODEL.load_spokes(spoke_path)
    # Move spokes to GPU, keeping fp32 dtype.
    # SpokeLayer.forward() explicitly casts input to fp32 for numerical stability
    # (h.float() on line 206 of qwen_spoke_adapter.py), so spoke weights must
    # stay fp32 to match. The 110MB overhead is negligible vs the base model.
    for spoke in MODEL.spokes.values():
        for param in spoke.parameters():
            param.data = param.data.to(device=DEVICE)
    spoke_device = next(iter(MODEL.spokes.values())).gate_bias.device
    print(f"  Spokes moved to {spoke_device} (fp32, ~110MB)")
    MODEL.eval()
    print(f"Model ready on {DEVICE}")

    # torch.compile for fused kernels
    # Gemma 4 E2B has ISWA + PLE + Gated Delta Net — use "default" mode
    # to avoid cudagraph issues with these novel components.
    if not no_compile and DEVICE.type == "cuda":
        import os
        os.environ.setdefault("TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL", "1")
        print("Compiling model with torch.compile (30-120s on first call)...")
        MODEL.base_model.forward = torch.compile(
            MODEL.base_model.forward, mode="default"
        )
        _warmup_generate()
        print("Compilation complete.")

    # Load embedding model on CPU to save VRAM
    if embedding_model:
        from sentence_transformers import SentenceTransformer
        print(f"Loading embedding model: {embedding_model}")
        EMBED_MODEL = SentenceTransformer(embedding_model, device="cpu")
        dim = EMBED_MODEL.get_sentence_embedding_dimension()
        print(f"Embedding model ready ({dim}d)")


def _warmup_generate():
    """Trigger torch.compile tracing with a short generation."""
    dummy_ids = TOKENIZER.encode("Hello", return_tensors="pt").to(DEVICE)
    with torch.no_grad():
        MODEL.base_model.generate(
            dummy_ids,
            max_new_tokens=2,
            do_sample=False,
            pad_token_id=TOKENIZER.eos_token_id,
        )


# ---------------------------------------------------------------------------
# Inference
# ---------------------------------------------------------------------------

def _strip_code_fences(text: str) -> str:
    """Strip markdown code fences from model output.

    Gemma chat models often wrap JSON in ```json ... ```. The daemon
    expects raw JSON in the response content field.
    """
    stripped = text.strip()
    if stripped.startswith("```"):
        # Remove opening fence (```json, ```JSON, ```, etc.)
        first_newline = stripped.find("\n")
        if first_newline != -1:
            stripped = stripped[first_newline + 1:]
        # Remove closing fence
        if stripped.rstrip().endswith("```"):
            stripped = stripped.rstrip()[:-3].rstrip()
    return stripped


def _prepare_messages(messages: list[dict]) -> list[dict]:
    """Normalize incoming messages for Gemma's chat template.

    The daemon sends system + user messages, but EXP-30 training data used
    user-only messages (no system prompt). Gemma 4's chat template does
    support system messages, so we pass them through — the model handles
    the template internally. If the system message is the standard encoding
    agent prompt, it's redundant with the faithful prompt in the user message,
    but harmless.
    """
    # Filter to roles Gemma supports: system, user, assistant
    normalized = []
    for msg in messages:
        role = msg.get("role", "user")
        content = msg.get("content", "")
        if not content:
            continue
        if role in ("system", "user", "assistant"):
            normalized.append({"role": role, "content": content})
        elif role == "tool":
            # Tool responses aren't used for encoding — skip
            continue
        else:
            # Unknown role — treat as user
            normalized.append({"role": "user", "content": content})
    return normalized


def generate(
    messages: list[dict],
    max_tokens: int = 4096,
    temperature: float = 0.0,
) -> dict:
    """Generate a completion from chat messages.

    Returns dict with text, prompt_tokens, completion_tokens, tok_per_sec.
    """
    messages = _prepare_messages(messages)

    # Apply Gemma chat template
    prompt = TOKENIZER.apply_chat_template(
        messages, tokenize=False, add_generation_prompt=True
    )

    input_ids = TOKENIZER.encode(prompt, return_tensors="pt").to(DEVICE)
    prompt_len = input_ids.shape[1]
    attention_mask = torch.ones_like(input_ids)

    # Generation config — match EXP-30 eval settings
    gen_kwargs = dict(
        max_new_tokens=max_tokens,
        attention_mask=attention_mask,
        pad_token_id=TOKENIZER.eos_token_id,
    )

    if temperature <= 0.0:
        gen_kwargs["do_sample"] = False
        gen_kwargs["temperature"] = None
        gen_kwargs["top_p"] = None
    else:
        gen_kwargs["do_sample"] = True
        gen_kwargs["temperature"] = temperature
        gen_kwargs["top_p"] = 0.95

    with GENERATE_LOCK:
        start = time.perf_counter()
        with torch.no_grad():
            output_ids = MODEL.base_model.generate(input_ids, **gen_kwargs)
        elapsed = time.perf_counter() - start

    generated_ids = output_ids[0, prompt_len:]
    text = TOKENIZER.decode(generated_ids, skip_special_tokens=True).strip()
    # Strip markdown code fences — Gemma chat models often wrap JSON in ```json ... ```
    text = _strip_code_fences(text)
    completion_tokens = len(generated_ids)
    tok_per_sec = completion_tokens / elapsed if elapsed > 0 else 0.0

    return {
        "text": text,
        "prompt_tokens": prompt_len,
        "completion_tokens": completion_tokens,
        "elapsed": elapsed,
        "tok_per_sec": tok_per_sec,
    }


def embed(texts: list[str]) -> list[list[float]]:
    """Generate embeddings for a list of texts."""
    if EMBED_MODEL is None:
        raise RuntimeError("Embedding model not loaded (start with --embedding-model)")
    with EMBED_LOCK:
        embeddings = EMBED_MODEL.encode(texts, normalize_embeddings=True)
    return embeddings.tolist()


# ---------------------------------------------------------------------------
# HTTP server (OpenAI-compatible)
# ---------------------------------------------------------------------------

class SpokeHandler(BaseHTTPRequestHandler):
    """OpenAI-compatible API handler for Gemma spoke inference."""

    def do_POST(self):
        if self.path == "/v1/chat/completions":
            self._handle_chat()
        elif self.path == "/v1/embeddings":
            self._handle_embeddings()
        else:
            self._error(404, f"Not found: {self.path}")

    def do_GET(self):
        if self.path in ("/v1/models", "/v1/models/"):
            self._handle_models()
        elif self.path == "/health":
            self._respond(200, {"status": "ok"})
        else:
            self._error(404, f"Not found: {self.path}")

    def _read_body(self) -> dict | None:
        """Read and parse JSON request body."""
        try:
            length = int(self.headers.get("Content-Length", 0))
            return json.loads(self.rfile.read(length))
        except (json.JSONDecodeError, ValueError) as e:
            self._error(400, f"Invalid JSON: {e}")
            return None

    def _handle_chat(self):
        body = self._read_body()
        if body is None:
            return

        messages = body.get("messages", [])
        if not messages:
            self._error(400, "messages is required")
            return

        max_tokens = body.get("max_tokens", 4096)
        temperature = body.get("temperature", 0.0)

        try:
            result = generate(messages, max_tokens, temperature)
        except Exception as e:
            self._error(500, str(e))
            return

        resp = {
            "id": f"chatcmpl-{uuid.uuid4().hex[:12]}",
            "object": "chat.completion",
            "created": int(time.time()),
            "model": body.get("model", MODEL_ID),
            "choices": [
                {
                    "index": 0,
                    "message": {
                        "role": "assistant",
                        "content": result["text"],
                    },
                    "finish_reason": "stop",
                }
            ],
            "usage": {
                "prompt_tokens": result["prompt_tokens"],
                "completion_tokens": result["completion_tokens"],
                "total_tokens": result["prompt_tokens"] + result["completion_tokens"],
            },
        }

        print(
            f"  [{result['elapsed']:.1f}s] "
            f"{result['prompt_tokens']}+{result['completion_tokens']} tokens "
            f"({result['tok_per_sec']:.1f} tok/s)"
        )
        self._respond(200, resp)

    def _handle_embeddings(self):
        body = self._read_body()
        if body is None:
            return

        inp = body.get("input", [])
        if isinstance(inp, str):
            inp = [inp]
        if not inp:
            self._error(400, "input is required")
            return

        start = time.perf_counter()
        try:
            vectors = embed(inp)
        except RuntimeError as e:
            self._error(500, str(e))
            return

        elapsed = time.perf_counter() - start
        data = [
            {"object": "embedding", "index": i, "embedding": vec}
            for i, vec in enumerate(vectors)
        ]
        resp = {
            "object": "list",
            "data": data,
            "model": body.get("model", "all-MiniLM-L6-v2"),
            "usage": {
                "prompt_tokens": sum(len(t.split()) for t in inp),
                "total_tokens": sum(len(t.split()) for t in inp),
            },
        }
        print(f"  [embed {elapsed:.3f}s] {len(inp)} text(s)")
        self._respond(200, resp)

    def _handle_models(self):
        models = [
            {"id": MODEL_ID, "object": "model", "owned_by": "local"},
        ]
        if EMBED_MODEL is not None:
            models.append(
                {"id": "all-MiniLM-L6-v2", "object": "model", "owned_by": "local"}
            )
        self._respond(200, {"object": "list", "data": models})

    def _respond(self, status: int, body: dict):
        data = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def _error(self, status: int, message: str):
        self._respond(status, {"error": {"message": message, "type": "server_error"}})

    def log_message(self, fmt, *args):
        pass  # Suppress default access log


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="Serve Gemma 4 E2B + Felix spokes as an OpenAI-compatible API",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Default (NF4, port 8899, with embeddings):
  python serve_gemma_spokes.py --spokes ../../checkpoints/exp30_gemma4_v7_faithful/best_spokes.pt

  # Full precision, no torch.compile:
  python serve_gemma_spokes.py --spokes <path> --no-quantize --no-compile

  # Custom port, no embeddings:
  python serve_gemma_spokes.py --spokes <path> --port 8800 --embedding-model none
""",
    )
    parser.add_argument(
        "--base-model",
        default="google/gemma-4-E2B-it",
        help="Base model (HF name or local path). Default: google/gemma-4-E2B-it",
    )
    parser.add_argument(
        "--spokes",
        required=True,
        help="Path to trained spoke weights (.pt checkpoint)",
    )
    parser.add_argument(
        "--port", type=int, default=8899,
        help="Server port. Default: 8899",
    )
    parser.add_argument(
        "--device", default="auto",
        help="Device: auto, cpu, cuda. Default: auto",
    )
    parser.add_argument(
        "--embedding-model",
        default="sentence-transformers/all-MiniLM-L6-v2",
        help="Embedding model for /v1/embeddings ('none' to disable). "
             "Default: sentence-transformers/all-MiniLM-L6-v2",
    )
    parser.add_argument(
        "--no-quantize", action="store_true",
        help="Load in bf16 instead of NF4 (requires >16GB VRAM)",
    )
    parser.add_argument(
        "--no-compile", action="store_true",
        help="Skip torch.compile (faster startup, slower inference)",
    )
    parser.add_argument(
        "--no-ple-offload", action="store_true",
        help="Keep PLE on GPU (requires >16GB VRAM)",
    )
    args = parser.parse_args()

    # Validate spoke path
    spoke_path = Path(args.spokes)
    if not spoke_path.exists():
        print(f"Error: spoke checkpoint not found: {spoke_path}")
        sys.exit(1)

    embed_model = None if args.embedding_model == "none" else args.embedding_model

    load_model(
        args.base_model,
        str(spoke_path),
        args.device,
        embedding_model=embed_model,
        no_quantize=args.no_quantize,
        no_compile=args.no_compile,
        no_ple_offload=args.no_ple_offload,
    )

    server = HTTPServer(("0.0.0.0", args.port), SpokeHandler)
    print(f"\nServing on http://0.0.0.0:{args.port}")
    print(f"  POST /v1/chat/completions  (model: {MODEL_ID})")
    if EMBED_MODEL is not None:
        print(f"  POST /v1/embeddings")
    print(f"  GET  /v1/models")
    print(f"  GET  /health")
    print("Ctrl+C to stop\n")

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nShutting down...")
        server.shutdown()


if __name__ == "__main__":
    main()
