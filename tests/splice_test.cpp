// SPLICE Phase 1 verification: prove that modifying a spoke gate_bias
// changes model output between inference batches.
//
// Build:
//   g++ -std=c++17 -O2 tests/splice_test.cpp -o bin/splice_test \
//       -Ithird_party/llama.cpp/include -Ithird_party/llama.cpp/ggml/include \
//       -Lthird_party/llama.cpp/build/src -lllama \
//       -Lthird_party/llama.cpp/build/ggml/src -lggml -lggml-base -lggml-cpu \
//       -lm -lstdc++ -lpthread -fopenmp
//
// Run:
//   ./bin/splice_test models/gemma4-e2b-exp31-spokes-f16.gguf

#include "llama.h"
#include "ggml-backend.h"

#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>
#include <vector>

static std::string generate(llama_context * ctx, const llama_vocab * vocab,
                            const char * prompt, int max_tokens) {
    // Tokenize
    int n = strlen(prompt);
    std::vector<llama_token> tokens(n + 64);
    int n_tokens = llama_tokenize(vocab, prompt, n, tokens.data(), tokens.size(), false, true);
    if (n_tokens < 0) {
        tokens.resize(-n_tokens);
        n_tokens = llama_tokenize(vocab, prompt, n, tokens.data(), tokens.size(), false, true);
    }
    tokens.resize(n_tokens);

    // Clear KV cache
    llama_memory_clear(llama_get_memory(ctx), true);

    // Decode prompt
    int n_batch = llama_n_batch(ctx);
    for (int i = 0; i < n_tokens; i += n_batch) {
        int chunk = std::min(n_tokens - i, n_batch);
        llama_batch batch = llama_batch_init(chunk, 0, 1);
        for (int j = 0; j < chunk; j++) {
            int idx = batch.n_tokens;
            batch.token[idx] = tokens[i + j];
            batch.pos[idx] = i + j;
            batch.n_seq_id[idx] = 1;
            batch.seq_id[idx][0] = 0;
            batch.logits[idx] = (i + j == n_tokens - 1) ? 1 : 0;
            batch.n_tokens++;
        }
        llama_decode(ctx, batch);
        llama_batch_free(batch);
    }

    // Set up greedy sampler (deterministic)
    auto sparams = llama_sampler_chain_default_params();
    llama_sampler * smpl = llama_sampler_chain_init(sparams);
    llama_sampler_chain_add(smpl, llama_sampler_init_greedy());

    // Generate
    std::string output;
    int n_pos = n_tokens;
    for (int i = 0; i < max_tokens; i++) {
        llama_token new_token = llama_sampler_sample(smpl, ctx, -1);
        if (llama_vocab_is_eog(vocab, new_token)) break;

        char buf[256];
        int len = llama_token_to_piece(vocab, new_token, buf, sizeof(buf), 0, true);
        if (len > 0) output.append(buf, len);

        llama_batch batch = llama_batch_init(1, 0, 1);
        batch.token[0] = new_token;
        batch.pos[0] = n_pos++;
        batch.n_seq_id[0] = 1;
        batch.seq_id[0][0] = 0;
        batch.logits[0] = 1;
        batch.n_tokens = 1;
        llama_decode(ctx, batch);
        llama_batch_free(batch);
    }

    llama_sampler_free(smpl);
    return output;
}

int main(int argc, char ** argv) {
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <model.gguf>\n", argv[0]);
        return 1;
    }

    const char * model_path = argv[1];
    const char * prompt = "Summarize the following text in JSON format:\n\nThe quick brown fox jumped over the lazy dog near the river bank on a sunny afternoon.\n\nJSON:";
    const int n_layers = 35;
    const int max_tokens = 30;

    printf("SPLICE Phase 1 Test\n");
    printf("===================\n");
    printf("Model: %s\n", model_path);
    printf("Prompt: \"%s\"\n", prompt);
    printf("Strategy: disable ALL %d spoke layers\n\n", n_layers);

    // Init
    llama_backend_init();

    auto mparams = llama_model_default_params();
    mparams.n_gpu_layers = 99;

    printf("Loading model...\n");
    llama_model * model = llama_model_load_from_file(model_path, mparams);
    if (!model) {
        fprintf(stderr, "Failed to load model\n");
        return 1;
    }

    auto cparams = llama_context_default_params();
    cparams.n_ctx = 512;
    cparams.n_batch = 512;
    cparams.n_threads = 4;
    cparams.n_threads_batch = 4;

    llama_context * ctx = llama_init_from_model(model, cparams);
    if (!ctx) {
        fprintf(stderr, "Failed to create context\n");
        return 1;
    }

    const llama_vocab * vocab = llama_model_get_vocab(model);

    // Save original gate biases for all layers
    float original_biases[35];
    printf("Reading original gate biases:\n");
    for (int il = 0; il < n_layers; il++) {
        char tname[64];
        snprintf(tname, sizeof(tname), "blk.%d.spoke.gate_bias", il);
        int rc = llama_model_get_tensor_data(model, tname, &original_biases[il], 0, sizeof(float));
        if (rc != 0) {
            printf("  WARNING: could not read layer %d (rc=%d)\n", il, rc);
            original_biases[il] = 0.0f;
        }
    }
    printf("  Layer  0: %.4f (sigmoid=%.3f)\n", original_biases[0], 1.0f/(1.0f+expf(-original_biases[0])));
    printf("  Layer 17: %.4f (sigmoid=%.3f)\n", original_biases[17], 1.0f/(1.0f+expf(-original_biases[17])));
    printf("  Layer 34: %.4f (sigmoid=%.3f)\n", original_biases[34], 1.0f/(1.0f+expf(-original_biases[34])));

    // --- Generation 1: original spokes ---
    printf("\n--- Generation 1 (original spokes) ---\n");
    std::string out1 = generate(ctx, vocab, prompt, max_tokens);
    printf("Output: \"%s\"\n", out1.c_str());

    // --- Disable ALL spokes ---
    printf("\n--- Disabling ALL %d spoke layers (gate_bias = -10.0) ---\n", n_layers);
    float disabled_bias = -10.0f;
    for (int il = 0; il < n_layers; il++) {
        char tname[64];
        snprintf(tname, sizeof(tname), "blk.%d.spoke.gate_bias", il);
        int rc = llama_model_set_tensor_data(model, tname, &disabled_bias, 0, sizeof(float));
        if (rc != 0) {
            fprintf(stderr, "  FAILED to set layer %d (rc=%d)\n", il, rc);
        }
    }
    printf("All spokes disabled.\n");

    // --- Generation 2: all spokes disabled ---
    printf("\n--- Generation 2 (ALL spokes disabled) ---\n");
    std::string out2 = generate(ctx, vocab, prompt, max_tokens);
    printf("Output: \"%s\"\n", out2.c_str());

    // --- Restore ALL original biases ---
    printf("\n--- Restoring ALL original gate biases ---\n");
    for (int il = 0; il < n_layers; il++) {
        char tname[64];
        snprintf(tname, sizeof(tname), "blk.%d.spoke.gate_bias", il);
        llama_model_set_tensor_data(model, tname, &original_biases[il], 0, sizeof(float));
    }
    printf("All spokes restored.\n");

    // --- Generation 3: restored ---
    printf("\n--- Generation 3 (restored) ---\n");
    std::string out3 = generate(ctx, vocab, prompt, max_tokens);
    printf("Output: \"%s\"\n", out3.c_str());

    // Verdict
    printf("\n===================\n");
    printf("RESULTS:\n");
    printf("  Gen1 == Gen2: %s\n", (out1 == out2) ? "SAME (FAIL — spokes had no effect)" : "DIFFERENT (PASS — disabling spokes changed output)");
    printf("  Gen1 == Gen3: %s\n", (out1 == out3) ? "SAME (PASS — restore worked)" : "DIFFERENT (FAIL — restore didn't work)");

    bool pass = (out1 != out2) && (out1 == out3);
    printf("\n  SPLICE Phase 1: %s\n", pass ? "PASS" : "FAIL");

    // Cleanup
    llama_free(ctx);
    llama_model_free(model);
    llama_backend_free();

    return pass ? 0 : 1;
}
