# Sub-agent Tool Grants and UI Representation

## Status

Draft design spec. This captures the current direction for agentic sub-agents: keep explicit, dynamic tool grants and reuse the unified tool catalog as the source of truth for the skills a parent agent may grant.

## Decision

Sub-agents are real Cercano agents with their own bounded tool loop and, when persistence is available, their own child conversation. They do **not** run with the full parent tool registry by default. Instead, the parent agent dynamically chooses a least-privilege toolset for the specific delegated task.

The unified tool catalog should be reused as the parent-visible skill menu and as the server-side validation source. The catalog already carries the load-bearing metadata needed for delegation:

- canonical tool name;
- description;
- input schema;
- permission tier;
- current runtime availability;
- name normalization behavior for host-routed tool names where applicable.

The parent passes explicit tool names to the sub-agent dispatch API. The server validates those names against the registered tool catalog, constructs a narrowed registry, and exposes only that narrowed registry to the sub-agent's language model provider.

In short:

> A sub-agent's skills are dynamically selected by the parent from the unified catalog and enforced by mounting only the granted tools into the child registry.

## Non-goals

### No static behavior buckets

We are intentionally not pursuing predefined sub-agent profiles such as `research`, `audit`, `debug`, `edit`, or `full` as the primary model.

Those buckets are attractive because they look simple, but they put agents into broad behavior categories. Real tasks do not fit those categories cleanly. For example:

- one debugging task may need only `Read`, `Grep`, and `Glob`;
- another debugging task may need `Bash` to run a narrow test;
- another may need `Edit` after the root cause is confirmed;
- another may need git history tools;
- a research task may be read-only in one case and need network fetch/search tools in another.

A static profile system either becomes too broad to be safe or too fragmented to be useful. The parent agent is in the best position to design the child toolset because it has the task context, expected output, and risk tolerance in view.

Static profiles can still be reconsidered later as optional shortcuts or policy overlays, but they should not replace explicit per-task grants.

### No prompt-only enforcement

The grant must not be only a system prompt instruction such as "you may only use Read and Grep." That is weaker than registry-level enforcement.

The sub-agent should literally receive only the schemas for granted tools. Ungranted tools should be unavailable, not merely discouraged.

## Delegation contract

The parent agent's dispatch/workflow tool description should make the contract explicit:

1. Choose the narrowest useful tool set for the delegated task.
2. Prefer read-only tools for audits, searches, summaries, and planning.
3. Add write tools only when the sub-agent is expected to make mechanical edits.
4. Add execute/shell tools only when the sub-agent must run tests, builds, diagnostics, or other commands.
5. If uncertain, under-grant and have the sub-agent report the missing tool rather than over-granting by default.
6. Keep the task instruction concrete enough that the grant can be understood by a human reviewing the transcript.

Example read-only delegation:

```json
{
  "task": "Audit the dispatch package for places where sub-agent lifecycle events should be emitted. Do not edit files.",
  "tools": ["Read", "Grep", "Glob"]
}
```

Example mechanical-edit delegation:

```json
{
  "task": "Apply the mechanical rename described below and run the narrow package tests.",
  "tools": ["Read", "Edit", "Grep", "Bash"]
}
```

## Grant resolution

Grant resolution should use the unified catalog and the agent tool registry as the source of truth.

Required behavior:

- Exact canonical tool names are accepted.
- Host routing prefixes are normalized when the provider exposes names with wire-level prefixes, but callers should still pass plain registered names such as `Read`, `Write`, `Edit`, `Bash`, `Glob`, `Grep`, and `LS`.
- Unknown requested tool names are ignored and reported back in the dispatch result.
- The actual granted tool list is reported back in the dispatch result.
- The child registry is created as a subset of the parent registry.
- If an explicit grant resolves to no tools, return a clear error instead of launching a helpless sub-agent.
- If no explicit tools are provided, default to read-only tools rather than all tools.

The existing `agenttools.Registry.Subset(names []string)` behavior is the right enforcement primitive: it creates a new registry containing only the named tools that exist in the source registry. The dispatch layer should perform intent validation and diagnostics around that primitive.

## Runtime model

A sub-agent run should have these properties:

- It has a distinct sub-conversation identifier when conversation persistence is available.
- It receives its own task prompt, selected model, working directory, and bounded iteration limit.
- It runs a normal tool loop against a narrowed registry.
- It returns final text plus metadata to the parent: model, token counts when available, sub-conversation id, granted tools, and ignored requested tools.
- It should emit lifecycle/progress events so interactive clients can display the sub-agent's activity separately from the parent turn.

The important enforcement rule is that the sub-agent's provider should see only the granted tool schemas. If `Bash` is not granted, the child should not be able to call it. If `Edit` is not granted, the child should not be able to modify files through `Edit`.

## UI representation

Interactive clients should make sub-agent execution visible rather than burying it in the parent transcript.

Recommended terminal UI direction:

- Create ephemeral tabs for active sub-agents.
- Reuse the settings page tab-strip component rather than creating a second tab implementation.
- Label sub-agent tabs hierarchically:
  - `sub 1`
  - `sub 1.1`
  - `sub 1.2`
  - `sub 2`
- Show each tab's grant in the header or metadata panel.
- Route structured sub-agent lifecycle/progress events to the corresponding tab's chat view.
- Preserve the parent transcript as the high-level delegation thread while allowing the user to inspect the child transcript when needed.

Example tab labels:

```text
sub 1 — Read, Grep, Glob
sub 1.1 — Read, Edit
sub 2 — research, fetch, Read
```

Example sub-agent header:

```text
Sub-agent: sub 1
Parent: main
Model: claude-sonnet-4-6
Tools: Read, Grep, Glob
Ignored tool names: none
Task: Audit dispatch package for lifecycle event emission points. Do not edit files.
```

The UI should make both the delegation and the guardrails obvious. A user should be able to answer "what is this child doing?" and "what is it allowed to do?" without reading raw debug logs.

## Event model

Sub-agent execution should eventually emit structured events for:

- child conversation creation;
- sub-agent start;
- resolved granted tools;
- ignored requested tool names;
- model/provider selection;
- tool call start/result/error;
- final answer;
- cancellation;
- crash or bounced-agent failure;
- completion.

These events should be routed to both:

1. the parent turn, as concise status updates; and
2. the child tab/chat view, as the detailed live transcript.

The parent transcript should avoid becoming noisy. It should show delegation boundaries and the final result, while the child view shows detailed progress.

## Safety properties

This design gives the desired safety properties without rigid profiles:

- The parent cannot accidentally give a child every tool unless it explicitly asks for every tool.
- The child cannot call tools whose schemas were not mounted.
- The human can see what was granted.
- Unknown grant names are visible instead of silently changing task behavior.
- Read-only work stays read-only by default.
- Write and execute capabilities remain deliberate per-task choices.

## Implementation notes

Relevant existing surfaces:

- `source/server/internal/agenttools/catalog.go` builds the language-model-facing catalog from the tool registry.
- `source/server/internal/agenttools/registry.go` owns registration, lookup, filtering, and `Subset` creation.
- `source/server/internal/dispatch/engine.go` carries agentic dispatch fields such as `Task`, `Tools`, `MaxIterations`, `GrantedTools`, and `IgnoredTools`.
- `source/server/internal/dispatch/agentic.go` defines the installed `AgenticRunner` hook.

The clean implementation path is:

1. Keep the unified catalog as the source of truth for available skills.
2. Normalize requested names before validation where host-prefixed names can appear.
3. Resolve grants into a narrowed `agenttools.Registry`.
4. Build the child provider tool catalog from that narrowed registry.
5. Persist and surface the child conversation id.
6. Return the actual `GrantedTools` and `IgnoredTools` in the dispatch result.
7. Add structured lifecycle events for UI routing.
8. Extract the existing settings tab strip into a reusable component before adding sub-agent tabs.

## Open questions

- Exact event payload shape for sub-agent lifecycle and progress events.
- Whether sub-agent tabs remain only ephemeral during a live run or can be reopened from persisted child conversations later.
- How much child detail should be mirrored into the parent transcript by default.
- Whether future policy overlays should constrain dynamic grants, for example "never allow execute tools in unattended mode," without replacing explicit task-specific grant selection.
