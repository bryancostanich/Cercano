# Coordinated Routing and Cloud Profile Settings

## Problem and motivation

Settings and routing must describe the same execution model. The current binary Cloud/Local router cannot express Primary chat delegating to an independent Secondary destination. Its existing backup cloud profile serves Primary failover, not Secondary dispatch. Cloud settings expose one profile-wide model rather than independent model-quality choices, and hosted catalog entries do not belong in local download management.

## Status and ownership

The user approved one coordinated routing-and-settings effort, Primary chat at Premium, dispatch to Secondary at Premium, and independently optional backups for both destinations. This document and plan.md are the authoritative effort. The earlier primary-secondary-local-tiers seed is supporting history, not a prerequisite or separate implementation plan. All earlier exclusions of the routing changes are withdrawn. Product approvals do not constitute execution approval of the plan.

## Goals and approved decisions

Primary is the main-chat execution destination. It normally uses a frontier model. If its configured provider becomes unavailable, busy, or exhausts its allowance, its optional backup can continue main-chat work within Primary. OpenAI to Claude is an example, not a required vendor pairing.

Primary dispatches delegated work to Secondary. Secondary is independent of Primary's preferred and backup profiles. Secondary has its own optional backup for continuity of delegated work. Either backup may be unset. Primary failover must neither select Secondary implicitly nor change Secondary's assignment. Secondary failover must not use Primary's backup merely because it exists.

Primary, Secondary, and Local are execution destinations. Economy, Standard, and Premium are model-quality levels within a destination. General chat defaults to Primary / Premium. Dispatch defaults to Secondary / Premium. Tasks contain destination/quality assignments, not persisted model IDs. Profile settings specify which models fill their quality levels. Existing per-invocation model overrides are separate functionality and remain supported; they do not restore legacy profile-wide pin precedence.

Explicit dispatch difficulty overrides the saved quality default for that call only: light selects fast_light (Economy), standard selects everyday (Standard), and deep selects most_capable (Premium). Omitted difficulty uses the saved default, initially Premium. Preserve the existing fast_light handling of unrecognized difficulty rather than creating another product gate. Permission tiers are unrelated to model-quality tiers.

Each cloud profile has sparse tier_overrides keyed economy, standard, and premium. Map most_capable to Premium, everyday to Standard, and fast_light/fast_light_text to Economy. Persist only customizations. Removing an override restores inherited recommendations, so future shipped changes affect untouched choices without replacing explicit choices. Two profiles for the same vendor can differ.

DeepInfra recommendations are Economy openai/gpt-oss-120b, Standard zai-org/GLM-5.3-Flash, and Premium zai-org/GLM-5.3. These are user-approved choices, not claims of measured reliability or benchmark leadership.

The old profile-wide Model/ModelPinned behavior is replaced outright, without migration into tier overrides or preservation of its precedence. This does not authorize removing credentials, authentication routes, endpoint configuration, or existing Primary/backup assignments.

Each cloud profile also has a dedicated image-model choice independent of its text levels. Reuse the existing image inspection pipeline and fail-closed model-capability evidence. No new shipped image recommendation was approved; do not invent one or substitute the chat model. Preserve the existing optional local vision slot and open-only embedding behavior.

## Routing and failure invariants

Provider failover within a destination and degradation across destinations are distinct policies. Do not build dispatch by forcing Primary to fail, swapping the active profile, or relabeling Primary's backup as Secondary. Provider wrappers must retain destination identity and the requested model quality while resolving the actual model against the selected backup profile. No originating vendor model ID may leak blindly into another vendor's request.

Automatic Secondary-to-Local fallback is prohibited. A Secondary request that cannot be served must not silently run on Local or Primary. Surface failure through the existing tool/error path after applicable retries and its optional backup are exhausted. Hard locality restrictions remain authoritative: open_only must make no cloud calls and cloud_only must make no Local calls. Existing non-dispatch co-processor and image routing policies remain the baseline unless adapting them is required to enforce these boundaries; do not accidentally send all RoleCoproc work to Secondary.

Destinations must resolve profile identity separately from hardware placement. Do not equate a profile name or provider backend with a destination. Preserve access to local, remote OpenAI-compatible, and cloud providers and the existing explicit Local runtime selection. Do not introduce GPU hand-off or automatic local model eviction to make Secondary fit; GPU strategy remains outside this effort. Local embeddings remain separate.

Use existing retry/error classification where it meets the continuity requirements. Busy, rate-limit, quota/allowance, authentication, cancellation, and partially streamed failures require focused probes. No promise of cross-provider seamless continuation justifies duplicate visible output or repeated side-effecting tool execution. Stop for review if the existing streaming/retry contract cannot satisfy those safety constraints without a new policy.

## Configuration, settings, and transport contract

Reuse existing configuration persistence and profile identity. Existing active and backup cloud assignments retain their Primary meaning. Add independent Secondary and Secondary-backup bindings; no configuration migration may repurpose Primary's backup. Empty backup assignment means none. Validate references and prevent self-failover loops. Missing or removed bindings must not silently bind another destination.

Keep sparse profile choices and saved task assignments server-owned. Settings must distinguish inherited values from explicit overrides. Profile Save applies the model-quality and image draft together; selecting or resetting a value only edits the draft. Discard abandons it, picker cancellation changes nothing, and leaving with unsaved edits prompts the user. Authentication, sign-in, and activation remain separate actions.

Reuse the existing mutation conventions: omitted changes preserve values, explicit set replaces them, and explicit clear removes the override or optional assignment. New fields need presence information sufficient to distinguish omission from clearing. Preserve unrelated profile fields, including route, region, and AWS profile. Do not build a general patch framework. Keep obsolete protobuf field numbers reserved or deprecated rather than reusing them for new semantics.

Transport must carry all referenced destination profiles, optional backup identities, profile overrides, dedicated image choices, task assignments, and effective Local vision selection. Deduplicate profiles by identity when referenced more than once. Continue fetching credentials on demand through the existing profile-keyed mechanism; do not put new secrets in snapshots or settings responses. Host and worker must use the same resolution rules. Edits to any referenced preferred or backup profile must refresh future provider/snapshot state, not just edits to the active profile. Preserve the existing per-turn worker snapshot lifecycle rather than promising mid-turn rebinding.

Inside Cloud settings, expose destination assignments and clearly distinguish Primary backup from Secondary and Secondary backup. Profile editors own model choices; task controls own destination/quality assignments. Reuse provider-specific discovery and preserve selected IDs absent from results. Display honest unknown pricing/context values.

Rename Models to Local Models and exclude hosted entries from browse/download/RAM estimation there. Preserve local runtime management, sparse overrides, source-qualified references, and server-side local recommendations. DeepInfra discovery belongs inside its Cloud profile and reuses the registered /models/list catalog, eligibility filters, bounded fetches, cache, and stale-on-error behavior. Do not create a second catalog implementation.

## Source audit and verification limits

Static inspection on 2026-09-10 found locus.TierLocal/TierCloud, inference.Tiers.Cloud/Open, and provider selection driven by Main/Coproc. Dispatch's model callback receives only isCloud and quality; it cannot identify Secondary. Host and worker main model resolution use Everyday rather than the approved Premium default. Omitted dispatch difficulty currently selects fast_light.

CloudProfileInfo and UpsertCloudProfileRequest expose one model field. GetCloudProvidersResponse and ConfigSnapshot expose the existing Primary active/backup assignments. Worker SnapshotConfig serializes those profiles individually. New bindings and selection fields must cross those interfaces explicitly. Existing local sparse mutation and profile Save machinery are reusable; the immediate existing-model save exception must be removed for the new editor.

Earlier audits found active-only provider refresh and preservation of legacy pins on empty model writes. Reproduce these before fixing them. Current cloud discovery support and model-metadata foundation must be rechecked in the implementation worktree: historical observations differ across commits. Reuse any already-landed functionality rather than rebuilding it or treating old observations as fresh failures.

Image byte transport, stable-ID placeholders, conversation-scoped storage, inspection, caching, and confirmed model-capability gates already exist. Local vision selection was omitted from the inspected worker snapshot slot list. Cloud vision wiring used the chat/everyday choice. Reproduce selection/transport gaps; do not rewrite image storage, byte transport, or cache semantics. Evidence must cover each selected and backup image model, not merely transport-wide SupportsVision.

This planning work ran no builds, tests, inference, or user-setting mutations. Delegation, execution, worktree, and checkpoint tools were unavailable in the planning tool set; focused read tools were used. Documentation is not committed by this planning work.

## Non-goals

No authentication replacement, cloud marketplace, new independent DeepInfra index, generic patch framework, legacy model-pin migration, image-cache redesign, GPU scheduling strategy, or automatic local fallback for Secondary. Do not change unrelated permission tiers, co-processor defaults, watchdog overrides, or local model defaults as collateral work. No paid inference or user credential modification is required for deterministic verification.

## Acceptance criteria

Primary chat and Secondary dispatch resolve their approved default and explicit quality choices on both host and worker. A four-profile fixture proves Primary preferred/backup and Secondary preferred/backup are independent, including either or both backups absent. Primary failover leaves Secondary unchanged. Secondary failover preserves destination and quality without invoking Local or Primary. Open-only/cloud-only restrictions hold.

Configuration save/reload, explicit clears, profile removal/reference validation, and worker transport preserve these assignments and sparse overrides. Authentication and route fields survive model edits. Context budgeting and usage attribution follow the actual selected provider/model, including failover. Cancellation and partial-stream paths do not duplicate output or tool effects.

Profile Save/Discard, reset-to-inheritance, task defaults, dedicated image selection, same-vendor profile isolation, and selection preservation during discovery failure work through server/client interfaces. Image requests require confirmed selected-model evidence, including failover, and Local vision survives the worker round trip.

Hosted models remain outside Local Models and cannot enter download or RAM-estimate paths. Focused unit, interface integration, and CLI smoke tests demonstrate the complete flow without paid inference. All results and unrun checks are reported honestly before completion.
