# Follow-up: llama-server 500 `Compute error.` diagnostics

This effort intentionally did not root-cause live `llama_server:catalog:glm-4.5-air-q4_k_m` HTTP 500 `Compute error.` responses. The routing/context fixes prevent context-overflow failures from cascading blindly into smaller or unknown local fallback targets, but the local runtime failure itself remains unresolved.

Future diagnostic scope:

- Reproduce a failing local compaction/summarization request outside the main turn loop.
- Capture provider/model/window/input-token metadata without prompt bodies or API keys.
- Distinguish deterministic context-fit failures from model-loading/runtime/Metal failures.
- Verify whether `Compute error.` is tied to payload shape, model context size, llama-server lifecycle, or local hardware/runtime state.
