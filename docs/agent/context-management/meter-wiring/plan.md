# Context Meter Wiring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the CLI context-window meter show real cloud-context occupancy: provider-reported tokens snapshotted against the cloud model's window after each tool-loop turn.

**Architecture:** Thread Anthropic streaming usage (`message_start` input, `message_delta` output) through `llm.StreamEvent` → `collectStream` → `ChatResponse` → `ToolLoopResult`, then have the server call a new `agent.RecordContextUsage` to snapshot the per-conversation `contextmeter.Counter` against the cloud model's window. Downstream (RPC → CLI render) is unchanged.

**Tech Stack:** Go (module `cercano/source/server`), `anthropic-sdk-go@v1.51.0`, internal `llm`/`agent`/`contextmeter` packages.

## Global Constraints

- All work is in the `source/server` Go module. Build: `cd source/server && go build ./...`. Test: `cd source/server && go test ./... -count=1`.
- Snapshot semantics (reset-then-add), matching the legacy path (`agent.go:159`). `used = input + output`.
- The meter must use the **cloud** model's window (`s.currentConfig.CloudModel`), not the local model.
- Never clobber a good reading: skip the meter update when `inputTokens <= 0`.
- Commit messages MUST NOT contain the word "Claude" anywhere. No Co-Authored-By trailer.

Reference shapes (already defined — do not redefine):

```go
// internal/llm/stream.go
type StreamEventType string
const ( EventMessageStart StreamEventType = "message_start"; EventTextDelta ...; EventMessageStop StreamEventType = "message_stop"; ... )
type StreamEvent struct { Type StreamEventType; TextDelta string; ToolUseID, ToolName string; ToolInputRaw json.RawMessage; StopReason string; ErrText string }
// StreamReader: Next() (StreamEvent, bool, error); Close() error

// internal/llm/provider.go
type ChatResponse struct { Blocks []Block; StopReason string; InputTokens int; OutputTokens int }

// internal/contextmeter (counter.go / tokenizer.go)
func (r *Registry) Get(id, tokModel string) *Counter
func (c *Counter) SetMax(max int); func (c *Counter) Reset(); func (c *Counter) AddCount(n int); func (c *Counter) Used() int; func (c *Counter) Max() int
func ModelMax(model string) int
// agent.go fields: a.meter *contextmeter.Registry ; a.meterModel string
// agent.GetContextUsage(convID) (used, max int)  // existing reader at agent.go:~270
```

---

### Task 1: Thread provider usage into ChatResponse

Add usage to `StreamEvent`, populate it in the Anthropic stream converter, and capture it in `collectStream` so `ChatResponse` carries token counts on the streaming path.

**Files:**
- Modify: `source/server/internal/llm/stream.go` (StreamEvent struct)
- Modify: `source/server/internal/llm/anthropic/stream.go` (`convert`, message_start ~line 33-34 and message_delta ~line 61-62)
- Modify: `source/server/internal/agent/toolloop.go` (`collectStream`)
- Test: `source/server/internal/agent/collectstream_test.go` (new)

**Interfaces:**
- Produces: `StreamEvent.InputTokens int`, `StreamEvent.OutputTokens int`; `collectStream` sets `ChatResponse.InputTokens`/`OutputTokens`.

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/agent/collectstream_test.go`:

```go
package agent

import (
	"context"
	"testing"

	"cercano/source/server/internal/llm"
)

// fakeReader yields a fixed event sequence, then EOF.
type fakeReader struct {
	events []llm.StreamEvent
	i      int
}

func (r *fakeReader) Next() (llm.StreamEvent, bool, error) {
	if r.i >= len(r.events) {
		return llm.StreamEvent{}, false, nil
	}
	ev := r.events[r.i]
	r.i++
	return ev, true, nil
}
func (r *fakeReader) Close() error { return nil }

func TestCollectStream_CapturesUsage(t *testing.T) {
	rdr := &fakeReader{events: []llm.StreamEvent{
		{Type: llm.EventMessageStart, InputTokens: 1234},
		{Type: llm.EventTextDelta, TextDelta: "hi"},
		{Type: llm.EventMessageStop, StopReason: "end_turn", OutputTokens: 56},
	}}
	resp, err := collectStream(context.Background(), rdr)
	if err != nil {
		t.Fatalf("collectStream: %v", err)
	}
	if resp.InputTokens != 1234 {
		t.Errorf("InputTokens = %d, want 1234", resp.InputTokens)
	}
	if resp.OutputTokens != 56 {
		t.Errorf("OutputTokens = %d, want 56", resp.OutputTokens)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/agent/ -run TestCollectStream_CapturesUsage -count=1`
Expected: FAIL — `StreamEvent` has no `InputTokens`/`OutputTokens` field (compile error).

- [ ] **Step 3: Add fields to StreamEvent**

In `source/server/internal/llm/stream.go`, add to the `StreamEvent` struct:

```go
	// Provider-reported token usage. InputTokens is set on EventMessageStart
	// (full prompt: system + tools + history); OutputTokens on EventMessageStop.
	InputTokens  int
	OutputTokens int
```

- [ ] **Step 4: Populate usage in the Anthropic converter**

In `source/server/internal/llm/anthropic/stream.go` `convert()`:

Replace the `message_start` case:

```go
	case "message_start":
		return llm.StreamEvent{Type: llm.EventMessageStart, InputTokens: int(raw.Message.Usage.InputTokens)}, true
```

Replace the `message_delta` case:

```go
	case "message_delta":
		return llm.StreamEvent{Type: llm.EventMessageStop, StopReason: string(raw.Delta.StopReason), OutputTokens: int(raw.Usage.OutputTokens)}, true
```

(`raw.Message.Usage.InputTokens` and `raw.Usage.OutputTokens` are `int64` on `MessageStreamEventUnion` in `anthropic-sdk-go@v1.51.0` — confirm by building. If a field path differs, find it with: `grep -rn "InputTokens" $(go env GOMODCACHE)/github.com/anthropics/anthropic-sdk-go@v1.51.0/message.go`.)

- [ ] **Step 5: Capture usage in collectStream**

In `source/server/internal/agent/toolloop.go`, `collectStream` consumes `StreamEvent`s and builds `out` (an `llm.ChatResponse`). Make two additions:

1. It already handles the stop event with a line `out.StopReason = ev.StopReason`. **Keep that line** and add directly after it:

```go
			out.OutputTokens = ev.OutputTokens
```

2. The start event (`llm.EventMessageStart`) is currently ignored. Add handling so it captures input tokens:

```go
		case llm.EventMessageStart:
			out.InputTokens = ev.InputTokens
```

(Place this as a new case in the existing `switch ev.Type` — or, if the handling is an if/else chain, add an equivalent branch. Do not remove or alter the `out.StopReason = ev.StopReason` assignment.)

End state: `out.InputTokens` is set from the start event; `out.StopReason` **and** `out.OutputTokens` are both set from the stop event.

- [ ] **Step 6: Run test to verify it passes**

Run: `cd source/server && go test ./internal/agent/ -run TestCollectStream_CapturesUsage -count=1 -v`
Expected: PASS. Then `cd source/server && go build ./...` — clean (confirms the SDK field paths compile).

- [ ] **Step 7: Commit**

```bash
git add source/server/internal/llm/stream.go source/server/internal/llm/anthropic/stream.go source/server/internal/agent/toolloop.go source/server/internal/agent/collectstream_test.go
git commit -m "feat(llm): carry provider token usage through streaming into ChatResponse"
```

---

### Task 2: Expose usage on ToolLoopResult

Track the last LLM call's usage across the loop and return it, so the server can read the final context occupancy.

**Files:**
- Modify: `source/server/internal/agent/toolloop.go` (`ToolLoopResult`, `RunToolLoop`)
- Test: `source/server/internal/agent/toolloop_usage_test.go` (new)

**Interfaces:**
- Consumes: `ChatResponse.InputTokens`/`OutputTokens` (Task 1).
- Produces: `ToolLoopResult.InputTokens int`, `ToolLoopResult.OutputTokens int` — the **last** LLM call's usage.

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/agent/toolloop_usage_test.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/llm"
)

// usageProvider streams scripted block sequences, each with its own usage.
type usageProvider struct {
	scripts [][]llm.Block
	usages  [][2]int // {input, output} per call
	calls   int
}

func (p *usageProvider) Name() string                   { return "usage" }
func (p *usageProvider) Capabilities() llm.Capabilities { return llm.Capabilities{SupportsTools: true} }
func (p *usageProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (p *usageProvider) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	i := p.calls
	p.calls++
	evs := []llm.StreamEvent{{Type: llm.EventMessageStart, InputTokens: p.usages[i][0]}}
	for _, b := range p.scripts[i] {
		switch b.Type {
		case llm.BlockText:
			evs = append(evs, llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: b.Text})
		case llm.BlockToolUse:
			evs = append(evs,
				llm.StreamEvent{Type: llm.EventToolUseStart, ToolUseID: b.ToolUseID, ToolName: b.ToolName},
				llm.StreamEvent{Type: llm.EventToolUseInputDelta, TextDelta: string(b.ToolInput)},
				llm.StreamEvent{Type: llm.EventToolUseStop})
		}
	}
	evs = append(evs, llm.StreamEvent{Type: llm.EventMessageStop, StopReason: "end_turn", OutputTokens: p.usages[i][1]})
	return &fakeReader{events: evs}, nil
}

func TestRunToolLoop_ReturnsLastCallUsage(t *testing.T) {
	prov := &usageProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "LS", ToolInput: json.RawMessage(`{"path":"."}`)}},
			{{Type: llm.BlockText, Text: "done"}},
		},
		usages: [][2]int{{100, 10}, {250, 20}},
	}
	res, err := RunToolLoop(context.Background(), ToolLoopInput{
		Provider: prov, Registry: agenttools.DefaultRegistry(), UserInput: "list",
	})
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if res.InputTokens != 250 || res.OutputTokens != 20 {
		t.Fatalf("usage = (%d,%d), want last call (250,20)", res.InputTokens, res.OutputTokens)
	}
}
```

(`fakeReader` is defined in `collectstream_test.go` from Task 1 — same package.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/agent/ -run TestRunToolLoop_ReturnsLastCallUsage -count=1`
Expected: FAIL — `res.InputTokens`/`OutputTokens` undefined on `ToolLoopResult` (compile error).

- [ ] **Step 3: Add fields to ToolLoopResult**

In `toolloop.go`, add to `ToolLoopResult`:

```go
	InputTokens  int // last LLM call's provider-reported input tokens (context occupancy)
	OutputTokens int // last LLM call's provider-reported output tokens
```

- [ ] **Step 4: Track and return the last call's usage**

In `RunToolLoop`, declare loop-scoped trackers before the `for iter` loop (near `consecutiveErrors := 0`):

```go
	var lastIn, lastOut int
```

Immediately after `collectStream` succeeds (right after the `if err != nil { return ToolLoopResult{}, err }` that follows `resp, err := collectStream(...)`), record usage:

```go
		lastIn, lastOut = resp.InputTokens, resp.OutputTokens
```

Add `InputTokens: lastIn, OutputTokens: lastOut` to every `ToolLoopResult{...}` literal that carries `History` (the no-tool-calls success return; the no-requester abort; the user-denied abort; the 3-consecutive-errors abort; and the max-iterations return). Leave the three bare `return ToolLoopResult{}, err` error returns (StreamChat error, collectStream error, permission-requester error) untouched. Example for the success return:

```go
		if len(toolCalls) == 0 {
			return ToolLoopResult{
				FinalText: finalText, FinalBlocks: resp.Blocks,
				Iterations: iter + 1, History: hist,
				InputTokens: lastIn, OutputTokens: lastOut,
			}, nil
		}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd source/server && go test ./internal/agent/ -run TestRunToolLoop_ReturnsLastCallUsage -count=1 -v`
Expected: PASS (last call's usage 250/20, not the first call's 100/10).

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/agent/toolloop.go source/server/internal/agent/toolloop_usage_test.go
git commit -m "feat(agent): expose last-call token usage on ToolLoopResult"
```

---

### Task 3: Snapshot the meter against the cloud window

Add `agent.RecordContextUsage`, call it from the server after each tool-loop turn with the cloud model, and prove the meter reads real numbers end-to-end.

**Files:**
- Modify: `source/server/internal/agent/agent.go` (new `RecordContextUsage` method)
- Modify: `source/server/internal/server/server.go` (`streamProcessRequestWithToolLoop`, after `RunToolLoop`)
- Modify: `source/server/internal/server/toolloop_persist_test.go` (give `newServerWithStore`'s agent a meter; extend the scripted provider to emit usage)
- Test: `source/server/internal/agent/context_usage_test.go` (new); plus an integration test in `toolloop_persist_test.go`

**Interfaces:**
- Consumes: `ToolLoopResult.InputTokens`/`OutputTokens` (Task 2); `contextmeter` API; `agent.GetContextUsage` (existing reader).
- Produces: `func (a *Agent) RecordContextUsage(convID, model string, inputTokens, outputTokens int)`.

- [ ] **Step 1: Write the failing agent unit test**

Create `source/server/internal/agent/context_usage_test.go`:

```go
package agent

import (
	"testing"

	"cercano/source/server/internal/contextmeter"
)

func TestRecordContextUsage_SnapshotsAgainstModel(t *testing.T) {
	reg := contextmeter.NewRegistry()
	a := &Agent{}
	WithContextMeter(reg, "qwen3-coder")(a) // some local default; the call passes a cloud model explicitly

	a.RecordContextUsage("c1", "claude-opus-4", 1000, 200)

	used, max := a.GetContextUsage("c1")
	if used != 1200 {
		t.Errorf("used = %d, want 1200 (in+out)", used)
	}
	if want := contextmeter.ModelMax("claude-opus-4"); max != want {
		t.Errorf("max = %d, want %d (cloud model window)", max, want)
	}

	// A zero-input update must not clobber the prior reading.
	a.RecordContextUsage("c1", "claude-opus-4", 0, 0)
	used2, _ := a.GetContextUsage("c1")
	if used2 != 1200 {
		t.Errorf("used after zero update = %d, want 1200 (unchanged)", used2)
	}
}
```

(If `WithContextMeter(reg, model)` returns an `AgentOption` you apply as `WithContextMeter(...)(a)`, use that form; if `Agent` fields are unexported but the test is in package `agent`, direct construction `&Agent{}` + option works.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/agent/ -run TestRecordContextUsage -count=1`
Expected: FAIL — `a.RecordContextUsage` undefined.

- [ ] **Step 3: Implement RecordContextUsage**

In `source/server/internal/agent/agent.go` (near `GetContextUsage`):

```go
// RecordContextUsage snapshots a conversation's context-window meter from
// provider-reported token usage, measured against the given model's window
// (typically the cloud model that served the turn). Snapshot semantics:
// reset then set used = inputTokens + outputTokens. No-op when no meter is
// configured or inputTokens <= 0 (a provider that reports no usage must not
// clobber a prior good reading).
func (a *Agent) RecordContextUsage(convID, model string, inputTokens, outputTokens int) {
	if a == nil || a.meter == nil || convID == "" || inputTokens <= 0 {
		return
	}
	c := a.meter.Get(convID, model)
	c.SetMax(contextmeter.ModelMax(model))
	c.Reset()
	c.AddCount(inputTokens)
	c.AddCount(outputTokens)
}
```

Confirm `agent.go` already imports `cercano/source/server/internal/contextmeter` (it does — used by `WithContextMeter`).

- [ ] **Step 4: Run the agent unit test to verify it passes**

Run: `cd source/server && go test ./internal/agent/ -run TestRecordContextUsage -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Write the failing server integration test**

In `toolloop_persist_test.go`, first give the test server's agent a meter. In `newServerWithStore`, change the agent construction to:

```go
	a := agent.NewAgent(&mockRouter{}, &mockCoordinator{},
		agent.WithPersistentStore(store),
		agent.WithContextMeter(contextmeter.NewRegistry(), "test-model"))
```

and add the import `"cercano/source/server/internal/contextmeter"`.

Extend `scriptedProvider` to emit usage: add a field `usage [2]int` to the struct, and in its `StreamChat`, after building events from `blocksToEvents(blocks)`, set usage on the first/last events:

```go
	evs := blocksToEvents(blocks)
	if p.usage[0] != 0 {
		evs[0].InputTokens = p.usage[0]              // EventMessageStart is always events[0]
		evs[len(evs)-1].OutputTokens = p.usage[1]    // EventMessageStop is always the last
	}
	p.calls++
	return &scriptedStream{events: evs}, nil
```

Then the test:

```go
func TestStreamToolLoop_UpdatesContextMeter(t *testing.T) {
	srv, _ := newServerWithStore(t)
	prov := &scriptedProvider{
		scripts: [][]llm.Block{{{Type: llm.BlockText, Text: "hello"}}},
		caps:    llm.Capabilities{SupportsTools: true},
		usage:   [2]int{4321, 99},
	}
	srv.SetCloudLLMProvider(prov)

	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "hi", ConversationId: "conv-meter"},
		&fakeStream{ctx: context.Background()}); err != nil {
		t.Fatalf("streamProcessRequestWithToolLoop: %v", err)
	}

	resp, err := srv.GetContextUsage(context.Background(), &proto.GetContextUsageRequest{ConversationId: "conv-meter"})
	if err != nil {
		t.Fatalf("GetContextUsage: %v", err)
	}
	if resp.TokensUsed != 4321+99 {
		t.Errorf("TokensUsed = %d, want %d", resp.TokensUsed, 4321+99)
	}
	if want := int32(contextmeter.ModelMax("test-model")); resp.ModelMax != want {
		t.Errorf("ModelMax = %d, want %d (cloud window)", resp.ModelMax, want)
	}
}
```

(Confirm `srv.GetContextUsage` is the RPC method and `GetContextUsageResponse` fields are `TokensUsed int32` / `ModelMax int32` per `agent.proto`. If the scriptedProvider already has a `usage`-like field or a different struct shape, adapt the field add accordingly.)

- [ ] **Step 6: Run integration test to verify it fails, then wire the server**

Run: `cd source/server && go test ./internal/server/ -run TestStreamToolLoop_UpdatesContextMeter -count=1`
Expected: FAIL — meter not updated (TokensUsed == 0), because the server doesn't call `RecordContextUsage` yet.

Then in `server.go` `streamProcessRequestWithToolLoop`, immediately after the existing `s.persistToolLoopTurns(ctx, req, result, injectedLen)` call, add:

```go
	s.agent.RecordContextUsage(req.GetConversationId(), s.currentConfig.CloudModel,
		result.InputTokens, result.OutputTokens)
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `cd source/server && go test ./internal/server/ ./internal/agent/ -count=1`
Expected: PASS (new tests green; existing persist/replay tests still green — the scripted-provider `usage` field defaults to `[2]int{0,0}`, so prior tests skip the meter update via the `inputTokens <= 0` guard).

Then full module:

Run: `cd source/server && go build ./... && go test ./... -count=1`
Expected: build clean; all packages PASS.

- [ ] **Step 8: Commit**

```bash
git add source/server/internal/agent/agent.go source/server/internal/agent/context_usage_test.go source/server/internal/server/server.go source/server/internal/server/toolloop_persist_test.go
git commit -m "feat(server): snapshot context meter from provider usage against cloud window"
```

---

## Self-Review

**Spec coverage:**
- §1 capture usage in streaming layer → Task 1.
- §2 surface usage through the loop (last call) → Task 2.
- §3 snapshot meter against cloud window (RecordContextUsage + server call) → Task 3.
- §4 safety: skip-on-zero → Task 3 (`inputTokens <= 0` guard, tested); loop abort records last usage → Task 2 (all History-carrying returns set usage); SetMax repairs local-window counter → Task 3.
- §5 CLI unchanged → no task (intentional).
- §6 testing (stream unit, loop unit last-call, agent unit incl. zero-no-clobber, server integration) → Tasks 1-3.

**Type consistency:** `RecordContextUsage(convID, model string, inputTokens, outputTokens int)`, `ToolLoopResult.InputTokens/OutputTokens`, `StreamEvent.InputTokens/OutputTokens`, `ChatResponse.InputTokens/OutputTokens` are used identically across tasks. `contextmeter` method names (`Get`, `SetMax`, `Reset`, `AddCount`) match `counter.go`.

**Placeholder scan:** no TBD/TODO; every code step shows full code and exact commands. The two "confirm the SDK field path / RPC field names by building" notes are deliberate build-time verifications of names in third-party/generated code, not unresolved requirements.
