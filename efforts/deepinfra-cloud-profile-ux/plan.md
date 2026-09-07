# DeepInfra Profile Overrides and Task-Tier Assignments

Implement the approved separation: tasks select tiers; provider profiles override the models filling tiers; shipped recommendations remain inherited defaults. DeepInfra selection belongs inside its Cloud profile, and hosted models stay out of Local Models.

Status: implementation plan draft, with mandatory design gates in Phase 1. User approval of the product semantics does not approve an unreviewed configuration schema or legacy migration. Do not begin production edits until those gates are resolved and the revised plan receives request_plan_approval. All paths below are relative to the Cercano repository. No implementation or tests have been run as part of authoring this plan.

## Phase 1 — Close the configuration and compatibility contract

Objective: turn the approved semantics into an explicit, reviewable storage and resolution contract without guessing what existing pins mean. Files to inspect: source/server/pkg/config/config.go, models.go, model_profiles_test.go, deepinfra_tier_test.go; source/proto/agent.proto; source/server/internal/dispatch/engine.go; source/server/internal/capabilities/builtins/dispatch_cap.go; source/clients/cli/internal/ui/cloud_section.go and cloud_commit.go. Documentation to update: this plan and spec.md. Tests to specify: legacy fixtures, tier precedence, reset semantics, dispatch difficulty, and image selection.

- [~] Inventory existing task model reads and settings writes, distinguishing model taxonomy tiers, cloud cost tiers, permission tiers, and locus routing.
- [ ] Present and obtain approval for the exact task-assignment and profile-override schema, including the mapping between task taxonomy and cloud economy/standard/premium tiers.
- [ ] Define default task assignments for chat, dispatch, and image processing. Dispatch precedence is approved: omitted difficulty uses the saved dispatch tier (initial default `fast_light`); explicit `light`/`standard`/`deep` takes precedence and selects `fast_light`/`everyday`/`most_capable`. Chat/image defaults and unrecognized dispatch difficulty handling remain unresolved.
- [ ] Inventory legacy CloudProfile.Model, ModelPinned, UpdateConfig.CloudModel, provider-level custom tiers, and per-invocation ModelOverride behavior with synthetic configuration fixtures.
- [ ] Present compatibility alternatives and obtain approval before implementing any migration. Do not silently discard pins, expand one pin into three overrides, or reinterpret it as a task binding.
- [ ] Define cloud vision selection and capability validation, including unknown metadata and no suitable model. Preserve the existing optional local vision slot and open-only boundary.
- [ ] Define transport presence semantics so omission, set, and clear are distinct; define old-client behavior without allowing stale model fields to undo new settings.
- [ ] Record the approved contracts in spec.md, make subsequent phases concrete where necessary, and obtain plan approval before production edits.

## Phase 2 — Reproduce current behavior and establish regression tests

Objective: observe the smallest failing cases before changing resolution or persistence. Files: source/server/pkg/config/*_test.go; focused tests alongside source/server/internal/server/server.go, source/server/internal/toolstack/vision.go, and source/server/internal/worker/worker.go. Tests use in-memory providers, temporary config, and synthetic profiles; no user credentials or production settings.

- [ ] After execution approval, create an isolated worktree using the repository workflow tools and record the baseline.
- [ ] Probe a profile with one Model value across multiple requested tiers and assert the observed current behavior.
- [ ] Probe UpsertCloudProfile with an empty model to establish why clearing currently preserves the pin.
- [ ] Probe host and worker image-model selection independently of chat and observe whether unsuitable models pass the selection boundary.
- [ ] Add regression tests for the approved target behavior, with failures attributable to the observed causes rather than assumed architecture.

## Phase 3 — Implement profile overrides and task-tier resolution

Objective: add the approved storage contract and deterministic resolution without altering credentials or provider routing. Files: source/server/pkg/config/config.go, models.go and associated tests; source/server/internal/openmodels/resolver.go only where required for shared task resolution. Tests: sparse persistence, profile isolation, precedence, validation, recommendation changes, and approved compatibility behavior.

- [ ] Implement sparse profile tier overrides using the Phase 1 contract; persist only user choices.
- [ ] Implement task-tier assignments without persisted task model IDs and validate task/tier compatibility.
- [ ] Resolve a task's tier against the selected profile override and then its applicable shipped recommendation; preserve route-aware provider resolution.
- [ ] Implement reset as removal of an override, not copying the current recommendation.
- [ ] Implement only the explicitly approved legacy compatibility behavior, including idempotence and preservation of authentication fields.
- [ ] Update DeepInfra recommendations to economy openai/gpt-oss-120b, standard zai-org/GLM-5.3-Flash, premium zai-org/GLM-5.3; do not invent prices for the changed premium model.
- [ ] Test two profiles of the same vendor, untouched tiers tracking changed defaults, and customized tiers staying fixed.
- [ ] Run focused configuration tests and checkpoint the solved unit with explicit file paths.

## Phase 4 — Expose settings and provider-scoped discovery

Objective: provide a client contract that distinguishes effective values from overrides and supports explicit clearing. Files: source/proto/agent.proto; generated source/server/pkg/proto files; source/server/internal/server/server.go and cloud-profile handlers; source/server/pkg/agentclient/client.go; source/server/internal/deepinfracatalog only if adapting its existing public surface is necessary. Tests: server/client round trips, request validation, provider scoping, and discovery failure.

- [ ] Extend configuration/profile messages according to the approved schema using append-only protobuf field numbers and regenerate with the repository's generation workflow.
- [ ] Implement set, clear, and omission handling for profile tiers and task assignments; keep credentials out of settings responses.
- [ ] Return sufficient information to display inherited versus overridden values without storing defaults client-side.
- [ ] Extend ListCloudProfileModels for DeepInfra by reusing the registered /models/list catalog source and its cache, filters, and bounded fetch behavior.
- [ ] Preserve existing supported-provider discovery and scope each picker to its profile's provider.
- [ ] Carry available price, context, and capability metadata with honest unknown values; preserve selected IDs absent from discovery.
- [ ] Update agentclient types and methods and test configuration save/reload/reset through the transport boundary.
- [ ] Run focused server/client integration tests and checkpoint the interface change.

## Phase 5 — Wire task execution, failover, and image processing

Objective: use the same assignment semantics across host and workers and retain tier identity through failover. Files: source/server/internal/server/models_resolve.go and server.go; source/server/internal/hostsvc/providers/providers.go; source/server/internal/worker/worker.go; source/server/internal/dispatch/engine.go; source/server/internal/capabilities/builtins/dispatch_cap.go; source/server/internal/toolstack/vision.go. Tests: capturing fake-provider requests, host/worker parity, backup resolution, settings refresh, and routing boundaries.

- [ ] Route general chat and dispatch model selection through the approved task-tier resolver.
- [ ] Preserve dispatch difficulty semantics according to Phase 1, without conflating permission TierFor with model-tier selection.
- [ ] Replace implicit chat-model reuse for image processing with the approved capability-aware task selection.
- [ ] Resolve the original requested tier against backup profile overrides and defaults; never forward a primary vendor's model ID blindly to a backup vendor.
- [ ] Preserve provider-failure and locus-fallback mechanisms as distinct policies; test each applicable path.
- [ ] Ensure settings changes refresh the relevant provider/worker state, model context limits, and watchdog dependencies without stale model selection.
- [ ] Test unavailable vision configuration, text-only selections, unknown capability metadata, and open-only prevention of cloud calls.
- [ ] Run focused execution integration tests and checkpoint the resolved behavior.

## Phase 6 — Build separate provider and task settings controls

Objective: expose model choices where they belong and make task assignments tier-only. Files: source/clients/cli/internal/ui/cloud_section.go, cloud_models.go, cloud_commit.go, config_tabs.go, runtime_dashboard.go, runtime_tiers.go, runtime_served_test.go, plus task-settings UI files identified in Phase 1. Tests: UI state transitions, profile isolation, reset persistence, cancellation, and hosted/local separation.

- [ ] Show DeepInfra's three effective tier choices in its Cloud profile with inherited/overridden labels and restore-default actions.
- [ ] Integrate provider-scoped search with price/context display, loading/error states, and preservation of current choices on discovery failure.
- [ ] Add separate task-tier controls using the approved task inventory; do not expose task-level model override controls.
- [ ] Handle existing pins using the approved compatibility UX instead of silently repurposing the old model field.
- [ ] Rename Models to Local Models and exclude hosted entries from local browse, download, and RAM-estimate candidates while retaining defensive server guards.
- [ ] Preserve source-qualified references and local runtime override behavior.
- [ ] Test save, reload, reset, cancel, unavailable catalog entries, and edits to two profiles of the same provider.
- [ ] Run focused CLI UI tests and checkpoint the settings change.

## Phase 7 — Verify and document the complete settings flow

Objective: demonstrate the user-visible behavior and interface integrity without an unrelated full end-to-end suite or paid inference requirement. Files: affected package tests, relevant user configuration documentation, spec.md and this plan. Tests: focused cross-layer integration and a scripted manual CLI smoke check with synthetic profiles.

- [ ] Run build and static checks for affected server and CLI modules and all focused tests introduced above, recording exact commands and outcomes.
- [ ] Verify a complete task assignment -> profile tier override -> save/reload -> request-selection flow with fake providers.
- [ ] Verify reset -> inherited recommendation, changed shipped recommendation -> unchanged explicit overrides, and backup -> destination profile model at the same tier.
- [ ] Verify image selection remains independent of chat and local download/source-reference flows remain intact.
- [ ] Perform a CLI smoke check for Local Models separation and DeepInfra profile search; treat live catalog checks as optional network validation, not a requirement for deterministic tests.
- [ ] Document tier terminology, task assignments, profile overrides, reset behavior, and approved legacy handling.
- [ ] Review only the effort's changes, checkpoint explicit paths, and report remaining limitations and any checks not run. Never push without a separate request.
