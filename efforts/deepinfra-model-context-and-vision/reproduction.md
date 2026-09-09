# Reproduction results

Worktree: /Users/bryancostanich/git_repos/bryan_costanich/Cercano-model-metadata-fix
Branch: fix/model-metadata-context-vision. Base reported by delegated git tooling: dd890a75.

Baseline from source/server:
`go test ./internal/contextmeter ./internal/requestassembly ./internal/toolstack ./internal/visioninspect ./internal/deepinfracatalog ./internal/cloudfactory -count=1`
All six packages passed.

After adding only two probe test files:
`go test ./internal/toolstack ./internal/deepinfracatalog -run TestRepro -count=1 -v`
Both probes failed as expected:
- TestReproUnknownCloudModelMustNotReceiveImage: unknown model received 1 cloud image request; inspection returned no error. The fake provider advertises transport-level vision support but supplies no selected-model evidence.
- TestReproDiscoveredContextReachesConsumers: recorded catalog fixture capacity 262144; model-only meter and request-assembly fallback returned 131072 with Known=true. This isolates the missing metadata input rather than asserting a wired end-to-end path; it must be replaced/supplemented by integration coverage after the resolution seam is approved.

No production fixes, live cloud requests, production credential use, commits or pushes performed. The two probe tests remain intentionally red.

## Integration findings

worker/host.go constructs ConfigSnapshot at each turn and already resolves local model tiers host-side because workers cannot see the catalog. worker/wire.go serializes active/backup profile fields individually. worker/worker.go constructs vision using a model-only callback. toolstack/vision.go accepts that callback without checking capability.

requestassembly.Target already carries ContextWindow and ContextWindowKnown. runner/core.go resolves capacity per attempt, but its helper accepts only isCloud and model; provider identity must remain available when resolving destination metadata. The existing target.Provider is not by itself proof of full endpoint/profile identity.

DeepInfra caches the full wire index, filters browse entries afterward, and serves stale data after failed refreshes. Its toModel mapping currently leaves vision unset. Source-schema evidence for context and vision still needs verification; neither a bare false boolean nor missing tags will be treated as confirmed lack of support.

## Pending design gate

Recommend a host-resolved metadata snapshot sent with each worker turn, using the existing host catalog/cache and explicit provider/model identity. Alternative: worker-to-host metadata lookup RPC, which handles arbitrary mid-turn selections but adds a request lifecycle and failure boundary. Neither approach should create a second independent index. Exact transport/schema remains unimplemented pending human approval as required by the approved plan.

## Snapshot foundation and destination probe

User approved snapshot transport. Commit 5e69d2450340 adds pure metadata types, append-only ConfigSnapshot metadata entries and codecs. `go test ./internal/modelmetadata ./internal/worker -count=1` passed; `go vet ./internal/modelmetadata ./internal/worker` passed. These types are not yet wired into execution.

The public DeepInfra index was fetched without credentials (371 rows). Its keys include max_tokens, tags and type, but no explicit supports_vision field. GLM-5.3-Flash has multimodal and input-video tags; image support is not inferred from these alone. Documentation fetch at https://deepinfra.com/docs/models returned HTTP 403; semantic verification remains outstanding.

`go test ./internal/inference/resilience -run TestReproUnknownBackupMustNotReceiveImage -count=1 -v` failed as expected: the backup received 1 image request with no selected-model evidence. Code confirms resilience rewrites only Model for backup; the outer runner budget cannot see this change. The command was followed by a successful go vet in the same shell, so shell exit zero does NOT mean the reproduction test passed.

Pending approval: place shared model-aware request guards around each concrete provider inside resilience, versus adding per-attempt preparation hooks throughout resilience. Recommend concrete-provider guards to preserve retry policy and protect direct calls as well. No code for either alternative implemented.

## Outcome

Both original failures are fixed and verified against the real DeepInfra catalog source over its recorded index fixture (`modelevidence` integration tests), not hand-built fakes.

Context: `modelevidence` resolves capacity per exact identity; the runner consults it per attempt, so a failover budgets against the destination model. Host meter, live meter and dispatch pre-flight use the same resolution. Local runtime limits remain authoritative; absent evidence keeps the conventional fallback.

Vision: `toolstack` gained a confirmed-capability gate, and `cloudfactory` no longer hard-codes `SupportsVision: true` for OpenAI-compatible clients. Unknown and known-text-only both mean "do not send"; local fallback and `open_only` are unchanged.

Four defects were found by testing rather than by inspection:

1. `resilience.New` silently dropped its `PrimaryModelFor` option, so tier normalization never ran in production. Existing tests set the private field directly and could not catch it. Fixed in `3b0f82a7` with constructor-level coverage.
2. `modelmetadata.Vision`'s zero value was `""`, not `VisionUnknown`, so a zero `Evidence` compared equal to none of the three states and bypassed unknown-checks. Vision is now an iota enum with unknown as zero.
3. Shipped family knowledge was overriding provider-published capacity — the exact inversion this effort exists to fix. Shipped knowledge now carries vision only.
4. `GetProviderCapabilities` advertised vision when no cloud provider was built at all.

Verification: `go build ./...`, `go vet ./...`, and `go test ./internal/... ./pkg/...` pass for the server; the CLI module builds and its tests pass; touched files are gofmt-clean. `internal/localruntime`'s concurrent-download test is a pre-existing flake — it fails intermittently under `-count=5` in an untouched checkout of the main repository as well, and that package was never modified here.

## `max_tokens` semantics and vision evidence

DeepInfra's documentation pages return HTTP 403 to automated fetches, so the field was verified by cross-checking the live index against independently known values instead.

`max_tokens` is CONTEXT capacity, not an output cap. Decisive rows: `anthropic/claude-haiku-4-5` = 200000 and `google/gemini-3.1-pro` = 1000000, which match those models' published context windows exactly; their maximum output token limits are far smaller. `deepseek-ai/DeepSeek-V3.1` = 163840 likewise matches its published context. The distribution (4096 through 1048576, clustering at 131072 and 262144) is context-scale throughout.

Vision evidence: the index publishes no `supports_vision` field. The `multimodal` tag is the only affirmative image-capability signal, so it is read in one direction only — present means supported, absent means UNKNOWN, never "unsupported". An untagged model may still accept images; we simply have no evidence, and code that would send an image treats missing evidence as a refusal.

This is a heuristic on a vendor tag, not a documented contract. It is deliberately confined to affirmative use so the failure mode is a withheld image rather than an image sent to a model that cannot read it.
