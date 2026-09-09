# Selected Cloud Model Context and Vision Metadata

## Problem and motivation

Selecting a hosted DeepInfra model does not currently ensure that execution budgeting or the context meter uses that model's advertised context capacity. internal/contextmeter/tokenizer.go resolves context windows through model-name heuristics and defaults unknown models to 128,000 tokens with Known=false. The catalog already carries context metadata, but that metadata must reach execution rather than remain display-only.

Cloud vision selection in internal/toolstack/vision.go accepts a nonempty model ID and provider without a model-specific capability check. internal/cloudfactory/factory.go constructs OpenAI-compatible clients with SupportsVision=true regardless of the selected model. A provider's ability to transport images is not evidence that every model it serves supports them.

These are inspected code paths, not reproduced runtime failures. Execution must begin with focused failing tests or probes confirming the failures before applying fixes.

## Goals

Use metadata for the actual selected provider and model when determining context capacity for execution budgeting and the context meter. Apply this consistently in host and worker paths, after settings changes, and when failover selects a destination profile and model. Preserve existing prompt/output budget reservations; a context-window limit is not an output-token limit.

Prefer available provider-specific context metadata over model-family heuristics. When authoritative metadata is unavailable, preserve an operational fallback and its known-versus-estimated distinction. Missing, zero, or invalid metadata must not become unlimited context or falsely known capacity.

Require confirmed model-specific vision support before sending images to a cloud model. Known text-only models and models with unknown vision capability are unavailable for cloud image inspection. Use cached capability evidence when available. Continue through the existing permitted local vision fallback, or return a clear vision-unavailable result if no suitable route exists. Preserve the hard prohibition on cloud calls in open-only mode.

## Constraints

Reuse the existing DeepInfra catalog source and its bounded fetching, cache, and stale-on-failure behavior; do not implement a second independent index. Do not infer model vision support solely from API flavor or a provider-wide SupportsVision flag. Preserve the distinction between unknown capability and confirmed lack of support wherever metadata is resolved.

Metadata must be scoped to the selected provider/model, not inferred from a model ID shared by unrelated providers. Worker metadata must correspond to the worker's effective configuration. Host and worker decisions must agree for the same selection and evidence. Network discovery must remain bounded and must not become an unbounded operation in token-counting or image-routing hot paths.

Preserve cloud credentials, authentication routes, tier selection, profile overrides, and existing permitted failover behavior. Do not silently replace a user-selected model with another cloud model because metadata is absent.

## Decisions

The user requested fixes for both context metadata propagation and cloud vision capability validation.

The user approved requiring confirmed vision support, using cached metadata when available and the existing permitted local fallback or a clear unavailable result otherwise.

Decision: should unknown cloud vision capability block image requests?

| Axis | Require confirmed vision support — selected | Allow unknown models to try — not selected |
|---|---|---|
| Implementation and tests | Distinguish supported, unsupported, and unknown; test rejection and fallback | Same metadata distinction; test attempted requests and remote-error fallback |
| Risk | A working but unlisted model can be unavailable | Unsupported models can receive images and fail remotely |
| Outcome | Predictable capability-checked routing | Greater compatibility with custom or newly released models |
| Side effects | Discovery failure can limit availability without cached evidence | Failed requests can add latency and cost |
| Main drawback | Conservative availability | Retains an optimistic assumption about unknown models |

Confirmed capability checking is preferred because it addresses the underlying unsafe routing assumption. The strongest case for allowing unknown models is compatibility with custom endpoints, but that does not satisfy the approved behavior.

This specification records behavior, not a new metadata transport or storage design. Any genuine structural alternatives discovered while tracing host/worker integration must be presented before implementation.

## Non-goals

Do not introduce a new cloud vision model setting or resolve the separate cloud vision-slot design gate in efforts/deepinfra-cloud-profile-ux. This fix validates whichever model the existing selection path chooses; it does not endorse general-chat inheritance as the future vision-assignment design.

Do not add new task settings, migrate legacy model pins, redesign provider routing, implement the DeepInfra profile picker, replace tokenizers, or run model benchmarks.

## Acceptance criteria

Focused tests demonstrate that a selected DeepInfra model's advertised context capacity reaches both budgeting and metering, including values above and below the historical fallback. Invalid or missing metadata retains an explicitly estimated operational fallback when no trusted capacity is available.

Tests cover provider/model identity isolation, selection changes, destination-model failover, and matching host/worker behavior. They verify that existing output reserves and local runtime limits are preserved.

Image-routing tests prove that supported models can receive images, while known text-only and unknown models receive no image request. Tests cover cached evidence, discovery failure without evidence, local fallback, no suitable model, and open-only restrictions. Provider transport support alone cannot authorize a model for image inspection.

Use fixture-backed tests without production credentials. Run focused integration tests for any changed host/worker transport or service interface, in addition to unit tests. No production settings or credentials are changed during verification.

## Approved worker metadata transport

The user approved host-resolved, per-turn metadata snapshots after reviewing snapshots versus worker-to-host lookup requests. Reuse the host's existing catalog/cache; carry provider/endpoint/route/model-scoped evidence alongside turn configuration. Include tier and backup selections. Unknown or uncaptured selections receive estimated context fallback and cannot receive cloud images without affirmative capability evidence. This does not change model selection or routing policy.
