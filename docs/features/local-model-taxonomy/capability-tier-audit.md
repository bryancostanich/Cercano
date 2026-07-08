# Capability → Model-Tier Audit

> **Status:** implemented. Tier-aware dispatch (`343f0904`), per-capability
> tier declarations (`84b1233d`), parallel-taxonomy deletion (`db92efc4`),
> compaction summarizer → fast_light_text (`8b433054`). Open Q1 resolved:
> deep_research runs uniformly on fast_light_text (no synthesize split until
> quality data says otherwise). Open Q2 (everyday.open recommends a coder
> model for prose capabilities) remains a data-only decision in
> `tier_recommendations.yaml`.

Audit of every model-calling capability/skill: how each resolves its model
today, the proposed tier default for each, the parallel taxonomies to delete,
and a sanity check of the tier set against real workloads. Follows the
2026-07-07 tier-as-truth design (`design.md`). Goal: **every capability
declares a taxonomy tier as its default model class; tiers are the single
source of truth; no capability keeps its own model-name lists.**

## How capability models resolve today (findings)

- **The dispatch engine is tier-blind.** `Engine.modelFor` is
  `func(isCloud bool) string` (`dispatch/engine.go:89`) — keyed only on
  cloud-vs-open. Every dispatch, RoleMain or RoleCoproc, gets the same model.
- **It reads retired config keys.** `cmd/cercano/main.go:440` installs
  `SetModelFor` returning `cfg.CloudModel` / `cfg.OpenModel`. `open_model`
  was retired by `cd7630ed` ("tiers are the source of truth") — the coproc
  lane still reads the legacy value instead of resolving a tier.
- **Roles select provider side, not model.** `dispatch.Select` maps
  RoleMain/RoleCoproc through locus mode to cloud-vs-open only
  (`dispatch/select.go`). Locus tiers (cloud/open) and taxonomy tiers
  (most_capable/…) are separate vocabularies — this stays.
- **Only two call sites are tier-aware today:** the watchdog one-shot lane
  (`fast_light_text`, strict-open — `watchdog_wire.go`) and the recap
  generator (`fast_light_text` — `1a63bc39`). Everything else inherits the
  legacy chat model.
- **One true parallel taxonomy exists in code:**
  `internal/research/modelcheck.go` — `codeOnlyModels` blocklist +
  `researchCapableModels` preference list, powering
  `preCheckModelForResearch` (blocks deep-research runs) and a post-run
  "switch models" note (`mcp/server.go:831`). Both are superseded by tiers:
  the code-only/text split is exactly the `fast_light` vs `fast_light_text`
  charter.
- **Not parallel taxonomies (leave alone):** `ollamacatalog`, `cloudcatalog`
  (install catalogs), `gguf` (format detection), `contextmeter/tokenizer.go`
  (tokenizer mapping), `SupportsTools` (per-provider capability flag),
  `tier_recommendations.yaml` (the taxonomy's own data).
- **MCP surface reuses the same lane.** `grpcModelCaller*` →
  `ProcessRequest(Coproc: true, ModelOverride: use_model)` → coproc dispatch.
  Fixing the engine fixes MCP too.

## Proposed tier defaults per capability

R-tier tools (fs/git/grep/paths/run/get_protocol) make no model calls — out
of scope. Model-calling capabilities:

| Capability | Role today | Proposed tier |
|---|---|---|
| classify | Coproc | fast_light_text |
| summarize | Coproc | fast_light_text |
| extract | Coproc | fast_light_text |
| explain | Coproc | fast_light_text |
| suggest_next_prompt | Coproc | fast_light_text |
| research (analysis) | Coproc | fast_light_text |
| deep_research (analysis) | Coproc | fast_light_text |
| deep_research (synthesize) | Coproc | everyday *(open Q1)* |
| local_offload (cercano_local) | Coproc | everyday |
| review (adversarial) | Main | everyday |
| dispatch_cap (sub-agent) | Main | everyday |
| gitflow land-review | Main | everyday |
| watchdog | Coproc | fast_light_text *(already)* |
| recap | Coproc | fast_light_text *(already)* |
| compaction summarizer | direct open-provider call | fast_light_text *(from fast_light — see below)* |
| embeddings | n/a | embedding slot *(already, 2c6fb3df)* |

Explicit `use_model` / `model` argument survives everywhere as a per-call
override; the tier is only the default.

## Tier sanity check

- **The fast_light vs fast_light_text split earns its keep**: every
  high-volume coproc workload above is prose judgment, exactly the
  fast_light_text charter. Nothing currently needs plain `fast_light`
  (code-flavored small model) except possibly local_offload's
  generate-validate loop — resolved by giving it `everyday`.
- **Finding (open Q2): everyday.open recommends a coder model.**
  `tier_recommendations.yaml` open side lists `qwen3-coder-next` for
  most_capable/everyday/fast_light. Any prose-heavy capability defaulting to
  `everyday` on the open side (deep_research synthesize, review, explain if
  promoted) re-creates the exact "coder model doing text judgment" problem
  modelcheck.go existed to warn about. Options: (a) add a text-capable
  candidate ahead of the coder for prose use — but the slot serves chat too;
  (b) an `everyday_text` tier mirroring the fast_light split; (c) accept it —
  frontier coder models are far better text judges than small ones, and the
  wizard candidates are only defaults. Leaning (c) now, revisit if synthesis
  quality disappoints.
- **No missing tier otherwise.** The four tiers + embedding slot cover every
  audited workload without overloading any slot's meaning.

## Deletion list (the parallel taxonomy)

- `internal/research/modelcheck.go` + `modelcheck` tests — whole file.
- `preCheckModelForResearch` (`mcp/server.go:173`) + its call at `:1081` —
  deep research stops refusing to run; it asks for the right tier instead.
- Post-run model note at `mcp/server.go:831–838`.
- `ollama pull qwen2.5` hint string (`mcp/server.go:214–217`).
- `use_model` jsonschema text "Suggested by the model check…" — reword.
- SKILL.md files mentioning the model check (deep-research, research).

## Implementation phases

1. **Tier-aware dispatch.** `dispatch.Spec` gains `Tier config.Tier`;
   `SetModelFor` becomes `func(isCloud bool, tier config.Tier) string`,
   wired through `Server.resolveTierModel` (kills the retired-key read in
   `main.go`). Empty Spec.Tier keeps today's behavior: role default
   (RoleMain → everyday, RoleCoproc → fast_light_text).
2. **Per-capability defaults.** Set Spec.Tier per the table; `runCoproc`
   gains a tier parameter (or a variant) so the four coproc caps declare
   theirs; `dispatchModelCaller` carries the research tiers.
3. **Delete the parallel taxonomy** per the deletion list; update docs.
4. **Recommendations sanity pass.** Resolve open Q2 in
   `tier_recommendations.yaml` data only.

## Open questions

1. Does deep_research's synthesize phase get `everyday`, or start everything
   on `fast_light_text` and split only if report quality disappoints?
2. everyday.open = coder model for prose capabilities — option (a)/(b)/(c)
   above.
3. ~~Compaction summarizer's current model path~~ **Resolved.** It bypasses
   the dispatch engine (calls the open provider directly from a closure in
   `cmd/cercano/main.go:236`) and is already tier-aware — but resolves
   `fast_light`, not `fast_light_text`, even though the taxonomy's own
   charter (`models.go:17`) names "summaries" as fast_light_text work and
   recap was already moved there (`1a63bc39`). Proposed: resolve
   `fast_light_text`, keep the explicit `compaction.summarizer_model`
   override (same rule as `use_model`), keep the greedy-decoding requirement
   untouched. Its "interactive open model" fallback-of-last-resort also
   still reads retired `cfg.OpenModel` — same class of legacy read as the
   dispatch engine's `SetModelFor`.
