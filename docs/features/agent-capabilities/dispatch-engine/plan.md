# Dispatch Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build one "delegated model work" primitive — `Dispatch` — on the modern `llm.Provider` boundary, with a single seam where model routing, project-context injection, and usage recording attach; then migrate the co-processor commands and the agentic loop onto it so co-processor (one-shot) and subagent (agentic) work share that one seam.

**Architecture:** A new `internal/dispatch` engine exposes `Dispatch(ctx, Spec) (Result, error)` in two modes. **OneShot** = a single routed `llm.Provider.Chat` call (the co-processor case). **Agentic** = a bounded tool loop that **reuses the existing, already-parameterized `agent.RunToolLoop`** over a capability subset (the subagent case). Provider selection is a locus-governed routing seam (`internal/dispatch/select.go`). Usage is recorded by a `RecordingProvider` decorator (`internal/usage`) wrapped around every `llm.Provider` at construction, so the main loop, co-processor work, and dispatched work all emit one usage event shape. Project context is injected by the primitive when a capability opts in. The legacy `ModelProvider` co-processor path and the engine-based `dispatch.Loop` are retired.

**Tech Stack:** Go 1.26; `internal/llm` (Provider/Chat/StreamChat, Message/Block, Tool); `internal/agent` (RunToolLoop, ToolLoopInput, processCoproc, SmartRouter); `internal/locus` (Mode.Main/Coproc, Resolution); `internal/capabilities` (Capability/Registry/Services, Spec 0a); `internal/agenttools` (Registry, BuildToolCatalog); `internal/telemetry` (Collector, Event); `internal/context` (project-context Loader, Spec 0b-adjacent).

## Global Constraints

- **Single seam:** routing, project-context injection, and usage recording attach at exactly one place — the `Dispatch` primitive and the `RecordingProvider` decorator. No new parallel model-call path is introduced; the legacy `ModelProvider.Process` co-processor path and the `engine.InferenceEngine`-based `dispatch.Loop` are removed by the end of this plan.
- **Locus is the hard governor:** every provider selection goes through `internal/locus` resolution (`Main()` for subagent/default role, `Coproc()` for co-processor role). A caller hint (`ModelOverride`) is advisory within locus bounds, never an override of locus. `cloud_only`/`local_only` forbid the other tier; preferred/fallback honored only when `CrossAllowed`.
- **Behavior preservation (co-processor migration):** the migrated co-processor path must preserve, exactly: the `Coproc()` locus resolution (note `cloud_primary` keeps co-proc *local*), `ModelOverride` passthrough into `RoutingMetadata.ModelName`, the fallback `Notice` string `"locus: preferred co-processor tier unavailable — ran on %s (%s)"`, the no-provider hard error `"locus mode %q: no %s provider available for co-processor work"`, project-context + conversation-history prepend, conversation-turn persistence, and telemetry emission (local inference `Event` + separate `EmitCloudUsage` for host-reported cloud tokens). Cloud token counts, previously zero on the langchaingo path, are now real (the Anthropic `llm.Provider` returns them) — this is an improvement, not a regression.
- **Surface-split interaction:** standalone agentic dispatch may be interactive (live events, permission requests up to the main loop); MCP-surface dispatch is request/response, non-interactive, synchronous with best-effort progress events. No reliance on MCP callbacks.
- **Least-privilege subagent tools:** an agentic dispatch's tool grant defaults to R-tier capabilities; W/X capabilities are granted only when explicitly named in the spec, and a subagent never exceeds the parent session's permission mode.
- **Protocol injection seam** rides Spec 0b (already built): an agentic dispatch's system prompt is composed from persona + the 0b steering block + project context. Task-triggered protocol-body selection is a seam only (the model pulls bodies via `get_protocol`); no watchdog here.
- **Parallel fan-out is out of scope** for this plan (single-dispatch only); note where an orchestrator would add it.
- Commit messages must not contain the word "Claude"; no `Co-Authored-By` trailer.
- `go test ./...` green in `source/server` after every task; `gofmt` clean.

---

## File Structure

- **Create `internal/usage/recording_provider.go`** — `RecordingProvider` decorator implementing `llm.Provider`; emits a `telemetry` event per Chat/StreamChat. One responsibility: cost-telemetry capture at the provider boundary.
- **Create `internal/dispatch/select.go`** — the routing seam: `Role`, `Selection`, `Select(...)`. Locus-governed provider+model resolution, factored out of `agent.processCoproc` and `server.resolveMainProvider` so both and the new engine share one selector.
- **Create `internal/dispatch/engine.go`** — the primitive: `Spec`, `Result`, `Mode`, `Engine`, `Engine.Dispatch`. OneShot path here.
- **Create `internal/dispatch/agentic.go`** — Agentic path: composes a filtered `agenttools.Registry`, system prompt, permission mode, and event sink, then calls `agent.RunToolLoop`.
- **Modify `internal/agent/toolloop.go`** — add `MaxIterations`/`MaxTokensPerTurn` fields to `ToolLoopInput` (optional; default to today's constants) so dispatch jobs can cap depth.
- **Modify `internal/agent/agent.go`** — `processCoproc` reimplemented to call the new selector + `llm.Provider.Chat` (via the engine's OneShot), preserving all behavior. Remove the legacy `ModelProvider`-routed body.
- **Modify `internal/capabilities/capability.go`** — add optional `ContextAware` interface (`WantsProjectContext() bool`); the engine checks it.
- **Modify `internal/capabilities/services.go`** — wire `RunCoproc` to the engine's OneShot; add the engine/selector handles the adapters need.
- **Create `internal/capabilities/builtins/coproc_*.go`** — `summarize`, `extract`, `classify`, `explain` as capabilities (fixed prompt template + `WantsProjectContext`), built on `Services.RunCoproc`. (`research`/`document` stay orchestrations whose per-step model calls now flow through the migrated path — see Phase 5 note.)
- **Create `internal/capabilities/builtins/dispatch_cap.go`** — the `dispatch` capability (agentic), aliased `workflow`.
- **Create `internal/capabilities/builtins/review.go`** — the `review` capability (refute-style verdict), built on Dispatch.
- **Modify `internal/agenttools/registry.go`** — add `Subset(names []string) *Registry` for least-privilege tool grants.
- **Modify `internal/mcp/server.go`** — `cercano_dispatch` routes to the new Agentic engine; retire `SetDispatch`/`dispatch.Loop`/`dispatch.Store` wiring.
- **Delete (end of plan):** `internal/dispatch/dispatch.go` (engine-based `Loop`), `internal/dispatch/events.go`, `internal/dispatch/store.go`, and the legacy `ModelProvider` co-processor routing in `agent.processCoproc`. Legacy `legacymodels.CloudModelProvider` (langchaingo) removal is a follow-on once nothing references it.

---

## Phase 1 — Usage seam at the provider boundary

### Task 1: `RecordingProvider` decorator

**Files:**
- Create: `source/server/internal/usage/recording_provider.go`
- Test: `source/server/internal/usage/recording_provider_test.go`

**Interfaces:**
- Consumes: `llm.Provider`, `telemetry.Collector` (its `Emit(telemetry.Event)` method).
- Produces: `func Wrap(p llm.Provider, source string, isCloud bool, sink func(Usage)) llm.Provider` and `type Usage struct { Source, Model string; IsCloud bool; InputTokens, OutputTokens int }`.

The decorator wraps a provider and, on each `Chat` (and each fully-drained `StreamChat`), reports a `Usage` to the injected sink. The sink (wired in Task 2) translates `Usage` into a `telemetry.Event`. Keeping the sink a plain func avoids `internal/usage` importing `internal/telemetry` (no import cycle) and keeps the decorator unit-testable with a fake sink.

- [ ] **Step 1: Write the failing test**

```go
package usage

import (
	"context"
	"testing"

	"cercano/source/server/internal/llm"
)

type fakeProvider struct {
	resp   llm.ChatResponse
	stream []llm.StreamEvent
}

func (fakeProvider) Name() string                 { return "fake" }
func (fakeProvider) Capabilities() llm.Capabilities { return llm.Capabilities{SupportsTools: true} }
func (f fakeProvider) Chat(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return f.resp, nil
}
func (f fakeProvider) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	return &sliceReader{events: f.stream}, nil
}

type sliceReader struct {
	events []llm.StreamEvent
	i      int
}

func (r *sliceReader) Next() (llm.StreamEvent, bool, error) {
	if r.i >= len(r.events) {
		return llm.StreamEvent{}, false, nil
	}
	e := r.events[r.i]
	r.i++
	return e, true, nil
}
func (r *sliceReader) Close() error { return nil }

func TestWrapRecordsChatUsage(t *testing.T) {
	var got []Usage
	inner := fakeProvider{resp: llm.ChatResponse{InputTokens: 11, OutputTokens: 7}}
	p := Wrap(inner, "coproc:summarize", true, func(u Usage) { got = append(got, u) })

	if _, err := p.Chat(context.Background(), llm.ChatRequest{Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 usage event, got %d", len(got))
	}
	u := got[0]
	if u.Source != "coproc:summarize" || !u.IsCloud || u.Model != "m" || u.InputTokens != 11 || u.OutputTokens != 7 {
		t.Fatalf("bad usage: %+v", u)
	}
}

func TestWrapRecordsStreamUsageOnDrain(t *testing.T) {
	var got []Usage
	inner := fakeProvider{stream: []llm.StreamEvent{
		{Type: llm.EventMessageStart, InputTokens: 20},
		{Type: llm.EventTextDelta, TextDelta: "hi"},
		{Type: llm.EventMessageStop, OutputTokens: 5},
	}}
	p := Wrap(inner, "main", false, func(u Usage) { got = append(got, u) })

	r, err := p.StreamChat(context.Background(), llm.ChatRequest{Model: "local-x"})
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, ok, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
	}
	_ = r.Close()
	if len(got) != 1 {
		t.Fatalf("want exactly 1 usage event after drain, got %d", len(got))
	}
	if got[0].InputTokens != 20 || got[0].OutputTokens != 5 || got[0].Model != "local-x" || got[0].IsCloud {
		t.Fatalf("bad stream usage: %+v", got[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/usage/ -v`
Expected: FAIL — package/`Wrap` undefined.

- [ ] **Step 3: Write the implementation**

```go
// Package usage records per-call LLM token usage at the provider boundary.
// A RecordingProvider decorates an llm.Provider; every completed Chat or
// fully-drained StreamChat reports one Usage to a sink. This is the single
// chokepoint for cost telemetry across the main loop, co-processor work, and
// dispatched subagents (each provider is exactly one of cloud/local, so the
// tier is known for free). The live-context meter is a separate system and is
// unaffected.
package usage

import (
	"context"

	"cercano/source/server/internal/llm"
)

// Usage is one recorded model call.
type Usage struct {
	Source       string // who initiated it, e.g. "main", "coproc:summarize", "dispatch"
	Model        string
	IsCloud      bool
	InputTokens  int
	OutputTokens int
}

// Wrap returns a provider that reports a Usage to sink after each call. sink
// must tolerate being called from the goroutine that drains a stream; it is
// nil-safe (a nil sink disables recording).
func Wrap(p llm.Provider, source string, isCloud bool, sink func(Usage)) llm.Provider {
	return &recordingProvider{inner: p, source: source, isCloud: isCloud, sink: sink}
}

type recordingProvider struct {
	inner   llm.Provider
	source  string
	isCloud bool
	sink    func(Usage)
}

func (r *recordingProvider) Name() string                 { return r.inner.Name() }
func (r *recordingProvider) Capabilities() llm.Capabilities { return r.inner.Capabilities() }

func (r *recordingProvider) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	resp, err := r.inner.Chat(ctx, req)
	if err == nil {
		r.report(req.Model, resp.InputTokens, resp.OutputTokens)
	}
	return resp, err
}

func (r *recordingProvider) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	inner, err := r.inner.StreamChat(ctx, req)
	if err != nil {
		return nil, err
	}
	return &recordingReader{inner: inner, model: req.Model, rp: r}, nil
}

func (r *recordingProvider) report(model string, in, out int) {
	if r.sink == nil {
		return
	}
	r.sink(Usage{Source: r.source, Model: model, IsCloud: r.isCloud, InputTokens: in, OutputTokens: out})
}

// recordingReader accumulates token counts off the stream and reports exactly
// once, when the stream is exhausted or closed.
type recordingReader struct {
	inner    llm.StreamReader
	model    string
	rp       *recordingProvider
	in, out  int
	reported bool
}

func (rr *recordingReader) Next() (llm.StreamEvent, bool, error) {
	ev, ok, err := rr.inner.Next()
	if ok {
		if ev.InputTokens > 0 {
			rr.in = ev.InputTokens
		}
		if ev.OutputTokens > 0 {
			rr.out = ev.OutputTokens
		}
	}
	if !ok && err == nil {
		rr.flush()
	}
	return ev, ok, err
}

func (rr *recordingReader) Close() error {
	rr.flush()
	return rr.inner.Close()
}

func (rr *recordingReader) flush() {
	if rr.reported {
		return
	}
	rr.reported = true
	rr.rp.report(rr.model, rr.in, rr.out)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/usage/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git -C <worktree> add source/server/internal/usage/
git -C <worktree> commit -m "feat(usage): RecordingProvider decorator emits per-call token usage at the provider boundary"
```

### Task 2: Wire `RecordingProvider` around the constructed providers

**Files:**
- Modify: `source/server/internal/server/server.go` (where `s.cloudLLMProvider` / `s.localLLMProvider` are assigned)
- Test: `source/server/internal/server/usage_wire_test.go`

**Interfaces:**
- Consumes: `usage.Wrap`, the existing `telemetry.Collector` on the server (`s.collector`).
- The server wraps each `llm.Provider` once at construction with a sink that emits a `telemetry.Event{ToolName: u.Source, Model: u.Model, InputTokens, OutputTokens, ...}` via `s.collector.Emit`. `IsCloud` maps to the event's cloud fields consistent with existing `emitEvent` usage.

- [ ] **Step 1: Find the provider construction site**

Run: `cd source/server && grep -n "cloudLLMProvider\|localLLMProvider" internal/server/*.go`
Read those assignments; identify the single function that builds the providers (provider construction in server setup).

- [ ] **Step 2: Write the failing test**

```go
package server

import (
	"context"
	"testing"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
)

// A fake collector capturing emitted events.
type capCollector struct{ events []string }

func (c *capCollector) record(source string) { c.events = append(c.events, source) }

func TestUsageSinkEmitsToCollector(t *testing.T) {
	cc := &capCollector{}
	sink := newUsageSink(cc.record) // helper added in Step 3; maps Usage -> collector
	inner := stubProvider{in: 3, out: 4}
	p := usage.Wrap(inner, "main", false, sink)
	if _, err := p.Chat(context.Background(), llm.ChatRequest{Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if len(cc.events) != 1 || cc.events[0] != "main" {
		t.Fatalf("collector did not receive the usage event: %+v", cc.events)
	}
}

type stubProvider struct{ in, out int }

func (stubProvider) Name() string                  { return "stub" }
func (stubProvider) Capabilities() llm.Capabilities { return llm.Capabilities{} }
func (s stubProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{InputTokens: s.in, OutputTokens: s.out}, nil
}
func (stubProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}
```

(Adjust `newUsageSink`'s collaborator type to match the real `telemetry.Collector`; the test uses a minimal `record(source)` shim to prove the wiring without coupling to the full Event shape.)

- [ ] **Step 3: Implement the sink + wrap at construction**

Add a `newUsageSink` helper near the provider construction that adapts `usage.Usage` → a `telemetry.Event` and calls `s.collector.Emit`. Then wrap each provider:

```go
// after building cloudP / localP (the raw llm.Provider values):
s.cloudLLMProvider = usage.Wrap(cloudP, "main", true, s.usageSink())
s.localLLMProvider = usage.Wrap(localP, "main", false, s.usageSink())
```

`s.usageSink()` returns a `func(usage.Usage)` that builds the telemetry event from the existing `telemetry.Event` fields (`ToolName: u.Source`, `Model: u.Model`, `InputTokens`, `OutputTokens`, and the cloud/local mapping `emitEvent` already uses). Keep "main" as the source here; dispatch/co-proc override the source by wrapping with their own source string in later phases (or by passing the source through the dispatch path — see Task 5/Task 9).

- [ ] **Step 4: Run tests**

Run: `cd source/server && go test ./internal/server/ -run TestUsage -v`
Expected: PASS.

- [ ] **Step 5: Build + full suite**

Run: `cd source/server && go build ./... && go test ./... -count=1`
Expected: green. The main loop now emits a usage event per turn through the wrapped provider.

- [ ] **Step 6: Commit**

```bash
git -C <worktree> add source/server/internal/server/server.go source/server/internal/server/usage_wire_test.go
git -C <worktree> commit -m "feat(server): record main-loop token usage via RecordingProvider at provider construction"
```

---

## Phase 2 — Routing seam (locus-governed provider selection)

### Task 3: `dispatch.Select` — one locus-governed selector

**Files:**
- Create: `source/server/internal/dispatch/select.go`
- Test: `source/server/internal/dispatch/select_test.go`

**Interfaces:**
- Consumes: `locus.Mode`/`locus.Resolution`/`locus.Tier`, `llm.Provider`.
- Produces:
  ```go
  type Role int
  const ( RoleMain Role = iota; RoleCoproc )
  type Providers struct { Cloud, Local llm.Provider } // either may be nil
  type Selection struct {
      Provider llm.Provider
      IsCloud  bool
      FellBack bool
      Notice   string // set only when FellBack
  }
  func Select(mode locus.Mode, role Role, p Providers) (Selection, error)
  ```
- This generalizes the `pick`/fallback logic currently inline in `agent.processCoproc` and `server.resolveMainProvider`. `RoleCoproc` uses `mode.Coproc()`; `RoleMain` uses `mode.Main()`. A cloud provider whose `Name() == "NONE"` (the absent-cloud sentinel) counts as unavailable.

- [ ] **Step 1: Write the failing test**

```go
package dispatch

import (
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
)

type namedProvider struct{ name string }

func (n namedProvider) Name() string                  { return n.name }
func (namedProvider) Capabilities() llm.Capabilities  { return llm.Capabilities{} }
func (namedProvider) Chat(_ any) {}                    // unused in selection tests

func TestSelectCoprocPrefersLocalUnderCloudPrimary(t *testing.T) {
	local := stubLLM{"local"}
	cloud := stubLLM{"cloud"}
	sel, err := Select(locus.CloudPrimary, RoleCoproc, Providers{Cloud: cloud, Local: local})
	if err != nil {
		t.Fatal(err)
	}
	if sel.IsCloud || sel.Provider.Name() != "local" {
		t.Fatalf("coproc under cloud_primary must run local, got %+v", sel)
	}
}

func TestSelectFallbackNotice(t *testing.T) {
	cloud := stubLLM{"cloud"}
	// local_primary, no local available -> fall back to cloud, with notice.
	sel, err := Select(locus.LocalPrimary, RoleCoproc, Providers{Cloud: cloud, Local: nil})
	if err != nil {
		t.Fatal(err)
	}
	if !sel.FellBack || !sel.IsCloud {
		t.Fatalf("expected cloud fallback, got %+v", sel)
	}
	if !strings.Contains(sel.Notice, "preferred co-processor tier unavailable") {
		t.Fatalf("missing fallback notice: %q", sel.Notice)
	}
}

func TestSelectNoProviderErrors(t *testing.T) {
	if _, err := Select(locus.LocalOnly, RoleCoproc, Providers{}); err == nil {
		t.Fatal("expected error when no provider is available")
	}
}

func TestSelectCloudOnlyForbidsLocal(t *testing.T) {
	local := stubLLM{"local"}
	if _, err := Select(locus.CloudOnly, RoleMain, Providers{Local: local}); err == nil {
		t.Fatal("cloud_only with only local available must error, never run local")
	}
}

type stubLLM struct{ n string }

func (s stubLLM) Name() string                  { return s.n }
func (stubLLM) Capabilities() llm.Capabilities  { return llm.Capabilities{} }
func (stubLLM) Chat(_ any)                       {}
```

(Note: the `stubLLM` here only needs `Name()`+`Capabilities()` for selection; if the compiler requires the full `llm.Provider` interface, give it no-op `Chat`/`StreamChat` matching the real signatures. The real signatures are `Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error)` and `StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error)`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/dispatch/ -run TestSelect -v`
Expected: FAIL — `Select` undefined.

- [ ] **Step 3: Write the implementation**

```go
package dispatch

import (
	"fmt"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
)

// Role selects which locus policy governs the choice.
type Role int

const (
	RoleMain   Role = iota // main agentic work: mode.Main()
	RoleCoproc             // co-processor / one-shot work: mode.Coproc()
)

// Providers holds the candidate providers; either may be nil/absent.
type Providers struct {
	Cloud llm.Provider
	Local llm.Provider
}

// Selection is the resolved provider for a unit of work.
type Selection struct {
	Provider llm.Provider
	IsCloud  bool
	FellBack bool
	Notice   string
}

// Select resolves a provider under the given locus mode and role. Locus is the
// hard governor: cloud_only/local_only never cross tiers; preferred/fallback
// are honored only when the resolution permits crossing.
func Select(mode locus.Mode, role Role, p Providers) (Selection, error) {
	res := mode.Main()
	if role == RoleCoproc {
		res = mode.Coproc()
	}
	pick := func(t locus.Tier) llm.Provider {
		if t == locus.TierCloud {
			if p.Cloud != nil && p.Cloud.Name() != "NONE" {
				return p.Cloud
			}
			return nil
		}
		return p.Local
	}
	if prov := pick(res.Preferred); prov != nil {
		return Selection{Provider: prov, IsCloud: res.Preferred == locus.TierCloud}, nil
	}
	if res.CrossAllowed {
		if prov := pick(res.Fallback); prov != nil {
			return Selection{
				Provider: prov,
				IsCloud:  res.Fallback == locus.TierCloud,
				FellBack: true,
				Notice:   fmt.Sprintf("locus: preferred co-processor tier unavailable — ran on %s (%s)", res.Fallback, prov.Name()),
			}, nil
		}
	}
	return Selection{}, fmt.Errorf("locus mode %q: no %s provider available for co-processor work", mode, res.Preferred)
}
```

(The Notice/error strings are copied verbatim from `agent.processCoproc` to preserve client-visible behavior; the `RoleMain` path produces the same Notice when it falls back, which is acceptable — the main path previously had its own fallback message in `resolveMainProvider`; Task 7 reconciles the main caller to this selector or leaves `resolveMainProvider` as-is if its message differs. Decision recorded in Task 7.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/dispatch/ -run TestSelect -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git -C <worktree> add source/server/internal/dispatch/select.go source/server/internal/dispatch/select_test.go
git -C <worktree> commit -m "feat(dispatch): locus-governed provider selector (one seam for main + coproc)"
```

---

## Phase 3 — The Dispatch primitive: OneShot

### Task 4: `Spec`/`Result`/`Engine` + OneShot execution

**Files:**
- Create: `source/server/internal/dispatch/engine.go`
- Test: `source/server/internal/dispatch/engine_test.go`

**Interfaces:**
- Consumes: `dispatch.Select`, `llm.Provider`, `context.Loader` (project context).
- Produces:
  ```go
  type Mode int
  const ( OneShot Mode = iota; Agentic )
  type Spec struct {
      Mode                Mode
      Role                Role
      Prompt              string   // OneShot: the full user prompt
      System              string   // optional system text
      WantsProjectContext bool
      WorkDir             string
      ConversationID      string
      Source              string   // usage label, e.g. "coproc:summarize"
      // Agentic-only fields added in Phase 6.
  }
  type Result struct {
      Text         string
      Model        string
      IsCloud      bool
      Notice       string
      InputTokens  int
      OutputTokens int
  }
  type Engine struct { /* providers, mode getter, ctxLoader */ }
  func NewEngine(p Providers, modeFn func() locus.Mode, ctx *projectctx.Loader) *Engine
  func (e *Engine) Dispatch(ctx context.Context, spec Spec) (Result, error)
  ```
- OneShot path: resolve provider via `Select(modeFn(), spec.Role, providers)`; if `WantsProjectContext` and `WorkDir` set, `prompt = ctxLoader.PrependContext(WorkDir, prompt)`; build `llm.ChatRequest{Model: <selected model>, System, Messages: [user(prompt)]}`; call `provider.Chat`; assemble `Result` (Text from the text blocks, tokens from the response, Notice from the selection). The selected **model name** comes from the provider tier via a model-name resolver passed into the engine (cloud vs local model from config) — keep it a small injected `modelFor(isCloud bool) string` func so the engine does not import config.

- [ ] **Step 1: Write the failing test** (uses a fake provider returning a known text block + tokens; asserts Result fields, project-context prepend, and that locus governs the tier)

```go
package dispatch

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
)

func TestOneShotReturnsTextAndTokens(t *testing.T) {
	prov := echoProvider{} // returns a text block echoing the last user message + fixed tokens
	eng := NewEngine(
		Providers{Local: prov},
		func() locus.Mode { return locus.LocalOnly },
		nil, // no project context loader
	)
	eng.SetModelFor(func(isCloud bool) string { return "local-model" })

	res, err := eng.Dispatch(context.Background(), Spec{
		Mode: OneShot, Role: RoleCoproc, Prompt: "summarize this", Source: "coproc:summarize",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "summarize this") {
		t.Fatalf("expected echoed prompt, got %q", res.Text)
	}
	if res.Model != "local-model" || res.IsCloud {
		t.Fatalf("locus_only=local must pick local model: %+v", res)
	}
	if res.InputTokens == 0 || res.OutputTokens == 0 {
		t.Fatalf("tokens not propagated: %+v", res)
	}
}
```

(Provide an `echoProvider` implementing the full `llm.Provider` interface: `Chat` returns `llm.ChatResponse{Blocks: []llm.Block{{Type: llm.BlockText, Text: "echo: " + lastUserText(req)}}, InputTokens: 9, OutputTokens: 4}`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/dispatch/ -run TestOneShot -v`
Expected: FAIL — `NewEngine`/`Dispatch` undefined.

- [ ] **Step 3: Write `engine.go`** (OneShot only; Agentic returns `errors.New("agentic mode not yet implemented")` until Phase 6). Implement: selection → optional context prepend → `llm.ChatRequest` with a single `llm.Message{Role: RoleUser, Blocks: [{Type: BlockText, Text: prompt}]}` → `provider.Chat` → collect text from `BlockText` blocks → `Result`. The engine holds `providers Providers`, `modeFn func() locus.Mode`, `ctxLoader *projectctx.Loader` (nil-safe), and `modelFor func(bool) string` (set via `SetModelFor`, default returns "").

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/dispatch/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git -C <worktree> add source/server/internal/dispatch/engine.go source/server/internal/dispatch/engine_test.go
git -C <worktree> commit -m "feat(dispatch): OneShot Dispatch primitive (routing + context + provider.Chat)"
```

### Task 5: `ContextAware` capability opt-in + usage source on OneShot

**Files:**
- Modify: `source/server/internal/capabilities/capability.go` (add the optional interface)
- Modify: `source/server/internal/dispatch/engine.go` (label usage with `spec.Source` by wrapping the selected provider with `usage.Wrap` before `Chat`)
- Test: `source/server/internal/capabilities/capability_context_test.go`, extend `engine_test.go`

**Interfaces:**
- Produces:
  ```go
  // ContextAware is implemented by capabilities that want the project-context
  // digest prepended to their dispatched prompt. Capabilities that don't
  // implement it default to no project context (e.g. fetch).
  type ContextAware interface{ WantsProjectContext() bool }
  ```
- The engine, in OneShot, wraps the selected provider with `usage.Wrap(prov, spec.Source, sel.IsCloud, e.usageSink)` so co-processor/dispatch usage is labeled by source (not "main"). `e.usageSink` is injected (set via `SetUsageSink`), defaulting nil.

- [ ] **Step 1: Write the failing test** — a capability implementing `WantsProjectContext() bool { return true }`; assert the engine prepends context when a loader + workdir are present, and that a usage event with the spec's `Source` is emitted via the injected sink.

- [ ] **Step 2: Run; fail.**

- [ ] **Step 3: Implement** — add `ContextAware` to `capability.go`; in `engine.go` OneShot, wrap provider with `usage.Wrap(...)` using `spec.Source`. (The capability→engine link is in Phase 5; here the engine just honors `spec.WantsProjectContext` and `spec.Source`.)

- [ ] **Step 4: Run; pass. Step 5: Build + suite. Step 6: Commit**

```bash
git -C <worktree> commit -m "feat(dispatch): project-context opt-in (ContextAware) + source-labeled usage on OneShot"
```

---

## Phase 4 — Migrate co-processor execution onto the engine

### Task 6: Reimplement `processCoproc` on `llm.Provider` via the engine

**Files:**
- Modify: `source/server/internal/agent/agent.go` (`processCoproc`)
- Modify: `source/server/internal/server/server.go` (construct + inject the `dispatch.Engine` into the agent; provide `Providers`, `modeFn`, `modelFor`, `ctxLoader`, `usageSink`)
- Test: `source/server/internal/agent/coproc_dispatch_test.go`

**Interfaces:**
- `processCoproc` now: `loadHistory` (unchanged — project context + conversation prepend) → call the engine's OneShot with `Spec{Mode: OneShot, Role: RoleCoproc, Prompt: augmentedInput, ModelOverride?, Source: "coproc", WantsProjectContext: false (context already prepended by loadHistory), ConversationID}` → map `dispatch.Result` back to the legacy `*Response{Output, InputTokens, OutputTokens, RoutingMetadata{ModelName, Confidence: 1.0, IsCloud}, Notice}` → `storeConversationTurn` (unchanged).
- `ModelOverride`: thread it through `Spec` (add `ModelOverride string` to `Spec`); the engine uses it as the model name when set, and the returned `Result.Model` reflects the override (preserving the legacy `modelName = override` rule).

**Migration preservation (assert in tests):** `cloud_primary` keeps co-proc local; fallback sets the exact Notice; no-provider errors with the exact message; `RoutingMetadata.Confidence == 1.0`; `IsCloud` matches the selected tier; tokens populated (now real for cloud).

- [ ] **Step 1: Write failing tests** covering: (a) local pick under `local_primary`; (b) `cloud_primary` keeps local; (c) fallback Notice exact string when preferred tier absent; (d) hard error when no provider; (e) `ModelOverride` reflected in `RoutingMetadata.ModelName`. Use fake `llm.Provider`s injected into the engine. Drive through `agent.processCoproc` with a test `Agent` whose engine + locus getter are set.

- [ ] **Step 2: Run; fail** (engine not yet injected into agent).

- [ ] **Step 3: Implement** — add an `engine *dispatch.Engine` field to `Agent`; reimplement `processCoproc` body to call it; delete the legacy `router.GetModelProviders()` pick/fallback block from `processCoproc`. Wire the engine in `server.go` (build `dispatch.NewEngine(Providers{Cloud: s.cloudLLMProvider, Local: s.localLLMProvider}, s.locusModeGetter, s.contextLoader)`, `SetModelFor(s.mainModelFor)`, `SetUsageSink(s.usageSink())`, then `agent.SetDispatchEngine(eng)`).

- [ ] **Step 4: Run; pass. Step 5: Build + full suite** (the gRPC `ProcessRequest{Coproc:true}` contract is unchanged; the six MCP handlers keep working, now on llm.Provider). Run a manual smoke: build `bin/cercano`, exercise one co-processor MCP tool if a local model is available, confirm a sensible response + telemetry event.

- [ ] **Step 6: Commit**

```bash
git -C <worktree> commit -m "feat(agent): route co-processor work through the Dispatch engine on llm.Provider (retire ModelProvider coproc path)"
```

### Task 7: Reconcile the main-loop provider selection to `dispatch.Select`

**Files:**
- Modify: `source/server/internal/server/server.go` (`resolveMainProvider`)
- Test: `source/server/internal/server/resolve_main_test.go`

Replace `resolveMainProvider`'s inline locus pick with a call to `dispatch.Select(mode, dispatch.RoleMain, Providers{...})`, so main and co-proc share the one selector. If the main path's existing fallback message differs from the selector's Notice and that difference is user-visible, preserve the main message by setting it at the call site (do not change the selector). Record the decision in the commit message.

- [ ] **Step 1: Test** — `resolveMainProvider` returns cloud under `cloud_only`, errors when the only provider is forbidden by mode, falls back when `CrossAllowed`. **Step 2: fail. Step 3: implement. Step 4: pass. Step 5: build+suite. Step 6: commit**

```bash
git -C <worktree> commit -m "refactor(server): main-loop provider selection via shared dispatch.Select"
```

---

## Phase 5 — Co-processor commands as capabilities

> **Wire-up:** `Services.RunCoproc` (currently unassigned) is wired in Task 8 to the engine's OneShot, so a capability runs a fixed-template prompt with one call. The simple commands (`summarize`, `extract`, `classify`, `explain`) become capabilities here. **`research` and `document` stay orchestrations** (research is a multi-step pipeline; document parses Go and loops per symbol) — their internal per-step model calls already flow through the migrated co-processor path after Phase 4, so they gain the unified seam without being collapsed into a single capability. This is a deliberate scope line, logged here so it isn't read as an omission.

### Task 8: Wire `Services.RunCoproc` to the engine; add a coproc capability base

**Files:**
- Modify: `source/server/internal/capabilities/services.go` (document `RunCoproc`'s contract)
- Modify: `source/server/internal/server/server.go` (assign `Services.RunCoproc` to a closure over the engine's OneShot)
- Create: `source/server/internal/capabilities/builtins/coproc.go` (a small helper `runCoproc(call, promptTemplate, content, wantsCtx) (*Result, error)` shared by the four commands, using `call.Svc.RunCoproc`)
- Test: `source/server/internal/capabilities/builtins/coproc_test.go`

`RunCoproc(ctx, prompt, projectDir)` calls `engine.Dispatch(ctx, Spec{Mode: OneShot, Role: RoleCoproc, Prompt: prompt, WantsProjectContext: true, WorkDir: projectDir, Source: "coproc"})` and returns `Result.Text`. The capability helper builds the fixed prompt, calls `call.Svc.RunCoproc`, returns `NewTextResult`.

- [ ] **Steps 1-6 (TDD):** test a fake `Svc.RunCoproc` is invoked with the assembled prompt; implement; build; commit.

```bash
git -C <worktree> commit -m "feat(capabilities): wire Services.RunCoproc to the OneShot engine + coproc helper"
```

### Task 9: `summarize`, `extract`, `classify`, `explain` capabilities

**Files:**
- Create: `source/server/internal/capabilities/builtins/coproc_summarize.go` (+ `_extract`, `_classify`, `_explain`)
- Modify: `source/server/internal/capabilities/builtins/builtins.go` (register the four; tier R; both surfaces; each implements `WantsProjectContext() bool { return true }`)
- Modify the registration-count test (bump expected count by 4)
- Test: `source/server/internal/capabilities/builtins/coproc_caps_test.go`

Each capability mirrors the existing MCP handler's prompt template **verbatim** (copy from `internal/mcp/server.go` handleSummarize/handleExtract/handleClassify/handleExplain), takes `{text|file_path, ...}` args, and returns `runCoproc(...)`. Reading a `file_path` reuses the existing read helper used by `fs_read` (or `os.ReadFile` with the same error wrapping convention).

- [ ] **Steps 1-7 (TDD):** test each capability's name/tier/surfaces and that it forwards the right prompt to a fake `RunCoproc`; register; bump count test; build; the four now appear as standalone tools AND as `cercano_<name>` via the 0a MCP adapter. Commit.

```bash
git -C <worktree> commit -m "feat(capabilities): summarize/extract/classify/explain as capabilities on the OneShot engine (both surfaces)"
```

> **Follow-on note (not this plan):** once these capabilities are proven on both surfaces, the bespoke `handleSummarize/...` MCP handlers in `internal/mcp/server.go` can be removed in favor of `InvokeCapability` routing, eliminating the duplicate prompt templates. Left out here to keep this plan's blast radius bounded; logged so it isn't lost.

---

## Phase 6 — Agentic dispatch (reuse `RunToolLoop`) + retire the engine loop

### Task 10: Make `RunToolLoop` depth-configurable

**Files:**
- Modify: `source/server/internal/agent/toolloop.go` (add `MaxIterations int`, `MaxTokensPerTurn int` to `ToolLoopInput`; default to the existing constants when zero)
- Test: `source/server/internal/agent/toolloop_limits_test.go`

- [ ] **Steps 1-5 (TDD):** test that a small `MaxIterations` caps the loop; that zero preserves today's default (50); implement (replace the package-constant references with `in.MaxIterations`-or-default); build; commit.

```bash
git -C <worktree> commit -m "feat(agent): make RunToolLoop iteration/token caps injectable (default unchanged)"
```

### Task 11: `agenttools.Registry.Subset` (least-privilege grants)

**Files:**
- Modify: `source/server/internal/agenttools/registry.go` (`func (r *Registry) Subset(names []string) *Registry`)
- Test: `source/server/internal/agenttools/registry_subset_test.go`

`Subset` returns a new `Registry` containing only the named tools that exist in `r`; unknown names are ignored (the caller validates). Used to build a least-privilege catalog for a subagent.

- [ ] **Steps 1-5 (TDD):** test subset contains exactly the named, existing tools; commit.

```bash
git -C <worktree> commit -m "feat(agenttools): Registry.Subset for least-privilege tool grants"
```

### Task 12: Agentic dispatch on `RunToolLoop`

**Files:**
- Create: `source/server/internal/dispatch/agentic.go`
- Modify: `source/server/internal/dispatch/engine.go` (Agentic branch in `Dispatch`)
- Test: `source/server/internal/dispatch/agentic_test.go`

**Interfaces:**
- Add Agentic fields to `Spec`: `Task string` (the open-ended instruction), `Tools []string` (capability/tool allowlist; default = R-tier only), `Interactive bool` (standalone may be true; MCP false), `MaxIterations int`.
- Agentic execution: resolve provider via `Select(modeFn(), spec.Role, providers)`; build the tool catalog by `agenttools.Subset(spec.Tools)` (or, if `spec.Tools` empty, the R-tier filter `Registry.Filter(llm.PermR)`); compose the system prompt = persona + `protocols.SteeringBlock(protocols.ForDomain(protocols.DomainCore))` + (optional) project context; choose permission mode — **non-interactive (MCP):** a `PermissionStore` in bypass-equivalent for granted tiers but **never exceeding the parent mode** (if parent is strict/permissive, W/X tools are simply not in the allowlist); **interactive (standalone):** pass the parent's requester through; build a `ToolLoopInput` and call `agent.RunToolLoop`; translate `agent.LoopEvent`s into dispatch progress (best-effort) and return a `Result{Text: final assistant text, Model, IsCloud, tokens aggregated}`.
- The engine needs an injected handle to the standalone tool registry (`agenttools.Registry`) and the persona/steering builder; inject via `SetAgenticDeps(reg *agenttools.Registry, systemFn func(workDir string) string)`.

- [ ] **Step 1: Write the failing test** — inject a fake `llm.Provider` whose `StreamChat` returns a script: one turn that calls a stub R-tier tool, then a turn with final text. Assert: the loop ran the tool, the granted catalog excluded a W-tier tool not in `Tools`, and `Result.Text` is the final assistant text. (Use a stub capability registered in a test `agenttools.Registry`.)

- [ ] **Step 2: Run; fail. Step 3: Implement `agentic.go` + the Agentic branch. Step 4: Run; pass. Step 5: Build + suite. Step 6: Commit**

```bash
git -C <worktree> commit -m "feat(dispatch): Agentic mode reusing RunToolLoop over a least-privilege capability subset"
```

### Task 13: `dispatch` capability (aliased `workflow`) + retire the engine-based loop

**Files:**
- Create: `source/server/internal/capabilities/builtins/dispatch_cap.go`
- Modify: `source/server/internal/capabilities/builtins/builtins.go` (register `dispatch`; add `"dispatch": "workflow"`? — alias is the agent display name; see AgentAliases) ; bump count test
- Modify: `source/server/internal/mcp/server.go` (route `cercano_dispatch` through `InvokeCapability` to the new capability; remove `SetDispatch`, `dispatchLoop`, `dispatchStore` wiring)
- Delete: `source/server/internal/dispatch/dispatch.go`, `events.go`, `store.go` (the engine-based `Loop`/`Event`/`Store`)
- Test: `source/server/internal/capabilities/builtins/dispatch_cap_test.go`

The `dispatch` capability (tier W — it can run W/X tools when granted; always confirmed on standalone unless bypass): args `{task string, tools []string, conversation_id string}`. `Execute` calls `call.Svc`'s engine handle (add `Services.Dispatch func(ctx, dispatch.Spec) (dispatch.Result, error)` or a typed engine handle) with `Mode: Agentic`, `Interactive` derived from surface (standalone vs MCP — the MCP adapter sets non-interactive). Aliased `workflow` for the agent display so host models reaching for "the workflow tool" find it.

- [ ] **Step 1: Test** the capability forwards a Spec with the task + tool allowlist to a fake engine; name/tier/surfaces; alias present. **Step 2: fail. Step 3: implement + reroute MCP + delete the old loop files. Step 4: pass. Step 5: build + full suite** (confirm nothing imports the deleted `dispatch.Loop`/`Store`; `grep -rn "dispatch.NewLoop\|dispatchLoop\|dispatch.Store"` returns nothing). **Step 6: commit**

```bash
git -C <worktree> commit -m "feat(capabilities): dispatch/workflow capability on the Agentic engine; retire engine-based dispatch.Loop"
```

---

## Phase 7 — `review` capability

### Task 14: `review` capability (refute-style verdict)

**Files:**
- Create: `source/server/internal/capabilities/builtins/review.go`
- Modify: `builtins.go` (register; tier R; both surfaces); bump count test
- Test: `source/server/internal/capabilities/builtins/review_test.go`

`review` is a first-class capability built on Dispatch (OneShot with a refute-style prompt + a verdict schema, or Agentic when given tools to inspect files). Args: `{claim string, context string, tools []string (optional)}`. With no tools → OneShot: prompt the model to *try to refute* the claim and return a structured verdict `{real bool, reasoning string}` (parse from the model text, or request JSON). With tools → Agentic over the named (default R-tier) subset so the reviewer can read files. Enforcement that review actually *runs* before risky actions is the **0b watchdog's** job (post-MVP), not baked here.

- [ ] **Step 1: Test** — OneShot review returns a verdict parsed from a fake provider's response; the prompt instructs refutation; name/tier/surfaces. **Step 2: fail. Step 3: implement. Step 4: pass. Step 5: build+suite. Step 6: commit**

```bash
git -C <worktree> commit -m "feat(capabilities): review capability (refute-style verdict on the Dispatch engine)"
```

---

## Phase 8 — Least-privilege & surface-split enforcement

### Task 15: Subagent permission bounds

**Files:**
- Modify: `source/server/internal/dispatch/agentic.go` (enforce: granted tools never exceed parent permission mode; W/X only when explicitly named AND parent mode allows)
- Test: `source/server/internal/dispatch/agentic_perms_test.go`

Enforce the constraint in code, not just by convention: when building the Agentic catalog, if the parent permission mode is strict/permissive, drop any W/X tool from the grant (or require it to pass the live gate via the parent requester when interactive); in bypass, honor the explicit allowlist. A subagent dispatched over MCP (non-interactive) gets at most what the parent mode permits without a human in the loop.

- [ ] **Step 1: Test** — a spec naming a W-tier tool under a strict parent yields a catalog without it (or gated); under bypass it's included. **Step 2: fail. Step 3: implement. Step 4: pass. Step 5: build+suite. Step 6: commit**

```bash
git -C <worktree> commit -m "feat(dispatch): enforce least-privilege subagent tool grants bounded by parent permission mode"
```

---

## Deferred / follow-on (not this plan)

- **Remove bespoke co-processor MCP handlers** in favor of `InvokeCapability` routing once the four capabilities are proven (eliminates duplicate prompt templates). Phase 5 follow-on note.
- **Retire `legacymodels.CloudModelProvider` (langchaingo)** and the `ModelProvider` interface entirely once nothing references them (the SmartRouter's classify/select path for the main agent still uses `ModelProvider` today; that intent-routing migration is its own effort and pairs with the future embedded-small-model router).
- **Async job + poll** MCP dispatch mode (sync default today) for long-running work that risks host tool-call timeouts.
- **Parallel fan-out** orchestrator (bounded concurrency) over the single-dispatch primitive.
- **The watchdog** (Spec 0b Part C) consuming `review` + protocol triggers — depends on the small-model routing this engine seeds.

---

## Self-Review

- **Spec coverage:** usage at the provider boundary (Phase 1); locus-governed routing seam (Phase 2); OneShot primitive + project-context opt-in (Phase 3); co-processor migration onto `llm.Provider` with full behavior preservation (Phase 4); co-processor commands as capabilities (Phase 5); Agentic dispatch reusing `RunToolLoop` + `dispatch`/`workflow` capability + retiring the engine loop (Phase 6); `review` capability (Phase 7); least-privilege subagent bounds (Phase 8). Watchdog correctly deferred (depends on 0b Part C + small-model routing).
- **Single-seam honored:** every model call funnels through `llm.Provider`; routing via `dispatch.Select`; usage via `usage.Wrap`; context via the engine. No second model-call path survives (engine `dispatch.Loop` and the `ModelProvider` coproc path are deleted).
- **Behavior-preservation flagged** with verbatim strings and an explicit test list (Task 6).
- **Reuse over rebuild:** Agentic dispatch reuses `agent.RunToolLoop` (verified fully parameterizable) instead of porting a second loop — the single biggest risk reducer.
- **Scope lines logged, not silently dropped:** `research`/`document` stay orchestrations; bespoke MCP handlers and langchaingo removal are explicit follow-ons; parallel fan-out and async-poll deferred.
- **Type consistency:** `dispatch.Select`/`Selection`/`Providers`/`Role` used consistently across Tasks 3-7; `Spec`/`Result`/`Engine` across Tasks 4-14; `ContextAware` (Task 5) consumed in Phase 5; `ToolLoopInput` new fields (Task 10) consumed in Task 12; `Registry.Subset` (Task 11) consumed in Tasks 12/15.
- **Dependency note:** Phases 1-4 are the spine and must land in order. Phase 5 depends on Task 8's `RunCoproc` wiring. Phase 6 depends on Tasks 10-11. Phases 7-8 depend on Phase 6's Agentic engine.
