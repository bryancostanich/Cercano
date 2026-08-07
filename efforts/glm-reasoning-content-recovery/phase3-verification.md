# Phase 3 — Live GLM Verification Evidence

Server: throwaway GLM-4.5-Air llama-server, port 51919, launched
`--model GLM-4.5-Air-Q4_K_M-00001-of-00002.gguf --jinja --ctx-size 8192`,
build 9890 (`/opt/homebrew/bin/llama-server`). Health: `{"status":"ok"}`.
Model load took ~15s wall once mmap warmed (log: `model loaded` at 0.14.580).

## Probes

### P1 — non-streaming single-turn
Prompt: "Reply with exactly: hello world"
- `content = 'hello world'`  ← answer in content (correct)
- `reasoning_content = 'We are given a very specific instruction...'` (thinking)
Adapter behavior: takes content, ignores reasoning. Correct.

### P2 — non-streaming multi-turn with prior assistant history
Messages: France?→Paris.→"And of Japan? One word."
- `content = 'Tokyo'`  ← answer in content (correct)
- `reasoning_content = 'Okay, the user is asking...'` (thinking)
Adapter behavior: takes content, ignores reasoning. Correct.

### P3 — STREAMING single-turn — REPRODUCES THE BUG
Prompt: "Reply with exactly: streaming works"
- streamed `content = ''`  ← EMPTY
- streamed `reasoning_content` length = 290  ← full answer misfiled here

This is the exact failure the fix targets. Without the Phase 2 streaming
fallback, the CLI shows an empty assistant turn. With it, the buffered
reasoning is flushed as a single visible text delta at EOF.

## Conclusion

- The empty-`content` + populated-`reasoning_content` failure is real and
  reproduces live on the streaming path with `--jinja`.
- Phase 1 (non-streaming) and Phase 2 (streaming) unit tests model exactly
  these shapes and pass deterministically.
- GLM is usable through the adapter: non-streaming returns clean content;
  streaming is recovered by the fallback.
- Gate for Phase 4 (catalog/config routing) is SATISFIED.

## Note for Phase 4 launch args

`--jinja` produced valid output on all probes (answer in content for
non-stream, in reasoning_content for stream). The prior `compute status: -1`
failure was observed only on the *no-jinja* default-template path. Therefore
the GLM catalog entry should launch with `--jinja` (via per-model ExtraArgs)
OR the adapter fix makes the reasoning-content split harmless either way.
Recommend `--jinja` in catalog ExtraArgs as the safe default, given no-jinja
compute-fails on this build.
