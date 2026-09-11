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
