# Context View Tab (`/c`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A read-only `/c` content page that shows the active conversation's turns (role, kind, preview, per-turn token estimate) and the exact total-vs-window usage.

**Architecture:** A new side-effect-free `GetConversationTurns` RPC returns display-ready turn summaries (server tokenizes each turn for a `≈` estimate). A new CLI content page copies the `runtimeDashboard` pattern (sync gRPC load, line-buffer scroll windowing) and is opened via `/c`.

**Tech Stack:** Go (modules `cercano/source/server` and `cercano/source/clients/cli`), gRPC/protobuf, Bubble Tea, `contextmeter` tokenizer.

## Global Constraints

- Two Go modules. Server: `cd source/server && go build ./... && go test ./... -count=1`. CLI: `cd source/clients/cli && go build ./... && go test ./... -count=1`.
- **Proto regen command** (verified to reproduce the checked-in stubs byte-for-byte). Run with `$GOPATH/bin` on PATH (`protoc-gen-go`, `protoc-gen-go-grpc` live there):
  ```bash
  export PATH="$PATH:$(go env GOPATH)/bin"
  cd source/proto && protoc \
    --go_out=../server/pkg/proto --go_opt=paths=source_relative \
    --go-grpc_out=../server/pkg/proto --go-grpc_opt=paths=source_relative \
    agent.proto
  ```
- **Read-only.** No `conversation.Store` mutation, no meter/session changes. `GetConversationTurns` must not alter `GetContextUsage` for the conversation.
- Per-turn tokens are an estimate (`contextmeter` cl100k proxy), shown with `≈`. The exact total comes from `GetContextUsage`.
- Commit messages MUST NOT contain the word "Claude" anywhere. No Co-Authored-By trailer.

Reference shapes (already defined — do not redefine):

```go
// internal/conversation/store.go
type Turn struct { ID, ConversationID, Role, Content, BlocksJSON string; TokensIn, TokensOut, LatencyMs int; CreatedAt time.Time }
// Store.GetTurns(ctx, convID) ([]Turn, error)  ;  server reaches it via s.agent.PersistentStore()

// internal/llm/messages.go
type Block struct { Type BlockType; Text, ToolUseID, ToolName string; ToolInput json.RawMessage; ToolUseRef, Content string; ... }
const ( BlockText BlockType="text"; BlockToolUse BlockType="tool_use"; BlockToolResult BlockType="tool_result" )

// internal/contextmeter/tokenizer.go
type Tokenizer interface { Count(s string) int }
func Default() Tokenizer       // cl100k proxy
func ModelMax(model string) int

// internal/ui (package-private helpers context_view.go may call directly):
// dashboardContentHeight(h int) int ; dashboardPanelWidth(w int) int
// scrollbarColumn(total, height, offset int) []rune ; countLines([]string) int ; clampInt/maxInt
// content_page.go: contentPageScrollState{Total,Height,Offset}; contentPageScroller{ScrollBy,ScrollTo,ScrollState}

// pkg/agentclient/client.go (existing): GetContextUsage(ctx, convID) (*ContextUsage, error)
//   type ContextUsage struct { TokensUsed, ModelMax int; Percent float64 }
```

---

### Task 1: `GetConversationTurns` RPC (server data path)

Add the RPC + messages, regenerate stubs, implement a side-effect-free handler that returns display-ready summaries, and add the agentclient wrapper.

**Files:**
- Modify: `source/proto/agent.proto`
- Regenerate: `source/server/pkg/proto/agent.pb.go`, `agent_grpc.pb.go`
- Create: `source/server/internal/server/context_turns.go` (handler + pure helper)
- Modify: `source/server/pkg/agentclient/client.go` (wrapper)
- Test: `source/server/internal/server/context_turns_test.go`

**Interfaces:**
- Produces (proto): `GetConversationTurns(GetConversationTurnsRequest{conversation_id}) → GetConversationTurnsResponse{ repeated ContextTurn turns }`; `ContextTurn{ role, kind, preview, est_tokens }`.
- Produces (Go): `func (s *Server) GetConversationTurns(ctx, *proto.GetConversationTurnsRequest) (*proto.GetConversationTurnsResponse, error)`; `func contextTurnView(t conversation.Turn, tok contextmeter.Tokenizer) *proto.ContextTurn`; `agentclient.ContextTurn{Role,Kind,Preview string; EstTokens int}` + `(*Client).GetConversationTurns(ctx, convID) ([]ContextTurn, error)`.

- [ ] **Step 1: Add the RPC + messages to the proto**

In `source/proto/agent.proto`, add to the `service Agent { ... }` block (next to `GetContextUsage`):

```proto
  // GetConversationTurns returns display-ready, side-effect-free summaries of a
  // conversation's turns for the /c context viewer. Unlike ResumeConversation
  // it does NOT re-hydrate server session state.
  rpc GetConversationTurns (GetConversationTurnsRequest) returns (GetConversationTurnsResponse) {}
```

And add the messages (near `GetContextUsageResponse`):

```proto
message GetConversationTurnsRequest  { string conversation_id = 1; }
message GetConversationTurnsResponse { repeated ContextTurn turns = 1; }
message ContextTurn {
  string role       = 1; // "user" | "assistant" | "system"
  string kind       = 2; // "text" | "tool_use" | "tool_result"
  string preview    = 3; // flattened, truncated, display-ready
  int32  est_tokens = 4; // contextmeter tokenizer estimate
}
```

- [ ] **Step 2: Regenerate the stubs**

Run the proto regen command from Global Constraints. Then confirm the new types exist:

Run: `cd source/server && go build ./pkg/proto/`
Expected: clean build; `proto.GetConversationTurnsRequest`, `proto.ContextTurn`, and the `AgentServer.GetConversationTurns` method now exist.

- [ ] **Step 3: Write the failing handler test**

Create `source/server/internal/server/context_turns.go` with a stub first so the package compiles for the test:

```go
package server

import (
	"context"

	"cercano/source/server/pkg/proto"
)

func (s *Server) GetConversationTurns(ctx context.Context, req *proto.GetConversationTurnsRequest) (*proto.GetConversationTurnsResponse, error) {
	return &proto.GetConversationTurnsResponse{}, nil // replaced in Step 5
}
```

Create `source/server/internal/server/context_turns_test.go`:

```go
package server

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

func TestGetConversationTurns_SummariesAndSideEffectFree(t *testing.T) {
	srv, store := newServerWithStore(t)
	ctx := context.Background()
	convID := "conv-view"
	if err := store.EnsureConversation(ctx, convID, "", "test-model"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	useJSON, _ := json.Marshal([]llm.Block{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "LS", ToolInput: json.RawMessage(`{"path":"."}`)}})
	resJSON, _ := json.Marshal([]llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "u1", Content: "a.go\nb.go"}})
	for _, tn := range []conversation.Turn{
		{ConversationID: convID, Role: "user", Content: "list the files please"},
		{ConversationID: convID, Role: "assistant", BlocksJSON: string(useJSON)},
		{ConversationID: convID, Role: "user", BlocksJSON: string(resJSON)},
	} {
		if err := store.Append(ctx, tn); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	resp, err := srv.GetConversationTurns(ctx, &proto.GetConversationTurnsRequest{ConversationId: convID})
	if err != nil {
		t.Fatalf("GetConversationTurns: %v", err)
	}
	if len(resp.Turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(resp.Turns))
	}
	if resp.Turns[0].Role != "user" || resp.Turns[0].Kind != "text" || resp.Turns[0].Preview == "" || resp.Turns[0].EstTokens <= 0 {
		t.Errorf("turn0 = %+v", resp.Turns[0])
	}
	if resp.Turns[1].Kind != "tool_use" || resp.Turns[1].Preview == "" {
		t.Errorf("turn1 kind/preview = %+v", resp.Turns[1])
	}
	if resp.Turns[2].Kind != "tool_result" {
		t.Errorf("turn2 kind = %q", resp.Turns[2].Kind)
	}

	// Side-effect-free: usage must be unchanged (still zero — no turn was run).
	used, _ := srv.agent.GetContextUsage(ctx, convID)
	if used != 0 {
		t.Errorf("GetConversationTurns mutated the meter: used = %d, want 0", used)
	}
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `cd source/server && go test ./internal/server/ -run TestGetConversationTurns -count=1`
Expected: FAIL — the stub returns an empty list (`turns = 0, want 3`).

- [ ] **Step 5: Implement the handler + pure helper**

Replace `context_turns.go` with:

```go
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

const contextTurnPreviewMax = 120

// GetConversationTurns returns side-effect-free, display-ready summaries of a
// conversation's turns for the /c context viewer. Reads the store only.
func (s *Server) GetConversationTurns(ctx context.Context, req *proto.GetConversationTurnsRequest) (*proto.GetConversationTurnsResponse, error) {
	out := &proto.GetConversationTurnsResponse{}
	if s.agent == nil {
		return out, nil
	}
	store := s.agent.PersistentStore()
	convID := req.GetConversationId()
	if store == nil || convID == "" {
		return out, nil
	}
	turns, err := store.GetTurns(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("get turns: %w", err)
	}
	tok := contextmeter.Default()
	for _, t := range turns {
		out.Turns = append(out.Turns, contextTurnView(t, tok))
	}
	return out, nil
}

// contextTurnView derives a display summary (kind, preview, token estimate) from
// a stored turn. Pure — no I/O. tool turns synthesize a label since their
// Content may be empty.
func contextTurnView(t conversation.Turn, tok contextmeter.Tokenizer) *proto.ContextTurn {
	kind := "text"
	preview := t.Content
	tokenSrc := t.Content

	if t.BlocksJSON != "" {
		var blocks []llm.Block
		if err := json.Unmarshal([]byte(t.BlocksJSON), &blocks); err == nil {
			tokenSrc = t.BlocksJSON
			for _, b := range blocks {
				switch b.Type {
				case llm.BlockToolUse:
					kind = "tool_use"
					preview = b.ToolName + " " + flattenPreview(string(b.ToolInput))
				case llm.BlockToolResult:
					kind = "tool_result"
					preview = "→ " + flattenPreview(b.Content)
				case llm.BlockText:
					if preview == "" {
						preview = b.Text
					}
				}
			}
		}
	}

	return &proto.ContextTurn{
		Role:      t.Role,
		Kind:      kind,
		Preview:   truncate(flattenPreview(preview), contextTurnPreviewMax),
		EstTokens: int32(tok.Count(tokenSrc)),
	}
}

func flattenPreview(s string) string { return strings.Join(strings.Fields(s), " ") }

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
```

(`flattenPreview`/`truncate` are local to this file — if the package already has identically-named helpers, build will report the redeclaration; rename these to `ctPreview`/`ctTruncate` if so.)

- [ ] **Step 6: Add the agentclient wrapper**

In `source/server/pkg/agentclient/client.go` (near `GetContextUsage`):

```go
// ContextTurn is one display-ready turn summary from GetConversationTurns.
type ContextTurn struct {
	Role      string
	Kind      string
	Preview   string
	EstTokens int
}

// GetConversationTurns returns side-effect-free turn summaries for the /c viewer.
func (c *Client) GetConversationTurns(ctx context.Context, conversationID string) ([]ContextTurn, error) {
	resp, err := c.agent.GetConversationTurns(ctx, &proto.GetConversationTurnsRequest{ConversationId: conversationID})
	if err != nil {
		return nil, err
	}
	out := make([]ContextTurn, 0, len(resp.GetTurns()))
	for _, t := range resp.GetTurns() {
		out = append(out, ContextTurn{
			Role:      t.GetRole(),
			Kind:      t.GetKind(),
			Preview:   t.GetPreview(),
			EstTokens: int(t.GetEstTokens()),
		})
	}
	return out, nil
}
```

(`c.agent` is the existing `proto.AgentClient` field used by `GetContextUsage` — confirm the field name in this file and match it.)

- [ ] **Step 7: Run tests + build**

Run: `cd source/server && go test ./internal/server/ -run TestGetConversationTurns -count=1 -v && go build ./...`
Expected: PASS; clean build (server module + pkg/agentclient).

- [ ] **Step 8: Commit**

```bash
git add source/proto/agent.proto source/server/pkg/proto/ source/server/internal/server/context_turns.go source/server/internal/server/context_turns_test.go source/server/pkg/agentclient/client.go
git commit -m "feat(server): GetConversationTurns RPC — side-effect-free context summaries"
```

---

### Task 2: CLI context-view content page

A `contentPage` that loads the snapshot synchronously and renders a usage header + windowed turn list. Not wired to `/c` yet (Task 3).

**Files:**
- Create: `source/clients/cli/internal/ui/context_view.go`
- Modify: `source/clients/cli/internal/ui/content_page.go` (add the ID const)
- Test: `source/clients/cli/internal/ui/context_view_test.go`

**Interfaces:**
- Consumes: `agentclient.GetConversationTurns`/`GetContextUsage` (Task 1); package-private `dashboardContentHeight`, `dashboardPanelWidth`, `scrollbarColumn`, `countLines`, `clampInt`, `maxInt`.
- Produces: `func newContextView(ag *agentclient.Client, p theme.Palette, s theme.Styles, convID string, w, h int) (*contextView, tea.Cmd)`; implements `contentPage` + `contentPageScroller`; `contentPageContext contentPageID = "context"`.

- [ ] **Step 1: Add the content-page ID**

In `content_page.go`, add to the `contentPageID` const block:

```go
	contentPageContext contentPageID = "context"
```

- [ ] **Step 2: Write the failing test**

Create `source/clients/cli/internal/ui/context_view_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func newTestContextView(snap contextSnapshot) *contextView {
	return &contextView{
		width: 80, height: 24,
		palette:  theme.Cracker(),
		styles:   theme.NewStyles(theme.Cracker()),
		convID:   "c1",
		snapshot: snap,
	}
}

func TestContextView_RendersTurnsAndTotal(t *testing.T) {
	cv := newTestContextView(contextSnapshot{
		Turns: []agentclient.ContextTurn{
			{Role: "user", Kind: "text", Preview: "hello there", EstTokens: 12},
			{Role: "assistant", Kind: "text", Preview: "hi back", EstTokens: 8},
		},
		Usage: &agentclient.ContextUsage{TokensUsed: 4321, ModelMax: 200000, Percent: 0.0216},
	})
	out := cv.View()
	for _, want := range []string{"hello there", "hi back", "4,321", "200,000"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q\n%s", want, out)
		}
	}
}

func TestContextView_EmptyAndNoConversation(t *testing.T) {
	if got := newTestContextView(contextSnapshot{}).View(); !strings.Contains(got, "context is empty") {
		t.Errorf("empty state: %q", got)
	}
	noConv := newTestContextView(contextSnapshot{})
	noConv.convID = ""
	if got := noConv.View(); !strings.Contains(got, "no conversation yet") {
		t.Errorf("no-conversation state: %q", got)
	}
}

func TestContextView_ScrollState(t *testing.T) {
	turns := make([]agentclient.ContextTurn, 100)
	for i := range turns {
		turns[i] = agentclient.ContextTurn{Role: "user", Kind: "text", Preview: "line", EstTokens: 1}
	}
	cv := newTestContextView(contextSnapshot{Turns: turns, Usage: &agentclient.ContextUsage{ModelMax: 1000}})
	st0 := cv.ScrollState()
	cv.ScrollBy(10)
	if cv.ScrollState().Offset <= st0.Offset {
		t.Errorf("ScrollBy did not advance offset: %d -> %d", st0.Offset, cv.ScrollState().Offset)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestContextView -count=1`
Expected: FAIL — `contextView`/`contextSnapshot`/`newContextView` undefined (compile error).

- [ ] **Step 4: Implement `context_view.go`**

Create `source/clients/cli/internal/ui/context_view.go`:

```go
package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

type contextSnapshot struct {
	Turns    []agentclient.ContextTurn
	TurnsErr error
	Usage    *agentclient.ContextUsage
	UsageErr error
}

type contextView struct {
	width, height int
	palette       theme.Palette
	styles        theme.Styles
	agent         *agentclient.Client
	convID        string
	snapshot      contextSnapshot
	scrollOffset  int
}

func loadContextSnapshot(ag *agentclient.Client, convID string) contextSnapshot {
	var snap contextSnapshot
	if ag == nil || convID == "" {
		return snap
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snap.Turns, snap.TurnsErr = ag.GetConversationTurns(ctx, convID)
	snap.Usage, snap.UsageErr = ag.GetContextUsage(ctx, convID)
	return snap
}

func newContextView(ag *agentclient.Client, p theme.Palette, s theme.Styles, convID string, w, h int) (*contextView, tea.Cmd) {
	cv := &contextView{palette: p, styles: s, agent: ag, convID: convID, width: w, height: h}
	cv.snapshot = loadContextSnapshot(ag, convID)
	return cv, nil
}

func (c *contextView) ID() contentPageID { return contentPageContext }

func (c *contextView) SetSize(w, h int) {
	c.width = w
	c.height = h
	c.clampScroll()
}

func (c *contextView) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "esc", "q":
		return nil, true
	case "r":
		c.snapshot = loadContextSnapshot(c.agent, c.convID)
		return nil, false
	case "up", "k":
		c.ScrollBy(-1)
	case "down", "j":
		c.ScrollBy(1)
	case "pgup", "ctrl+b":
		c.ScrollBy(-dashboardContentHeight(c.height))
	case "pgdown", "ctrl+f":
		c.ScrollBy(dashboardContentHeight(c.height))
	case "ctrl+u":
		c.ScrollBy(-maxInt(1, dashboardContentHeight(c.height)/2))
	case "ctrl+d":
		c.ScrollBy(maxInt(1, dashboardContentHeight(c.height)/2))
	}
	return nil, false
}

func (c *contextView) View() string {
	full, contentH := c.fullContent()
	return c.renderScrollableContent(full, contentH)
}

func (c *contextView) fullContent() (string, int) {
	var lines []string
	lines = append(lines, c.renderHeader())
	lines = append(lines, "")
	if c.convID == "" {
		lines = append(lines, c.styles.Muted.Render("no conversation yet"))
	} else if c.snapshot.TurnsErr != nil {
		lines = append(lines, c.styles.Error.Render("turns unavailable: "+c.snapshot.TurnsErr.Error()))
	} else if len(c.snapshot.Turns) == 0 {
		lines = append(lines, c.styles.Muted.Render("context is empty"))
	} else {
		for i, t := range c.snapshot.Turns {
			lines = append(lines, c.renderTurn(i, t))
		}
	}
	return strings.Join(lines, "\n"), dashboardContentHeight(c.height)
}

func (c *contextView) renderHeader() string {
	if c.snapshot.UsageErr != nil || c.snapshot.Usage == nil {
		return c.styles.Muted.Render("context  usage unavailable")
	}
	u := c.snapshot.Usage
	pct := int(u.Percent*100 + 0.5)
	bar := renderMeterBar(u.Percent, 10, c.styles)
	return fmt.Sprintf("%s  %s / %s  · %d%%  %s",
		c.styles.Bright.Render("context"),
		formatThousands(u.TokensUsed), formatThousands(u.ModelMax), pct, bar)
}

func (c *contextView) renderTurn(i int, t agentclient.ContextTurn) string {
	badge := c.styles.Info.Render("[" + t.Role + "]")
	switch t.Kind {
	case "tool_use", "tool_result":
		badge = c.styles.Muted.Render("[" + t.Kind + "]")
	}
	toks := c.styles.Muted.Render(fmt.Sprintf("≈%s", formatTokens(t.EstTokens)))
	return fmt.Sprintf("%s %s  %s", badge, toks, t.Preview)
}

// --- scroller (mirrors runtimeDashboard) ---

func (c *contextView) ScrollBy(delta int) { c.scrollOffset += delta; c.clampScroll() }
func (c *contextView) ScrollTo(offset int) { c.scrollOffset = offset; c.clampScroll() }
func (c *contextView) ScrollState() contentPageScrollState {
	full, contentH := c.fullContent()
	total := countLines([]string{full})
	return contentPageScrollState{Total: total, Height: contentH, Offset: clampInt(c.scrollOffset, 0, maxInt(0, total-contentH))}
}
func (c *contextView) clampScroll() { c.scrollOffset = c.ScrollState().Offset }

func (c *contextView) renderScrollableContent(full string, height int) string {
	if height < 1 {
		height = 1
	}
	lines := strings.Split(full, "\n")
	c.scrollOffset = clampInt(c.scrollOffset, 0, maxInt(0, len(lines)-height))
	panelW := dashboardPanelWidth(c.width)
	col := scrollbarColumn(len(lines), height, c.scrollOffset)
	var b strings.Builder
	for i := 0; i < height; i++ {
		line := ""
		if src := c.scrollOffset + i; src >= 0 && src < len(lines) {
			line = lines[src]
		}
		b.WriteString(ansi.Truncate(line, panelW, ""))
		b.WriteString(" ")
		if i < len(col) {
			switch col[i] {
			case '█':
				b.WriteString(c.styles.Border.Render("█"))
			case '░':
				b.WriteString(c.styles.BorderDim.Render("░"))
			default:
				b.WriteString(" ")
			}
		} else {
			b.WriteString(" ")
		}
		if i < height-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// --- small formatters ---

func formatTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func formatThousands(n int) string {
	s := fmt.Sprintf("%d", n)
	if n < 1000 {
		return s
	}
	var out []byte
	for i, ch := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, ch)
	}
	return string(out)
}

func renderMeterBar(pct float64, width int, s theme.Styles) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	filled := int(pct*float64(width) + 0.5)
	style := s.Accent
	switch {
	case pct >= 0.9:
		style = s.Error
	case pct >= 0.7:
		style = s.Warn
	}
	return style.Render(strings.Repeat("█", filled)) + s.Dim.Render(strings.Repeat("░", width-filled))
}
```

(If any of `formatThousands`/`formatTokens`/`renderMeterBar`/`maxInt`/`clampInt`/`countLines`/`dashboardContentHeight`/`dashboardPanelWidth`/`scrollbarColumn` already exist in the `ui` package, do NOT redeclare — delete the local copy and use the existing one. Build errors will name any collision; resolve by reusing.)

- [ ] **Step 5: Run the test + build**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestContextView -count=1 -v && go build ./...`
Expected: PASS (all three tests); clean build.

- [ ] **Step 6: Commit**

```bash
git add source/clients/cli/internal/ui/context_view.go source/clients/cli/internal/ui/content_page.go source/clients/cli/internal/ui/context_view_test.go
git commit -m "feat(cli): read-only context-view content page"
```

---

### Task 3: Wire `/c` to open the context view

**Files:**
- Modify: `source/clients/cli/internal/slash/registry.go` (add `ResultOpenContextView`)
- Create: `source/clients/cli/internal/slash/contextview.go` (the `/c` command)
- Modify: `source/clients/cli/internal/ui/model.go` (register the command; `runSlash` case)
- Test: `source/clients/cli/internal/slash/contextview_test.go`

**Interfaces:**
- Consumes: `newContextView` (Task 2); `ResultOpenContextView`.
- Produces: `RegisterContextView(r *Registry)`; `/c` → `Result{Kind: ResultOpenContextView}`.

- [ ] **Step 1: Add the result kind**

In `registry.go`, add to the `ResultKind` const block (after `ResultOpenRuntimeDashboard`):

```go
	ResultOpenContextView
```

- [ ] **Step 2: Write the failing slash test**

Create `source/clients/cli/internal/slash/contextview_test.go`:

```go
package slash

import "testing"

func TestSlash_C_OpensContextView(t *testing.T) {
	r := New()
	RegisterContextView(r)
	res, ok := r.Dispatch("/c")
	if !ok {
		t.Fatal("/c not dispatched")
	}
	if res.Kind != ResultOpenContextView {
		t.Errorf("kind = %v, want ResultOpenContextView", res.Kind)
	}
}
```

(Confirm the registry constructor/dispatch names against an existing slash test, e.g. `internal/slash/runtime_test.go` or `permissions_test.go` — match `New()`/`Dispatch` exactly; adapt if the helpers differ.)

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/slash/ -run TestSlash_C -count=1`
Expected: FAIL — `RegisterContextView` undefined.

- [ ] **Step 4: Implement the command**

Create `source/clients/cli/internal/slash/contextview.go`:

```go
package slash

// RegisterContextView installs the /c command, which opens the read-only
// context viewer for the active conversation.
func RegisterContextView(r *Registry) {
	r.Register(Command{
		Name: "c",
		Help: "Open the context viewer for the current conversation.",
		Handler: func(args []string) Result {
			return Result{Kind: ResultOpenContextView}
		},
	})
}
```

(Match the `Command` struct field set used by `internal/slash/runtime.go` — `Name`/`Help`/`Handler` with `Handler func([]string) Result`. If `runtime.go` differs, mirror it exactly.)

- [ ] **Step 5: Wire registration + runSlash in model.go**

In `model.go`, next to the other `slash.Register*` calls (e.g. `slash.RegisterRuntime(reg)`):

```go
	slash.RegisterContextView(reg)
```

In `runSlash`, next to the `ResultOpenRuntimeDashboard` case:

```go
	case slash.ResultOpenContextView:
		cv, cmd := newContextView(m.agent, m.palette, m.styles, m.convID, m.width, m.height)
		m.content = cv
		return m, cmd
```

(Confirm the conversation-id field name on the model — the history picker / streaming uses `m.convID`; match it. If `runSlash` returns `(tea.Model, tea.Cmd)` vs a different shape, match the neighboring case exactly.)

- [ ] **Step 6: Run tests + build the CLI**

Run: `cd source/clients/cli && go test ./internal/slash/ -run TestSlash_C -count=1 && go build ./... && go test ./... -count=1`
Expected: PASS; clean CLI build; full CLI module test suite green.

- [ ] **Step 7: Commit**

```bash
git add source/clients/cli/internal/slash/registry.go source/clients/cli/internal/slash/contextview.go source/clients/cli/internal/slash/contextview_test.go source/clients/cli/internal/ui/model.go
git commit -m "feat(cli): /c opens the read-only context viewer"
```

---

## Self-Review

**Spec coverage:**
- §1 GetConversationTurns RPC (proto, handler, side-effect-free, wrapper) → Task 1.
- §2 content page (load, contentPage, scroller, header meter, turn list) → Task 2.
- §3 wiring (ResultOpenContextView, /c command, runSlash) → Task 3.
- §4 error/empty states (no convID, turns error, empty, usage error) → Task 2 (`fullContent` branches; tests cover empty + no-conversation).
- §5 testing (server unit incl. side-effect-free, CLI unit incl. scroll/empty, slash) → Tasks 1-3.
- Out of scope (mutation/chat) → not in this plan.

**Type consistency:** `agentclient.ContextTurn{Role,Kind,Preview,EstTokens}`, `proto.ContextTurn{role,kind,preview,est_tokens}`, `contextSnapshot`, `newContextView(ag,p,s,convID,w,h)`, `contentPageContext`, `ResultOpenContextView`, `RegisterContextView` are used identically across tasks.

**Placeholder scan:** no TBD/TODO; every code step shows full code and exact commands. The "confirm field/helper name against existing file" notes are deliberate build-time reconciliations with existing code (the implementer matches the real names and resolves any redeclaration), not unresolved requirements.
