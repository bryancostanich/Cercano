# Context Management: History Replay (Foundation) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the modern tool-loop chat path conversational — persist each turn's full message stream losslessly and replay it into the model every turn.

**Architecture:** Add a deterministic turn ordering tie-break; persist the loop's *new* messages (delta) faithfully for all roles including `tool_result`; reconstruct a window-valid `[]llm.Message` from stored turns with `tool_use`↔`tool_result` pairing repair; inject it as `ConvHistory` before `RunToolLoop`.

**Tech Stack:** Go (module `cercano/source/server`), SQLite (`modernc.org/sqlite`, pure Go), internal `llm` block/message types, `agent.RunToolLoop`.

## Global Constraints

- All work is in the `source/server` Go module. Build: `cd source/server && go build ./...`. Test: `cd source/server && go test ./... -count=1`.
- **No bounding/windowing.** Replay everything (spec option A); compaction (a later feature) owns bounding.
- **Faithful structured replay:** preserve text *and* tool blocks in order; the reconstructed array must always be valid to send (every `tool_use` has a following `tool_result`; no orphan `tool_result`).
- **Persist the delta, never the whole array.** `result.History` includes the injected `ConvHistory` prefix; persisting all of it each turn duplicates prior turns.
- **Best-effort, never fail the turn:** history load/persist errors log to stderr and degrade (empty history / skip), exactly like the existing persist path.
- **Commit messages must not contain the word "Claude" anywhere** (project rule). Do not add Co-Authored-By trailers naming Claude.

Reference types (already defined, do not redefine):

```go
// internal/llm/messages.go
type BlockType string
const ( BlockText BlockType = "text"; BlockToolUse BlockType = "tool_use"; BlockToolResult BlockType = "tool_result" )
type Role string
const ( RoleUser Role = "user"; RoleAssistant Role = "assistant"; RoleSystem Role = "system" )
type Block struct {
    Type       BlockType       `json:"type"`
    Text       string          `json:"text,omitempty"`
    ToolUseID  string          `json:"id,omitempty"`          // set on tool_use blocks
    ToolName   string          `json:"name,omitempty"`
    ToolInput  json.RawMessage `json:"input,omitempty"`
    ToolUseRef string          `json:"tool_use_id,omitempty"` // set on tool_result blocks; refers to a tool_use's ToolUseID
    Content    string          `json:"content,omitempty"`
    IsError    bool            `json:"is_error,omitempty"`
}
type Message struct { Role Role `json:"role"`; Blocks []Block `json:"content"` }

// internal/conversation/store.go
type Turn struct {
    ID, ConversationID, Role, Content, BlocksJSON string
    TokensIn, TokensOut, LatencyMs int
    CreatedAt time.Time
}
// Store: Append(ctx, Turn) error; GetTurns(ctx, convID) ([]Turn, error); EnsureConversation(ctx, id, dir, model) error; Open(path) (Store, error)

// internal/agent/toolloop.go
type ToolLoopInput  struct { ConvHistory []llm.Message; UserInput string; /* ... */ }
type ToolLoopResult struct { FinalText string; Iterations int; History []llm.Message }
// RunToolLoop seeds hist from in.ConvHistory then appends; result.History == ConvHistory ++ new messages.
```

---

### Task 1: Deterministic turn ordering

`created_at` is unix **seconds** (`store.go:211/231`). A single tool-loop turn appends several messages within one second; `GetTurns` orders `ORDER BY created_at ASC` (`store.go:326`) with no tie-break, so SQLite may return same-second turns in an unspecified order — scrambling `tool_use`↔`tool_result` adjacency. Add a stable tie-break on the implicit `rowid` (monotonic with insertion order; the table has a `TEXT PRIMARY KEY`, so `rowid` is not aliased and is safe to use).

**Files:**
- Modify: `source/server/internal/conversation/store.go` (the `GetTurns` query, ~line 326)
- Test: `source/server/internal/conversation/store_test.go`

**Interfaces:**
- Consumes: `Open(":memory:")`, `EnsureConversation`, `Append`, `GetTurns` (existing).
- Produces: `GetTurns` returns same-second turns in insertion order.

- [ ] **Step 1: Write the failing test**

Add to `store_test.go`:

```go
func TestGetTurns_SameSecondPreservesInsertionOrder(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil { t.Fatalf("Open: %v", err) }
	defer s.Close()
	ctx := context.Background()
	if err := s.EnsureConversation(ctx, "c1", "", "m"); err != nil { t.Fatalf("Ensure: %v", err) }

	ts := time.Unix(1_700_000_000, 0) // identical timestamp for all three
	for _, c := range []string{"a", "b", "c"} {
		if err := s.Append(ctx, Turn{ConversationID: "c1", Role: "assistant", Content: c, CreatedAt: ts}); err != nil {
			t.Fatalf("Append %s: %v", c, err)
		}
	}
	turns, err := s.GetTurns(ctx, "c1")
	if err != nil { t.Fatalf("GetTurns: %v", err) }
	got := []string{}
	for _, tn := range turns { got = append(got, tn.Content) }
	if strings.Join(got, "") != "abc" {
		t.Fatalf("order not preserved: got %v, want [a b c]", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/conversation/ -run TestGetTurns_SameSecond -count=1 -v`
Expected: usually PASS by luck (SQLite often returns insertion order) — but it is unspecified. To prove the tie-break is real, temporarily change the query to `ORDER BY created_at ASC, content DESC` and confirm the test FAILS (`got [c b a]`). Revert that probe before Step 3.

- [ ] **Step 3: Add the tie-break to the query**

In `store.go` `GetTurns`, change:

```go
		ORDER BY created_at ASC`, conversationID)
```

to:

```go
		ORDER BY created_at ASC, rowid ASC`, conversationID)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/conversation/ -count=1`
Expected: PASS (all conversation tests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/conversation/store.go source/server/internal/conversation/store_test.go
git commit -m "fix(conversation): stable GetTurns ordering via rowid tie-break"
```

---

### Task 2: `BuildLLMHistory` reconstruction (pure function)

A pure converter from stored turns to a window-valid `[]llm.Message`. Lives in `agent` (already imports both `conversation` and `llm`; keeps `conversation` free of an `llm` dependency).

**Files:**
- Create: `source/server/internal/agent/history.go`
- Test: `source/server/internal/agent/history_test.go`

**Interfaces:**
- Consumes: `conversation.Turn`, `llm.Message`, `llm.Block`.
- Produces: `func BuildLLMHistory(turns []conversation.Turn) []llm.Message` — order-preserving; drops any `tool_use` with no following `tool_result` and any `tool_result` with no matching `tool_use`; drops messages left with no blocks.

- [ ] **Step 1: Write the failing tests**

Create `source/server/internal/agent/history_test.go`:

```go
package agent

import (
	"encoding/json"
	"testing"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

func blocksJSON(t *testing.T, bs []llm.Block) string {
	t.Helper()
	b, err := json.Marshal(bs)
	if err != nil { t.Fatalf("marshal: %v", err) }
	return string(b)
}

func TestBuildLLMHistory_TextOnly(t *testing.T) {
	turns := []conversation.Turn{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "yo"},
	}
	got := BuildLLMHistory(turns)
	if len(got) != 2 { t.Fatalf("len = %d, want 2", len(got)) }
	if got[0].Role != llm.RoleUser || got[0].Blocks[0].Text != "hi" { t.Errorf("turn0 = %+v", got[0]) }
	if got[1].Role != llm.RoleAssistant || got[1].Blocks[0].Text != "yo" { t.Errorf("turn1 = %+v", got[1]) }
}

func TestBuildLLMHistory_ToolRoundTripPreserved(t *testing.T) {
	useBlocks := []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "LS", ToolInput: json.RawMessage(`{}`)}}
	resBlocks := []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "u1", Content: "ok"}}
	turns := []conversation.Turn{
		{Role: "user", Content: "list"},
		{Role: "assistant", BlocksJSON: blocksJSON(t, useBlocks)},
		{Role: "user", BlocksJSON: blocksJSON(t, resBlocks)},
		{Role: "assistant", Content: "done"},
	}
	got := BuildLLMHistory(turns)
	if len(got) != 4 { t.Fatalf("len = %d, want 4", len(got)) }
	if got[1].Blocks[0].Type != llm.BlockToolUse || got[1].Blocks[0].ToolUseID != "u1" { t.Errorf("tool_use lost: %+v", got[1]) }
	if got[2].Blocks[0].Type != llm.BlockToolResult || got[2].Blocks[0].ToolUseRef != "u1" { t.Errorf("tool_result lost: %+v", got[2]) }
}

func TestBuildLLMHistory_OrphanToolUseStripped(t *testing.T) {
	useBlocks := []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "LS"}}
	turns := []conversation.Turn{
		{Role: "user", Content: "list"},
		{Role: "assistant", BlocksJSON: blocksJSON(t, useBlocks)}, // no following tool_result (legacy lossy data)
	}
	got := BuildLLMHistory(turns)
	if len(got) != 1 { t.Fatalf("len = %d, want 1 (orphan tool_use message dropped)", len(got)) }
	if got[0].Blocks[0].Text != "list" { t.Errorf("kept wrong message: %+v", got[0]) }
}

func TestBuildLLMHistory_OrphanToolResultStripped(t *testing.T) {
	resBlocks := []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "ghost", Content: "x"}}
	turns := []conversation.Turn{
		{Role: "user", Content: "hi"},
		{Role: "user", BlocksJSON: blocksJSON(t, resBlocks)}, // no matching tool_use
	}
	got := BuildLLMHistory(turns)
	if len(got) != 1 { t.Fatalf("len = %d, want 1 (orphan tool_result dropped)", len(got)) }
	if got[0].Blocks[0].Text != "hi" { t.Errorf("kept wrong message: %+v", got[0]) }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd source/server && go test ./internal/agent/ -run TestBuildLLMHistory -count=1`
Expected: FAIL — `undefined: BuildLLMHistory`.

- [ ] **Step 3: Implement `BuildLLMHistory`**

Create `source/server/internal/agent/history.go`:

```go
package agent

import (
	"encoding/json"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

// BuildLLMHistory reconstructs a window-valid llm.Message slice from stored
// turns. It is pure (no I/O) and order-preserving. To keep the result always
// safe to send to the provider, it drops any tool_use block with no following
// tool_result and any tool_result block with no matching tool_use (e.g. legacy
// data persisted before tool_result turns were saved), then drops any message
// left with no blocks.
func BuildLLMHistory(turns []conversation.Turn) []llm.Message {
	msgs := make([]llm.Message, 0, len(turns))
	for _, t := range turns {
		role := llm.RoleUser
		switch t.Role {
		case string(llm.RoleAssistant):
			role = llm.RoleAssistant
		case string(llm.RoleSystem):
			role = llm.RoleSystem
		}
		var blocks []llm.Block
		if t.BlocksJSON != "" {
			if err := json.Unmarshal([]byte(t.BlocksJSON), &blocks); err != nil {
				blocks = nil
			}
		}
		if len(blocks) == 0 {
			if t.Content == "" {
				continue
			}
			blocks = []llm.Block{{Type: llm.BlockText, Text: t.Content}}
		}
		msgs = append(msgs, llm.Message{Role: role, Blocks: blocks})
	}
	return repairPairing(msgs)
}

// repairPairing removes orphaned tool_use / tool_result blocks so the array is
// always valid to send. tool_use is kept only if some tool_result references
// its id; tool_result is kept only if some tool_use declares its ref.
func repairPairing(msgs []llm.Message) []llm.Message {
	resulted := map[string]bool{} // tool_use ids that have a matching tool_result
	used := map[string]bool{}     // tool_use ids that were declared
	for _, m := range msgs {
		for _, b := range m.Blocks {
			switch b.Type {
			case llm.BlockToolUse:
				used[b.ToolUseID] = true
			case llm.BlockToolResult:
				resulted[b.ToolUseRef] = true
			}
		}
	}
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		kept := make([]llm.Block, 0, len(m.Blocks))
		for _, b := range m.Blocks {
			switch b.Type {
			case llm.BlockToolUse:
				if !resulted[b.ToolUseID] {
					continue
				}
			case llm.BlockToolResult:
				if !used[b.ToolUseRef] {
					continue
				}
			}
			kept = append(kept, b)
		}
		if len(kept) == 0 {
			continue
		}
		out = append(out, llm.Message{Role: m.Role, Blocks: kept})
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd source/server && go test ./internal/agent/ -run TestBuildLLMHistory -count=1 -v`
Expected: PASS (all four).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/agent/history.go source/server/internal/agent/history_test.go
git commit -m "feat(agent): BuildLLMHistory reconstructs window-valid history with pairing repair"
```

---

### Task 3: Lossless delta persistence

Rewrite `persistToolLoopTurns` to persist the loop's **new** messages (`result.History[injectedLen:]`) faithfully — every role, with `BlocksJSON` and concatenated `Content`. This drops the old behavior (separate `req.Input` user append + assistant-only filter) and starts saving the user-role `tool_result` messages. The caller passes `injectedLen` (0 until Task 4 wires injection).

**Files:**
- Modify: `source/server/internal/server/server.go` (`persistToolLoopTurns` ~857; its call site ~838)
- Test: `source/server/internal/server/toolloop_persist_test.go` (update existing expectations + add a delta test)

**Interfaces:**
- Consumes: `agent.ToolLoopResult.History`, `conversation.Store.Append`, `llm.Block`.
- Produces: `func (s *Server) persistToolLoopTurns(ctx context.Context, req *proto.ProcessRequestRequest, result agent.ToolLoopResult, injectedLen int)` — appends only `result.History[injectedLen:]`, one turn per message, all roles.

- [ ] **Step 1: Update existing tests + add the delta test**

In `toolloop_persist_test.go`, update `TestStreamToolLoop_PersistsMultiTurnHistory` to expect the now-persisted `tool_result` turn (4 turns: user, assistant tool_use, user tool_result, assistant text):

```go
	// 1 user + assistant tool_use + user tool_result + assistant text.
	if len(turns) != 4 {
		t.Fatalf("expected 4 turns, got %d", len(turns))
	}
	if turns[0].Role != "user" {
		t.Errorf("turn 0 role: %q", turns[0].Role)
	}
	if turns[1].Role != "assistant" || turns[1].BlocksJSON == "" {
		t.Errorf("turn 1 should be assistant with blocks, got role=%q", turns[1].Role)
	}
	if turns[2].Role != "user" || turns[2].BlocksJSON == "" {
		t.Errorf("turn 2 should be the user tool_result turn, got role=%q blocks=%q", turns[2].Role, turns[2].BlocksJSON)
	}
	if turns[3].Role != "assistant" || turns[3].Content != "All done." {
		t.Errorf("turn 3: role=%q content=%q", turns[3].Role, turns[3].Content)
	}
```

Add a focused delta test (persist only the tail past `injectedLen`):

```go
func TestPersistToolLoopTurns_DeltaOnly(t *testing.T) {
	srv, store := newServerWithStore(t)
	ctx := context.Background()
	req := &proto.ProcessRequestRequest{Input: "second", ConversationId: "conv-delta"}

	// Simulate a result whose History carries a 2-message injected prefix plus
	// 2 new messages. Only the 2 new ones should be persisted.
	result := agent.ToolLoopResult{History: []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "old-user"}}},      // injected
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "old-asst"}}}, // injected
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "second"}}},        // new
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "reply"}}},    // new
	}}

	srv.persistToolLoopTurns(ctx, req, result, 2)

	turns, err := store.GetTurns(ctx, "conv-delta")
	if err != nil { t.Fatalf("GetTurns: %v", err) }
	if len(turns) != 2 {
		t.Fatalf("expected 2 persisted (delta only), got %d", len(turns))
	}
	if turns[0].Content != "second" || turns[1].Content != "reply" {
		t.Errorf("unexpected delta turns: %q, %q", turns[0].Content, turns[1].Content)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd source/server && go test ./internal/server/ -run 'TestStreamToolLoop_PersistsMultiTurnHistory|TestPersistToolLoopTurns_DeltaOnly' -count=1`
Expected: FAIL — multi-turn still produces 3 turns (old lossy behavior); delta test fails to compile (signature has no `injectedLen`).

- [ ] **Step 3: Rewrite `persistToolLoopTurns` + its call site**

Replace the body of `persistToolLoopTurns` (server.go ~857) with:

```go
func (s *Server) persistToolLoopTurns(ctx context.Context, req *proto.ProcessRequestRequest, result agent.ToolLoopResult, injectedLen int) {
	if s.agent == nil {
		return
	}
	store := s.agent.PersistentStore()
	convID := req.GetConversationId()
	if store == nil || convID == "" {
		return
	}
	if err := store.EnsureConversation(ctx, convID, req.GetWorkDir(), s.currentConfig.CloudModel); err != nil {
		fmt.Fprintf(os.Stderr, "[tool-loop] EnsureConversation(%s) failed: %v\n", convID, err)
		return
	}
	// Persist only the messages added this turn — result.History begins with the
	// injected ConvHistory prefix, which is already stored. Clamp defensively.
	if injectedLen < 0 || injectedLen > len(result.History) {
		injectedLen = 0
	}
	for _, m := range result.History[injectedLen:] {
		blocksJSON, err := json.Marshal(m.Blocks)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[tool-loop] marshal blocks failed: %v\n", err)
			continue
		}
		var text string
		for _, b := range m.Blocks {
			if b.Type == llm.BlockText {
				text += b.Text
			}
		}
		role := string(m.Role)
		if err := store.Append(ctx, conversation.Turn{
			ConversationID: convID,
			Role:           role,
			Content:        text,
			BlocksJSON:     string(blocksJSON),
		}); err != nil {
			fmt.Fprintf(os.Stderr, "[tool-loop] Append(%s, %s) failed: %v\n", role, convID, err)
		}
	}
}
```

Update the call site (server.go ~838) to pass `0` for now:

```go
	s.persistToolLoopTurns(ctx, req, result, 0)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd source/server && go test ./internal/server/ -count=1`
Expected: PASS — including the updated multi-turn test (4 turns) and the delta test. `TestStreamToolLoop_PersistsTurns` (no tools) still passes: user + assistant = 2 turns.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/server/server.go source/server/internal/server/toolloop_persist_test.go
git commit -m "feat(server): persist full tool-loop message delta losslessly"
```

---

### Task 4: Inject `ConvHistory` from the store

Load prior turns and replay them into `RunToolLoop` each turn, and pass the injected length to the persist step so only the delta is saved.

**Files:**
- Modify: `source/server/internal/server/server.go` (`streamProcessRequestWithToolLoop`, around the `RunToolLoop` call ~825 and the persist call ~838)
- Test: `source/server/internal/server/toolloop_persist_test.go`

**Interfaces:**
- Consumes: `agent.BuildLLMHistory` (Task 2), `conversation.Store.GetTurns`, `agent.ToolLoopInput.ConvHistory`, `persistToolLoopTurns(..., injectedLen)` (Task 3).
- Produces: second and later turns in a conversation observe non-empty `ConvHistory`; turn count grows linearly across turns (no duplication).

- [ ] **Step 1: Add provider-input capture, then write the failing integration test**

First, make `scriptedProvider` record the messages it receives each call, so the test can assert that turn 2 actually carries prior history. In `scriptedProvider.StreamChat` (toolloop_persist_test.go), add a capture line, and add the `seen` field to the struct:

```go
// struct: add field
type scriptedProvider struct {
	scripts [][]llm.Block
	calls   int
	caps    llm.Capabilities
	seen    [][]llm.Message // req.Messages captured per StreamChat call
}

// in StreamChat, before incrementing p.calls:
	p.seen = append(p.seen, req.Messages)
```

Then add the test:

```go
func TestStreamToolLoop_ReplaysHistoryNoDuplication(t *testing.T) {
	srv, store := newServerWithStore(t)
	prov := &scriptedProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockText, Text: "one"}},
			{{Type: llm.BlockText, Text: "two"}},
		},
		caps: llm.Capabilities{SupportsTools: true},
	}
	srv.SetCloudLLMProvider(prov)

	// Turn 1.
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "first", ConversationId: "conv-r"},
		&fakeStream{ctx: context.Background()}); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	// Turn 2.
	if err := srv.streamProcessRequestWithToolLoop(
		&proto.ProcessRequestRequest{Input: "second", ConversationId: "conv-r"},
		&fakeStream{ctx: context.Background()}); err != nil {
		t.Fatalf("turn 2: %v", err)
	}

	// (a) Replay: the provider's turn-2 input must include the prior turn
	// (user "first", assistant "one") ahead of the new user "second".
	if len(prov.seen) != 2 {
		t.Fatalf("expected 2 provider calls, got %d", len(prov.seen))
	}
	turn2 := prov.seen[1]
	if len(turn2) != 3 {
		t.Fatalf("turn-2 provider input = %d messages, want 3 (first, one, second)", len(turn2))
	}
	if turn2[0].Blocks[0].Text != "first" || turn2[1].Blocks[0].Text != "one" || turn2[2].Blocks[0].Text != "second" {
		t.Errorf("turn-2 history wrong: %q / %q / %q",
			turn2[0].Blocks[0].Text, turn2[1].Blocks[0].Text, turn2[2].Blocks[0].Text)
	}

	// (b) No duplication: 2 messages per turn × 2 turns = 4, linear.
	turns, err := store.GetTurns(context.Background(), "conv-r")
	if err != nil { t.Fatalf("GetTurns: %v", err) }
	if len(turns) != 4 {
		t.Fatalf("expected 4 turns (linear growth, no re-save), got %d", len(turns))
	}
	want := []string{"first", "one", "second", "two"}
	for i, w := range want {
		if turns[i].Content != w {
			t.Errorf("turn %d = %q, want %q", i, turns[i].Content, w)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/server/ -run TestStreamToolLoop_ReplaysHistoryNoDuplication -count=1`
Expected: FAIL at assertion (a) — turn-2 provider input has **1** message (`second`) because no history is injected yet. (The turn-count check (b) would pass at baseline; (a) is the real red.)

- [ ] **Step 3: Wire injection + injectedLen**

In `streamProcessRequestWithToolLoop`, immediately before the `agent.RunToolLoop(...)` call (server.go ~825), build the history:

```go
	var convHistory []llm.Message
	if store := s.agent.PersistentStore(); store != nil && req.GetConversationId() != "" {
		if turns, err := store.GetTurns(ctx, req.GetConversationId()); err != nil {
			fmt.Fprintf(os.Stderr, "[tool-loop] GetTurns(%s) failed: %v\n", req.GetConversationId(), err)
		} else {
			convHistory = agent.BuildLLMHistory(turns)
		}
	}
	injectedLen := len(convHistory)
```

Add `ConvHistory: convHistory,` to the `agent.ToolLoopInput{...}` literal:

```go
	result, err := agent.RunToolLoop(ctx, agent.ToolLoopInput{
		Provider:            s.cloudLLMProvider,
		Registry:            s.toolRegistry,
		Permissions:         s.permStore,
		UserInput:           req.GetInput(),
		Model:               s.currentConfig.CloudModel,
		EventSink:           sink,
		PermissionRequester: requester,
		ConvHistory:         convHistory,
	})
```

Change the persist call (server.go ~838) from `0` to `injectedLen`:

```go
	s.persistToolLoopTurns(ctx, req, result, injectedLen)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd source/server && go test ./internal/server/ -count=1`
Expected: PASS — `TestStreamToolLoop_ReplaysHistoryNoDuplication` shows 4 turns in order `first, one, second, two`; all prior persist tests still pass.

Then full-module sanity:

Run: `cd source/server && go build ./... && go test ./... -count=1`
Expected: build clean; all packages PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/server/server.go source/server/internal/server/toolloop_persist_test.go
git commit -m "feat(server): replay conversation history into the tool loop each turn"
```

---

## Self-Review

**Spec coverage:**
- §1 lossless persistence → Task 3.
- §2 reconstruct & inject (BuildLLMHistory + pairing repair + injection) → Task 2 (build/repair) + Task 4 (inject).
- §2 delta-persist (no duplication) → Task 3 + Task 4 (`injectedLen`).
- §1 stable ordering prerequisite (discovered in spec review) → Task 1.
- §4 testing (unit, round-trip, integration, no-duplication) → Tasks 2 and 4.
- §5 backward compat (orphan stripping) → Task 2 tests (`OrphanToolUseStripped`, `OrphanToolResultStripped`).
- Out of scope (meter, `/c`, compaction) → not in this plan, by design.

**Type consistency:** `BuildLLMHistory(turns []conversation.Turn) []llm.Message` and `persistToolLoopTurns(..., injectedLen int)` are used identically in Tasks 2–4. Block field names (`ToolUseID`, `ToolUseRef`) match `internal/llm/messages.go`.

**Placeholder scan:** no TBD/TODO; every code step shows full code and exact commands.
