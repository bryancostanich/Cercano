# Baseline verification — 2026-09-10

## Worktree and scope

Delegated repository inspection reported `feat/cloud-profile-routing` at `751ec13c`. The approved plan/spec and supporting-history spec were copied from the shared checkout; the shared originals were not modified. A subsequent delegated status audit found only those three documents and `source/server/pkg/config/routing_contract_repro_test.go` changed. No production changes, live inference, credential changes, pushes, or merges were performed.

## Executed checks

From `source/server`:

- Direct: `go test ./internal/inference/resilience -count=1` — PASS (0.339s package time).
- Delegated with concrete output: `go test ./internal/modelevidence ./internal/toolstack ./internal/worker -count=1` — PASS (0.604s, 0.919s, 19.036s respectively; exit 0).
- Direct: `go test ./pkg/config -run TestRoutingContractLegacyPinDoesNotOverrideQuality -count=1` — expected FAIL, six assertions. For `ModelPinned=false` and `true`, `fast_light`, `everyday`, and `most_capable` all return `legacy` rather than fixture recommendations `eco`, `std`, and `pro`.

The initial delegated regression used nonexistent types and failed compilation. Its types were corrected against the inspected `CloudCostProfiles`, `VendorCostTiers`, and `CostTierModel` declarations, formatted, and rerun. The final failure is behavioral, not a compilation failure. The regression remains deliberately red pending Phase 2; this branch is not green.

## Confirmed causes and safety baseline

`pkg/config/config.go:176` (`ResolveCloudModelForTier`) returns any nonempty `prof.Model` before quality-table lookup. `ModelPinned` is not consulted. This directly explains all six observed failures. No production fix has been applied.

`internal/inference/resilience/resilience.go:348` (`reader.Next`) returns subsequent stream results directly once `emitted` or `failedOver` is true. Before that boundary, `decide` checks context cancellation, permits one retry for retryable errors, and permits an eligible backup. Existing passing tests include busy retry, quota failover/cooldown, context cancellation, `TestStream_MidStreamFailureNeverRecovers`, and `TestStream_NoCascadeFromBackup`. This is evidence for preserving the existing no-replay boundary, not a claim of seamless cross-provider continuation or exhaustive side-effect safety.

`internal/dispatch/startup_fallback.go` also refuses to retry an opened stream, even if a later `Next` returns an error. No new continuity policy has been introduced. Authentication/rate-limit classification and all higher-level streaming consumers still require the planned focused audit.

The `internal/modelevidence` package exists and its focused tests pass. Toolstack and worker tests pass. This establishes existing foundations, not completion of the destination-routing changes or full verification of their future integration.

## Earlier delegation blocker (direct execution resumed)

The user requested direct execution. The audit and reproduction work below was performed directly, without implementation delegation. The earlier delegation failures no longer block this task.

## Direct audit and reproductions

- `internal/server/UpsertCloudProfile` constructs a fresh profile and only merges omitted Route and Model. `hostsvc/config.UpsertProfile` replaces the whole record under its lock and reports only whether it is active. The handler rebuilds only for that active flag. Backup provider closures capture config at construction, so backup-only edits do not rebuild those closures through this handler. A dynamic refresh regression remains outstanding.
- `TestRoutingContractProfileEditPreservesIdentity` FAILS: a bedrock model edit preserves Route=direct but erases Provider=anthropic, Region=us-west-2, and AWSProfile=fixture. No credential or network access is involved.
- `TestRoutingBaselineEmptyModelPreservesPin` PASSES: omitted Model leaves Model=legacy and ModelPinned=true. This is a temporary characterization of the obsolete mutation, not the desired new presence-aware contract.
- `TestRoutingContractLocalVisionSurvivesSnapshot` FAILS: runtime=fixture and text=local-text survive, while image is empty instead of local-image. `SnapshotConfig` and reconstruction enumerate text and embedding slots but omit vision. Worker `OpenVisionModel` consumes that missing slot.
- Worker `CloudVisionModel` resolves Everyday and falls back to profile/global Model. Its confirmed model-evidence gate already exists and must be preserved; dedicated image selection is missing, not the gate.
- `TestRoutingContractDispatchDefaultQuality` FAILS only for omitted difficulty: actual fast_light versus desired most_capable. Explicit light/standard/deep and unrecognized input pass their approved mappings. The eventual saved-default resolver must replace this fixed default expectation with assignment-aware coverage.
- Host `MainModel` and `PrimaryModel` resolve Everyday; `Server.DispatchModelFor` and worker dispatch resolution select only the active profile. `inference.Tiers` exposes Cloud/Open and `Selection` exposes IsCloud, with no independent Secondary identity.
- Explicit dispatch is constructed in `capabilities/builtins/dispatch_cap.go`. Other RoleCoproc consumers are builtins coproc, local_offload, web_common, agent.processCoproc, and host/worker watchdog wiring. MCP and server entry points propagate invocation-specific ModelOverride. Do not redirect this entire role when adding explicit Secondary dispatch.
- Host `resolveCredential` looks up the requested profile name in the turn config, then uses the same name for static secrets or subscription token sources. Worker preferred/backup factories likewise retain each profile name. Preserve this identity mechanism when adding independent chains.
- CLI `cloud_commit.go` already has draft Save, but shouldApplyModelEdit bypasses it for existing-profile model edits. Row selection is immediate; this does not implement the approved unsaved-navigation confirmation.
- Retry classification: Busy/Network/Unknown retry; authentication is not retryable but is failoverable. Invalid requests fail over only for provider/model-unavailable errors, and context overflow requires stronger window evidence. Stream recovery refuses replay after emitted output. Additional rate-limit/authentication capturing-provider cases remain outstanding.

Direct commands from source/server:

- `go test ./internal/server ./internal/worker -run 'TestRouting(Contract|Baseline)' -count=1 -v`: FAIL as described above; tests compile and exercise the actual mutation/snapshot paths.
- `go test ./internal/capabilities/builtins -run TestRoutingContractDispatchDefaultQuality -count=1 -v`: FAIL for omitted difficulty only.
- `go test ./internal/inference/resilience ./internal/dispatch ./internal/hostsvc/config ./internal/hostsvc/providers -count=1`: PASS for all four packages (0.314s, 0.863s, 0.545s, 0.476s).

No production changes have yet been applied. Newly added regressions intentionally keep the branch red until their planned implementation phases. Remaining baseline work includes dynamic backup refresh, actual main/context selection probes, and additional failure-class fixtures.

## Historical blocker and remaining work

Several bounded delegations returned only tool-use boilerplate, not findings or test outputs. One wrote an invalid test despite supplying no useful completion report. A higher-reasoning Phase 1 delegation then failed tool validation: it reported completion without using any granted write/execute tools. Scoped git audits and a simple test delegation did return concrete results; broader implementation delegation is not currently reliable.

Phase 1 remains incomplete: exact mutation/refresh and credential interfaces, omitted/explicit dispatch, chat budgeting, independent Secondary selection, local vision transport and dedicated image selection probes, routing-consumer inventory, and remaining error-class tests are outstanding. No assumptions about those paths have been turned into production edits. Full builds, UI checks, interface integration, and all later phases remain unrun.

## Autonomous direct continuation

- Ran `go test ./internal/server ./internal/hostsvc/providers -run 'TestRoutingContract(Backup|Main)' -count=1 -v`: expected FAIL. Backup edit retains the same provider pointer; main and meter both resolve standard rather than premium. Synthetic secrets remain owned by the backup name. No inference was sent.
- Ran `go test ./internal/inference/resilience -count=1`: PASS (0.278s), including the new authentication case (one primary call, one backup call, no retry sleep, destination-model re-resolution), existing HTTP 429 busy retry, quota/cooldown, cancellation, and partial-stream no-recovery cases. No new continuity/replay policy is required.
- Cloud image selection gap remains established at the worker resolver source seam (Everyday plus legacy Model fallbacks); dedicated choice cannot be exercised until the config field exists. Capability gating is already separately covered. This limitation is retained rather than claiming an end-to-end image selection probe ran.

## Shared configuration foundation

Implemented typed destination/task assignments, independently validated Primary and Secondary bindings, sparse profile quality overrides and image selection, reference deduplication, transactional binding/task setters, and removal of legacy Model precedence. DeepInfra Premium is now the approved GLM-5.3 recommendation. Load/save discard obsolete model pins without migrating them. New tests cover same-vendor isolation, all backup-presence combinations, missing references, self-failover, explicit clears, image independence, changed inherited recommendations, and task difficulty precedence.

A new persistence probe failed because Save stripped values through the caller's shared profile slice. Save now deep-clones before normalization, and profile override/task maps are independently cloned throughout config ownership. Host config removal clears matching bindings without substitution.

Verification: `go test ./pkg/config ./internal/hostsvc/config -count=1` PASS (0.526s/0.394s), `go build ./...` from source/server PASS. Existing tests asserting legacy pin precedence were updated to the approved retirement contract. These results do not claim that provider routing, transport, or CLI integration is complete; their target regressions remain outstanding.

## Destination routing foundation and safety-review gate

Added explicit task routing alongside legacy role routing, destination/profile-aware selections, shared Target/Dispatch resolution, independent profile-chain construction for host/worker, and saved chat quality selection. Four-provider synthetic dispatch tests cover independent preferred/backup chains with either backup absent, quality-preserving failover, missing Secondary, open-only prohibition, and unchanged non-dispatch co-processor routing. Omitted dispatch difficulty now remains unset at the capability boundary so the engine consumes the saved assignment. Host and worker construction share profilechain.Build; complete transport/evidence/UI integration is still pending.

Passing verification before the safety stop:
`go test ./internal/dispatch ./internal/inference/... ./internal/capabilities/builtins ./internal/hostsvc/providers ./pkg/config ./internal/hostsvc/config -count=1`
All listed test packages passed; profilechain itself has no direct test file yet. `go build ./...` from source/server also passed. No full worker/server/runner suite or CLI verification is claimed for this intermediate foundation.

**Safety blocker (autonomous decision 6):** the earlier statement that no new replay policy is required was too broad. It covered only the resilience provider wrapper, not the outer main runner. `internal/runner/core.go` explicitly performs a whole-tool-loop rerun after transient errors, including mid-stream errors after visible output. No production runner retry fix was applied.

`go test ./internal/runner -run TestRoutingContractRunnerDoesNotReplayVisibleOutput -count=1 -v` FAILS with:
- provider calls=2
- visible output="visible-prefixvisible-prefix"

The fixture emits message_start, then one text delta, then a network error. The runner emits both prefixes to its event sink. The first draft of the fixture omitted message_start and was correctly rejected by the framing guard; that draft was corrected before drawing the visible-output conclusion. Tool-effect replay risk is inferred from restarting the same tool loop; a dedicated executed-tool reproduction remains required, not claimed verified.

The approved plan requires review before changing this continuity policy. Recommended policy: suppress automatic whole-turn retries and cross-destination fallback once visible output or tool execution has occurred; surface the interruption instead. Resuming safely from a failed iteration would need a separate, substantially broader continuation contract. Phase 3 is blocked at this policy gate. The run is not complete and has not been marked complete.

## Approved no-replay guard

The user approved stopping and reporting interruption instead of automatically restarting a turn after visible output or tool execution. The runner now keeps an atomic, turn-scoped replay-unsafe flag, set before nonempty text is forwarded or a tool-execution-start event is forwarded. Both same-provider whole-turn retry and cross-destination whole-turn fallback check this flag. It is never reset between attempts. Existing retry classification before output/tool execution remains unchanged.

Before the fix, direct probes observed two copies of visible-prefix, and a second probe observed four provider requests and two executions of the same tool. After the fix, the text probe sees one request/one prefix and the tool probe sees two requests (tool plus failed continuation)/one execution. An explicit cloud-primary fallback test confirms zero Local calls after visible text. `go test -race ./internal/runner -count=1` PASS (2.681s), including existing pre-output retry/fallback coverage. The safety-review gate is resolved; other routing integration tasks are still incomplete.

## Profile-choice and worker transport slice

Added append-only protobuf messages/fields for complete model-choice drafts, effective quality models, profile metadata, assignments, deduplicated routing snapshots, and Local vision. Regenerated through `make proto`. Shared routingwire helpers encode settings/snapshots without credentials, distinguish absent from present-empty choice messages, validate snapshot references, and preserve compatibility with absent new snapshot fields. Invalid new routing snapshots fail closed rather than falling back to old cloud-profile fields.

Profile upserts preserve omitted structural/AWS metadata, reject invalid quality keys before mutation, retire legacy Model fields, and rebuild after any referenced profile edit. Retired UpdateConfig.cloud_model requests now return an explicit error rather than installing ignored pins. Settings response formatting follows the resolved chat choice. Existing tests requiring old pins were updated to use explicit Premium choices or assert retirement. Client-facing controls and assignment mutations are not yet complete.

Verification: `go test ./internal/routingwire ./internal/server ./internal/worker ./internal/hostsvc/providers -count=1` PASS (0.327s / 2.035s / 17.346s / 0.663s). Tests include protobuf round trips across four profiles, deduplication, absent/empty choices, invalid references, profile identity preservation, backup refresh, and Local vision transport. `go build ./...` from source/server PASS. CLI has not been rebuilt for this slice. Complete image intent/evidence, task binding settings, and cross-layer routing verification remain outstanding.

## Dedicated image routing and remaining runner boundary

Direct image-chain probe found that an unset backup image choice reused the primary image model ID and sent a request. Resilience now refuses backup invocation when a configured model resolver cannot resolve the requested intent. Image inspectors send TierVision, and host/worker wiring uses the dedicated ImageModel rather than chat. Raw profile providers have a profile/endpoint-bound vision guard, including subscription transports; unknown backup evidence cannot borrow support from a primary with the same model ID. Host evidence collection now enumerates every referenced profile, including Secondary and its backup, and the existing TierVision enumeration collects image choices.

Tests cover independent image choices, missing backup choices (zero calls), identical model IDs with different profile confirmation, streaming rejection of unknown images, and preservation of image intent/no-tools. An existing worker failover fixture was migrated from retired Model to Premium overrides rather than restoring legacy fallback. Full worker/server/inference/visioninspect/toolstack/provider tests PASS; exact command and durations are in the current test output (worker 20.619s, server 1.700s, toolstack 2.512s).

A separate main-runner probe assigned chat to Secondary and observed an automatic Local request after Secondary failed. The runner now disables cross-destination fallback for non-Primary task destinations and sends the saved chat quality as explicit provider-neutral intent through retries/failover. `go test -race ./internal/runner -count=1` PASS (2.986s). Budget evidence and attribution integration are not claimed complete.

Settings delegation was attempted at standard and deep tiers; both failed validation without making write calls. No delegated implementation is being counted as progress. Settings/client/CLI work remains pending and must be implemented directly.
