# mistral.rs integration — execution plan

> **Status (2026-07-12): plan, not started.** The *what/why* and the resolved
> decisions live in [`mistralrs-integration.md`](mistralrs-integration.md)
> (Decisions D1–D3, Distribution, verified Metal status). This is the *how and
> in what order* — each phase is an independently landable, testable increment.
> Nothing here is built yet.

## Guiding constraints (from the resolved decisions)

- **Single active runtime**, `open_runtime` config, restart to swap — never two
  resident (D3).
- **Uniform per-model directories** for downloaded models; the download manager
  writes a file manifest into `modelDir/<model>/` and has no file-vs-dir branch
  (D2).
- **Format declared by the runtime** (`RuntimeCapabilities.CatalogFormats`); the
  catalog just filters (D1).
- **Fetch upstream prebuilt tarballs** per (os, arch, accelerator); Cercano owns
  no build matrix. Pin **v0.9.0** to start (Distribution).
- **hybrid-MoE-on-Metal is held back** until PR #2201/#2206 merge and release;
  the curated Metal set ships only what runs on stock v0.9.0 (verified status).
- **Binary is `mistralrs`** (crates `mistralrs-cli` + `mistralrs-server-core`);
  the old `mistralrs-server` name is gone.

## Branch strategy

Substantial multi-package work → a dedicated worktree/branch
`feat/mistralrs-runtime` off `main`, landed phase-by-phase. Each phase is its own
commit (or small commit series) with green tests before the next starts. Not
pushed unless asked.

## Dependency graph

```
Phase 1 (provider+engine+config, GGUF-only)
        └─> Phase 2 (arch gate + runtime-aware gate selection)
        └─> Phase 3 (uniform per-model directories, D2)  [validated on GGUF]
                    └─> Phase 4 (safetensors/UQFF directory download)
                                └─> Phase 5 (online safetensors discovery, D1)
Cross-cutting after P1: curated Metal catalog + wizard; tiering nudge (D3)
```

Phases 1–2 deliver "run a mistral.rs GGUF and admit qwen3next-class arches";
3–4 deliver "download and run safetensors/UQFF"; 5 is browse-beyond-curated.

---

## Phase 1 — Provider + engine + config (GGUF-only)

**Goal.** mistral.rs runs an existing on-disk GGUF as a managed sidecar, served
through the agent's engine seam. Proves process supervision, health, logs, and
the OpenAI-compatible engine with **zero** new download/gate/layout work
(mistral.rs reads GGUF via its `gguf` subcommand).

**Changes.**
- `internal/localruntime/mistralrs/provider.go` — implement
  `localruntime.Provider` (`Name/Capabilities/Discover/Start/Stop/Probe`).
  Supervisor (`startProcess/waitReady/watch/kill/finishReadiness/pipeLogs`)
  mirrors `llamaserver/provider.go`; the difference is `argsFor` (subcommand
  shape: `mistralrs --port <p> gguf -m <dir> -f <file>`) and readiness path.
- `internal/localruntime/mistralrs/install.go` — locate the `mistralrs` binary;
  if absent, fetch+extract the pinned **v0.9.0** Metal tarball
  (`mistralrs-metal-aarch64-apple-darwin.tar.gz`) via the machine-probe select.
- `internal/localruntime/mistralrs/orphans.go` + `process_*.go` — pid registry +
  `SweepOrphans`, mirrored from llamaserver.
- Config: add `MistralRSConfig` (binary path, model dirs, ISQ default, device
  knobs, extra args, ready timeout) + defaults, mirroring the llama-server
  runtime config.
- `pkg/config/config.go`: add `open_runtime` (`llama_server` default |
  `mistralrs`).
- `internal/engine/mistralrs/{engine.go,llmprovider.go}` — `InferenceEngine`
  over mistral.rs `/v1` + the `llm.Provider` wrapper, mirroring
  `engine/llamaserver` (different base URL / health path).
- `cmd/cercano/main.go`: construct + `RegisterProvider` the mistral.rs provider;
  register its engine; extend `selectOpenEngine` to honor `open_runtime`.

**Tests.** Unit: `argsFor` (gguf subcommand), `Capabilities`, install
tarball-name selection per (os/arch), supervisor state transitions (mirror the
llamaserver supervisor tests). Integration (gated/manual, needs the real binary
+ a small GGUF): start → `waitReady` → one chat completion via the engine →
stop; orphan sweep on restart.

**Acceptance.** With `open_runtime: mistralrs` and a small GGUF on disk, the
runtime dashboard shows a healthy mistral.rs instance and a chat request routed
to the open lane returns a completion. llama-server path unchanged when
`open_runtime: llama_server`.

**Not in scope.** No arch gate change, no download-layout change, no safetensors,
no online browse.

---

## Phase 2 — Arch gate + runtime-aware gate selection

**Goal.** The compatibility gate reflects the *active runtime*, so mistral.rs
admits the arches llama-server rejects (`qwen3next`) and vice-versa.

**Changes.**
- `internal/mistralrscompat/{arch.go,arch_test.go}` — `Supported/Normalize/
  SupportedArches`, seeded from mistral.rs's loader registry (its
  `normal_loaders.rs`/`NormalLoaderType` set), structural twin of `llamacompat`.
- `server.go`: replace the hardcoded `llamacompat.Supported(...)` in
  `buildCatalogDownloadRecord` with `gateFor(runtime).Supported(...)`
  (`llama_server → llamacompat`, `mistralrs → mistralrscompat`).

**Tests.** Unit: `mistralrscompat` boundary (admits `qwen3next`, `qwen3_5_moe`;
normalizes case). Extend the existing refusal test
(`TestBuildCatalogDownloadRecord_GatesUnsupportedArch`) with a matrix: `qwen3next`
**admitted** for `mistralrs`, **refused** for `llama_server`.

**Acceptance.** A download request for a `qwen3next` model is refused under
llama-server with the existing clear message and accepted (gate-wise) under
mistral.rs.

**Not in scope.** Actually downloading safetensors (Phase 4) — this phase is the
gate decision only; validate with an arch value, not a real multi-GB fetch.

---

## Phase 3 — Uniform per-model directories (D2)

**Goal.** Every downloaded model lives in `modelDir/<model>/`; the download
manager writes a file manifest into that dir with no file-vs-dir branch. Layout
groundwork for safetensors, validated on GGUF alone.

**Changes.**
- `internal/localruntime/types.go`: `ModelRecord.Path` = the model **directory**;
  add explicit `LoadTarget` (the file a file-loaded runtime opens, or the dir).
- `server.go` `buildCatalogDownloadRecord` + llamaserver `catalogModels`/
  `catalogTargetDir`/`allShardsPresent`: place new downloads under
  `modelDir/<model>/`; set `LoadTarget`.
- `manager.go`: download writes the manifest into the model dir; delete removes
  the dir (legacy flat file → rm file).
- llamaserver `argsFor`: point `--model` at `LoadTarget` (the `.gguf` inside).
- Optional lazy migration note: leave legacy flat GGUFs in place — `Discover`
  (`filepath.WalkDir`) already finds them at any depth.

**Tests.** Update the multi-shard download tests for subdir placement; add a
delete-removes-dir test; a legacy-flat-still-discovered test.

**Acceptance.** A fresh GGUF download lands in `modelDir/<model>/`, starts, and
deletes cleanly; a pre-existing flat GGUF is still discovered and runnable.

**Not in scope.** safetensors content (Phase 4) — this only changes *where* files
land, still GGUF.

---

## Phase 4 — safetensors / UQFF directory download

**Goal.** Download a multi-file safetensors (or UQFF) model into its per-model
dir and run it on mistral.rs — the genuinely new plumbing.

**Changes.**
- `catalog`/`modelcatalog` `ResolveDownload`: for a safetensors model, enumerate
  the full file manifest (weights shards + `config.json` + tokenizer files) into
  a `DownloadPlan`; `PrimaryFile`/`LoadTarget` denote the dir.
- `manager.go`: reuse the manifest-into-dir path from Phase 3 (UQFF ≈ N small
  files; safetensors = many). "downloaded" = every manifest file present.
- mistral.rs `argsFor`: `plain -m <dir> --isq <level>` (safetensors+ISQ) or
  `uqff -m <dir> -f <file>`.
- Curated mistral.rs catalog: add vetted **stock-v0.9.0** entries (safetensors
  + UQFF), Metal set conservative per the verified status (no hybrid-MoE yet).

**Tests.** Unit: manifest enumeration for a safetensors repo (fixture);
`argsFor` for `plain`/`uqff`. Integration (gated/manual): download a small
safetensors model → all files present → mistral.rs loads the dir → completion.

**Acceptance.** A curated safetensors/UQFF model downloads to its dir and serves
a completion on Apple Silicon; delete clears the whole dir.

**Not in scope.** Online browse of arbitrary safetensors (Phase 5); shipping a
`qwen3next` Metal entry (blocked on #2201/#2206).

---

## Phase 5 — Online safetensors discovery (D1)

**Goal.** Browse beyond the curated set for the active mistral.rs runtime.

**Changes.**
- `catalog.ListOptions`: add `Format`; `catalog.Detail`: arch from the `gguf`
  block or, when absent, `config.json` `architectures[0]` (auto-detect).
- `modelcatalog/hf.go` + `hf_backend.go`: switch `filter=gguf`/`safetensors`;
  parse config.json arch for safetensors.
- `RuntimeCapabilities.CatalogFormats` (ordered) per runtime; `server.go`
  `ListRuntimeModels` passes the active runtime's primary format into `List`.

**Tests.** Unit: HF backend against safetensors fixtures (list + config.json
detail); format-selection from the active runtime. Extend browse tests.

**Acceptance.** With `open_runtime: mistralrs`, browse surfaces trusted-uploader
safetensors repos with correct arch/tool badges and the gate blocks
incompatible ones.

**Not in scope.** Multi-format browse from one runtime (later refinement).

---

## Cross-cutting (after Phase 1, land opportunistically)

- **Curated catalog + wizard.** mistral.rs curated `catalog.json` (RAM-tiered,
  Metal-conservative) + loader mirroring llamaserver; the wizard offers
  mistral.rs models when it's the chosen runtime. (Entries grow with Phase 4.)
- **Tiering incompatibility nudge (D3).** In the model-tiering view, flag
  configured tier models the active runtime's gate can't run, with a prompt to
  change them — proactive, not only at download time.
- **Naming cleanup.** Ensure every reference is `mistralrs`, not the retired
  `mistralrs-server` crate.

## Test-tier policy

- Provider/gate/catalog logic: **unit tests** (fixtures, httptest), the default
  gate for these internal changes.
- Anything crossing the process/engine boundary (start→ready→completion,
  download→load): **integration tests** gated behind a build tag / env flag that
  needs the real `mistralrs` binary + a small model, since the interface to an
  external process changed. Not run in the normal unit sweep.

## Risks & rollback

- **Metal maturity (tracked).** Do not ship hybrid-MoE Metal entries until
  #2201/#2206 merge+release (recheck trigger in the sketch). Phases 1–5 all work
  on stock v0.9.0 for standard arches regardless.
- **Binary acquisition.** If the pinned tarball name/scheme changes upstream,
  `install.go` is the single point to fix; pin the version so it can't drift
  silently.
- **Rollback.** Each phase is its own commit on a feature branch; `open_runtime`
  defaults to `llama_server`, so mistral.rs is inert until explicitly selected —
  a bad phase can be reverted without touching the shipped llama-server path.

## Open items to pin before/at Phase 1

- Exact **bare-binary size** of the Metal tarball (download once; informs the
  install UX and any bundling/Homebrew choice).
- The precise mistral.rs **CLI subcommand/flag surface** for v0.9.0
  (`gguf`/`plain`/`uqff`, ISQ flag spelling, port/host flags, health path) —
  confirm against the pinned binary's `--help`, since `argsFor` depends on it.
- Where the llama-server **runtime config** struct lives, to mirror it for
  `MistralRSConfig`.
