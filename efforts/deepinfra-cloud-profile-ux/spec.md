# DeepInfra Cloud Profile Model Selection

## Problem and motivation

The unified catalog can represent downloadable local models and hosted DeepInfra models, but the settings experience does not yet respect that distinction. Hosted models do not belong in local model management. DeepInfra selection belongs inside its cloud provider profile, where users can override the provider's recommended tier choices without changing the recommendations themselves.

## Goals

Rename the Models settings tab to Local Models and exclude hosted entries from its browse and download workflow. Preserve local downloads, runtime management, tier selection, and source-qualified references.

Inside a DeepInfra profile in Cloud settings, expose economy, standard, and premium model choices. Show the effective choice and whether it is inherited from the provider recommendation or explicitly overridden for this profile. Offer provider-specific model search with available pricing and context information. Selecting a model changes only that profile's choice for that tier. Restoring the recommendation removes the override rather than saving a copy of the current default.

Ship these DeepInfra recommendations: economy is openai/gpt-oss-120b; standard is zai-org/GLM-5.3-Flash; premium is zai-org/GLM-5.3. Economy remains the provisional low-cost baseline. These choices were confirmed by the user after a research survey; benchmark leadership and live service reliability have not been established by Cercano testing.

## Constraints and invariants

Provider recommendations remain shipped defaults. Persist only user customizations, so untouched choices follow future recommendation updates. Never rewrite provider defaults when editing a profile. Two profiles for the same provider may retain different model choices.

Preserve the existing cloud authentication and per-profile keychain behavior, provider routes, and primary/backup failover. On failover, model selection must use the destination profile's applicable choices and provider recommendations, not blindly carry an originating vendor's model ID across vendors.

Reuse the existing DeepInfra catalog source backed by /models/list, including its eligibility filters, bounded fetch, cache, and stale-on-failure behavior. Do not add a second independent DeepInfra index implementation. Provider-specific discovery must not leak unrelated provider models into a profile picker. Keep existing selections intact when discovery fails or no longer lists them. Missing metadata must not be presented as zero-cost service or unlimited context.

Preserve cloud authentication and intentional user model choices, but do not assume that current profile-wide pin precedence correctly represents those choices. The user clarified two separate axes: provider model-set overrides and task assignments (general chat, sub-agent dispatch, image processing, etc.). A task resolving to a single model is not a provider-wide override. Do not convert a single profile Model into three tier overrides; that proposal was withdrawn. Trace assignment writes and reads before defining compatibility or migration behavior. Existing OpenAI and Anthropic override behavior was not thoroughly exercised and must not be treated as the intended contract solely because the code implements it.

Local/open recommendation resolution remains server-side. No local-runtime catalog defaults move into the configuration package.

## Decisions

The user confirmed the layered model: each provider supplies its default or recommended model set, and users override choices within their profiles. Provider-wide defaults and profile-specific overrides are complementary, not alternative designs. This effort implements that confirmed behavior rather than reopening the scope of overrides.

The user confirmed that hosted DeepInfra selection belongs inside its Cloud profile, not in Local Models and not in a shared cross-provider cloud browser.

The user confirmed GLM-5.3 for premium and GLM-5.3-Flash for standard. Keep gpt-oss-120b for economy for now. Premium therefore changes from the existing DeepSeek-V4-Pro recommendation.

No additional product-level alternatives are needed to record these settled decisions. Technical compatibility questions discovered while planning must be surfaced separately, not silently answered in implementation.

### New: Tier Override Schema and Task Assignments

The user approved the cloud profile override schema: profile-local sparse `tier_overrides: {economy: model_id, standard: model_id, premium: model_id}`. This structure allows users to override provider recommendations per tier without affecting other tiers.

Task assignments continue using the existing config.Tier taxonomy:
- most_capable -> premium
- everyday -> standard  
- fast_light and fast_light_text -> economy

Vision model selection remains a separate undecided axis. Embedding models remain open-only.

This approval specifically does NOT resolve:
- Task assignment schema details
- Transport presence semantics  
- Migration of legacy Model pins
- Dispatch precedence rules
- Vision model selection

The tier override schema is approved for implementation; remaining compatibility and migration questions require separate design review.

## Confirmed task-assignment semantics

The user confirmed that tasks select tiers. Users override the models filling those tiers within provider profiles; task bindings do not contain model overrides. Changing a task's tier changes its requested capability level. Changing a profile's tier model affects every task using that profile and tier. Clearing a profile override restores its shipped recommendation. Backup resolution keeps the requested tier and resolves against the destination profile.

This supersedes the earlier proposal for a profile-plus-tier task binding with an optional task model override. The existing per-invocation dispatch ModelOverride is a separate compatibility surface to inventory; its continued availability or removal is not decided by these settings semantics alone.

General chat, sub-agent dispatch, and image processing must consume task-tier assignments consistently in host and worker paths. Image processing must not silently inherit the general chat model and must require a suitable model. Existing locus boundaries, including open-only prohibition on cloud calls, remain authoritative. This effort does not implement the larger Primary/Secondary/Local provider-routing redesign.

## Confirmed dispatch precedence

The saved dispatch task tier is a default, not a forced tier. Omitted per-call difficulty uses the saved tier, with `fast_light` as the initial default when no assignment is saved. An explicit `light`, `standard`, or `deep` request takes precedence and selects `fast_light`, `everyday`, or `most_capable`, respectively. This selects a tier only; profile model resolution and locus routing remain separate. Handling unrecognized difficulty values remains to be specified.

The saved-default versus explicit-difficulty design gate is resolved by user approval; references below to that gate describe the original review scope, not a pending choice.

## Planning gates

The task-tier/profile-override separation is approved. Exact configuration and transport shape, handling existing profile-wide Model pins, interaction of a saved dispatch assignment with per-invocation difficulty, and cloud vision-slot capability handling still need concrete design review before affected implementation. These are not licenses to select a migration or schema silently. The execution plan begins with this review and must be revised and approved before production edits.

## Current implementation observations

The CLI config tab is currently labeled Models in source/clients/cli/internal/ui/config_tabs.go. The cloud model option helper in cloud_models.go preserves the current model even when it is absent from discovery results.

The server ListCloudProfileModels endpoint currently accepts only messages-flavor profiles and fetches their /v1/models list. It rejects chat_completions profiles, so DeepInfra discovery must be integrated into that profile-specific workflow.

Existing cloud resolution supports a profile-wide explicit model pin ahead of provider tier defaults. Separate per-tier profile overrides are an implementation gap, not a new product decision.

Settings write-path trace: cloud_section.go exposes a single `model` field; cloud_commit.go sends it as CloudProfileInfo.Model through UpsertCloudProfile (immediately for existing profiles). The server saves it as CloudProfile.Model and marks nonempty values ModelPinned. The legacy UpdateConfig.CloudModel path also writes the active profile's Model. Neither request identifies a task. UpsertCloudProfile treats an empty Model as omission and preserves the previous value, so clearing cannot currently express restore-inheritance through that field. ResolveCloudModelForTier returns a nonempty profile Model before tier lookup, independently of ModelPinned.

Local settings instead write runtime-qualified slots through UpdateConfig.ModelTierKey/ModelTierValue and ApplyModelTierPatch into Models.Open.Overrides. These are model taxonomy slots, not independent task-to-profile bindings; tasks choose their slots in code. Dispatch has a separate per-invocation ModelOverride that is not persisted by this settings path.

Image task wiring is asymmetric: local uses the explicit vision slot; host cloud vision uses activeCloudModel(), and worker cloud vision resolves TierEveryday with legacy fallbacks. BuildVision accepts a nonempty resolved model and provider without a capability check at that selection boundary. Therefore a cloud text-model choice can also become the image task's model. These are static code observations, not a reproduced runtime failure. The implementation plan must keep provider overrides and task assignments distinct rather than treating existing profile Model as a proven task assignment.

## Non-goals

Do not implement the larger Primary/Secondary/Local routing redesign, replace the cloud authentication system, introduce a shared cloud marketplace, or redesign local runtime tier configuration. Do not treat research findings as measured Cercano agent performance or run a broad benchmark suite as part of this UX change.

## Acceptance criteria

A user can open a DeepInfra profile, see all three effective recommended models, search eligible DeepInfra models, override one tier, save and reload without changing another tier or profile, and restore inheritance for that tier. A later shipped recommendation update affects inherited choices but leaves explicit overrides unchanged.

Hosted models never appear as local download candidates. Downloadable model browsing and source-qualified download/RAM-estimate behavior continue to work.

Tests cover profile isolation, override persistence and clearing, recommendation inheritance, existing pin compatibility, backup profile resolution, provider-scoped discovery, missing metadata, discovery failures, and hosted/local UI separation. Focused server/client integration tests cover any changed transport contract. No production settings or credentials are changed during verification.
