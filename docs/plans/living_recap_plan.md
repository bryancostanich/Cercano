# Living Recap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a one-line, locally-generated, persisted "what got done" recap per conversation, shown live in the CLI chat footer, the history picker, and on resume.

**Architecture:** The agent owns recap generation (it holds the SQLite store and the local engine). After each persisted turn it calls a debounced `recap.Generator`, which asks the local model to update the recap incrementally and writes it back to a new `recap` column. The CLI reads the recap via a new `GetConversation` RPC and refreshes it on turn boundaries.

**Tech Stack:** Go, modernc.org/sqlite (pure-Go), gRPC (protoc-gen-go v1.36.11), bubbletea/lipgloss TUI.

## Global Constraints

- Module path: `cercano/source/server`. All Go imports use this prefix.
- Recap generation is **local model only** — never call a cloud provider. Recap must stay zero-cost and private.
- Recap failures must never block a turn or surface an error to the user — log to stderr in the existing `[persistent-store]`-style and keep the prior recap.
- Every grid/DB write goes through the store's existing `s.mu` mutex pattern.
- Follow existing file conventions; do not reformat untouched code.
- Commit messages must NOT contain the word "Claude" anywhere (user rule).
- Run all Go commands from `source/server/` unless stated otherwise. Build check: `go build ./...`. Test: `go test ./<pkg>/ -count=1`.

---

### Task 1: Conversation store — recap column, migration, UpdateRecap, Get

**Files:**
- Modify: `source/server/internal/conversation/schema.sql`
- Modify: `source/server/internal/conversation/store.go`
- Test: `source/server/internal/conversation/store_test.go`

**Interfaces:**
- Consumes: existing `conversation.Info`, `conversation.Turn`, `sqliteStore`.
- Produces:
  - `Info.Recap string`, `Info.RecapUpdatedAt time.Time`
  - `Store.UpdateRecap(ctx context.Context, conversationID, recap string) error`
  - `Store.Get(ctx context.Context, conversationID string) (Info, error)`

- [ ] **Step 1: Write failing tests**

Append to `store_test.go`:

```go
func TestUpdateRecapAndGet(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	if err := s.EnsureConversation(ctx, "c1", "/proj", "qwen3-coder"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRecap(ctx, "c1", "Refactored the router and added tests"); err != nil {
		t.Fatalf("UpdateRecap: %v", err)
	}

	got, err := s.Get(ctx, "c1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Recap != "Refactored the router and added tests" {
		t.Errorf("recap = %q", got.Recap)
	}
	if got.RecapUpdatedAt.IsZero() {
		t.Error("RecapUpdatedAt not set")
	}
}

func TestGetMissingReturnsError(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	if _, err := s.Get(context.Background(), "nope"); err == nil {
		t.Error("expected error for missing conversation")
	}
}

func TestListIncludesRecap(t *testing.T) {
	s, _ := Open(":memory:")
	defer s.Close()
	ctx := context.Background()
	_ = s.EnsureConversation(ctx, "c1", "", "")
	_ = s.UpdateRecap(ctx, "c1", "did a thing")
	list, err := s.List(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Recap != "did a thing" {
		t.Errorf("list recap = %+v", list)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

Run: `go test ./internal/conversation/ -run 'Recap|GetMissing|ListIncludesRecap' -count=1`
Expected: FAIL — `UpdateRecap`/`Get` undefined, `Info.Recap` undefined.

- [ ] **Step 3: Add columns to `schema.sql`**

In `schema.sql`, add the two columns to the `conversations` CREATE TABLE (after `last_turn_at`):

```sql
    last_turn_at INTEGER NOT NULL,
    recap            TEXT NOT NULL DEFAULT '',
    recap_updated_at INTEGER NOT NULL DEFAULT 0
```

(This covers fresh DBs. Existing DBs are migrated in Step 4.)

- [ ] **Step 4: Add idempotent migration in `Open()`**

In `store.go`, in `Open()`, immediately after the `db.Exec(schemaSQL)` block succeeds (before `return &sqliteStore{db: db}, nil`), add:

```go
	// Migrate pre-existing DBs that predate the recap columns. ALTER fails
	// with "duplicate column name" once applied — ignore that case.
	for _, alter := range []string{
		`ALTER TABLE conversations ADD COLUMN recap TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE conversations ADD COLUMN recap_updated_at INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(alter); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("migrate recap columns: %w", err)
		}
	}
```

(`strings` and `fmt` are already imported.)

- [ ] **Step 5: Add fields to `Info`**

In `store.go`, add to the `Info` struct (after `TurnCount int`):

```go
	Recap          string
	RecapUpdatedAt time.Time
```

- [ ] **Step 6: Add `UpdateRecap` and `Get` to the `Store` interface**

In the `Store` interface, after `Rename(...)`:

```go
	// UpdateRecap sets the LLM-generated living recap and its timestamp.
	UpdateRecap(ctx context.Context, conversationID, recap string) error

	// Get returns a single conversation's Info, or an error if not found.
	Get(ctx context.Context, conversationID string) (Info, error)
```

- [ ] **Step 7: Implement `UpdateRecap` and `Get` on `sqliteStore`**

In `store.go`, after the `Rename` method:

```go
func (s *sqliteStore) UpdateRecap(ctx context.Context, conversationID, recap string) error {
	if conversationID == "" {
		return errors.New("conversation id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`UPDATE conversations SET recap = ?, recap_updated_at = ? WHERE id = ?`,
		recap, time.Now().Unix(), conversationID)
	return err
}

func (s *sqliteStore) Get(ctx context.Context, conversationID string) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var info Info
	var startedAt, lastTurnAt, recapAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT c.id, c.title, c.project_dir, c.model, c.started_at, c.last_turn_at,
		       c.recap, c.recap_updated_at,
		       (SELECT COUNT(*) FROM turns t WHERE t.conversation_id = c.id) AS turn_count
		FROM conversations c WHERE c.id = ?`, conversationID).
		Scan(&info.ID, &info.Title, &info.ProjectDir, &info.Model,
			&startedAt, &lastTurnAt, &info.Recap, &recapAt, &info.TurnCount)
	if err != nil {
		return Info{}, err
	}
	info.StartedAt = time.Unix(startedAt, 0)
	info.LastTurnAt = time.Unix(lastTurnAt, 0)
	if recapAt > 0 {
		info.RecapUpdatedAt = time.Unix(recapAt, 0)
	}
	return info, nil
}
```

- [ ] **Step 8: Select recap in `List`**

In `List`, change the SELECT to include the recap columns and scan them. Replace the query's first line and the `rows.Scan` call:

Query (add `c.recap, c.recap_updated_at,` before the turn_count subselect):

```go
		SELECT c.id, c.title, c.project_dir, c.model, c.started_at, c.last_turn_at,
		       c.recap, c.recap_updated_at,
		       (SELECT COUNT(*) FROM turns t WHERE t.conversation_id = c.id) AS turn_count
		FROM conversations c
		WHERE (? = '' OR c.project_dir = ?)
		ORDER BY c.last_turn_at DESC
```

Scan block (replace the existing loop body's scan):

```go
		var info Info
		var startedAt, lastTurnAt, recapAt int64
		if err := rows.Scan(&info.ID, &info.Title, &info.ProjectDir, &info.Model,
			&startedAt, &lastTurnAt, &info.Recap, &recapAt, &info.TurnCount); err != nil {
			return nil, err
		}
		info.StartedAt = time.Unix(startedAt, 0)
		info.LastTurnAt = time.Unix(lastTurnAt, 0)
		if recapAt > 0 {
			info.RecapUpdatedAt = time.Unix(recapAt, 0)
		}
		out = append(out, info)
```

- [ ] **Step 9: Run tests, verify pass**

Run: `go test ./internal/conversation/ -count=1`
Expected: PASS (all, including pre-existing tests).

- [ ] **Step 10: Commit**

```bash
git add source/server/internal/conversation/
git commit -m "feat(conversation): recap column, UpdateRecap, Get"
```

---

### Task 2: Recap generator (debounced, incremental, local-only)

**Files:**
- Create: `source/server/internal/recap/recap.go`
- Test: `source/server/internal/recap/recap_test.go`

**Interfaces:**
- Consumes: `conversation.Info`, `conversation.Turn` (types only).
- Produces:
  - `type CompleteFunc func(ctx context.Context, prompt string) (string, error)`
  - `type Store interface { Get(...); GetTurns(...); UpdateRecap(...) }` (subset of conversation.Store)
  - `func New(store Store, complete CompleteFunc, debounce time.Duration, maxTurns int) *Generator`
  - `func (g *Generator) Schedule(conversationID string)` — satisfies `agent.RecapScheduler` (Task 4)

- [ ] **Step 1: Write failing tests**

Create `recap_test.go`:

```go
package recap

import (
	"context"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
)

type fakeStore struct {
	mu      sync.Mutex
	info    conversation.Info
	turns   []conversation.Turn
	recaps  []string
	updated chan string
}

func (f *fakeStore) Get(ctx context.Context, id string) (conversation.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.info, nil
}
func (f *fakeStore) GetTurns(ctx context.Context, id string) ([]conversation.Turn, error) {
	return f.turns, nil
}
func (f *fakeStore) UpdateRecap(ctx context.Context, id, recap string) error {
	f.mu.Lock()
	f.recaps = append(f.recaps, recap)
	f.mu.Unlock()
	f.updated <- recap
	return nil
}

func TestScheduleDebouncesToOneGeneration(t *testing.T) {
	calls := 0
	var cmu sync.Mutex
	complete := func(ctx context.Context, prompt string) (string, error) {
		cmu.Lock()
		calls++
		cmu.Unlock()
		return "did stuff", nil
	}
	fs := &fakeStore{
		turns:   []conversation.Turn{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}},
		updated: make(chan string, 4),
	}
	g := New(fs, complete, 30*time.Millisecond, 10)

	g.Schedule("c1")
	g.Schedule("c1")
	g.Schedule("c1")

	select {
	case got := <-fs.updated:
		if got != "did stuff" {
			t.Errorf("recap = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("recap never generated")
	}
	time.Sleep(50 * time.Millisecond)
	cmu.Lock()
	defer cmu.Unlock()
	if calls != 1 {
		t.Errorf("complete called %d times, want 1 (debounce coalescing)", calls)
	}
}

func TestGenerationFailureKeepsPriorRecap(t *testing.T) {
	complete := func(ctx context.Context, prompt string) (string, error) {
		return "", context.DeadlineExceeded
	}
	fs := &fakeStore{
		turns:   []conversation.Turn{{Role: "user", Content: "hi"}},
		updated: make(chan string, 1),
	}
	g := New(fs, complete, 10*time.Millisecond, 10)
	g.Schedule("c1")
	select {
	case <-fs.updated:
		t.Fatal("UpdateRecap should not be called on generation failure")
	case <-time.After(80 * time.Millisecond):
		// success: nothing written
	}
}

func TestFirstLineCaps(t *testing.T) {
	got := firstLine("  line one\nline two  ", 100)
	if got != "line one" {
		t.Errorf("firstLine = %q", got)
	}
	if l := firstLine("abcdefghij", 4); l != "abc…" {
		t.Errorf("cap = %q", l)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

Run: `go test ./internal/recap/ -count=1`
Expected: FAIL — package/`New`/`firstLine` undefined.

- [ ] **Step 3: Implement `recap.go`**

Create `recap.go`:

```go
// Package recap maintains a short, living, locally-generated summary of the
// work in a conversation. Generation is debounced per-conversation and runs
// off the request path; failures are swallowed so a turn is never blocked.
package recap

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"cercano/source/server/internal/conversation"
)

// CompleteFunc runs a single local-model completion. The wiring layer adapts
// the local provider into this; recap never sees a cloud path.
type CompleteFunc func(ctx context.Context, prompt string) (string, error)

// Store is the subset of conversation.Store the generator needs.
type Store interface {
	Get(ctx context.Context, conversationID string) (conversation.Info, error)
	GetTurns(ctx context.Context, conversationID string) ([]conversation.Turn, error)
	UpdateRecap(ctx context.Context, conversationID, recap string) error
}

const (
	maxRecapChars   = 80
	genTimeout      = 30 * time.Second
)

// Generator debounces recap regeneration per conversation.
type Generator struct {
	store    Store
	complete CompleteFunc
	debounce time.Duration
	maxTurns int

	mu     sync.Mutex
	timers map[string]*time.Timer
}

// New builds a Generator. debounce is the minimum quiet period after the last
// Schedule before generation fires; maxTurns caps how many recent turns feed
// the prompt.
func New(store Store, complete CompleteFunc, debounce time.Duration, maxTurns int) *Generator {
	return &Generator{
		store:    store,
		complete: complete,
		debounce: debounce,
		maxTurns: maxTurns,
		timers:   make(map[string]*time.Timer),
	}
}

// Schedule requests a (debounced) recap regeneration for the conversation.
// Rapid calls coalesce into a single generation after the quiet period.
func (g *Generator) Schedule(conversationID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if t, ok := g.timers[conversationID]; ok {
		t.Reset(g.debounce)
		return
	}
	g.timers[conversationID] = time.AfterFunc(g.debounce, func() {
		g.mu.Lock()
		delete(g.timers, conversationID)
		g.mu.Unlock()
		g.regenerate(conversationID)
	})
}

func (g *Generator) regenerate(conversationID string) {
	ctx, cancel := context.WithTimeout(context.Background(), genTimeout)
	defer cancel()

	info, err := g.store.Get(ctx, conversationID)
	if err != nil {
		return // conversation gone; nothing to do
	}
	turns, err := g.store.GetTurns(ctx, conversationID)
	if err != nil || len(turns) == 0 {
		return
	}
	prompt := buildPrompt(info.Recap, turns, g.maxTurns)

	out, err := g.complete(ctx, prompt)
	if err != nil {
		return // keep prior recap on failure
	}
	line := firstLine(out, maxRecapChars)
	if line == "" {
		return
	}
	_ = g.store.UpdateRecap(ctx, conversationID, line)
}

// buildPrompt produces an incremental summarization prompt from the prior
// recap plus the most recent maxTurns turns.
func buildPrompt(prior string, turns []conversation.Turn, maxTurns int) string {
	if maxTurns > 0 && len(turns) > maxTurns {
		turns = turns[len(turns)-maxTurns:]
	}
	var b strings.Builder
	for _, t := range turns {
		c := t.Content
		if len(c) > 500 {
			c = c[:500]
		}
		fmt.Fprintf(&b, "%s: %s\n", t.Role, c)
	}
	priorLine := prior
	if priorLine == "" {
		priorLine = "(none yet)"
	}
	return fmt.Sprintf(
		"You maintain a one-line running summary of a coding session.\n"+
			"Prior summary: %s\n\nRecent activity:\n%s\n"+
			"Write ONE updated line, past tense, at most %d characters, describing "+
			"what the session has accomplished so far. Output ONLY the line, no quotes, no preamble.",
		priorLine, b.String(), maxRecapChars)
}

// firstLine returns the first non-empty trimmed line, truncated to maxChars
// (with an ellipsis when cut).
func firstLine(s string, maxChars int) string {
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		r := []rune(ln)
		if len(r) > maxChars {
			return string(r[:maxChars-1]) + "…"
		}
		return ln
	}
	return ""
}
```

- [ ] **Step 4: Run tests, verify pass**

Run: `go test ./internal/recap/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/recap/
git commit -m "feat(recap): debounced local-only recap generator"
```

---

### Task 3: Proto — recap fields + GetConversation RPC + regenerate

**Files:**
- Modify: `source/proto/agent.proto`
- Regenerate: `source/server/pkg/proto/agent.pb.go`, `source/server/pkg/proto/agent_grpc.pb.go`

**Interfaces:**
- Produces (generated Go): `Conversation.Recap`, `Conversation.RecapUpdatedAt`, `GetConversationRequest{ConversationId}`, `AgentClient.GetConversation`, `AgentServer.GetConversation`.

- [ ] **Step 1: Add fields to `Conversation` message**

In `agent.proto`, in `message Conversation`, after `int32 turn_count = 7;`:

```proto
  string recap = 8;
  int64  recap_updated_at = 9;
```

- [ ] **Step 2: Add the RPC and messages**

In `service Agent`, after the `RenameConversation` rpc:

```proto
  rpc GetConversation (GetConversationRequest) returns (Conversation) {}
```

After `message RenameConversationResponse { ... }`:

```proto
message GetConversationRequest { string conversation_id = 1; }
```

(The response reuses the existing `Conversation` message.)

- [ ] **Step 3: Install protoc Go plugins (pinned)**

From repo root:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
export PATH="$PATH:$(go env GOPATH)/bin"
```

- [ ] **Step 4: Regenerate**

From repo root (`.../Cercano`):

```bash
protoc --proto_path=. \
  --go_out=source/server --go_opt=module=cercano/source/server \
  --go-grpc_out=source/server --go-grpc_opt=module=cercano/source/server \
  source/proto/agent.proto
```

- [ ] **Step 5: Verify generated symbols + build**

Run:
```bash
grep -n 'func.*Conversation.*GetRecap\|GetConversationRequest\|GetConversation(ctx' source/server/pkg/proto/agent.pb.go source/server/pkg/proto/agent_grpc.pb.go | head
cd source/server && go build ./pkg/proto/
```
Expected: symbols present; build PASS. (The server/client don't implement the new RPC yet — that's Tasks 5–6. `go build ./...` will fail until Task 5 adds the server method; that's expected.)

- [ ] **Step 6: Commit**

```bash
git add source/proto/agent.proto source/server/pkg/proto/
git commit -m "feat(proto): recap fields + GetConversation rpc"
```

---

### Task 4: Agent — RecapScheduler hook + GetConversation wrapper

**Files:**
- Modify: `source/server/internal/agent/agent.go`
- Test: `source/server/internal/agent/agent_test.go`

**Interfaces:**
- Consumes: `recap.Generator.Schedule` (via interface), `conversation.Store.Get`.
- Produces:
  - `type RecapScheduler interface { Schedule(conversationID string) }`
  - `func WithRecapScheduler(rs RecapScheduler) AgentOption`
  - `func (a *Agent) GetConversation(ctx, id) (conversation.Info, error)`

- [ ] **Step 1: Write failing test**

Append to `agent_test.go`:

```go
type fakeRecap struct{ scheduled []string }

func (f *fakeRecap) Schedule(id string) { f.scheduled = append(f.scheduled, id) }

func TestWithRecapSchedulerStoresHook(t *testing.T) {
	fr := &fakeRecap{}
	a := NewAgent(nil, nil, WithRecapScheduler(fr))
	if a.recap == nil {
		t.Fatal("recap scheduler not attached")
	}
}
```

- [ ] **Step 2: Run test, verify it fails**

Run: `go test ./internal/agent/ -run TestWithRecapScheduler -count=1`
Expected: FAIL — `WithRecapScheduler` / `a.recap` undefined.

- [ ] **Step 3: Add the interface, option, and field**

In `agent.go`, add a field to the `Agent` struct (after `contextLoader`):

```go
	recap         RecapScheduler
```

Add near the other options:

```go
// RecapScheduler requests a (debounced) recap regeneration for a conversation.
// Implemented by *recap.Generator; kept as an interface so agent doesn't import
// the recap package.
type RecapScheduler interface {
	Schedule(conversationID string)
}

// WithRecapScheduler attaches the living-recap generator. After each persisted
// assistant turn the agent calls Schedule to refresh the conversation recap.
func WithRecapScheduler(rs RecapScheduler) AgentOption {
	return func(a *Agent) { a.recap = rs }
}
```

- [ ] **Step 4: Call Schedule after the assistant turn is persisted**

In `storeConversationTurn`, inside the `if a.persistent != nil {` block, immediately after the assistant `Append` block closes (after line ~183), add:

```go
		if a.recap != nil {
			a.recap.Schedule(conversationID)
		}
```

- [ ] **Step 5: Add the `GetConversation` wrapper**

In `agent.go`, after `ListConversations`:

```go
// GetConversation returns a single conversation's Info, including its recap.
func (a *Agent) GetConversation(ctx context.Context, conversationID string) (conversation.Info, error) {
	if a.persistent == nil {
		return conversation.Info{}, fmt.Errorf("no persistent store configured")
	}
	return a.persistent.Get(ctx, conversationID)
}
```

- [ ] **Step 6: Run tests, verify pass**

Run: `go test ./internal/agent/ -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add source/server/internal/agent/
git commit -m "feat(agent): recap scheduler hook + GetConversation"
```

---

### Task 5: Server — GetConversation handler + recap in ListConversations

**Files:**
- Modify: `source/server/internal/server/server.go`

**Interfaces:**
- Consumes: `agent.GetConversation`, `proto.GetConversationRequest`, `proto.Conversation`.
- Produces: `Server.GetConversation` (implements `proto.AgentServer`).

- [ ] **Step 1: Add recap to the ListConversations mapping**

In `ListConversations`, add the two fields to the `&proto.Conversation{...}` literal:

```go
			Recap:          i.Recap,
			RecapUpdatedAt: i.RecapUpdatedAt.Unix(),
```

- [ ] **Step 2: Add the `GetConversation` handler**

After `ResumeConversation` in `server.go`:

```go
// GetConversation implements proto.AgentServer — returns a single
// conversation's metadata including its living recap. Lightweight: no turn
// rehydration.
func (s *Server) GetConversation(ctx context.Context, req *proto.GetConversationRequest) (*proto.Conversation, error) {
	i, err := s.agent.GetConversation(ctx, req.GetConversationId())
	if err != nil {
		return nil, err
	}
	return &proto.Conversation{
		Id:             i.ID,
		Title:          i.Title,
		ProjectDir:     i.ProjectDir,
		Model:          i.Model,
		StartedAt:      i.StartedAt.Unix(),
		LastTurnAt:     i.LastTurnAt.Unix(),
		TurnCount:      int32(i.TurnCount),
		Recap:          i.Recap,
		RecapUpdatedAt: i.RecapUpdatedAt.Unix(),
	}, nil
}
```

- [ ] **Step 3: Build**

Run: `cd source/server && go build ./...`
Expected: PASS (server now satisfies the regenerated `AgentServer` interface).

- [ ] **Step 4: Commit**

```bash
git add source/server/internal/server/server.go
git commit -m "feat(server): GetConversation handler + recap in list"
```

---

### Task 6: CLI agent client — recap fields + GetConversation

**Files:**
- Modify: `source/server/internal/cli/agentclient/client.go`

**Interfaces:**
- Produces:
  - `ConversationInfo.Recap string`, `ConversationInfo.RecapUpdatedAt time.Time`
  - `func (c *Client) GetConversation(ctx, id) (ConversationInfo, error)`

- [ ] **Step 1: Add fields to `ConversationInfo`**

In `client.go`, add to the `ConversationInfo` struct (after `TurnCount int`):

```go
	Recap          string
	RecapUpdatedAt time.Time
```

- [ ] **Step 2: Populate recap in `ListConversations`**

In `ListConversations`, add to the `ConversationInfo{...}` literal in the append loop:

```go
			Recap:          c.GetRecap(),
			RecapUpdatedAt: time.Unix(c.GetRecapUpdatedAt(), 0),
```

- [ ] **Step 3: Add `GetConversation`**

After `RenameConversation` in `client.go`:

```go
// GetConversation fetches a single conversation's metadata including its recap.
func (c *Client) GetConversation(ctx context.Context, conversationID string) (ConversationInfo, error) {
	resp, err := c.agent.GetConversation(ctx, &proto.GetConversationRequest{ConversationId: conversationID})
	if err != nil {
		return ConversationInfo{}, err
	}
	return ConversationInfo{
		ID:             resp.GetId(),
		Title:          resp.GetTitle(),
		ProjectDir:     resp.GetProjectDir(),
		Model:          resp.GetModel(),
		StartedAt:      time.Unix(resp.GetStartedAt(), 0),
		LastTurnAt:     time.Unix(resp.GetLastTurnAt(), 0),
		TurnCount:      int(resp.GetTurnCount()),
		Recap:          resp.GetRecap(),
		RecapUpdatedAt: time.Unix(resp.GetRecapUpdatedAt(), 0),
	}, nil
}
```

- [ ] **Step 4: Build**

Run: `cd source/server && go build ./internal/cli/agentclient/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/cli/agentclient/client.go
git commit -m "feat(cli): agentclient GetConversation + recap fields"
```

---

### Task 7: Wiring — construct the generator and attach it

**Files:**
- Modify: `source/server/cmd/cercano/main.go`

**Interfaces:**
- Consumes: `recap.New`, `recap.CompleteFunc`, `agent.WithRecapScheduler`, the local provider, `persistentStore`.

- [ ] **Step 1: Build the generator and pass the option**

In `cmd/cercano/main.go`, locate the block that opens the store (line ~159) and builds the agent with `agent.WithPersistentStore(persistentStore)` (line ~178). The local provider used elsewhere in this file is an `*llm.LocalModelProvider` — identify its variable (search `NewLocalModelProvider` in this file; call it `localProvider`).

Immediately before the `agent.NewAgent(...)` call, add (only when a persistent store exists):

```go
	var recapGen *recap.Generator
	if persistentStore != nil {
		recapComplete := func(ctx context.Context, prompt string) (string, error) {
			resp, err := localProvider.Process(ctx, &agent.Request{Input: prompt})
			if err != nil {
				return "", err
			}
			return resp.Output, nil
		}
		recapGen = recap.New(persistentStore, recapComplete, 8*time.Second, 12)
	}
```

Then add the option to the `agent.NewAgent(...)` option list (guarded so a nil generator isn't attached):

```go
	agentOpts := []agent.AgentOption{
		agent.WithPersistentStore(persistentStore),
		// ...keep the other existing options here unchanged...
	}
	if recapGen != nil {
		agentOpts = append(agentOpts, agent.WithRecapScheduler(recapGen))
	}
	orchestrator := agent.NewAgent(lazyRouter, coordinator, agentOpts...)
```

(Adapt to the exact existing option list at line ~178–180; move the inline options into `agentOpts`. Add imports: `cercano/source/server/internal/recap`, `time` if not already imported. `agent` and `llm` are already imported in this file.)

- [ ] **Step 2: Build**

Run: `cd source/server && go build ./...`
Expected: PASS.

- [ ] **Step 3: Smoke test generation end-to-end (manual, local model required)**

Run (requires Ollama running with the configured local model):
```bash
cd source/server && go run ./cmd/cercano
```
Send two messages, wait ~10s, quit, then:
```bash
sqlite3 ~/.config/cercano/conversations.db "SELECT recap FROM conversations ORDER BY last_turn_at DESC LIMIT 1;"
```
Expected: a non-empty one-line recap. (If Ollama isn't available, skip — confirm no crash and recap stays empty.)

- [ ] **Step 4: Commit**

```bash
git add source/server/cmd/cercano/main.go
git commit -m "feat(cli): wire living-recap generator into agent"
```

---

### Task 8: TUI — recap footer + turn-boundary refresh

**Files:**
- Modify: `source/server/internal/cli/ui/model.go`

**Interfaces:**
- Consumes: `agentclient.Client.GetConversation`.
- Produces: `Model.recap string`, `recapLoadedMsg`, `fetchRecap`, footer render.

- [ ] **Step 1: Add the model field**

In the `Model` struct definition (search `type Model struct`), add:

```go
	recap string // living one-line work summary; shown in the chat footer
```

- [ ] **Step 2: Add the message type and fetch command**

Near `fetchContextUsage` (model.go ~265), add:

```go
type recapLoadedMsg struct{ recap string }

// fetchRecap asks the agent for the conversation's latest living recap.
func fetchRecap(ag *agentclient.Client, convID string) tea.Cmd {
	return func() tea.Msg {
		if convID == "" {
			return recapLoadedMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		info, err := ag.GetConversation(ctx, convID)
		if err != nil {
			return recapLoadedMsg{}
		}
		return recapLoadedMsg{recap: info.Recap}
	}
}
```

(`context`, `time`, `tea`, `agentclient` are already imported in model.go.)

- [ ] **Step 3: Batch the recap fetch on turn end + handle the message**

In the `streamEndMsg` case, change the return to also fetch the recap:

```go
		return m, tea.Batch(fetchContextUsage(m.agent, m.convID), fetchRecap(m.agent, m.convID))
```

Add a new case in `Update` (next to `ctxUsageMsg`):

```go
	case recapLoadedMsg:
		m.recap = msg.recap
		return m, nil
```

- [ ] **Step 4: Render the footer in the default (no-overlay) view**

In `View()`, in the `switch` that renders the main body, change the `default` branch so the recap sits at the bottom of the chat area:

```go
	default:
		parts = append(parts, m.viewport.View())
		if m.recap != "" {
			parts = append(parts, m.renderRecap())
		}
```

Add the render helper near `renderStatus` / `renderHeader`:

```go
// renderRecap draws the living one-line work summary at the bottom of the
// chat area, dimmed and truncated to terminal width. Only rendered in the
// default (no-overlay) view.
func (m Model) renderRecap() string {
	label := m.styles.Muted.Render("recap ")
	avail := m.width - lipgloss.Width(label)
	if avail < 8 {
		return ""
	}
	text := m.recap
	if lipgloss.Width(text) > avail {
		r := []rune(text)
		if len(r) > avail-1 {
			text = string(r[:avail-1]) + "…"
		}
	}
	return label + m.styles.BorderDim.Render(text)
}
```

(`lipgloss` is already imported in model.go. If `m.styles` lacks `Muted`/`BorderDim`, use the closest existing dim styles seen in `renderStatus`.)

- [ ] **Step 5: Build + run**

Run: `cd source/server && go build ./...`
Expected: PASS. Then run the CLI, send a message, and after the turn ends confirm a `recap …` line appears at the bottom of the chat area and disappears when the history overlay (`/history`) opens.

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/cli/ui/model.go
git commit -m "feat(cli): living recap footer with turn-boundary refresh"
```

---

### Task 9: TUI — history picker recap + resume banner

**Files:**
- Modify: `source/server/internal/cli/ui/history_picker.go`
- Modify: `source/server/internal/cli/ui/model.go` (`applyResume`)

**Interfaces:**
- Consumes: `ConversationInfo.Recap`, `agentclient.Client.GetConversation`.

- [ ] **Step 1: Use recap as the picker row summary when present**

In `history_picker.go` `buildHistoryRows`, replace the `summary` construction so recap (when present) leads, with turn count + time as the meta line. Change the per-conversation loop body:

```go
		meta := fmt.Sprintf("%d turn", c.TurnCount)
		if c.TurnCount != 1 {
			meta += "s"
		}
		meta += " · " + relativeTime(c.LastTurnAt)
		if c.Model != "" {
			meta += " · " + c.Model
		}
		summary := meta
		if c.Recap != "" {
			summary = c.Recap + "  ·  " + meta
		}
```

(Leave the `overlay.Row{...}` append using `summary` as before.)

- [ ] **Step 2: Print a recap banner on resume**

In `model.go` `applyResume` (search `func (m Model) applyResume`), after the turns are loaded and scrollback is rebuilt and `m.convID = conversationID` is set, fetch the recap synchronously and prepend a banner entry. Add:

```go
	// Surface the prior session's living recap as a one-line banner.
	rctx, rcancel := context.WithTimeout(context.Background(), 3*time.Second)
	if info, err := m.agent.GetConversation(rctx, conversationID); err == nil && info.Recap != "" {
		m.recap = info.Recap
		m.appendSystemLine("Recap: " + info.Recap) // use the existing scrollback/system-line helper
	}
	rcancel()
```

NOTE: replace `m.appendSystemLine(...)` with whatever helper `applyResume` already uses to add a line to scrollback (inspect the surrounding code — e.g. appending to `m.entries` then `m.refreshViewport()`). If no helper exists, append a system entry the same way the function renders resumed turns, then call `m.refreshViewport()`.

- [ ] **Step 3: Build + run**

Run: `cd source/server && go build ./...`
Expected: PASS. Run CLI, `/history`, confirm rows show the recap; select one, confirm a `Recap: …` banner prints and the footer shows the recap.

- [ ] **Step 4: Commit**

```bash
git add source/server/internal/cli/ui/
git commit -m "feat(cli): recap in history picker + resume banner"
```

---

## Self-Review

**Spec coverage:**
- Data model (recap column, UpdateRecap, Get, Info fields) → Task 1. ✓
- Proto recap + GetConversation → Task 3. ✓
- Generation server-side, debounced, incremental, local-only → Task 2 (generator) + Task 4 (Schedule hook) + Task 7 (wiring with local provider). ✓
- Footer (default view only, truncated) → Task 8. ✓
- Turn-boundary + (implicit) overlay-close refresh → Task 8 (footer persists in `m.recap`, reappears when overlay closes; refresh fires on turn end). ✓
- History picker recap-or-title → Task 9. ✓
- Resume banner → Task 9. ✓
- Error handling (failure keeps prior recap; never blocks turn — Schedule is fire-and-forget; missing recap renders nothing) → Tasks 2, 8. ✓
- Tests (store, generator debounce/failure, agent hook) → Tasks 1, 2, 4. ✓

**Notes / minor deviations from the design spec:**
- "Refresh when an overlay closes" is satisfied passively: `m.recap` is retained, so the footer redraws on the next default render after the overlay closes. No extra fetch needed.
- The design mentioned a CLI footer/picker test; bubbletea view tests for the footer are optional polish and folded into manual run steps to keep tasks shippable. Add a `renderRecap` truncation unit test if desired.
- `recap_updated_at` is stored and plumbed through proto/client but not yet displayed; reserved for a future "updated 2m ago" affordance.

**Placeholder scan:** No TBD/TODO; every code step shows real code. The two "inspect surrounding code" notes (Task 7 option list, Task 9 scrollback helper) are unavoidable adaptations to existing local code, with explicit fallback instructions.

**Type consistency:** `Schedule(conversationID string)` matches between `agent.RecapScheduler`, `recap.Generator`, and the `fakeRecap` test double. `CompleteFunc`, `Store` subset, `Info.Recap`/`RecapUpdatedAt`, and the proto getters (`GetRecap`, `GetRecapUpdatedAt`) are consistent across tasks.
