# Compaction 2b-3a — Server Data Surface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose compaction to clients: a "compacting" in-flight flag, an extended `GetContextUsage` (sent/raw/compacting), and new `GetCompactionState` + `ExportContext` RPCs — server + agentclient.

**Architecture:** The generator tracks which conversations are mid-compaction; the agent surfaces it (`IsCompacting`). The server computes the honest sent size via `compactor.BuildSendView` and the raw size via `agent.BuildLLMHistory`, and serves the consolidated summary + frozen/live counts and a full-uncapped JSON export. The CLI (2b-3b/c) only renders these.

**Tech Stack:** Go; gRPC/protobuf; the 2b-1 `compactor`/`compaction`/`compactiongen` packages.

## Global Constraints

- All server-side. Clients consume only the RPCs.
- New RPCs change the generated `AgentClient` interface → the mcp `mockAgentClient` (`internal/mcp/server_test.go`) MUST gain stub methods, or the mcp test build breaks (a known prior regression).
- `sent` = `TotalTokens(BuildSendView(turns, state))`; `raw` = `TotalTokens(BuildLLMHistory(turns))`. When no compaction state exists, sent == raw.
- Proto regen (reproduces stubs):
  ```
  export PATH="$PATH:$(go env GOPATH)/bin"
  cd source/proto && protoc \
    --go_out=../server/pkg/proto --go_opt=paths=source_relative \
    --go-grpc_out=../server/pkg/proto --go-grpc_opt=paths=source_relative agent.proto
  ```
- Build + test: `cd source/server && go build ./... && go test ./... -count=1`.
- Commit messages must NOT contain "Claude"; no `Co-Authored-By` trailer.

---

## File Structure

- `source/server/internal/compactiongen/compactiongen.go` — in-flight set + `IsCompacting`.
- `source/server/internal/agent/agent.go` — `CompactionScheduler` gains `IsCompacting`; `(*Agent).IsCompacting`.
- `source/proto/agent.proto` — extend `GetContextUsageResponse`; add `GetCompactionState` + `ExportContext` RPCs/messages.
- `source/server/pkg/proto/*` — regenerated stubs (do not hand-edit).
- `source/server/internal/server/server.go` — extend `GetContextUsage`; add `GetCompactionState`, `ExportContext` handlers.
- `source/server/internal/mcp/server_test.go` — `mockAgentClient` stubs for the 2 new RPCs.
- `source/server/pkg/agentclient/client.go` — `ContextUsage` fields + `CompactionState` type + `GetCompactionState`/`ExportContext` methods.

---

## Task 1: Generator in-flight flag + agent `IsCompacting`

**Files:**
- Modify: `source/server/internal/compactiongen/compactiongen.go`
- Modify: `source/server/internal/agent/agent.go`
- Test: `source/server/internal/compactiongen/compactiongen_test.go`, `source/server/internal/agent/agent_test.go`

**Interfaces:**
- Produces: `(*compactiongen.Generator).IsCompacting(convID string) bool`; `agent.CompactionScheduler` gains `IsCompacting(conversationID string) bool`; `(*Agent).IsCompacting(convID string) bool` (nil-safe → false).

- [ ] **Step 1: Write the failing generator test**

Append to `source/server/internal/compactiongen/compactiongen_test.go`:

```go
func TestIsCompacting_TrueDuringPass(t *testing.T) {
	fs := &fakeStore{turns: bigTurns(12, 1000)}
	release := make(chan struct{})
	entered := make(chan struct{})
	summarize := func(context.Context, []llm.Message) (compaction.StructuredSummary, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release // block so the pass stays in-flight
		return compaction.StructuredSummary{Goal: "g"}, nil
	}
	cfg := compactor.Config{ActivationFloorTokens: 1000, SegmentTokens: 4000, VerbatimRecent: 2}
	g := New(fs, summarize, cfg, contextmeter.Default(), 10*time.Millisecond)

	go func() { _ = g.CompactNow(context.Background(), "c1") }()
	<-entered
	if !g.IsCompacting("c1") {
		t.Error("IsCompacting should be true while a pass runs")
	}
	close(release)
	// Wait for the pass to finish, then it must be false.
	deadline := time.Now().Add(2 * time.Second)
	for g.IsCompacting("c1") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if g.IsCompacting("c1") {
		t.Error("IsCompacting should clear after the pass finishes")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd source/server && go test ./internal/compactiongen/ -run TestIsCompacting -count=1`
Expected: FAIL — `IsCompacting` undefined.

- [ ] **Step 3: Add the in-flight set to the generator**

Add a field to `Generator` (beside `timers`):

```go
	inflight map[string]bool
```

Initialize it in `New` (beside `timers: make(...)`):

```go
		inflight: make(map[string]bool),
```

Wrap the body of `runCompaction` so the flag is set on entry and cleared on exit:

```go
func (g *Generator) runCompaction(ctx context.Context, conversationID string) error {
	g.mu.Lock()
	g.inflight[conversationID] = true
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.inflight, conversationID)
		g.mu.Unlock()
	}()

	turns, err := g.store.GetTurns(ctx, conversationID)
	// ... existing body unchanged ...
}
```

Add the accessor:

```go
// IsCompacting reports whether a compaction pass is currently running for the
// conversation.
func (g *Generator) IsCompacting(conversationID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inflight[conversationID]
}
```

- [ ] **Step 4: Write the failing agent test**

Append to `source/server/internal/agent/agent_test.go` (extend the existing `fakeCompactionScheduler` from the 2b-1b test with an `IsCompacting`):

```go
func (f *fakeCompactionScheduler) IsCompacting(string) bool { return f.compacting }

func TestAgentIsCompacting_NilSafeAndDelegates(t *testing.T) {
	a := NewAgent(nil, nil)
	if a.IsCompacting("c1") {
		t.Error("nil scheduler → IsCompacting false")
	}
	fc := &fakeCompactionScheduler{compacting: true}
	a2 := NewAgent(nil, nil, WithCompactionScheduler(fc))
	if !a2.IsCompacting("c1") {
		t.Error("should delegate to the scheduler")
	}
}
```

Add the `compacting bool` field to the existing `fakeCompactionScheduler` struct.

- [ ] **Step 5: Extend the interface + add the agent method**

In `agent.go`, add to the `CompactionScheduler` interface:

```go
	IsCompacting(conversationID string) bool
```

Add the method (beside `CompactNow`):

```go
// IsCompacting reports whether a compaction pass is currently running. Nil-safe.
func (a *Agent) IsCompacting(conversationID string) bool {
	if a.compaction != nil {
		return a.compaction.IsCompacting(conversationID)
	}
	return false
}
```

- [ ] **Step 6: Run both tests + full build**

Run: `cd source/server && go test ./internal/compactiongen/ ./internal/agent/ -run 'IsCompacting' -count=1`
Expected: PASS.
Run: `cd source/server && go build ./... && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd source/server
git add internal/compactiongen/compactiongen.go internal/compactiongen/compactiongen_test.go internal/agent/agent.go internal/agent/agent_test.go
git commit -m "feat(server): compaction in-flight flag (generator IsCompacting + agent delegate)"
```

---

## Task 2: Proto + server handlers (usage extension, state, export)

**Files:**
- Modify: `source/proto/agent.proto`
- Regenerate: `source/server/pkg/proto/*` (command above)
- Modify: `source/server/internal/server/server.go`
- Modify: `source/server/internal/mcp/server_test.go` (`mockAgentClient` stubs)
- Test: `source/server/internal/server/compaction_rpc_test.go`

**Interfaces:**
- Produces: `GetContextUsageResponse` with `raw_tokens` + `compacting`; RPCs `GetCompactionState` and `ExportContext` with their messages; server handlers for all three.

- [ ] **Step 1: Edit the proto**

In `source/proto/agent.proto`, extend the response:

```proto
message GetContextUsageResponse {
  int32  tokens_used = 1; // the SENT (compacted) size
  int32  model_max   = 2;
  double percent     = 3;
  int32  raw_tokens  = 4; // the uncompacted size
  bool   compacting  = 5; // a compaction pass is currently running
}
```

Add to `service Agent` (near the other context RPCs):

```proto
  rpc GetCompactionState (GetCompactionStateRequest) returns (GetCompactionStateResponse) {}
  rpc ExportContext (ExportContextRequest) returns (ExportContextResponse) {}
```

Add the messages:

```proto
message GetCompactionStateRequest  { string conversation_id = 1; }
message GetCompactionStateResponse {
  int64  frozen_through       = 1;
  int32  frozen_turns         = 2;
  int32  live_turns           = 3;
  int32  compacted_segments   = 4;
  int32  raw_tokens           = 5;
  int32  sent_tokens          = 6;
  string consolidated_summary = 7;
  bool   compacting           = 8;
}
message ExportContextRequest  { string conversation_id = 1; }
message ExportContextResponse { string json = 1; }
```

- [ ] **Step 2: Regenerate the stubs**

Run the proto regen command (Global Constraints). Confirm `git status` shows only `source/server/pkg/proto/agent*.go` changed (regenerated), nothing hand-edited.

- [ ] **Step 3: Add the mcp mock stubs (build will fail until you do)**

In `source/server/internal/mcp/server_test.go`, add to `mockAgentClient` (matching the generated `AgentClient` method signatures — copy the exact param/return types `protoc` produced):

```go
func (m *mockAgentClient) GetCompactionState(ctx context.Context, in *proto.GetCompactionStateRequest, opts ...grpc.CallOption) (*proto.GetCompactionStateResponse, error) {
	return &proto.GetCompactionStateResponse{}, nil
}
func (m *mockAgentClient) ExportContext(ctx context.Context, in *proto.ExportContextRequest, opts ...grpc.CallOption) (*proto.ExportContextResponse, error) {
	return &proto.ExportContextResponse{}, nil
}
```

- [ ] **Step 4: Write the failing handler test**

Create `source/server/internal/server/compaction_rpc_test.go`. Use the existing server-test harness (mirror `toolloop_persist_test.go`'s `newServerWithStore`). Seed a conversation with enough turns to compact, run a compaction pass (via the store + `compactor.Advance`, or by saving a `Compaction` row directly), then assert the RPCs. Minimal version asserting the no-compaction baseline + a state row:

```go
func TestExportContext_RoundTripsToMessages(t *testing.T) {
	s, store := newServerWithStore(t)
	ctx := context.Background()
	_ = store.EnsureConversation(ctx, "c1", "/p", "m")
	_ = store.Append(ctx, conversation.Turn{ConversationID: "c1", Role: "user", Content: "hello world"})

	resp, err := s.ExportContext(ctx, &proto.ExportContextRequest{ConversationId: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	var msgs []llm.Message
	if err := json.Unmarshal([]byte(resp.GetJson()), &msgs); err != nil {
		t.Fatalf("export is not valid []llm.Message JSON: %v", err)
	}
	if len(msgs) == 0 {
		t.Error("export should contain the turn")
	}
}

func TestGetCompactionState_NoStateIsEmpty(t *testing.T) {
	s, store := newServerWithStore(t)
	ctx := context.Background()
	_ = store.EnsureConversation(ctx, "c1", "/p", "m")
	resp, err := s.GetCompactionState(ctx, &proto.GetCompactionStateRequest{ConversationId: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetFrozenTurns() != 0 || resp.GetConsolidatedSummary() != "" {
		t.Errorf("no compaction → empty state, got %+v", resp)
	}
}
```

- [ ] **Step 5: Run to verify failure**

Run: `cd source/server && go test ./internal/server/ -run 'TestExportContext|TestGetCompactionState' -count=1`
Expected: FAIL — handlers undefined.

- [ ] **Step 6: Extend `GetContextUsage` + add the two handlers**

In `server.go`, replace the `GetContextUsage` body to compute sent/raw/compacting:

```go
func (s *Server) GetContextUsage(ctx context.Context, req *proto.GetContextUsageRequest) (*proto.GetContextUsageResponse, error) {
	convID := req.GetConversationId()
	_, max := s.agent.GetContextUsage(ctx, convID)
	sent, raw := 0, 0
	if store := s.agent.PersistentStore(); store != nil && convID != "" {
		if turns, err := store.GetTurns(ctx, convID); err == nil {
			state, _ := store.GetCompaction(ctx, convID)
			tok := contextmeter.Default()
			view, _ := compactor.BuildSendView(turns, state)
			sent = compaction.TotalTokens(tok, view)
			raw = compaction.TotalTokens(tok, agent.BuildLLMHistory(turns))
		}
	}
	var pct float64
	if max > 0 {
		pct = float64(sent) / float64(max)
		if pct > 1 {
			pct = 1
		}
	}
	return &proto.GetContextUsageResponse{
		TokensUsed: int32(sent), ModelMax: int32(max), Percent: pct,
		RawTokens: int32(raw), Compacting: s.agent.IsCompacting(convID),
	}, nil
}
```

Add the new handlers:

```go
// GetCompactionState implements proto.AgentServer — the compaction summary +
// frozen/live split for the /c viewer.
func (s *Server) GetCompactionState(ctx context.Context, req *proto.GetCompactionStateRequest) (*proto.GetCompactionStateResponse, error) {
	convID := req.GetConversationId()
	out := &proto.GetCompactionStateResponse{Compacting: s.agent.IsCompacting(convID)}
	store := s.agent.PersistentStore()
	if store == nil || convID == "" {
		return out, nil
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		return out, nil
	}
	state, _ := store.GetCompaction(ctx, convID)
	tok := contextmeter.Default()
	view, _ := compactor.BuildSendView(turns, state)
	out.SentTokens = int32(compaction.TotalTokens(tok, view))
	out.RawTokens = int32(compaction.TotalTokens(tok, agent.BuildLLMHistory(turns)))
	out.FrozenThrough = state.FrozenThrough
	for _, t := range turns {
		if t.CreatedAt.Unix() <= state.FrozenThrough {
			out.FrozenTurns++
		} else {
			out.LiveTurns++
		}
	}
	if state.SegmentSummariesJSON != "" {
		var segs []compaction.StructuredSummary
		if json.Unmarshal([]byte(state.SegmentSummariesJSON), &segs) == nil {
			out.CompactedSegments = int32(len(segs))
		}
	}
	if state.ConsolidatedJSON != "" {
		var cs compaction.StructuredSummary
		if json.Unmarshal([]byte(state.ConsolidatedJSON), &cs) == nil {
			out.ConsolidatedSummary = cs.RenderBlock().Text
		}
	}
	return out, nil
}

// ExportContext implements proto.AgentServer — the full uncapped raw history as
// a JSON []llm.Message.
func (s *Server) ExportContext(ctx context.Context, req *proto.ExportContextRequest) (*proto.ExportContextResponse, error) {
	store := s.agent.PersistentStore()
	if store == nil || req.GetConversationId() == "" {
		return &proto.ExportContextResponse{}, nil
	}
	turns, err := store.GetTurns(ctx, req.GetConversationId())
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(agent.BuildLLMHistory(turns))
	if err != nil {
		return nil, err
	}
	return &proto.ExportContextResponse{Json: string(b)}, nil
}
```

(Imports to confirm in `server.go`: `encoding/json`, `cercano/source/server/internal/compactor`, `.../internal/compaction`, `.../internal/contextmeter`, `.../internal/agent` — `agent` and `conversation` are already imported.)

- [ ] **Step 7: Run the handler tests + full build/suite**

Run: `cd source/server && go test ./internal/server/ -run 'TestExportContext|TestGetCompactionState' -count=1`
Expected: PASS.
Run: `cd source/server && go build ./... && go test ./... -count=1`
Expected: PASS (the mcp mock now satisfies the widened `AgentClient`).

- [ ] **Step 8: Commit**

```bash
cd source/server
git add ../proto/agent.proto pkg/proto/ internal/server/server.go internal/server/compaction_rpc_test.go internal/mcp/server_test.go
git commit -m "feat(server): GetContextUsage sent/raw/compacting + GetCompactionState + ExportContext RPCs"
```

---

## Task 3: agentclient methods

**Files:**
- Modify: `source/server/pkg/agentclient/client.go`
- Test: `source/server/pkg/agentclient/` (no live test harness; this task is a thin wrapper — covered by the CLI consuming it in 2b-3b/c. A compile + the existing build is the gate.)

**Interfaces:**
- Produces: `ContextUsage` gains `RawTokens int` + `Compacting bool`; `CompactionState{ FrozenThrough int64; FrozenTurns, LiveTurns, CompactedSegments, RawTokens, SentTokens int; ConsolidatedSummary string; Compacting bool }`; `(*Client).GetCompactionState(ctx, convID) (*CompactionState, error)`; `(*Client).ExportContext(ctx, convID) (string, error)`.

- [ ] **Step 1: Extend `ContextUsage` + map the new fields**

In `client.go`, add to `ContextUsage`:

```go
	RawTokens  int
	Compacting bool
```

In `GetContextUsage`, map them:

```go
	return &ContextUsage{
		TokensUsed: int(resp.GetTokensUsed()),
		ModelMax:   int(resp.GetModelMax()),
		Percent:    resp.GetPercent(),
		RawTokens:  int(resp.GetRawTokens()),
		Compacting: resp.GetCompacting(),
	}, nil
```

- [ ] **Step 2: Add `CompactionState` + the two methods**

```go
// CompactionState mirrors GetCompactionStateResponse for the /c viewer.
type CompactionState struct {
	FrozenThrough       int64
	FrozenTurns         int
	LiveTurns           int
	CompactedSegments   int
	RawTokens           int
	SentTokens          int
	ConsolidatedSummary string
	Compacting          bool
}

func (c *Client) GetCompactionState(ctx context.Context, conversationID string) (*CompactionState, error) {
	resp, err := c.agent.GetCompactionState(ctx, &proto.GetCompactionStateRequest{ConversationId: conversationID})
	if err != nil {
		return nil, err
	}
	return &CompactionState{
		FrozenThrough:       resp.GetFrozenThrough(),
		FrozenTurns:         int(resp.GetFrozenTurns()),
		LiveTurns:           int(resp.GetLiveTurns()),
		CompactedSegments:   int(resp.GetCompactedSegments()),
		RawTokens:           int(resp.GetRawTokens()),
		SentTokens:          int(resp.GetSentTokens()),
		ConsolidatedSummary: resp.GetConsolidatedSummary(),
		Compacting:          resp.GetCompacting(),
	}, nil
}

// ExportContext returns the full uncapped raw history as a JSON []llm.Message.
func (c *Client) ExportContext(ctx context.Context, conversationID string) (string, error) {
	resp, err := c.agent.ExportContext(ctx, &proto.ExportContextRequest{ConversationId: conversationID})
	if err != nil {
		return "", err
	}
	return resp.GetJson(), nil
}
```

- [ ] **Step 3: Build + full suite**

Run: `cd source/server && go build ./... && go test ./... -count=1`
Expected: PASS. Also build the CLI module to confirm the shared package still compiles: `cd source/clients/cli && go build ./...`.

- [ ] **Step 4: Commit**

```bash
cd source/server
git add pkg/agentclient/client.go
git commit -m "feat(server): agentclient ContextUsage raw/compacting + GetCompactionState/ExportContext"
```

---

## Self-Review

**Spec coverage** (against `compaction-2b3-visibility-design.md` §3 + the compacting flag):
- compacting in-flight flag (generator + agent) → Task 1. ✓
- `GetContextUsage` sent/raw/compacting → Task 2 + Task 3. ✓
- `GetCompactionState` (frozen/live counts, summary, sizes) → Task 2 + Task 3. ✓
- `ExportContext` (full uncapped JSON) → Task 2 + Task 3. ✓
- Layering (server computes, agentclient wraps) → all tasks server-side. ✓
- mcp mock updated for new RPCs → Task 2 Step 3. ✓

**Deferred to 2b-3b/c:** the footer rendering (sent/raw/savings + compacting animation) and the `/c` rendering (summary block, frozen/live split, original toggle, export keybind). This plan delivers the data they consume.

**Placeholder scan:** none — every step has complete code/commands. (Task 2 Step 3 says "copy the exact signatures protoc produced" — concrete instruction, since generated signatures must match exactly.)

**Type consistency:** `GetCompactionStateResponse` fields (Task 2 proto) map 1:1 to `CompactionState` (Task 3). `RawTokens`/`Compacting` added to `ContextUsage` (Task 3) match the proto fields (Task 2). `IsCompacting` signature identical across generator (Task 1), `CompactionScheduler` (Task 1), `*Agent` (Task 1), and the server's `s.agent.IsCompacting` calls (Task 2).
