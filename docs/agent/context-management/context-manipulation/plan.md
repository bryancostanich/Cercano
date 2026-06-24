# Context Manipulation (`/c` edit) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Natural-language turn deletion in the `/c` tab: instruction → model proposes turn IDs → user confirms → hard delete → context shrinks next turn.

**Architecture:** A `DeleteTurns` store method; a pure `internal/contextedit` picker (local model + cloud fallback, validated JSON); two RPCs (`ProposeContextEdit` read-only, `DeleteConversationTurns` mutating); a `/c` edit mode (instruction input → proposal-with-confirm). The modern path rebuilds history from the store every turn, so deletes take effect automatically.

**Tech Stack:** Go (modules `cercano/source/server`, `cercano/source/clients/cli`), gRPC/protobuf, SQLite, Bubble Tea.

## Global Constraints

- Two Go modules. Server: `cd source/server && go build ./... && go test ./... -count=1`. CLI: `cd source/clients/cli && go build ./... && go test ./... -count=1`.
- **Proto regen** (verified to reproduce stubs byte-for-byte; `$GOPATH/bin` has the plugins):
  ```bash
  export PATH="$PATH:$(go env GOPATH)/bin"
  cd source/proto && protoc \
    --go_out=../server/pkg/proto --go_opt=paths=source_relative \
    --go-grpc_out=../server/pkg/proto --go-grpc_opt=paths=source_relative \
    agent.proto
  ```
- **Adding RPCs adds methods to the generated `proto.AgentClient` interface** — the `internal/mcp` test mock (`mockAgentClient`) must gain stub methods or `go test ./internal/mcp/` fails to build. Task 3 handles this explicitly; do not skip it.
- Hard delete (v1). Propose→confirm is mandatory (no delete without the explicit second RPC). Picker: local first, cloud fallback, validate IDs against real turns.
- Commit messages MUST NOT contain the word "Claude". No Co-Authored-By trailer.

Reference shapes (already defined — do not redefine):

```go
// internal/conversation/store.go — sqliteStore has `db *sql.DB`, `mu sync.Mutex`; Turn.ID is the TEXT PK.
// internal/llm/provider.go
type ChatRequest  struct { Model, System string; Messages []Message; Tools []Tool; MaxTokens int; ... }
type ChatResponse struct { Blocks []Block; StopReason string; InputTokens, OutputTokens int }
// Provider: Chat(ctx, ChatRequest) (ChatResponse, error)
// Message{Role, Blocks}; Block{Type BlockType; Text string}; RoleUser; BlockText

// internal/server/server.go fields: localProvider *legacymodels.LocalModelProvider ; cloudLLMProvider llm.Provider ; currentConfig config.Config
// legacymodels.LocalModelProvider.Process(ctx, *agent.Request) (*agent.Response, error)  // Response.Output string
// internal/server/context_turns.go: func contextTurnView(t conversation.Turn, tok contextmeter.Tokenizer) *proto.ContextTurn
// proto ContextTurn currently: role, kind, preview, est_tokens (3a)
```

---

### Task 1: `Store.DeleteTurns`

**Files:**
- Modify: `source/server/internal/conversation/store.go` (interface + sqlite impl)
- Test: `source/server/internal/conversation/store_test.go`

**Interfaces:**
- Produces: `DeleteTurns(ctx context.Context, conversationID string, ids []string) error` — deletes the named turns of that conversation; unknown ids ignored; other conversations untouched.

- [ ] **Step 1: Write the failing test**

Add to `store_test.go`:

```go
func TestDeleteTurns_RemovesOnlyNamed(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil { t.Fatalf("open: %v", err) }
	defer s.Close()
	ctx := context.Background()
	if err := s.EnsureConversation(ctx, "c1", "", "m"); err != nil { t.Fatalf("ensure: %v", err) }
	if err := s.EnsureConversation(ctx, "c2", "", "m"); err != nil { t.Fatalf("ensure: %v", err) }
	// c1: three turns with known ids; c2: one turn that must survive.
	for _, tn := range []Turn{
		{ID: "a", ConversationID: "c1", Role: "user", Content: "one"},
		{ID: "b", ConversationID: "c1", Role: "assistant", Content: "two"},
		{ID: "c", ConversationID: "c1", Role: "user", Content: "three"},
		{ID: "z", ConversationID: "c2", Role: "user", Content: "other"},
	} {
		if err := s.Append(ctx, tn); err != nil { t.Fatalf("append: %v", err) }
	}

	if err := s.DeleteTurns(ctx, "c1", []string{"a", "c", "ghost"}); err != nil {
		t.Fatalf("DeleteTurns: %v", err)
	}
	got, _ := s.GetTurns(ctx, "c1")
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("c1 turns = %+v, want only [b]", got)
	}
	other, _ := s.GetTurns(ctx, "c2")
	if len(other) != 1 {
		t.Errorf("c2 should be untouched, got %d turns", len(other))
	}
	// Idempotent: deleting already-gone ids is a no-op, no error.
	if err := s.DeleteTurns(ctx, "c1", []string{"a"}); err != nil {
		t.Errorf("idempotent delete errored: %v", err)
	}
	// Empty id list is a no-op.
	if err := s.DeleteTurns(ctx, "c1", nil); err != nil {
		t.Errorf("empty delete errored: %v", err)
	}
}
```

(Verified: `Append` (store.go:227) preserves a provided non-empty `Turn.ID` and only generates one when empty — so the explicit IDs `a`/`b`/`c`/`z` above are stored as written. Task 3's test relies on the same behavior.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd source/server && go test ./internal/conversation/ -run TestDeleteTurns -count=1`
Expected: FAIL — `DeleteTurns` undefined.

- [ ] **Step 3: Add to the `Store` interface**

In the `Store` interface (next to `UpdateRecap`):

```go
	// DeleteTurns removes the named turns from a conversation. Unknown ids are
	// ignored (idempotent); other conversations are never affected.
	DeleteTurns(ctx context.Context, conversationID string, ids []string) error
```

- [ ] **Step 4: Implement on `sqliteStore`**

Add near `Delete` (store.go:346). Build a parameterized `IN (...)` list (never string-interpolate ids):

```go
func (s *sqliteStore) DeleteTurns(ctx context.Context, conversationID string, ids []string) error {
	if conversationID == "" {
		return errors.New("conversation id required")
	}
	if len(ids) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, conversationID)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := "DELETE FROM turns WHERE conversation_id = ? AND id IN (" + strings.Join(placeholders, ",") + ")"
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}
```

(`strings` and `errors` are already imported by store.go — confirm; add only if the build complains.)

- [ ] **Step 5: Run the test + build**

Run: `cd source/server && go test ./internal/conversation/ -run TestDeleteTurns -count=1 -v && go build ./...`
Expected: PASS; clean build.

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/conversation/store.go source/server/internal/conversation/store_test.go
git commit -m "feat(conversation): DeleteTurns store method"
```

---

### Task 2: `internal/contextedit` picker

**Files:**
- Create: `source/server/internal/contextedit/contextedit.go`
- Test: `source/server/internal/contextedit/contextedit_test.go`

**Interfaces:**
- Produces: `TurnSummary{ID,Role,Kind,Preview string}`; `Proposal{DeleteIDs []string; Rationale string}`; `CompleteFunc func(ctx, prompt string) (string, error)`; `func Propose(ctx, instruction string, turns []TurnSummary, local, cloud CompleteFunc) (Proposal, error)`.

- [ ] **Step 1: Write the failing tests**

Create `source/server/internal/contextedit/contextedit_test.go`:

```go
package contextedit

import (
	"context"
	"errors"
	"testing"
)

var sampleTurns = []TurnSummary{
	{ID: "a", Role: "user", Kind: "text", Preview: "let's debug the panic"},
	{ID: "b", Role: "assistant", Kind: "text", Preview: "the nil deref is in foo()"},
	{ID: "c", Role: "user", Kind: "text", Preview: "now design the API"},
}

func fixed(out string, err error) CompleteFunc {
	return func(context.Context, string) (string, error) { return out, err }
}

func TestPropose_ValidJSON(t *testing.T) {
	local := fixed(`{"delete_ids":["a","b"],"rationale":"removed the debugging tangent"}`, nil)
	p, err := Propose(context.Background(), "drop the debugging", sampleTurns, local, nil)
	if err != nil { t.Fatalf("Propose: %v", err) }
	if len(p.DeleteIDs) != 2 || p.DeleteIDs[0] != "a" || p.DeleteIDs[1] != "b" {
		t.Errorf("delete_ids = %v", p.DeleteIDs)
	}
	if p.Rationale == "" { t.Error("missing rationale") }
}

func TestPropose_DropsHallucinatedID(t *testing.T) {
	local := fixed(`{"delete_ids":["a","zzz"],"rationale":"x"}`, nil)
	p, err := Propose(context.Background(), "i", sampleTurns, local, nil)
	if err != nil { t.Fatalf("Propose: %v", err) }
	if len(p.DeleteIDs) != 1 || p.DeleteIDs[0] != "a" {
		t.Errorf("expected only real id [a], got %v", p.DeleteIDs)
	}
}

func TestPropose_JSONInProse(t *testing.T) {
	local := fixed("Sure! Here you go:\n{\"delete_ids\":[\"c\"],\"rationale\":\"y\"}\nDone.", nil)
	p, err := Propose(context.Background(), "i", sampleTurns, local, nil)
	if err != nil || len(p.DeleteIDs) != 1 || p.DeleteIDs[0] != "c" {
		t.Fatalf("prose-wrapped JSON not parsed: %v / %+v", err, p)
	}
}

func TestPropose_LocalFailsCloudFallback(t *testing.T) {
	local := fixed("", errors.New("local down"))
	cloud := fixed(`{"delete_ids":["a"],"rationale":"z"}`, nil)
	p, err := Propose(context.Background(), "i", sampleTurns, local, cloud)
	if err != nil || len(p.DeleteIDs) != 1 || p.DeleteIDs[0] != "a" {
		t.Fatalf("cloud fallback failed: %v / %+v", err, p)
	}
}

func TestPropose_AllUnparseable(t *testing.T) {
	local := fixed("no json here", nil)
	if _, err := Propose(context.Background(), "i", sampleTurns, local, nil); err == nil {
		t.Fatal("expected error when no parseable proposal")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd source/server && go test ./internal/contextedit/ -count=1`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Implement**

Create `source/server/internal/contextedit/contextedit.go`:

```go
// Package contextedit turns a natural-language instruction plus a conversation's
// turn summaries into a validated set of turn IDs to delete. The model calls are
// injected so the prompt/parse/validate logic is testable without a model.
package contextedit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type TurnSummary struct{ ID, Role, Kind, Preview string }
type Proposal struct {
	DeleteIDs []string
	Rationale string
}
type CompleteFunc func(ctx context.Context, prompt string) (string, error)

// Propose tries local then cloud, parses the model's JSON, and keeps only
// delete_ids that exist in turns. Returns an error if no provider yields a
// parseable, non-empty proposal.
func Propose(ctx context.Context, instruction string, turns []TurnSummary, local, cloud CompleteFunc) (Proposal, error) {
	prompt := buildPrompt(instruction, turns)
	valid := make(map[string]bool, len(turns))
	for _, t := range turns {
		valid[t.ID] = true
	}
	var lastErr error
	for _, fn := range []CompleteFunc{local, cloud} {
		if fn == nil {
			continue
		}
		raw, err := fn(ctx, prompt)
		if err != nil {
			lastErr = err
			continue
		}
		p, perr := parseProposal(raw)
		if perr != nil {
			lastErr = perr
			continue
		}
		kept := p.DeleteIDs[:0]
		for _, id := range p.DeleteIDs {
			if valid[id] {
				kept = append(kept, id)
			}
		}
		p.DeleteIDs = kept
		if len(p.DeleteIDs) == 0 {
			lastErr = errors.New("no matching turns selected")
			continue
		}
		return p, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no model available")
	}
	return Proposal{}, fmt.Errorf("could not interpret instruction: %w", lastErr)
}

func buildPrompt(instruction string, turns []TurnSummary) string {
	var b strings.Builder
	b.WriteString("You curate a conversation's context. Given an instruction and a list of turns, ")
	b.WriteString("decide which turns to DELETE. Respond with ONLY a JSON object: ")
	b.WriteString(`{"delete_ids": ["<id>", ...], "rationale": "<one sentence>"}.`)
	b.WriteString(" Delete only what the instruction asks to remove; keep everything it says to retain.\n\nTurns:\n")
	for _, t := range turns {
		fmt.Fprintf(&b, "- id=%s [%s/%s] %s\n", t.ID, t.Role, t.Kind, t.Preview)
	}
	fmt.Fprintf(&b, "\nInstruction: %s\n", instruction)
	return b.String()
}

// parseProposal extracts the first JSON object from raw (models often wrap it in
// prose/markdown) and unmarshals it.
func parseProposal(raw string) (Proposal, error) {
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end <= start {
		return Proposal{}, errors.New("no JSON object found")
	}
	var dto struct {
		DeleteIDs []string `json:"delete_ids"`
		Rationale string   `json:"rationale"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &dto); err != nil {
		return Proposal{}, fmt.Errorf("bad JSON: %w", err)
	}
	return Proposal{DeleteIDs: dto.DeleteIDs, Rationale: dto.Rationale}, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `cd source/server && go test ./internal/contextedit/ -count=1 -v`
Expected: PASS (all five).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/contextedit/
git commit -m "feat(contextedit): NL turn-deletion picker with cloud fallback"
```

---

### Task 3: RPCs + handlers + agentclient + mock + `ContextTurn.id`

**Files:**
- Modify: `source/proto/agent.proto`; regenerate `source/server/pkg/proto/*.pb.go`
- Modify: `source/server/internal/server/context_turns.go` (set `id`)
- Create: `source/server/internal/server/context_edit.go` (two handlers)
- Modify: `source/server/pkg/agentclient/client.go` (wrappers + `ContextTurn.ID`)
- Modify: `source/server/internal/mcp/server_test.go` (mock gains the 2 new methods)
- Test: `source/server/internal/server/context_edit_test.go`

**Interfaces:**
- Produces: RPCs `ProposeContextEdit`, `DeleteConversationTurns`; `agentclient.Proposal{DeleteIDs []string; Rationale string}` + `ProposeContextEdit(ctx, convID, instruction)`; `DeleteConversationTurns(ctx, convID, ids) (int, error)`; `ContextTurn.Id`.

- [ ] **Step 1: Edit the proto**

In `source/proto/agent.proto`, add to `service Agent`:

```proto
  rpc ProposeContextEdit      (ProposeContextEditRequest)      returns (ProposeContextEditResponse) {}
  rpc DeleteConversationTurns (DeleteConversationTurnsRequest) returns (DeleteConversationTurnsResponse) {}
```

Add messages, and extend `ContextTurn` with `id`:

```proto
message ProposeContextEditRequest  { string conversation_id = 1; string instruction = 2; }
message ProposeContextEditResponse { repeated string delete_ids = 1; string rationale = 2; }
message DeleteConversationTurnsRequest  { string conversation_id = 1; repeated string turn_id = 2; }
message DeleteConversationTurnsResponse { int32 deleted = 1; }
```

In the existing `ContextTurn` message add: `string id = 5;`

- [ ] **Step 2: Regenerate + confirm types**

Run the proto regen command (Global Constraints). Then:
Run: `cd source/server && go build ./pkg/proto/`
Expected: clean; `proto.ProposeContextEditRequest`, `proto.DeleteConversationTurnsRequest`, `ContextTurn.Id`, and the two `AgentServer`/`AgentClient` methods exist.

- [ ] **Step 3: Set `id` in `contextTurnView`**

In `context_turns.go` `contextTurnView`, set the new field on the returned `*proto.ContextTurn`:

```go
		Id:        t.ID,
```

(Add it to the struct literal alongside `Role`/`Kind`/`Preview`/`EstTokens`.)

- [ ] **Step 4: Write the failing handler test**

Create `source/server/internal/server/context_edit_test.go`:

```go
package server

import (
	"context"
	"testing"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/pkg/proto"
)

func TestDeleteConversationTurns_Deletes(t *testing.T) {
	srv, store := newServerWithStore(t)
	ctx := context.Background()
	_ = store.EnsureConversation(ctx, "c1", "", "m")
	for _, tn := range []conversation.Turn{
		{ID: "a", ConversationID: "c1", Role: "user", Content: "one"},
		{ID: "b", ConversationID: "c1", Role: "assistant", Content: "two"},
	} {
		_ = store.Append(ctx, tn)
	}
	resp, err := srv.DeleteConversationTurns(ctx, &proto.DeleteConversationTurnsRequest{
		ConversationId: "c1", TurnId: []string{"a"},
	})
	if err != nil { t.Fatalf("delete: %v", err) }
	if resp.Deleted != 1 { t.Errorf("deleted = %d, want 1", resp.Deleted) }
	got, _ := store.GetTurns(ctx, "c1")
	if len(got) != 1 || got[0].ID != "b" { t.Errorf("turns = %+v, want [b]", got) }
}
```

(A `ProposeContextEdit` handler test needs provider stubs; the picker logic itself is covered by Task 2's unit tests. Add a light propose test only if the server exposes injectable providers in tests — otherwise rely on Task 2 + the build. Note this choice in your report.)

- [ ] **Step 5: Run to verify it fails**

Run: `cd source/server && go test ./internal/server/ -run TestDeleteConversationTurns -count=1`
Expected: FAIL — `srv.DeleteConversationTurns` undefined.

- [ ] **Step 6: Implement the handlers**

Create `source/server/internal/server/context_edit.go`:

```go
package server

import (
	"context"
	"errors"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/contextedit"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

// ProposeContextEdit runs the picker model over the conversation's turn
// summaries and returns a validated deletion proposal. Read-only.
func (s *Server) ProposeContextEdit(ctx context.Context, req *proto.ProposeContextEditRequest) (*proto.ProposeContextEditResponse, error) {
	store := s.agent.PersistentStore()
	convID := req.GetConversationId()
	if store == nil || convID == "" {
		return &proto.ProposeContextEditResponse{}, nil
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		return nil, err
	}
	tok := contextmeter.Default()
	summaries := make([]contextedit.TurnSummary, 0, len(turns))
	for _, t := range turns {
		ct := contextTurnView(t, tok)
		summaries = append(summaries, contextedit.TurnSummary{
			ID: ct.GetId(), Role: ct.GetRole(), Kind: ct.GetKind(), Preview: ct.GetPreview(),
		})
	}

	var local, cloud contextedit.CompleteFunc
	if s.localProvider != nil {
		local = func(ctx context.Context, prompt string) (string, error) {
			resp, err := s.localProvider.Process(ctx, &agent.Request{Input: prompt})
			if err != nil {
				return "", err
			}
			return resp.Output, nil
		}
	}
	if s.cloudLLMProvider != nil {
		cloud = func(ctx context.Context, prompt string) (string, error) {
			resp, err := s.cloudLLMProvider.Chat(ctx, llm.ChatRequest{
				Model:     s.currentConfig.CloudModel,
				Messages:  []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: prompt}}}},
				MaxTokens: 1024,
			})
			if err != nil {
				return "", err
			}
			var out string
			for _, b := range resp.Blocks {
				if b.Type == llm.BlockText {
					out += b.Text
				}
			}
			return out, nil
		}
	}
	if local == nil && cloud == nil {
		return nil, errors.New("no model available for context editing")
	}

	p, err := contextedit.Propose(ctx, req.GetInstruction(), summaries, local, cloud)
	if err != nil {
		return nil, err
	}
	return &proto.ProposeContextEditResponse{DeleteIds: p.DeleteIDs, Rationale: p.Rationale}, nil
}

// DeleteConversationTurns hard-deletes the named turns. The next tool-loop turn
// rebuilds history from the store, so the context shrinks automatically.
func (s *Server) DeleteConversationTurns(ctx context.Context, req *proto.DeleteConversationTurnsRequest) (*proto.DeleteConversationTurnsResponse, error) {
	store := s.agent.PersistentStore()
	convID := req.GetConversationId()
	if store == nil || convID == "" {
		return &proto.DeleteConversationTurnsResponse{}, nil
	}
	if err := store.DeleteTurns(ctx, convID, req.GetTurnId()); err != nil {
		return nil, err
	}
	return &proto.DeleteConversationTurnsResponse{Deleted: int32(len(req.GetTurnId()))}, nil
}
```

- [ ] **Step 7: Agentclient wrappers + the mcp mock**

In `pkg/agentclient/client.go` add (and add `ID string` to the existing `ContextTurn` struct + map `t.GetId()` in `GetConversationTurns`):

```go
type Proposal struct {
	DeleteIDs []string
	Rationale string
}

func (c *Client) ProposeContextEdit(ctx context.Context, conversationID, instruction string) (Proposal, error) {
	resp, err := c.agent.ProposeContextEdit(ctx, &proto.ProposeContextEditRequest{
		ConversationId: conversationID, Instruction: instruction,
	})
	if err != nil {
		return Proposal{}, err
	}
	return Proposal{DeleteIDs: resp.GetDeleteIds(), Rationale: resp.GetRationale()}, nil
}

func (c *Client) DeleteConversationTurns(ctx context.Context, conversationID string, ids []string) (int, error) {
	resp, err := c.agent.DeleteConversationTurns(ctx, &proto.DeleteConversationTurnsRequest{
		ConversationId: conversationID, TurnId: ids,
	})
	if err != nil {
		return 0, err
	}
	return int(resp.GetDeleted()), nil
}
```

In `internal/mcp/server_test.go`, add two stub methods to `mockAgentClient` (mirroring the existing `GetConversationTurns` mock — same import set):

```go
func (m *mockAgentClient) ProposeContextEdit(ctx context.Context, in *proto.ProposeContextEditRequest, opts ...grpc.CallOption) (*proto.ProposeContextEditResponse, error) {
	return &proto.ProposeContextEditResponse{}, nil
}
func (m *mockAgentClient) DeleteConversationTurns(ctx context.Context, in *proto.DeleteConversationTurnsRequest, opts ...grpc.CallOption) (*proto.DeleteConversationTurnsResponse, error) {
	return &proto.DeleteConversationTurnsResponse{}, nil
}
```

- [ ] **Step 8: Run tests + build (incl. the mcp package)**

Run: `cd source/server && go test ./internal/server/ -run TestDeleteConversationTurns -count=1 && go test ./internal/mcp/ -count=1 && go build ./... && go test ./... -count=1`
Expected: PASS everywhere; the mcp test build no longer breaks (mock satisfies the interface).

- [ ] **Step 9: Commit**

```bash
git add source/proto/agent.proto source/server/pkg/proto/ source/server/internal/server/context_turns.go source/server/internal/server/context_edit.go source/server/internal/server/context_edit_test.go source/server/pkg/agentclient/client.go source/server/internal/mcp/server_test.go
git commit -m "feat(server): ProposeContextEdit + DeleteConversationTurns RPCs"
```

---

### Task 4: CLI `/c` edit mode

Extend the read-only `contextView` (3a) with an instruction input, a proposal-with-confirm step, and the delete call.

**Files:**
- Modify: `source/clients/cli/internal/ui/context_view.go`
- Modify: `source/clients/cli/internal/ui/model.go` (route the two async msgs)
- Test: `source/clients/cli/internal/ui/context_view_edit_test.go`

**Interfaces:**
- Consumes: `agentclient.ProposeContextEdit`/`DeleteConversationTurns` (Task 3); `agentclient.ContextTurn.ID`.
- Produces: edit-mode state on `contextView`; `contextEditProposalMsg`/`contextEditDeletedMsg` routed in `model.go`.

- [ ] **Step 1: Write the failing test**

Create `source/clients/cli/internal/ui/context_view_edit_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func newEditTestView() *contextView {
	return &contextView{
		width: 80, height: 24,
		palette: theme.Cracker(), styles: theme.NewStyles(theme.Cracker()),
		convID: "c1",
		snapshot: contextSnapshot{Turns: []agentclient.ContextTurn{
			{ID: "a", Role: "user", Kind: "text", Preview: "debug tangent", EstTokens: 5},
			{ID: "b", Role: "assistant", Kind: "text", Preview: "api design", EstTokens: 5},
		}},
	}
}

func TestContextView_ProposalMarksTurnsAndRationale(t *testing.T) {
	cv := newEditTestView()
	cv.applyProposal(agentclient.Proposal{DeleteIDs: []string{"a"}, Rationale: "removed the tangent"})
	out := cv.View()
	if !strings.Contains(out, "removed the tangent") {
		t.Errorf("rationale not shown:\n%s", out)
	}
	if !cv.markedForDelete("a") || cv.markedForDelete("b") {
		t.Errorf("wrong turns marked: a=%v b=%v", cv.markedForDelete("a"), cv.markedForDelete("b"))
	}
}

func TestContextView_CancelProposalClears(t *testing.T) {
	cv := newEditTestView()
	cv.applyProposal(agentclient.Proposal{DeleteIDs: []string{"a"}, Rationale: "x"})
	cv.cancelProposal()
	if cv.markedForDelete("a") {
		t.Error("cancel did not clear the proposal")
	}
}
```

(These exercise small, directly-testable methods — `applyProposal`, `cancelProposal`, `markedForDelete` — so the UI logic is unit-testable without a live model. Implement those methods in Step 3.)

- [ ] **Step 2: Run to verify it fails**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestContextView_Proposal -count=1`
Expected: FAIL — `applyProposal`/`markedForDelete`/`cancelProposal` undefined.

- [ ] **Step 3: Implement edit mode on `contextView`**

In `context_view.go`, add to the struct:

```go
	mode     contextViewMode // browse | editing | proposal
	input    textinput.Model
	proposal agentclient.Proposal
	editErr  string
```

with `type contextViewMode int; const ( cvBrowse contextViewMode = iota; cvEditing; cvProposal )`, and import `charm.land/bubbles/v2/textinput` (match the import used in `runtime_dashboard.go`). Initialize `input` in `newContextView` (`textinput.New()`, `SetWidth`, placeholder "instruction, e.g. 'drop the debugging tangent'").

Add the small testable methods:

```go
func (c *contextView) applyProposal(p agentclient.Proposal) {
	c.proposal = p
	c.mode = cvProposal
	c.editErr = ""
}
func (c *contextView) cancelProposal() {
	c.proposal = agentclient.Proposal{}
	c.mode = cvBrowse
}
func (c *contextView) markedForDelete(id string) bool {
	if c.mode != cvProposal {
		return false
	}
	for _, d := range c.proposal.DeleteIDs {
		if d == id {
			return true
		}
	}
	return false
}
```

Update `renderTurn` to mark deletions: when `c.markedForDelete(t.ID)`, prefix a `✗` and render with `c.styles.Error`/dim. Update the header/footer: in `cvProposal`, render the rationale line + `[y] delete  [n] cancel`; in `cvEditing`, render the `input.View()` and a hint; in `cvBrowse`, the 3a footer plus an `e: edit` hint.

Extend `Update` (keep the 3a scroll/`r`/`esc`/`q` for `cvBrowse`):

```go
	switch c.mode {
	case cvEditing:
		switch msg.String() {
		case "enter":
			instr := strings.TrimSpace(c.input.Value())
			if instr == "" { return nil, false }
			c.input.Reset()
			return c.proposeCmd(instr), false
		case "esc":
			c.mode = cvBrowse
			c.input.Blur()
			return nil, false
		}
		var cmd tea.Cmd
		c.input, cmd = c.input.Update(msg)
		return cmd, false
	case cvProposal:
		switch msg.String() {
		case "y":
			return c.deleteCmd(c.proposal.DeleteIDs), false
		case "n", "esc":
			c.cancelProposal()
			return nil, false
		}
		return nil, false
	default: // cvBrowse — existing 3a handling, plus:
		if msg.String() == "e" {
			c.mode = cvEditing
			return c.input.Focus(), false
		}
	}
```

Add the async commands + result messages:

```go
type contextEditProposalMsg struct { p agentclient.Proposal; err error }
type contextEditDeletedMsg  struct { n int; err error }

func (c *contextView) proposeCmd(instruction string) tea.Cmd {
	ag, convID := c.agent, c.convID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		p, err := ag.ProposeContextEdit(ctx, convID, instruction)
		return contextEditProposalMsg{p: p, err: err}
	}
}
func (c *contextView) deleteCmd(ids []string) tea.Cmd {
	ag, convID := c.agent, c.convID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		n, err := ag.DeleteConversationTurns(ctx, convID, ids)
		return contextEditDeletedMsg{n: n, err: err}
	}
}

// called by model.go when the async msgs arrive
func (c *contextView) onProposal(m contextEditProposalMsg) {
	if m.err != nil { c.editErr = "could not interpret that — try rephrasing"; c.mode = cvEditing; return }
	c.applyProposal(m.p)
}
func (c *contextView) onDeleted(m contextEditDeletedMsg) tea.Cmd {
	c.cancelProposal()
	c.snapshot = loadContextSnapshot(c.agent, c.convID) // reload after delete
	return nil
}
```

- [ ] **Step 4: Route the async msgs in `model.go`**

Next to the `runtimeDashboardActionMsg` case (the content-page async pattern), add:

```go
	case contextEditProposalMsg:
		if cv, ok := m.content.(*contextView); ok { cv.onProposal(msg) }
		return m, nil
	case contextEditDeletedMsg:
		if cv, ok := m.content.(*contextView); ok { return m, cv.onDeleted(msg) }
		return m, nil
```

- [ ] **Step 5: Run tests + build the CLI**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestContextView -count=1 && go build ./... && go test ./... -count=1`
Expected: PASS (the new edit tests + the 3a tests); clean CLI build; full CLI suite green.

- [ ] **Step 6: Commit**

```bash
git add source/clients/cli/internal/ui/context_view.go source/clients/cli/internal/ui/model.go source/clients/cli/internal/ui/context_view_edit_test.go
git commit -m "feat(cli): /c edit mode — propose, confirm, delete turns"
```

---

## Self-Review

**Spec coverage:**
- §1 store `DeleteTurns` → Task 1.
- §2 picker (local+cloud, JSON parse, validation) → Task 2.
- §3 RPCs + handlers + `ContextTurn.id` + agentclient + mock → Task 3.
- §4 CLI edit mode (input, proposal mark, confirm, delete, reload, async routing) → Task 4.
- §5 error/edge (no model, unparseable, hallucinated id, orphan pairs via repairPairing) → Task 2 (picker) + Task 4 (`onProposal` error) + existing repairPairing.
- §6 testing → Tasks 1–4.
- Out of scope (soft-exclude, multi-turn negotiation, compaction) → not in this plan.

**Type consistency:** `DeleteTurns(ctx, convID, ids)`, `contextedit.{TurnSummary,Proposal,CompleteFunc,Propose}`, `agentclient.{Proposal,ProposeContextEdit,DeleteConversationTurns,ContextTurn.ID}`, proto `ProposeContextEdit*`/`DeleteConversationTurns*`/`ContextTurn.Id`, and the CLI `contextViewMode`/`applyProposal`/`markedForDelete`/`onProposal` are used identically across tasks.

**Placeholder scan:** no TBD/TODO; every code step shows full code + exact commands. The "confirm import/Append-ID behavior / add light propose test only if injectable" notes are deliberate build-time reconciliations, not unresolved requirements.
