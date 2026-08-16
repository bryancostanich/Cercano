# Spec: Local fallback context budgeting and compact tool catalogs

## Problem

Large conversations can overflow both the preferred cloud model and the smaller local fallback model. The current fallback path detects that the local context is smaller and keeps only a recent conversation tail, but it does not budget the final provider request against the local model's real window.

The live failure in conversation `8599f39f50b73317` (`LUNIE - LUNIE VIEW INTEGRATION II`) proves the gap:

```text
openai-responses invalid_request: stream error: Your input exceeds the context window of this model
retrying on llama_server
local context is smaller — using recent conversation tail only
llama_server context_overflow (400) (47966 tokens used vs 32768 limit)
```

The new diagnostics showed the local retry was not a sub-agent dispatch. It was the main conversation request:

```text
conv=8599f39f50b73317
backend=llama_server
messages=275
message_bytes=161187
tools=88
tool_schema_bytes=50122
body_bytes=211453
approx_prompt_tokens=52828
max_tokens=8192
raw llama-server count: 47966 prompt tokens, n_ctx=32768
```

So the reduced tail still exceeded the local model's context because the budget calculation ignored material parts of the request: tool schema cost, provider overhead, and output-token reserve. It also advertised the full tool catalog at the exact moment when context was scarce.

## Goals

1. Before any provider call, budget the *actual* request shape against the selected provider/model context window: system prompt, message history, current user input/images, tool schemas, and output-token reserve.
2. When falling back to a smaller local context, trim history until the estimated request fits a conservative budget rather than relying on a fixed recent-tail fraction.
3. Reduce tool-schema pressure in tight local fallback by using an explicit compact local-fallback tool profile.
4. Add a path toward a hydrated tool catalog: in tight contexts, advertise a terse tool directory and let the model request full schemas for likely tools before use.
5. Preserve correctness and debuggability: log what was trimmed/pruned, fail fast with `context_overflow` when no safe request can fit, and keep provider-side overflow as the backstop.

## Non-goals

- Do not raise the local model's configured context size as the primary fix. A larger `n_ctx` helps but does not solve budgeting correctness.
- Do not replace all native tools with a generic `call_tool` router. That saves schema tokens but gives up provider-side schema guidance and validation.
- Do not rewrite the compaction system. This effort may consume existing compacted views, but it does not change how summaries are generated.
- Do not make dynamic, intent-based tool pruning in this effort. A static profile and optional explicit hydration are safer and testable.

## Design direction

### Request budget helper

Create a reusable prompt-budget helper that accepts the exact request components and a context window, then returns:

- estimated prompt tokens,
- estimated fixed cost,
- estimated history cost,
- estimated tool schema cost,
- output reserve,
- effective prompt budget,
- whether it fits,
- and a trimmed history if trimming is requested.

The helper should live close enough to the agent/tool-loop code to use existing `llm.Message` and `llm.Tool` types, but be independent enough to reuse from runner, dispatch, and future diagnostics.

The helper's estimates can start with the existing cheap four-characters-per-token heuristic plus the current flat image estimate. Provider-reported usage remains authoritative. The helper's job is to stay safely under local windows, not to meter billing exactly.

### Tool-loop enforcement

`agent.RunToolLoop` should build or filter the catalog first, then budget the exact request before the first provider call and before later iterations if history grows. The check must include:

- system prompt,
- all messages that will be sent after image placeholder rewriting,
- full tool schema bytes for the advertised catalog,
- `MaxTokens` / output reserve,
- configured context window.

If the request is over budget, the loop should trim oldest history messages while preserving valid tool-use/tool-result pairing. If it still cannot fit, return a classified `llm.ErrContextOverflow` before spending a provider round trip.

### Local fallback tool profile

When a cloud request fails due to context overflow and the runner retries on a local provider with a smaller context, mark the tool-loop input as tight local fallback. In that mode, use a conservative static tool profile instead of advertising all tools.

The first profile should include the tools most useful for recovery and code work, such as read/search/list, focused file edits, targeted shell execution if allowed by the active permission mode, git inspection/checkpoint tools, planning handoff tools, and the compact-tool hydration tools introduced below. The profile must never bypass the existing permission fence; it only narrows advertised tools.

### Hydrated tool catalog path

Add a compact catalog mechanism for tight contexts:

1. Advertise a small always-on native tool, for example `get_tool_info` or `enable_tools`, plus the static local-fallback profile.
2. Include a terse directory of non-advertised tools: name, category, one-line purpose, and permission tier.
3. When the model asks to hydrate a tool by name, validate that the active permission profile allows it, then add its full native schema to the catalog for subsequent iterations.
4. Log hydration events and the catalog size before each provider request.

This keeps native tool calling for actual tools after hydration, avoids a generic router, and makes the added latency explicit and testable.

## Acceptance criteria

- A test constructs a long-history request with a 32k local window and a large tool catalog. The loop trims/prunes before calling the provider, and the provider sees a request whose estimated prompt plus output reserve is under budget.
- The budget calculation includes tool schemas. A regression test fails if a request that only exceeds the window because of tools is allowed through.
- The local fallback path uses a reduced explicit tool profile and logs the profile/catalog size.
- If even zero history plus the reduced catalog cannot fit, the loop returns a classified `context_overflow` error with estimated used/limit values and does not call the provider.
- Tool-use/tool-result pairing remains valid after history trimming.
- Hydration tools can expose a full schema for an allowed tool in the next iteration, and cannot hydrate tools disallowed by the active permission profile.
- Existing normal cloud/frontier behavior remains unchanged unless the request is over budget.
- Server tests pass for the touched packages, with broader `go test ./...` if the implementation touches shared tool/catalog code.
