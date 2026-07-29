# Model RAM estimate — formula + calibration plan

> **Status (2026-07-27): plan, not started. Low priority / optional.** The
> mistral.rs KV-memory crash class was closed on 2026-07-26 by forcing
> PagedAttention on Metal (commit `fad654b3`), which caps KV growth in the
> engine itself. This plan is about making the *fit estimate* accurate — a
> quality-of-warning improvement, **not** a safety fix. Nothing here is built.

## Why this exists (context)

On 2026-07-26 a Qwen3-30B (afq4, ~16 GB weights) drove **wired** Metal memory
from ~4 GB to ~113 GB under load and triggered repeated WindowServer-watchdog
kernel panics. Root cause: `--paged-attn auto` **disables** the KV governor on
Metal, so KV/activation memory grew unbounded. Fix landed: force `--paged-attn
on` on Metal → KV hard-capped (observed: a 21406 MB request clamped to 768 MB).

Two memory facts we **measured** that day and that the current estimate ignores:

1. **A fixed ~18 GB activation/working-set floor** faults in on the *first*
   inference, independent of prompt size and `max_seq_len`. It is Metal/GPU
   *wired* memory — invisible to process RSS. (`DType selected is BF16`; the
   floor is BF16 activation buffers, not the 4-bit weights.)
2. The device-mapper log line `Layers 0-47: metal (108 GB)` is a **BF16
   capacity ESTIMATE, not an allocation** — real allocation with paged-attn on
   was 768 MB KV. Do not trust that log number as a footprint.

The current CLI estimate (`runtime_estimate.go`) models weights + a heuristic
`estimateOverheadBytes` + linear KV-by-context. It does **not** model the fixed
activation floor, so it under-predicts real footprint on large models.

## The core question this plan answers

*"Do we have to correct the estimate math for every model, or measure a few?"*

**Neither, exactly.** The footprint splits into parts that behave differently:

| Component | Source | Per-model? | How we get it |
|---|---|---|---|
| Weights | file size on disk | yes | **computed** (exact) |
| KV cache / token | `2·layers·kv_heads·head_dim·dtype` | yes | **computed** (closed form, exact) — GQA vs MHA differ 8×+, formula captures it |
| Activation floor | framework working set | scales w/ width | **formula × one calibrated constant** |
| Framework overhead `C` | mistral.rs/Metal backend | ~constant | **measured on a few models** |

So: the parts that vary *most* across models (weights, KV) are **computed per
model for free** — never measured. The only thing that needs measurement is the
single framework-overhead constant `C` in the activation-floor formula:

```
activation_floor ≈ C × f(hidden_dim, intermediate_dim, dtype, batch)
```

`f(...)` is derivable from each model's `config.json`; `C` is roughly stable
across models on the same backend. **Measure a few diverse models to fit `C`;
then compute the floor for any model — including ones never measured.**

A flat "floor ≈ 18 GB" constant would be wrong the moment a 7B (floor ~3 GB) or
a 120B loads. The formula-with-calibrated-constant generalizes; a bare measured
number does not.

## Plan (phased, each independently landable)

**Phase 1 — Closed-form KV + weights (no measurement).**
Replace the KV term with the exact `2·layers·kv_heads·head_dim·dtype_bytes`
from `config.json`. Weights from file size. This alone makes GQA models
estimate correctly. Pure computation; unit-testable against known configs.

**Phase 2 — Activation-floor formula (uncalibrated, conservative).**
Add `f(hidden_dim, intermediate_dim, dtype, batch=1)` as the activation term,
with a deliberately **conservative** placeholder `C` (over-estimate). Ship it
warning-only; over-warning is safe, under-warning is not.

**Phase 3 — Calibrate `C` (the measurement mini-project).**
Run the memory probe (built 2026-07-26, `/tmp/cercano_kvprobe.sh` — a
staircase-of-prompts sampler with an fsync'd wired/free log and a free-memory
abort floor) against **3–4 architecturally diverse models**:
- a small dense (~7B),
- a mid dense (~30B, the Qwen we have — floor ≈ 18 GB is the known anchor),
- a large and/or an MoE model,
- ideally one non-GQA for KV cross-check.
Fit `C` so the formula reproduces the measured floors. Replace the placeholder.

**Phase 4 — Wire the corrected estimate into the fit verdicts.**
`estimateFitLine` (dashboard) and `compactFitAnnot` (GGUF picker) consume the
new numbers. Keep the verdict **conservative** (prefer "△ tight" over a wrong
"✓ fits").

## Guiding constraints / non-goals

- **Stay conservative.** With paged-attn on, the estimate is advisory, not a
  guardrail. A slight over-estimate that occasionally says "tight" is correct
  behavior; a crash from an under-estimate is not.
- **`C` is not carved in stone.** It is a property of the mistral.rs/Metal
  backend and can shift on a version bump. Re-validate `C` when the pinned
  mistral.rs version changes. Consider recording the version `C` was fit
  against alongside the constant.
- **Don't measure every model.** Compute weights + KV per model; measure only
  the calibration set for `C`.
- **Out of scope:** CUDA/other-backend calibration (this plan is Metal-first,
  matching where the crash occurred); dynamic/live free-RAM guards (paged-attn
  already bounds the risky component).

## Open questions

- Does `C` hold across MoE vs. dense, or do MoE models need their own term
  (active-expert working set)? Phase 3's diverse set should reveal this.
- Is `f(...)` best keyed on `hidden_dim` alone, or `hidden_dim ×
  intermediate_dim`? Fit both in Phase 3 and pick the better predictor.
- Should the estimate expose its assumptions (dtype, batch, ctx) in the UI so a
  user can see *why* a model is flagged tight? Probably yes, cheaply.
