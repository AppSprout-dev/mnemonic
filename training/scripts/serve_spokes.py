#!/usr/bin/env python3
"""Serve Qwen 3.5 2B + Spokes as an OpenAI-compatible API.

Exposes POST /v1/chat/completions so the mnemonic daemon can use the
spoke model as a drop-in replacement for any OpenAI-compatible LLM provider.

Usage:
    source ~/Projects/felixlm/.venv/bin/activate
    python serve_spokes.py --port 8899 --spokes ../../checkpoints/exp18_v5_12k/best_spokes.pt

Requires: transformers, torch (ROCm or CUDA)
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

# Add training scripts to path for spoke adapter import
SCRIPT_DIR = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPT_DIR))

from qwen_spoke_adapter import QwenWithSpokes, SpokeConfig  # noqa: E402

# Global model state (loaded once at startup)
MODEL = None
TOKENIZER = None
DEVICE = None
GENERATE_LOCK = Lock()  # serialize GPU access


def load_model(base_model: str, spoke_path: str, device: str) -> None:
    """Load the base model + spoke weights into global state."""
    global MODEL, TOKENIZER, DEVICE

    if device == "auto":
        DEVICE = torch.device("cuda" if torch.cuda.is_available() else "cpu")
    else:
        DEVICE = torch.device(device)

    print(f"Loading tokenizer: {base_model}")
    TOKENIZER = AutoTokenizer.from_pretrained(base_model)

    print(f"Loading model: {base_model}")
    data = torch.load(spoke_path, weights_only=True, map_location="cpu")
    spoke_config = SpokeConfig(**data["spoke_config"])

    MODEL = QwenWithSpokes.from_pretrained(
        base_model, spoke_config=spoke_config, dtype=torch.bfloat16
    )
    MODEL.load_spokes(spoke_path)
    MODEL.to(DEVICE)
    MODEL.eval()
    print(f"Model ready on {DEVICE}")


def generate(messages: list[dict], max_tokens: int = 1024) -> dict:
    """Generate a completion from chat messages."""
    # Build prompt using chat template
    prompt = TOKENIZER.apply_chat_template(
        messages, tokenize=False, add_generation_prompt=True
    )
    # Skip thinking tokens — go straight to output
    prompt += "</think>\n\n"

    input_ids = TOKENIZER.encode(prompt, return_tensors="pt").to(DEVICE)
    prompt_len = input_ids.shape[1]

    with GENERATE_LOCK:
        with torch.no_grad():
            output_ids = MODEL.base_model.generate(
                input_ids,
                max_new_tokens=max_tokens,
                do_sample=False,
                temperature=None,
                top_p=None,
                pad_token_id=TOKENIZER.eos_token_id,
            )

    generated_ids = output_ids[0, prompt_len:]
    text = TOKENIZER.decode(generated_ids, skip_special_tokens=True).strip()
    completion_tokens = len(generated_ids)

    return {
        "text": text,
        "prompt_tokens": prompt_len,
        "completion_tokens": completion_tokens,
    }


class ChatCompletionHandler(BaseHTTPRequestHandler):
    """Handles OpenAI-compatible /v1/chat/completions requests."""

    def do_POST(self):
        if self.path == "/v1/chat/completions":
            self._handle_chat()
        else:
            self._respond(404, {"error": f"Not found: {self.path}"})

    def do_GET(self):
        if self.path in ("/v1/models", "/v1/models/"):
            self._handle_models()
        elif self.path == "/health":
            self._respond(200, {"status": "ok"})
        else:
            self._respond(404, {"error": f"Not found: {self.path}"})

    def _handle_chat(self):
        try:
            length = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(length))
        except (json.JSONDecodeError, ValueError) as e:
            self._respond(400, {"error": f"Invalid JSON: {e}"})
            return

        messages = body.get("messages", [])
        if not messages:
            self._respond(400, {"error": "messages is required"})
            return

        max_tokens = body.get("max_tokens", 1024)
        start = time.time()

        try:
            result = generate(messages, max_tokens)
        except Exception as e:
            self._respond(500, {"error": str(e)})
            return

        elapsed = time.time() - start
        resp = {
            "id": f"chatcmpl-{uuid.uuid4().hex[:12]}",
            "object": "chat.completion",
            "created": int(time.time()),
            "model": body.get("model", "qwen-spokes"),
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
                "total_tokens": result["prompt_tokens"]
                + result["completion_tokens"],
            },
        }

        print(
            f"  [{elapsed:.1f}s] {result['prompt_tokens']}+{result['completion_tokens']} tokens"
        )
        self._respond(200, resp)

    def _handle_models(self):
        self._respond(
            200,
            {
                "object": "list",
                "data": [
                    {
                        "id": "qwen-spokes",
                        "object": "model",
                        "owned_by": "local",
                    }
                ],
            },
        )

    def _respond(self, status: int, body: dict):
        data = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, fmt, *args):
        # Suppress default access log noise
        pass


def main():
    parser = argparse.ArgumentParser(
        description="Serve Qwen spokes as OpenAI-compatible API"
    )
    parser.add_argument(
        "--base-model",
        default="Qwen/Qwen3.5-2B",
        help="Base model path or HF name",
    )
    parser.add_argument(
        "--spokes",
        required=True,
        help="Path to spoke weights checkpoint",
    )
    parser.add_argument("--port", type=int, default=8899, help="Server port")
    parser.add_argument(
        "--device", default="auto", help="Device (auto, cpu, cuda)"
    )
    args = parser.parse_args()

    load_model(args.base_model, args.spokes, args.device)

    server = HTTPServer(("0.0.0.0", args.port), ChatCompletionHandler)
    print(f"\nServing on http://0.0.0.0:{args.port}/v1/chat/completions")
    print("Ctrl+C to stop\n")

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nShutting down...")
        server.shutdown()
        MODEL.remove_hooks()


if __name__ == "__main__":
    main()
