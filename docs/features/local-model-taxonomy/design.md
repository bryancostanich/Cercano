# Local model taxonomy cleanup — design

**Status:** draft for review · 2026-07-07
**Branch:** `feat/local-model-taxonomy` (worktree `.worktrees/local-model-taxonomy`)

## Problem

Three symptoms of one underlying issue — the local-model configuration grew in
layers and the older layers are still load-bearing:

1. **`open_model` is vestigial and confusing.** The models taxonomy
   (`models.tiers.*`) was added on top of the legacy `open_model` key, with
   `resolveTierModel` falling back from an empty `everyday.open` slot *to*
   `open_model`. So the config has two ways to say "my local chat model," the
   `/m` dashboard exposes both ("chat model" row + tier rows), and users
   reasonably can't tell which one drives what. It also stored runtime hash
   IDs until 2b7aeb58.
2. **Embeddings are hardwired to Ollama.** The SmartRouter's embedder is the
   Ollama engine (`internal/engine/ollama/ollama.go:292`, `/api/embeddings`)
   no matter what `open_runtime` says. Running llama_server still silently
   requires a live Ollama daemon for intent routing and anything else that
   embeds. This is why an "ollama URL" row haunts the `/m` open-model section
   on a llama-server setup.
3. **The `/m` "open model" section reflects the legacy shape** — runtime,
   chat model, ollama URL — rather than the taxonomy.

## Decisions (Bryan, 2026-07-07)

- **The model tiers are the source of truth.** The interactive local chat
  model is *implicitly* `models.tiers.everyday.open`. The standalone
  `open_model` setting goes away. ("The chat model should implicitly point to
  the everyday model.")
- **Embeddings run on whatever runtime is configured.** If `open_runtime` is
  `llama_server`, embeddings serve from llama-server; Ollama is only used
  when Ollama is the runtime.
- Done as one design track in this worktree, not incremental patches.

## Design

### A. Invert the chat-model resolution

- `resolveTierModel` drops its `everyday.open → cfg.OpenModel` fallback; the
  tier slot is authoritative.
- Everything that reads `cfg.OpenModel` to pick the interactive local model
  (tool-loop provider construction, MCP server mode, dispatch/coproc default,
  llama_server detection input in `localruntime/config.go`) resolves
  `everyday.open` via `resolveTierModel` instead.
- `GetConfig` keeps an `OpenModel` field for clients but it becomes a
  **derived, read-only report** of the resolved everyday-open model, so
  headers/dashboards keep rendering without a lockstep client upgrade.
- Writes are redirected, not duplicated: `/config local-model X` and the
  `ConfigUpdate.OpenModel` RPC field become aliases for
  `models.tiers.everyday.open = X` (one release of compat, then remove).

### B. Config migration

On config load, if `models.tiers.everyday.open` is empty and `open_model` is
set, seed the tier slot from it and rewrite the file without `open_model`.
Hash-ID values (`llama_server:<hash>`) are resolved to the model's display
name during migration when the file is present; unresolvable IDs migrate
as-is (they still resolve at runtime by ID). The `open_model` key is ignored
(with a one-line startup warning) after migration.

### C. Embeddings follow the runtime

- The engine layer gains an `Embed(ctx, model, text) ([]float32, error)`
  capability. The Ollama engine already has it in substance; the llama-server
  engine gets one speaking `/v1/embeddings`.
- llama-server embedding instances: `Start` learns an embedding mode —
  `argsFor` gets an `--embedding` variant (plus `--pooling` as needed for
  nomic-style models). Instances are keyed by model exactly like chat
  instances, so the embedding model stays **warm and resident alongside chat
  models** (the 5352116b warm-instance work is the substrate; an embedding
  GGUF is small and loads in seconds).
- Embedder selection follows `open_runtime`: `ollama` → Ollama engine,
  `llama_server` → llama-server engine. The SmartRouter/embedder wiring in
  `cmd/*/main.go` takes the selected engine instead of unconditionally
  `ollamaEng`.
- The embedding model source of truth is the `embedding.open` tier slot
  (already surfaced in the tier UI); the legacy `embedding_model` key gets
  the same migrate-and-alias treatment as `open_model`. Its value must be
  resolvable by the active runtime (the `-latest`-alias matcher from
  c9ccb5f9 makes bare names work on llama_server).

### D. `/m` UI cleanup

- The "open model" section shrinks to: **runtime** row (pick ollama /
  llama_server) and — only when the runtime is `ollama` — the **ollama URL**
  row. The "chat model" picker row is removed; the model tiers section
  directly below is the one place models are assigned.
- If the everyday.open slot is unset, the tiers section already renders the
  slot as "(unset)"; the dashboard adds a hint line pointing there instead of
  offering a second picker.
- The wizard's "local model" step writes `everyday.open` (it already walks
  tier slots; the separate chat-model commit path goes away).

## Blast radius

- Server: ~43 `OpenModel` references across `server.go`, `config_watcher.go`,
  `models_resolve.go`, `mcp/server.go`, `localruntime/config.go`,
  `cmd/agent/main.go`, `cmd/cercano/main.go`, `pkg/config`,
  `pkg/agentclient`.
- CLI: `runtime_open_model.go`, `runtime_dashboard.go`, `open_runtime_modal.go`,
  `settings_build.go`, `model.go` (header), `wizard/wizard.go`,
  `slash/config.go`.
- New llama-server embedding path (engine + provider args + selection wiring).
- Config load/migration in `pkg/config`.

## Phases

1. **Inversion + migration** — resolveTierModel inversion, OpenModel reader
   migration, config seed/alias, derived GetConfig report. Tests: migration
   fixtures (open_model only / both / neither), resolution precedence.
2. **Embeddings follow runtime** — engine Embed capability, llama-server
   embedding instances (warm, --embedding args), embedder selection by
   runtime. Tests: args builder, endpoint selection, fake-manager embed flow.
3. **UI cleanup** — `/m` section reshape, wizard step, `/config` alias text.
4. **Docs** — agent README config surfaces, cloud/local docs, this doc to
   status: shipped.

## Open questions

1. Keep `/config local-model` long-term as an ergonomic alias for
   `models.everyday.open`, or retire the verb after the compat release?
2. `embedding_model` → `embedding.open` tier: same inversion, same release?
   (Recommended: yes, it's the same shape and half the readers overlap.)
3. Ollama daemon health: after this, a llama_server setup has **no** Ollama
   dependency at all — should the wizard/doctor stop checking for Ollama in
   that configuration? (Recommended: yes.)
