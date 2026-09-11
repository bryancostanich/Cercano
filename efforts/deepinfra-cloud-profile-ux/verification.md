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

## Blocker and remaining work

Several bounded delegations returned only tool-use boilerplate, not findings or test outputs. One wrote an invalid test despite supplying no useful completion report. A higher-reasoning Phase 1 delegation then failed tool validation: it reported completion without using any granted write/execute tools. Scoped git audits and a simple test delegation did return concrete results; broader implementation delegation is not currently reliable.

Phase 1 remains incomplete: exact mutation/refresh and credential interfaces, omitted/explicit dispatch, chat budgeting, independent Secondary selection, local vision transport and dedicated image selection probes, routing-consumer inventory, and remaining error-class tests are outstanding. No assumptions about those paths have been turned into production edits. Full builds, UI checks, interface integration, and all later phases remain unrun.
