# Coordinated Routing and Cloud Profile Settings

One effort implements the user-approved routing and settings together: Primary chat / Premium, dispatch to independent Secondary / Premium, an optional backup within each destination, sparse profile quality overrides, and dedicated image selection. spec.md is the product contract. This replaces the earlier incomplete plan and its routing exclusions. All implementation tasks remain pending; planning is not verification or execution approval. Paths are relative to Cercano. Preserve unrelated work; never push without a separate request.

## Phase 1 — Establish the baseline and focused reproductions

Objective: confirm current interfaces and smallest failing cases before fixes. Files: source/server/pkg/config/config.go, models.go and tests; internal/locus, internal/inference/router.go, internal/inference/resilience, internal/dispatch, internal/hostsvc/providers, internal/worker, internal/toolstack; source/proto/agent.proto; source/clients/cli/internal/ui/cloud_commit.go and tests. Test with synthetic profiles and capturing providers, not credentials or live inference. Record observed data in the effort's verification notes.

- [x] After approval, pull executing-plans, ask for execution style through the available approval tool, and obtain the required worktree/dispatch/checkpoint tools. If unavailable, report the blocker rather than running inline git plumbing or editing the shared checkout.
- [x] Delegate repository state inspection and create an isolated worktree for substantial work. Record branch and SHA, preserve existing uncommitted work, and confirm which catalog/model-evidence changes are already present. Do not assume the metadata worktree has been integrated.
- [x] Inspect the exact config, profile mutation, provider construction, worker credential, stream retry, and CLI draft interfaces in this baseline. Inventory all routing consumers, distinguishing explicit dispatch from other RoleCoproc work.
- [x] Probe profile-wide Model precedence, empty-model upsert behavior, omitted versus explicit dispatch difficulty, main chat/context-meter Everyday selection, and inability to select Secondary directly. Capture actual values before changing behavior.
- [ ] Probe Local vision snapshot omission and cloud image reuse of chat, retaining already-working model capability gates.
- [x] Probe backup-profile edit refresh and profile field preservation, including route, region, AWS profile, and key ownership.
- [x] Establish fake-provider busy/rate-limit/quota/authentication/cancellation/partial-stream cases against existing retry classification. Identify unsupported continuity cases explicitly; obtain review before any new replay or degradation policy.
- [x] Add red regression tests for the approved target behavior and run existing focused baseline tests. Failures must identify observed causes, not assumed missing foundations.
- [ ] Checkpoint the reproduction unit with explicit paths and a conventional subject/body.

## Phase 2 — Configuration and shared resolution contract

Objective: represent independent destinations and profile choices without losing existing identity or credentials. Files: source/server/pkg/config/config.go, models.go, model_profiles_test.go, deepinfra_tier_test.go, tier_recommendations.yaml; internal/hostsvc/config/config.go and tests; shared resolution code alongside existing configuration/routing owners. Tests: sparse persistence, reference validation, defaults, independent bindings, task precedence, and legacy removal.

- [ ] Retain active/backup cloud bindings as Primary preferred/optional backup. Add independent Secondary preferred/optional backup references using existing profile identities. Empty backup means none; validate missing references and self-failover loops without substituting another destination.
- [ ] Implement profile-local sparse economy/standard/premium overrides and dedicated image selection. Resolve quality to override then shipped recommendation; no profile-wide Model/ModelPinned precedence or migration into overrides.
- [ ] Implement saved task destination/quality assignments with Primary/Premium chat and Secondary/Premium dispatch defaults, not task-level model IDs. Keep existing quality enums and permission tiers distinct from execution destinations.
- [ ] Preserve invocation-specific ModelOverride semantics for existing tools and inventory their interaction with backup selection; never carry an incompatible vendor model blindly across profiles.
- [ ] Use the same resolver for host, worker, task target budgeting, and execution. Preserve server-side Local runtime recommendations and existing embedding/vision slots.
- [ ] Implement explicit clear as removal, not copying a recommendation. Preserve unrelated profile configuration and existing credential/route identity through load/save and legacy model-field retirement.
- [ ] Set the approved DeepInfra recommendations: Economy openai/gpt-oss-120b, Standard zai-org/GLM-5.3-Flash, Premium zai-org/GLM-5.3. Do not invent image recommendations, prices, or model evidence.
- [ ] Test two profiles of the same vendor, changed shipped defaults, all backup-presence combinations, dangling references, legacy single-model fixtures, and task default/explicit-difficulty precedence.
- [ ] Run focused configuration and host configuration tests; checkpoint explicit paths.

## Phase 3 — Destination-aware routing and independent failover

Objective: add actual destination selection, not a backup-as-Secondary shortcut. Files: source/server/internal/locus/locus.go; internal/inference/router.go and resilience; internal/dispatch/engine.go, select.go, startup_fallback.go and agentic paths; internal/capabilities/builtins/dispatch_cap.go; internal/hostsvc/providers/providers.go; source/server/cmd/cercano/main.go. Tests: pure routing tables and capturing fake-provider integration.

- [ ] Extend provider candidate and selection identity to distinguish Primary, Secondary, and Local; retain concrete profile identity and physical cloud/local placement separately. Update callers currently relying only on isCloud so Target and Dispatch select the same profile/model.
- [ ] Build independent preferred/optional-backup provider chains for Primary and Secondary with existing authentication factories and profile-keyed credential lookup. Do not overwrite the active profile to dispatch.
- [ ] Carry destination and requested quality through one-shot and agentic dispatch and resilience. On backup selection, resolve that backup profile's model at the original quality and preserve route/usage attribution.
- [ ] Make omitted explicit-dispatch difficulty consume the saved Premium default; preserve explicit light/standard/deep and unrecognized-value baseline. Do not globally redirect all RoleCoproc callers.
- [ ] Enforce hard open_only/cloud_only restrictions and no automatic Secondary-to-Local fallback across normal routing, startup fallback, and error recovery. An unavailable Secondary must not silently become Primary.
- [ ] Preserve existing independent Local runtime management; no GPU eviction or hardware inference from profile names. Keep cross-destination degradation separate from provider failover.
- [ ] Implement only continuity fixes supported by Phase 1 probes. Test busy/quota failures, absent/exhausted backups, cancellation, and partial streaming without duplicated output or tool effects. Stop for review if safe continuation requires a new product policy.
- [ ] Run a four-profile fixture proving Primary failure invokes only Primary backup, dispatch invokes Secondary, and Secondary failure invokes only Secondary backup; assert forbidden providers receive zero calls.
- [ ] Run focused locus, inference/resilience, dispatch, provider, and dispatch-capability tests; checkpoint the routing foundation.

## Phase 4 — Settings interfaces, worker snapshots, and refresh

Objective: carry the same complete routing/configuration state through public settings interfaces and worker execution. Files: source/proto/agent.proto; generated source/server/pkg/proto files; source/server/pkg/agentclient/client.go; internal/server/server.go and profile handlers; internal/hostsvc/config; internal/worker/host.go, wire.go, worker.go, worker_dispatch.go and model metadata helpers. Tests: server/client and host/worker round trips with fake credentials.

- [ ] Extend settings responses and mutations for independent destination/backup bindings, task assignments, sparse profile quality/image choices, and effective-versus-overridden values. Use append-only protobuf field numbers and repository generation workflow; deprecate rather than repurpose old field numbers.
- [ ] Express omission, explicit set, and explicit clear using presence-aware additions to existing mutation patterns. Save profile quality/image edits together and preserve unrelated fields. Do not introduce a generic patch framework.
- [ ] Add assignment setters/clears and reference validation consistent with existing profile operations. Test deleting or clearing a referenced profile does not silently redirect work.
- [ ] Serialize every referenced preferred/backup profile and its overrides into the worker snapshot, deduplicating by identity. Include saved task assignments and effective Local vision slot; reconstruct the same binding graph on the worker.
- [ ] Preserve on-demand credential acquisition keyed by actual selected profile. No new secrets in settings responses or snapshots; test Secondary and both backups use their own identities.
- [ ] Collect context and vision evidence for selected and backup models across both destinations, including dedicated image choices. Preserve provider/endpoint-scoped evidence isolation.
- [ ] Refresh future provider chains and worker snapshots when any referenced preferred/backup profile or assignment changes. Keep existing per-turn snapshot semantics; no undocumented mid-turn rebinding.
- [ ] Test omission versus empty override map, backup clear, sparse reload, unrelated profile fields, full snapshot round trip, old snapshots with absent new fields, and profile-edit refresh for all four bindings.
- [ ] Run focused server/client/worker interface integration tests and build generated-code consumers; checkpoint the transport unit.

## Phase 5 — Chat, budgets, and dedicated image execution

Objective: ensure actual inference, budgeting, and image inspection use the same resolved target. Files: source/server/internal/server/models_resolve.go and server.go; internal/hostsvc/providers and persistence; internal/runner; internal/agent; internal/requestassembly; internal/toolstack/vision.go; internal/visioninspect; internal/worker. Tests: capturing requests and evidence-aware context/vision fixtures.

- [ ] Route chat through Primary's saved quality assignment in both host and worker, including context meter, request assembly, and compaction/budget consumers. Remove stale Everyday/global model fallbacks only where superseded by the new contract.
- [ ] Ensure failover updates actual profile/model attribution and context capacity without assuming the backup has the original provider's window. Verify existing evidence foundation rather than replacing it.
- [ ] Resolve cloud image calls from the dedicated selected profile choice, never implicitly from chat. Preserve inspection as a separate tool-free request, stable-ID store, byte transport, and existing cache semantics.
- [ ] Retain dedicated image intent through any applicable within-destination backup resolution and require confirmed capability for each selected model. Unknown/text-only models receive no image request; missing image choices remain honestly unavailable rather than gaining an invented recommendation.
- [ ] Wire the transported Local vision slot and preserve open-only embedding behavior and hard locality restrictions. Keep existing image mixed-mode policy unless necessary to enforce the no-Secondary-to-Local invariant.
- [ ] Test host/worker parity for chat, dispatch Target versus execution, Primary/Secondary backup capacity, unknown image evidence, distinct chat/image selections, and image-only backup intent.
- [ ] Run focused runner, requestassembly, persistence, toolstack, vision, and worker integration tests; checkpoint the execution unit.

## Phase 6 — Coordinated settings and provider-scoped discovery

Objective: expose controls that match the routing model without mixing destination assignment with profile model choice. Files: source/clients/cli/internal/ui/cloud_section.go, cloud_commit.go, cloud_models.go, config_tabs.go, runtime_dashboard.go, runtime_tiers.go, runtime_served_test.go and task controls alongside the existing settings pages; source/server/internal/server/cloud_models.go and catalog handlers; internal/deepinfracatalog only if adaptation is needed. Tests: CLI state transitions and server/client discovery integration.

- [ ] Expose clearly labeled Primary preferred/optional backup and Secondary preferred/optional backup controls backed by Phase 4 interfaces. Include explicit None for backups; do not relabel the existing Primary backup as Secondary.
- [ ] Add task destination/quality controls with approved defaults and no task-level model IDs. Keep explicit per-call difficulty independent of saved controls.
- [ ] Replace the single-model editor with sparse quality choices and dedicated image choice. Show inherited/overridden state and reset actions.
- [ ] Reuse profile drafts and explicit Save; remove the immediate-model-apply exception. Implement Discard, picker cancellation, unsaved-navigation confirmation, and save-error draft retention. Keep authentication/activation separate.
- [ ] Reuse any already-landed profile discovery; add missing DeepInfra support via the registered /models/list catalog rather than a parallel index. Preserve other providers, current selections missing from discovery, and honest price/context/capability unknowns.
- [ ] Rename Models to Local Models and filter hosted entries from browse/download/RAM-estimate candidates while retaining defensive server guards. Preserve source-qualified references and Local runtime overrides.
- [ ] Test assignment independence, optional backups, draft save/reload/reset/discard, two same-vendor profiles, disconnected/save-error paths, missing catalog entries, and hosted/local separation.
- [ ] Run focused CLI UI and provider-discovery integration tests, build CLI, and checkpoint the settings unit.

## Phase 7 — Integrated verification and completion

Objective: demonstrate the entire settings-to-routing flow and publish exact verification limits. Files: affected package tests, effort spec/plan, and relevant user configuration documentation. Tests use synthetic providers and isolated temporary config; live catalog calls are optional and paid inference is not required.

- [ ] Run server go build -o bin/cercano ./cmd/cercano/ and CLI go build ./... from their respective module directories. Run go vet on affected packages. Do not claim commands ran until their outputs are recorded.
- [ ] Run go test -count=1 for affected packages: pkg/config, internal/locus, internal/inference/..., internal/dispatch, internal/hostsvc/config, internal/hostsvc/providers, internal/hostsvc/persistence, internal/server, internal/worker, internal/toolstack, internal/visioninspect, internal/runner, internal/requestassembly, and internal/agent as touched. In CLI run affected internal/ui and internal/wizard tests. Adjust the precise list to the actual diff, recording it; no automatic unrelated full end-to-end suite.
- [ ] Run a cross-layer fixture: assign four profiles, edit quality/image choices, Save, reload, create worker snapshot, execute Primary chat and Secondary dispatch, then fail each preferred provider independently. Assert actual destination/profile/model, credentials, quality, context, and forbidden-call counts.
- [ ] Repeat with each backup unset, both unset, changed backup profile, explicit light/standard/deep, missing Secondary, unknown image capability, and hard locality modes. Verify failure reporting, cancellation, and partial-stream safety.
- [ ] Perform an isolated CLI smoke check for independent assignment labels, draft Save/Discard/reset, DeepInfra search, and Local Models filtering. Record manual checks not possible in the execution environment instead of marking them passed.
- [ ] Document destination versus quality terminology, independent optional backups, task defaults, profile inheritance, removed legacy pin behavior, image requirements, and per-turn refresh semantics.
- [ ] Delegate adversarial review of routing isolation, transport presence/clears, credentials, and stale refresh. Resolve findings with smallest-case probes and appropriate tests, not reasoning-only fixes.
- [ ] Delegate final repository review, checkpoint only explicit effort paths with a conventional subject/body, and report branch/SHA, passing checks, unrun checks, and remaining limits. Never push without separate approval.
