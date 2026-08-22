# Spec: Tune llama-server context sizes by RAM profile

## Problem

The llama-server context window is currently model-level launch policy, not RAM-tier policy. `glm-4.5-air-q4_k_m` pins:

```json
"extra_args": [
  "--jinja",
  "--ctx-size",
  "32768",
  "--cache-type-k",
  "q8_0",
  "--cache-type-v",
  "q8_0"
]
```

Both the `96` and `128` RAM profiles point at that same model entry, so changing the model's `--ctx-size` would silently change both profiles. That is wrong: context size is a runtime tuning decision for a machine class, not an intrinsic model requirement.

The current profile map intentionally uses the curated tiers `24`, `48`, `96`, and `128`. `ProfileForRAM` selects the largest threshold at or below the machine's RAM, so a 64 GB machine takes the `48` profile today. This effort keeps those tiers as-is; the requirement is that every existing tier has explicit context-size policy instead of inheriting model-level `--ctx-size` defaults.

## Goal

Move context-size tuning into RAM profiles, keep model entries focused on intrinsic model launch requirements, and set explicit context windows for the relevant machine classes:

- 128 GB profile: `glm-4.5-air-q4_k_m` at 128K (`131072`).
- 96 GB profile: same GLM model at a conservative 64K (`65536`).
- 48 GB and 24 GB profiles: keep explicit context sizes rather than relying on defaults or model `extra_args`. These also cover intermediate machines according to the existing largest-threshold-at-or-below selection rule.

## RAM math grounding

For `glm-4.5-air-q4_k_m`, GGUF metadata and catalog args give:

```text
weights:                 ~67.96 GiB
training context:        131,072
layers/blocks:           47
KV heads:                8
key/value length:        128/128
cache type:              q8_0 for K and V
q8_0 KV cost:            ~102,272 bytes/token
                         ~1.56 GiB per 16K context
```

Estimated GLM footprint from weights + q8_0 KV:

```text
 32K ctx  → KV ~3.12 GiB   → weights + KV ~71.09 GiB
 64K ctx  → KV ~6.24 GiB   → weights + KV ~74.21 GiB
 98K ctx  → KV ~9.36 GiB   → weights + KV ~77.33 GiB
128K ctx  → KV ~12.48 GiB  → weights + KV ~80.45 GiB
```

On a 128 GB machine, 128K is reasonable for one GLM server if the duplicate-server guard remains effective. On a 96 GB machine, 128K is too tight once OS, browser, compressor, Metal/runtime overhead, and working set are included; 64K is the safer default.

## Decisions

### Context size belongs in RAM profiles

Approved direction: add profile-level context overrides. The same model may run with different context sizes on different RAM classes.

Model `extra_args` should keep flags required for correctness, such as `--jinja`, and cache-type choices that are model-specific. It should not carry RAM-tier policy like the default `--ctx-size` once profile metadata exists.

### Keep the existing RAM profile thresholds

Keep the curated profile thresholds as `24`, `48`, `96`, and `128`. Do not add a `64` profile in this effort. Intermediate machines such as 64 GB continue to resolve to the `48` profile by the existing largest-threshold-at-or-below rule.

### Precedence

The effective context size should be resolved in this order:

```text
explicit user config llama_server.context_size
> profile ctx_size
> model extra_args --ctx-size (legacy/backward compatibility)
> default context_size
```

This preserves user control while allowing the curated RAM profile to provide sane defaults.

## Data model

Change profile entries from `tier -> modelID string` to a backward-compatible shape that can also carry launch policy:

```json
"128": {
  "most_capable": {
    "model": "glm-4.5-air-q4_k_m",
    "ctx_size": 131072
  }
}
```

Old string entries remain valid and parse as:

```go
ProfileEntry{Model: "...", ContextSize: 0}
```

This avoids breaking existing tests/fixtures and gives a smooth migration path.

## Proposed tier values

Initial target values to encode and test:

```text
24 GB profile:
  qwen3-14b-q4_k_m                ctx_size: 32768
  gemma-3-4b-it-q4_k_m vision     ctx_size: 32768
  nomic-embed-text-v1.5-f16        ctx_size: 8192 or no override if embedding ignores chat ctx

48 GB profile:
  glm-4.7-flash-q5_k_m             ctx_size: 32768 or 65536, chosen after KV math if local file metadata is available
  gemma/nomic as above

96 GB profile:
  glm-4.5-air-q4_k_m               ctx_size: 65536
  gemma/nomic as above

128 GB profile:
  glm-4.5-air-q4_k_m               ctx_size: 131072
  gemma/nomic as above
```

For 48/64 GLM Flash, the implementation should compute or inspect KV bytes/token if the GGUF is available locally. If not available, use conservative defaults and leave a note in the commit body.

## Runtime behaviour

- `argsFor` should emit exactly one `--ctx-size`, based on the resolved effective context.
- Per-model `ExtraArgs` should not be able to accidentally append a later conflicting `--ctx-size` when a user/profile context is active.
- `localContextWindow` and sub-agent preflight window resolution must use the same effective-context resolver as launch args.
- Memory guard projection should use the effective context size, not a stale config default.

## Non-goals

- Do not launch a second llama-server during verification.
- Do not increase context above a model's training context.
- Do not disable the memory guard or duplicate-server guard.
- Do not solve activation-floor calibration in this effort; use existing conservative memory guard behaviour.
- Do not redesign all provider profile metadata beyond llama-server RAM-tier context sizing.

## Acceptance criteria

- Catalog profiles support both old string entries and new object entries with `model` and `ctx_size`.
- The embedded catalog keeps the existing profile thresholds: `24`, `48`, `96`, and `128`.
- Every embedded profile entry has explicit context-size policy where applicable.
- The `128` profile resolves GLM to `ctx_size=131072`.
- The `96` profile resolves GLM to `ctx_size=65536`.
- A 64 GB machine class continues to resolve to threshold `48`, by design.
- Launch args contain exactly one `--ctx-size` and it matches the resolved profile/user value.
- User config `llama_server.context_size` still overrides profile `ctx_size`.
- Effective context used by tool-loop budgets, sub-agent preflight, and memory guard projection matches launch args.
- Tests cover profile parsing, profile selection, precedence, launch arg de-duplication, and window-budget resolution.
- Full server test suite passes.
