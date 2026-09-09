# Selected Cloud Model Context and Vision Metadata

Implement the approved spec.md. Spec approved by the user before this plan was authored. No production changes or executable probes have run during planning. All tasks below are pending. Preserve the existing task selection and routing policies; this effort supplies accurate metadata and validates selected models, not a new model-assignment system.

## Phase 1 — Reproduce failures and complete integration inventory

Objective: establish failing evidence and resolve implementation boundaries before fixes. Files to inspect: internal/contextmeter/{tokenizer,runtime_window}.go, internal/requestassembly/assembly.go, internal/runner/core.go, internal/hostsvc/persistence/persistence.go, internal/hostsvc/tools/tools.go, internal/server/server.go, internal/worker/{host,wire,worker}.go, internal/toolstack/vision.go, internal/cloudfactory/factory.go, internal/deepinfracatalog, internal/catalog, and source/proto/agent.proto. Tests to write: fixture-backed reproductions of ignored cloud context metadata and image requests reaching an unknown/text-only selected model.

- [x] Read repository guidance and the related deepinfra-cloud-profile-ux effort; preserve its unresolved vision-assignment gate.
- [x] Establish an isolated worktree using the prescribed tooling and record baseline test results for affected packages.
- [x] Trace selected provider/model identity through primary, backup, dispatch, host meter, worker snapshot, and per-attempt request assembly.
- [x] Probe a fixture model with a non-128000 context window and demonstrate the incorrect budget/meter result before changing production code.
- [x] Probe cloud image inspection with a fake provider and unknown/text-only model; demonstrate the missing capability gate before changing production code.
- [x] Verify the meaning of DeepInfra max_tokens and available image-capability evidence against documented source schema; distinguish context capacity from maximum output. Missing evidence remains unknown.
- [x] Inventory existing trusted metadata for other cloud providers so strict validation does not inadvertently remove confirmed vision support.
- [x] Identify the smallest shared metadata-resolution seam and how workers receive evidence corresponding to their effective selection. Present any genuine storage, module-boundary, or transport alternatives for human approval before structural edits; update the spec and plan only with approved decisions.

## Phase 2 — Resolve selected-model metadata consistently

Objective: provide context capacity and vision evidence scoped to the actual provider/model, reusing the existing catalog/cache. Files: internal/deepinfracatalog, internal/catalog and the shared resolution seam established in Phase 1; worker host/wire and proto only if the approved transport requires changes. Tests: identity isolation, metadata presence, invalid values, cache lifecycle and transport round trips.

- [x] Map verified DeepInfra context and vision evidence without interpreting an unset boolean as confirmed absence or support.
- [x] Resolve metadata for exact provider/model selections; prevent identical IDs from borrowing evidence across providers.
- [x] Reuse existing bounded discovery, fresh cache and stale-on-failure behavior without introducing another index or unbounded hot-path I/O.
- [x] Preserve trusted shipped evidence where applicable and explicitly represent unknown metadata.
- [x] Supply workers with consistent metadata for all effective selections needed by the turn, including backup/difficulty-selected models, using the approved integration seam.
- [x] Test missing, zero and invalid context values; supported, unsupported and unknown vision; cache failure with and without usable evidence; provider/model isolation; and any changed wire contract.
- [x] Run focused tests and checkpoint only the solved explicit paths with a conventional commit; do not push.

## Phase 3 — Apply context metadata to budgeting and metering

Objective: use the same selected-model capacity and certainty for request execution and display without changing token accounting or output reserves. Files: internal/runner/core.go, internal/requestassembly/assembly.go and tests, internal/contextmeter, internal/hostsvc/persistence/persistence.go, internal/hostsvc/tools/tools.go, internal/server/server.go, internal/worker and the shared resolver as needed. Tests: host/worker parity, primary/backup attempts, dispatch and settings refresh.

- [x] Pass resolved context capacity and certainty through existing requestassembly.Target.ContextWindow and ContextWindowKnown fields wherever possible.
- [x] Wire host context metering and dispatch budgeting to the same effective metadata, preserving estimated fallback semantics.
- [x] Re-resolve against the destination model for failover rather than carrying the primary model's capacity.
- [x] Honor live host selection changes and per-turn worker configuration refresh without stale model evidence.
- [x] Keep local runtime context limits authoritative for local execution and preserve all existing output/system/tool budget reservations.
- [x] Test capacities above and below 128000, missing metadata, destination-model failover, task difficulty changes, settings refresh and host/worker agreement.
- [x] Run focused context, request-assembly, runner, persistence and affected integration tests; checkpoint the solved explicit paths.

## Phase 4 — Require confirmed cloud image capability

Objective: prevent image transmission to selected cloud models without affirmative capability evidence while retaining existing fallback policy. Files: internal/toolstack/vision.go, internal/server/server.go, internal/worker/worker.go, internal/cloudfactory/factory.go, relevant inference/client capability consumers and internal/visioninspect tests. Tests: fake-provider image-call counts, fallback behavior and host/worker parity.

- [x] Validate the actual selected cloud model at the shared vision resolution boundary; unknown and text-only selections report unavailable without an image request.
- [x] Audit provider-level SupportsVision consumers and remove its use as proof of selected-model capability; distinguish transport support from model evidence without breaking text calls.
- [x] Preserve existing permitted local fallback and clear unavailable behavior; never route cloud in open-only mode.
- [x] Apply the same validation in host and worker execution and after effective model changes or failover where applicable.
- [x] Test supported models, known text-only models, unknown models, cached evidence, cold discovery failure, local fallback, no suitable route and open-only restrictions. Assert zero cloud image calls for blocked models.
- [x] Run focused vision, cloud-factory, affected client and host/worker integration tests; checkpoint the solved explicit paths.

## Phase 5 — Verify the complete behavior and close the effort

Objective: prove both fixes work across the actual service interfaces and document limits accurately. Files: affected integration tests plus this effort's documents. Tests: fixture-driven end-to-end service wiring for context and image routing, without production credentials; broader suites only where justified by changed interfaces.

- [x] Run integration coverage for the changed metadata and host/worker interfaces, including serialization if modified.
- [x] Re-run both original failing probes and show that selected context metadata reaches budgeting/metering and blocked models receive no images.
- [x] Run relevant formatting, generation consistency, build and static checks using repository-prescribed commands.
- [x] Review for residual model-name-only cloud budget consumers and image paths that bypass selected-model validation.
- [x] Record exact checks run, results and remaining limitations; do not mark unrun checks as verified.
- [~] Update task statuses, checkpoint final explicit paths and report completion without pushing. If required execution tools are unavailable, report that blocker rather than claiming tests or commits occurred.

## Transport gate resolved

User approved per-turn host-resolved metadata snapshots. Implement pure evidence types, exact provider/model identity and worker transport round-trip coverage. Do not add worker discovery or lookup RPCs. Other substantive scope/design gates remain in force.
