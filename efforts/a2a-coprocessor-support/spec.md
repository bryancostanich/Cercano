# A2A Coprocessor Support

## Problem / motivation

Cercano already exposes local and routed inference to other agents through the Model Context Protocol (MCP) surface. That works well for hosts that model Cercano as a tool provider: the host discovers `cercano_*` tools, calls one, and receives a tool result. The Agent2Agent Protocol (A2A) is solving a related but different interoperability problem: a remote agent advertises an Agent Card, accepts messages as tasks, streams status and artifact updates, and keeps agent internals opaque.

For Cercano-as-coprocessor, A2A is a good fit because it lets another agent delegate a whole unit of work to Cercano without pretending Cercano is just a bag of tools. It also matches the long-running direction of dispatch, deep research, planning, and task execution better than a single MCP tool call does.

The desired outcome is that external A2A clients can discover Cercano, send it work, receive progress/status, and get artifacts/results while Cercano continues to use its existing gRPC server, capability registry, permission model, routing, telemetry, and local/cloud provider stack.

## Background

A2A complements MCP rather than replacing it. MCP exposes tools and context to an agent. A2A lets one agent collaborate with another agent over tasks and messages. The current Cercano architecture already has the right separation for this: MCP is a thin transport adapter in front of the gRPC agent server, and the unified capability registry is shared across surfaces.

Relevant current architecture:

- `source/server/internal/mcp/server.go` is the current MCP adapter and registers hand-written `cercano_*` tools plus MCP-surface capabilities.
- `source/server/internal/capabilities/mcpadapter/adapter.go` converts shared capabilities into MCP tools that call `InvokeCapability` over gRPC.
- `source/server/internal/capabilities/builtins/builtins.go` is the shared built-in capability registry.
- `source/server/pkg/proto/agent.proto` and generated clients are the internal gRPC contract the adapters already consume.
- `docs/features/mcp-server/spec.md` records the adapter principle: protocol surfaces should remain thin clients of the core agent server.
- `docs/features/agent-skills/spec.md` records the same pattern for dynamic skill discovery: transport-specific discovery wraps existing server APIs rather than duplicating capability logic.

Relevant A2A concepts from the current public specification:

- Agent Card: JSON metadata describing the remote agent, skills, endpoint, capabilities, and authentication.
- Message: one turn sent by a client or agent, containing typed content parts.
- Part: text, file, or structured data content.
- Task: the stateful unit of work with lifecycle and status.
- Artifact: generated output from a task.
- Streaming: Server-Sent Events or equivalent binding for task status and artifact updates.
- A2A is transport-oriented around HTTP, JSON-RPC 2.0, Server-Sent Events, and optional gRPC/REST bindings.

## Goals

- Add an A2A server surface so Cercano can be used as a remote coprocessor agent by A2A-compatible clients.
- Preserve MCP as-is. A2A should be side-by-side with MCP, not a breaking replacement or an MCP extension.
- Reuse the existing core agent server and capability infrastructure rather than duplicating model routing, permissions, or tool execution logic.
- Publish an Agent Card that honestly advertises Cercano's coprocessor skills and supported modalities.
- Support a useful V1 task lifecycle for delegated work: send message, receive immediate task or message, stream progress where supported, fetch task status/result, and cancel where the underlying run supports cancellation.
- Map A2A messages and artifacts to existing Cercano concepts in a way that is explicit and testable.
- Keep security posture local-first and explicit: bind locally by default, avoid exposing a network listener accidentally, and require opt-in for non-local access.

## Non-goals

- Do not remove or redesign the existing MCP surface.
- Do not implement a full A2A client/orchestrator in V1. V1 is provider/server-side for Cercano-as-coprocessor.
- Do not expose Cercano's internal planning/taskmodel store as the A2A protocol Task type. A2A tasks are protocol exchange objects; Cercano planning tasks are implementation/domain objects.
- Do not implement every optional A2A feature in V1. Push notifications, extended authenticated Agent Cards, non-text modalities, and rich UI negotiation can be phased after a compliant minimal server works.
- Do not introduce a second capability registry for A2A.

## Constraints

- The A2A adapter must be a thin transport layer over existing gRPC/capability APIs, matching the MCP adapter pattern.
- A2A protocol Task objects must not be conflated with `internal/taskmodel.Task`; they have different lifecycles and semantics.
- The V1 listener must be opt-in and local-only by default. Network binding and authentication require explicit configuration.
- The implementation must keep generated protocol code isolated enough that A2A spec evolution does not pollute core agent code.
- Existing CLI, MCP, gRPC, task pane, and planning behavior must continue to pass their current tests.
- Streaming should preserve useful progress where Cercano already has progress events, but lack of a host-side stream for some operations should degrade loudly and predictably rather than silently dropping state.

## Proposed V1 shape

Implement a side-by-side A2A adapter package, likely `source/server/internal/a2a`, with a launcher path in the unified `cercano` binary, likely a flag such as `cercano --a2a`. The adapter connects to the existing agent gRPC server in the same broad style as `cercano --mcp`.

V1 should expose Cercano as one A2A remote agent with a small set of skills derived from the existing coprocessor surface:

- local/private prompt execution (`local` / `cercano_local` equivalent)
- summarize
- extract
- classify
- explain
- research / deep research if dependency readiness and long-running behavior are acceptable
- optional capability catalog exposure if it maps cleanly to A2A skills

The A2A Agent Card should describe Cercano as a local-first coprocessor agent, include supported input/output modes initially centered on text and structured JSON, and advertise streaming only after the adapter has a real event path.

Task execution should initially map to existing request paths:

- simple one-shot coprocessor work can call `InvokeCapability` or the existing gRPC request equivalent and return either a direct message or a completed task.
- streaming/agentic work should use `StreamProcessRequest` where appropriate and translate stream events into A2A task status updates and artifacts.
- task state should live in an A2A runtime task store separate from `internal/taskmodel`, enough to serve `GetTask`, `ListTasks`, and cancellation/status queries for recent tasks.

## Decisions

### Decision 1 — A2A integration shape

Chosen option: **side-by-side A2A adapter over the existing agent gRPC/capability APIs**.

| Axis | Side-by-side A2A adapter over gRPC/capabilities | Fold A2A into the MCP server | Add A2A directly inside the core agent gRPC server |
|---|---|---|---|
| Cost / complexity | Medium: likely one new adapter package, one launcher flag, an A2A task store, protocol mapping tests, and docs. Reuses existing gRPC/capability surfaces. | Medium-low initially: reuse `internal/mcp/server.go` process wiring, but mixes two protocols in one adapter and makes tests harder to isolate. | High: core server gains HTTP/A2A concerns, protocol structs, listener lifecycle, and task exchange logic in addition to existing gRPC responsibilities. |
| Risk | Low-medium: protocol bugs are isolated to the adapter; core behavior remains unchanged. Main risk is incomplete A2A compliance. | Medium-high: silent conceptual drift is likely because MCP tools and A2A tasks have different semantics. Debugging would cross protocol boundaries. | High: core agent stability risk; A2A spec churn could force changes in central server code and generated APIs. |
| Reward / outcome | Gives Cercano a clean new interoperability surface while preserving MCP. Keeps future A2A client support possible without reshaping core. | Faster-looking path for launcher reuse, but does not create a clean A2A boundary. | Maximum direct access to internals, but little user-visible benefit over an adapter and much worse coupling. |
| Side effects | Establishes a reusable protocol-adapter pattern next to MCP. Adds another binary mode/listener to document. | Bloats MCP with non-MCP concerns and makes the term MCP ambiguous in code and docs. | Makes core server harder to reason about and raises test/build overhead for unrelated server changes. |
| Best reason | Semantically correct: A2A is a peer-agent task protocol, so it deserves a peer protocol adapter backed by shared core services. | Smallest perceived first step if only the existing MCP process shape is considered. | Direct access may simplify some streaming/cancellation wiring. |
| Main drawback | More files and a little more adapter infrastructure up front. | A hack: it conflates MCP tool exposure with A2A agent-task exchange because both happen to use JSON-RPC-ish standards. | Too much coupling for a protocol surface that should remain optional and replaceable. |

Argument against the recommendation: folding into the MCP server would probably get a demo running with fewer new files, and direct core integration could avoid some gRPC impedance mismatch for streaming. The reason those do not win is that both make the wrong boundary permanent. A2A is not an MCP feature, and optional interoperability protocol code should not live in the core agent server.

### Decision 2 — V1 scope: provider/server only or provider plus A2A client

Chosen option: **provider/server-only V1**.

| Axis | Provider/server-only V1 | Provider plus A2A client/orchestrator in V1 |
|---|---|---|
| Cost / complexity | Medium: Agent Card, send/get/list/cancel/stream operations, task store, launcher, tests, docs. | High: everything in server-only plus outbound discovery, remote Agent Card registry, client auth, remote task orchestration, UI/config decisions, and failure handling. |
| Risk | Medium: compliance risk is bounded to inbound requests and local task state. | High: bidirectional semantics multiply auth, networking, task lifecycle, and UX failure modes. |
| Reward / outcome | Directly satisfies Cercano-as-coprocessor: other agents can delegate work to Cercano. Leaves outbound collaboration as a later, informed feature. | Enables Cercano to delegate to other A2A agents too, but that is not required for the stated coprocessor use case. |
| Side effects | Keeps V1 focused and testable. Documentation can explain A2A as another provider surface beside MCP. | Risks delaying the useful provider surface and creating unresolved product questions about how the CLI chooses remote agents. |
| Best reason | Matches the user's stated use case and the existing provider-first pattern from Agent Skills. | More complete A2A story in one milestone. |
| Main drawback | Cercano can be called by A2A clients but cannot yet call other A2A agents. | Over-scoped for the first implementation and likely to blur product boundaries. |

Argument against the recommendation: implementing both directions would be more exciting and might expose design issues earlier. The reason to reject that for V1 is scope control: the first value is letting external agents use Cercano as a local/private coprocessor, and the existing codebase already has a provider-first precedent.

### Decision 3 — A2A task storage versus existing planning taskmodel

Chosen option: **separate A2A runtime task store, with explicit mappings to runner/capability results**.

| Axis | Separate A2A runtime task store | Reuse `internal/taskmodel.Task` for A2A tasks |
|---|---|---|
| Cost / complexity | Medium: new small task state type, lifecycle mapping, retention policy, tests. | Low-medium initially: existing recursive task type and stores are available. |
| Risk | Low: avoids semantic collision; protocol task lifecycle can track A2A states exactly. | High: silent semantic errors because planning tasks are Markdown/execution checklist nodes, not remote protocol task envelopes. |
| Reward / outcome | Clean protocol compliance and clear future support for A2A history/artifacts/push notifications. | Less initial code if only title/status are considered. |
| Side effects | Adds another task-ish concept that docs must distinguish clearly. | A hack: overloads one `Task` type for two unrelated meanings and would invite machine IDs or protocol fields into planning plans. |
| Best reason | Preserves the already-settled taskmodel design and keeps A2A's protocol lifecycle honest. | Reuses existing validation/clone/store helpers. |
| Main drawback | More explicit mapping code. | Pollutes or distorts a human-readable planning model for protocol transport convenience. |

Argument against the recommendation: reuse is tempting because Cercano just built robust task tree storage and task-pane event flow. The reason not to reuse it is that the names collide but the concepts do not. A2A tasks are conversation/work-exchange envelopes; planning tasks are executable Markdown checklist nodes.

## Acceptance criteria

- `cercano --a2a` starts an A2A server surface without breaking `cercano --mcp` or the standalone CLI.
- The server publishes a valid Agent Card describing Cercano's V1 skills, endpoint, supported modes, and security posture.
- An A2A client can send a text message/task to Cercano and receive a completed message or task artifact through the adapter.
- Streaming, if advertised, sends task status/artifact events generated from Cercano's existing stream/progress path.
- `GetTask` returns the latest task state for a submitted A2A task.
- `CancelTask` is implemented where cancellation is wired, or returns a correct unsupported/not-cancelable error where it is not.
- All existing MCP tests continue to pass.
- New A2A adapter tests cover Agent Card generation, send-message mapping, task lifecycle state, error mapping, and at least one streaming path if streaming is included in V1.
- Documentation explains the difference between MCP and A2A in Cercano: MCP exposes Cercano tools; A2A exposes Cercano as a remote coprocessor agent.

## Open questions for plan capture

- Which Go A2A implementation should be used, if any: official/generated protocol code from the A2A project, a small hand-written HTTP/JSON-RPC binding for V1, or generated protobuf bindings only? This should be decided after checking current Go SDK maturity.
- Should V1 advertise streaming immediately, or start with blocking send/get task only and add streaming once event mapping is proven?
- What local-only default address should `cercano --a2a` use, and should it share any launcher/config conventions with `--mcp`?
- What authentication is required for non-local binding in V1: bearer token, API key, or defer network binding until a security story is designed?
- Which existing capabilities should be listed as A2A skills versus hidden behind one general `cercano-coprocessor` skill?
