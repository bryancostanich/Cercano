# Reusable `chatPane` (Phase 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A reusable `chatPane` agent-chat component (message log + live status line + confirm gate, driven by a pluggable `ChatDriver`), with a context-manager driver as the first consumer, wired into `/c` so context edits feel like a real agent chat.

**Architecture:** `chatPane` owns entries/status/scroll and renders driver-emitted `chatPaneMsg` events with the existing chat UX (reusing `Entry`/`renderEntry`/`animateSpinnerGlyph`/`animateLimeSweep`/`confirmRequest`). A `ChatDriver.Submit` returns a `tea.Cmd` that emits those events. The context-manager driver wraps `ProposeContextEdit`/`DeleteConversationTurns`. The `/c` page hosts the turns context + the pane, fed by the main prompt bar.

**Tech Stack:** Go, Bubble Tea v2, the existing `internal/ui` package.

## Global Constraints

- CLI module only: `cd source/clients/cli && go build ./... && go test ./... -count=1`.
- Reuse, don't copy: status line uses `animateSpinnerGlyph`/`animateLimeSweep`; messages use the existing `Entry` type + `renderEntry`-style rendering; the confirm is the existing `confirmRequest` gate raised on the `Model` (the pane does not own `m.pendingConfirm`).
- The event protocol must stay agent-agnostic (no context-edit-specific fields in `chatPane`/`ChatDriver`/`chatPaneMsg`) so a future main-agent driver can reuse it. Context-edit specifics live only in the context-manager driver.
- One in-flight exchange per pane (ignore `Submit` while busy) in v1.
- Commit messages MUST NOT contain the word "Claude". No Co-Authored-By trailer.

Reference shapes (already defined):

```go
// internal/ui/model.go
type Entry struct { Role Role; Content string; Streaming bool; Status string; Tool *ToolEntry }
func animateSpinnerGlyph() string ; func animateLimeSweep(text string) string
func progressAnimTick() tea.Cmd   // tea.Tick 50ms → progressAnimTickMsg
type confirmRequest struct { onYes, onNo func(Model)(Model,tea.Cmd); extras map[string]func(Model)(Model,tea.Cmd) }
// RoleUser, RoleAssistant, RoleSystem ; m.pendingConfirm *confirmRequest ; resolveConfirmKey
// context_view.go: contextView{ ..., showingProposal bool }, applyProposal, cancelProposal, markedForDelete,
//   proposeCmd/deleteCmd (to be replaced by the pane), loadContextSnapshot
// agentclient: ProposeContextEdit(ctx,convID,instr)(Proposal,error) ; DeleteConversationTurns(ctx,convID,ids)(int,error)
//   Proposal{ DeleteIDs []string; Rationale string }
```

---

### Task 1: `chatPane` core + `ChatDriver` + event protocol

The reusable component, agent-agnostic, tested with a fake driver.

**Files:**
- Create: `source/clients/cli/internal/ui/chatpane.go`
- Test: `source/clients/cli/internal/ui/chatpane_test.go`

**Interfaces:**
- Produces: `ChatDriver` interface; the `chatPaneMsg` event types; `chatPane` with `Submit`, `Apply`, `View`, `Busy`, and status fields.

- [ ] **Step 1: Write the failing test**

Create `chatpane_test.go`:

```go
package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"cercano/source/clients/cli/internal/theme"
)

// fakeDriver emits a scripted single status then an assistant message via cmds.
type fakeDriver struct{ name string }

func (d fakeDriver) Name() string { return d.name }
func (d fakeDriver) Submit(_ context.Context, input string) tea.Cmd {
	return func() tea.Msg { return chatAssistantMsg{text: "echo: " + input} }
}

func newTestPane() *chatPane {
	return newChatPane(fakeDriver{name: "tester"}, theme.NewStyles(theme.Cracker()), theme.Cracker(), 80, 12)
}

func TestChatPane_SubmitAppendsUserAndBusy(t *testing.T) {
	p := newTestPane()
	cmd := p.Submit("hello")
	if cmd == nil { t.Error("Submit should return the driver cmd") }
	if !p.Busy() { t.Error("pane should be busy after Submit") }
	if len(p.entries) != 1 || p.entries[0].Role != RoleUser || p.entries[0].Content != "hello" {
		t.Errorf("expected one user entry, got %+v", p.entries)
	}
}

func TestChatPane_ApplyAssistantAndDone(t *testing.T) {
	p := newTestPane()
	p.Submit("hi")
	p.Apply(chatAssistantMsg{text: "echo: hi"})
	p.Apply(chatDoneMsg{})
	if p.Busy() { t.Error("done should clear busy") }
	out := p.View()
	if !strings.Contains(out, "echo: hi") { t.Errorf("assistant text missing:\n%s", out) }
}

func TestChatPane_StatusShownWhileBusy(t *testing.T) {
	p := newTestPane()
	p.Submit("x")
	p.Apply(chatStatusMsg{activity: "thinking…"})
	if !strings.Contains(p.View(), "thinking…") { t.Error("busy status not rendered") }
}

func TestChatPane_ErrorClearsBusyAndShows(t *testing.T) {
	p := newTestPane()
	p.Submit("x")
	p.Apply(chatErrorMsg{err: errString("boom")})
	if p.Busy() { t.Error("error should clear busy") }
	if !strings.Contains(p.View(), "boom") { t.Error("error text not shown") }
}

type errString string
func (e errString) Error() string { return string(e) }

func TestChatPane_QueuesWhileBusyAndDrains(t *testing.T) {
	p := newTestPane()
	p.Submit("first")              // starts; busy
	if p.Submit("second") != nil { // busy → enqueue, returns nil
		t.Error("submit while busy should enqueue (nil cmd), not start")
	}
	if len(p.queued) != 1 || p.queued[0] != "second" {
		t.Fatalf("queue = %v, want [second]", p.queued)
	}
	if !strings.Contains(p.View(), "second") {
		t.Error("queued message should render")
	}
	// ending the first exchange auto-drains "second" (returns its start cmd)
	cmd := p.Apply(chatDoneMsg{})
	if cmd == nil { t.Error("done with a queued msg should return the drain cmd") }
	if len(p.queued) != 0 { t.Errorf("queue should be empty after drain, got %v", p.queued) }
	if !p.Busy() { t.Error("draining the next message should make the pane busy again") }
	// last queued can be unstaged
	p2 := newTestPane()
	p2.Submit("a"); p2.Submit("b") // a starts, b queued
	if got, ok := p2.unstageLastQueued(); !ok || got != "b" || len(p2.queued) != 0 {
		t.Errorf("unstage = (%q,%v), queue=%v", got, ok, p2.queued)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestChatPane -count=1`
Expected: FAIL — `chatPane`/`newChatPane`/event types undefined.

- [ ] **Step 3: Implement `chatpane.go`**

Create `source/clients/cli/internal/ui/chatpane.go`:

```go
package ui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

// ChatDriver plugs an agent into a chatPane. Submit returns a tea.Cmd that emits
// chatPaneMsg events (chatStatusMsg / chatAssistantMsg / chatConfirmMsg /
// chatDoneMsg / chatErrorMsg). The pane is agent-agnostic; all agent specifics
// live in the driver.
type ChatDriver interface {
	Name() string
	Submit(ctx context.Context, input string) tea.Cmd
}

// chatPaneMsg is the closed set of events a driver emits. They are top-level
// tea.Msg values routed by the model to the active pane.
type chatStatusMsg struct{ activity string }
type chatAssistantMsg struct{ text string }
type chatDoneMsg struct{ text string } // optional closing line; clears busy
type chatErrorMsg struct{ err error }

// chatConfirmMsg asks the host to raise the shared confirm gate. onYes/onNo are
// the driver's follow-up cmds (e.g. perform the delete, or cancel). The pane
// renders `assistant` as the agent's message; the MODEL raises the confirm
// (it owns m.pendingConfirm) — see model.go routing.
type chatConfirmMsg struct {
	assistant string
	onYes     tea.Cmd
	onNo      tea.Cmd
}

type chatPane struct {
	driver  ChatDriver
	styles  theme.Styles
	palette theme.Palette
	width   int
	height  int

	entries []*Entry
	busy    bool
	activity string
	started  time.Time
	queued   []string // FIFO; messages submitted while busy (mirrors main chat d808952)
	scrollOffset int
}

func newChatPane(d ChatDriver, s theme.Styles, p theme.Palette, w, h int) *chatPane {
	return &chatPane{driver: d, styles: s, palette: p, width: w, height: h}
}

func (c *chatPane) Busy() bool { return c.busy }

func (c *chatPane) SetSize(w, h int) { c.width = w; c.height = h }

// Submit appends the user message, marks the pane busy, and returns the driver's
// cmd batched with the animation tick. While busy it enqueues (FIFO) instead.
func (c *chatPane) Submit(input string) tea.Cmd {
	if c.busy {
		c.queued = append(c.queued, input)
		return nil
	}
	c.entries = append(c.entries, &Entry{Role: RoleUser, Content: input})
	c.busy = true
	c.activity = "working…"
	c.started = time.Now()
	ctx := context.Background()
	return tea.Batch(c.driver.Submit(ctx, input), progressAnimTick())
}

// Apply mutates pane state for a driver event and returns any follow-up cmd
// (notably auto-draining the next queued message when an exchange ends).
// chatConfirmMsg is handled by the model (it raises the confirm gate).
func (c *chatPane) Apply(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case chatStatusMsg:
		c.activity = m.activity
	case chatAssistantMsg:
		c.entries = append(c.entries, &Entry{Role: RoleAssistant, Content: m.text})
	case chatDoneMsg:
		if m.text != "" {
			c.entries = append(c.entries, &Entry{Role: RoleSystem, Content: m.text})
		}
		c.busy = false
		return c.drainNext()
	case chatErrorMsg:
		c.entries = append(c.entries, &Entry{Role: RoleSystem, Content: c.styles.Error.Render("error: " + m.err.Error())})
		c.busy = false
		return c.drainNext()
	}
	return nil
}

// drainNext pops and submits the oldest queued message after an exchange ends.
func (c *chatPane) drainNext() tea.Cmd {
	if len(c.queued) == 0 {
		return nil
	}
	next := c.queued[0]
	c.queued = c.queued[1:]
	return c.Submit(next) // busy is false here, so this starts the exchange
}

// unstageLastQueued pops the most-recently-queued message off the queue and
// returns it (for the host to put back into the prompt for editing). Returns
// "", false when the queue is empty. Mirrors the main chat.
func (c *chatPane) unstageLastQueued() (string, bool) {
	n := len(c.queued)
	if n == 0 {
		return "", false
	}
	last := c.queued[n-1]
	c.queued = c.queued[:n-1]
	return last, true
}

// clearQueue drops all pending messages (cancel/esc).
func (c *chatPane) clearQueue() { c.queued = nil }

// appendAssistant is used by the model when it handles chatConfirmMsg, so the
// agent's pre-confirm message shows in the log.
func (c *chatPane) appendAssistant(text string) {
	if text != "" {
		c.entries = append(c.entries, &Entry{Role: RoleAssistant, Content: text})
	}
}

func (c *chatPane) clearBusy() { c.busy = false }

// View renders the message log plus, while busy, the animated status line.
func (c *chatPane) View() string {
	var b strings.Builder
	for _, e := range c.entries {
		role := c.styles.Muted.Render(string(e.Role) + ": ")
		b.WriteString(role + e.Content + "\n")
	}
	if c.busy {
		line := c.activity + "  ·  " + time.Since(c.started).Truncate(time.Second).String()
		b.WriteString(animateSpinnerGlyph() + " " + animateLimeSweep(line) + "\n")
	}
	for _, q := range c.queued {
		b.WriteString(c.styles.Muted.Render("⏳ " + q) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
```

(The `View` rendering here is intentionally minimal/legible; in Task 3 you may swap the per-entry rendering to the richer `renderEntry` style to match the main chat exactly. Keep the status line using `animateSpinnerGlyph`/`animateLimeSweep` — that is the "same UX" requirement. If `theme.Styles` lacks `Error`/`Muted`, use the fields that exist.)

- [ ] **Step 4: Run the tests + build**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestChatPane -count=1 -v && go build ./...`
Expected: PASS; clean build.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/chatpane.go source/clients/cli/internal/ui/chatpane_test.go
git commit -m "feat(cli): reusable chatPane + ChatDriver agent-chat component"
```

---

### Task 2: `contextManagerDriver`

The first `ChatDriver` — wraps the context-edit RPCs and emits the event sequence.

**Files:**
- Create: `source/clients/cli/internal/ui/context_manager_driver.go`
- Test: `source/clients/cli/internal/ui/context_manager_driver_test.go`

**Interfaces:**
- Consumes: `ChatDriver`, the `chatPaneMsg` events (Task 1); `agentclient.ProposeContextEdit`/`DeleteConversationTurns`.
- Produces: `contextManagerDriver{ agent, convID }` implementing `ChatDriver`; a `reloadFn func()` hook the `/c` page sets to refresh its turns after a delete.

- [ ] **Step 1: Write the failing test**

Create `context_manager_driver_test.go`:

```go
package ui

import (
	"context"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// The driver's Submit returns a cmd; running it should yield a chatConfirmMsg
// carrying the rationale + onYes/onNo cmds when the (fake) propose succeeds.
// We test the proposal mapping by invoking the driver's internal propose path
// with a stub. Since agentclient calls need a server, this test exercises the
// pure mapping function the driver uses.

func TestContextManagerDriver_ProposalToConfirm(t *testing.T) {
	d := &contextManagerDriver{}
	msg := d.proposalToMsg(agentclient.Proposal{DeleteIDs: []string{"a"}, Rationale: "removed tangent"}, nil)
	cm, ok := msg.(chatConfirmMsg)
	if !ok { t.Fatalf("want chatConfirmMsg, got %T", msg) }
	if cm.assistant == "" || cm.onYes == nil || cm.onNo == nil {
		t.Errorf("confirm msg incomplete: %+v", cm)
	}
}

func TestContextManagerDriver_ProposalError(t *testing.T) {
	d := &contextManagerDriver{}
	msg := d.proposalToMsg(agentclient.Proposal{}, errString("nope"))
	if _, ok := msg.(chatErrorMsg); !ok {
		t.Fatalf("want chatErrorMsg on error, got %T", msg)
	}
}

func TestContextManagerDriver_EmptyProposalDone(t *testing.T) {
	d := &contextManagerDriver{}
	msg := d.proposalToMsg(agentclient.Proposal{DeleteIDs: nil, Rationale: "nothing"}, nil)
	if dm, ok := msg.(chatDoneMsg); !ok || dm.text == "" {
		t.Fatalf("want chatDoneMsg with text on empty proposal, got %T", msg)
	}
}

var _ = theme.Cracker // keep import if unused otherwise
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestContextManagerDriver -count=1`
Expected: FAIL — `contextManagerDriver`/`proposalToMsg` undefined.

- [ ] **Step 3: Implement the driver**

Create `context_manager_driver.go`:

```go
package ui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

// contextManagerDriver is the first ChatDriver: it edits the conversation's
// context via ProposeContextEdit (→ a confirm) and DeleteConversationTurns.
type contextManagerDriver struct {
	agent  *agentclient.Client
	convID string
	// onDeleted is set by the /c page so it can reload its turns list and mark
	// proposals after a delete.
	onDeleted func(ids []string)
	// mark/unmark let the driver tell the /c page which turns a live proposal
	// targets (so they render with ✗). Optional.
	mark   func(ids []string)
	unmark func()
}

func (d *contextManagerDriver) Name() string { return "context manager" }

func (d *contextManagerDriver) Submit(ctx context.Context, input string) tea.Cmd {
	ag, convID := d.agent, d.convID
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		p, err := ag.ProposeContextEdit(c, convID, input)
		return d.proposalToMsg(p, err)
	}
}

// proposalToMsg maps a propose result to the right pane event. Pure (no I/O) so
// it is unit-testable.
func (d *contextManagerDriver) proposalToMsg(p agentclient.Proposal, err error) tea.Msg {
	if err != nil {
		return chatErrorMsg{err: err}
	}
	if len(p.DeleteIDs) == 0 {
		return chatDoneMsg{text: "nothing to remove."}
	}
	ids := p.DeleteIDs
	return chatConfirmMsg{
		assistant: p.Rationale + fmt.Sprintf("  (will remove %d turn(s) — y/n)", len(ids)),
		onYes:     d.deleteCmd(ids),
		onNo:      func() tea.Msg { return chatDoneMsg{text: "kept everything."} },
	}
}

func (d *contextManagerDriver) deleteCmd(ids []string) tea.Cmd {
	ag, convID := d.agent, d.convID
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		n, err := ag.DeleteConversationTurns(c, convID, ids)
		if err != nil {
			return chatErrorMsg{err: err}
		}
		if d.onDeleted != nil {
			d.onDeleted(ids)
		}
		return chatDoneMsg{text: fmt.Sprintf("removed %d turn(s).", n)}
	}
}

var _ = theme.Cracker
```

(Remove the `theme` import + `var _` if unused after implementation. The "status while working" comes from the pane's `busy` line set in `Submit`; if you want the activity text to change to "analyzing context…" specifically, have the driver's `Submit` cmd FIRST return a `chatStatusMsg{activity:"analyzing context…"}` and chain the propose — but a single busy line is acceptable for v1. Keep it simple unless review asks.)

- [ ] **Step 4: Run tests + build**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestContextManagerDriver -count=1 -v && go build ./...`
Expected: PASS; clean build.

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/context_manager_driver.go source/clients/cli/internal/ui/context_manager_driver_test.go
git commit -m "feat(cli): context-manager ChatDriver over the edit RPCs"
```

---

### Task 3: Wire the pane into `/c`

Embed a `chatPane` in `contextView`, feed it from the prompt bar, route events, and render the chat log + status alongside the turns.

**Files:**
- Modify: `source/clients/cli/internal/ui/context_view.go`
- Modify: `source/clients/cli/internal/ui/model.go` (prompt-bar submit for `/c`; route `chat*` msgs)
- Test: `source/clients/cli/internal/ui/context_view_route_test.go`

**Interfaces:**
- Consumes: `chatPane` (Task 1); `contextManagerDriver` (Task 2); the existing `handleContextViewKey`, `m.pendingConfirm`/`confirmRequest`.

- [ ] **Step 1: Write the failing test**

Add to `context_view_route_test.go`:

```go
func TestContextView_PaneSubmitFromPromptBar(t *testing.T) {
	m := modelWithContextView() // existing helper
	cv := m.content.(*contextView)
	m.input.SetValue("drop the tangent")
	next, cmd := m.handleContextViewKey(cv, keyEnter()) // existing key helper
	if cmd == nil { t.Error("enter should submit to the pane (a cmd)") }
	if !cv.pane.Busy() { t.Error("pane should be busy after submit") }
	if next.input.Value() != "" { t.Errorf("input not cleared: %q", next.input.Value()) }
}

func TestContextView_ChatConfirmRaisesGate(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	m2, _ := m.routeChatMsg(chatConfirmMsg{assistant: "r", onYes: func() tea.Msg { return chatDoneMsg{} }, onNo: func() tea.Msg { return chatDoneMsg{} }})
	if m2.pendingConfirm == nil { t.Error("chatConfirmMsg should raise the confirm gate") }
	// the rationale should be in the pane log
	found := false
	for _, e := range cv.pane.entries { if e.Content == "r" { found = true } }
	if !found { t.Error("assistant rationale not appended to the pane") }
}
```

(Use the project's real key-message + helper construction — reconcile `keyEnter()`/`modelWithContextView()` against the existing `context_view_route_test.go`.)

- [ ] **Step 2: Run to verify it fails**

Run: `cd source/clients/cli && go test ./internal/ui/ -run 'TestContextView_PaneSubmit|TestContextView_ChatConfirm' -count=1`
Expected: FAIL — `cv.pane`/`routeChatMsg` undefined.

- [ ] **Step 3: Embed the pane in `contextView`**

In `context_view.go`:
- Add `pane *chatPane` and a `driver *contextManagerDriver` to the struct.
- In `newContextView`, build the driver (`&contextManagerDriver{agent: ag, convID: convID, onDeleted: func(ids []string){ cv.snapshot = loadContextSnapshot(ag, convID); cv.cancelProposal() }, mark: cv.applyProposalIDs, unmark: cv.cancelProposal}`) and `cv.pane = newChatPane(cv.driver, s, p, w, h)`. (Add a small `applyProposalIDs(ids []string)` that sets `proposal.DeleteIDs`+`showingProposal` for marking.)
- `View`/`fullContent`: append the pane's `View()` below the turns list (and the rationale/marks come from the proposal state). Keep the turns list as the top region; the pane (chat log + status) as the bottom region.
- Remove the now-unused `proposeCmd`/`deleteCmd`/`renderFooter` proposal-confirm bits that the pane+driver replace (keep `applyProposal`/`cancelProposal`/`markedForDelete` for turn marking).

- [ ] **Step 4: Route from the prompt bar + handle chat msgs in `model.go`**

In `handleContextViewKey`, change the `enter` branch to submit to the pane instead of `cv.proposeCmd`:

```go
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" { return m, nil }
		m.input.SetValue("")
		return m, cv.pane.Submit(text)
```

Add a `routeChatMsg` helper on `Model` and top-level cases for the chat events (next to the existing content-page async cases):

```go
func (m Model) routeChatMsg(msg tea.Msg) (Model, tea.Cmd) {
	cv, ok := m.content.(*contextView)
	if !ok { return m, nil }
	if cm, isConfirm := msg.(chatConfirmMsg); isConfirm {
		cv.pane.appendAssistant(cm.assistant)
		onYes, onNo := cm.onYes, cm.onNo
		m.pendingConfirm = &confirmRequest{
			onYes: func(m Model) (Model, tea.Cmd) { m.pendingConfirm = nil; return m, onYes },
			onNo:  func(m Model) (Model, tea.Cmd) { m.pendingConfirm = nil; cv.pane.clearBusy(); return m, onNo },
		}
		return m, progressAnimTick()
	}
	drain := cv.pane.Apply(msg) // may auto-submit the next queued message
	if cv.pane.Busy() {
		return m, tea.Batch(drain, progressAnimTick())
	}
	return m, drain
}
```

Top-level msg cases:

```go
	case chatStatusMsg, chatAssistantMsg, chatDoneMsg, chatErrorMsg, chatConfirmMsg:
		return m.routeChatMsg(msg)
```

Ensure `progressAnimTickMsg` already triggers a re-render while the pane is busy (the main chat's tick handler does this; the pane's animated line repaints on each tick — confirm the existing `progressAnimTickMsg` case re-renders and, if it only continues while `m.streaming`, extend it to also continue while a `*contextView` pane is busy).

**Queuing-key parity** (mirror the main chat in `handleContextViewKey`): the `enter` branch already enqueues via `cv.pane.Submit` when the pane is busy (no special-casing needed — `Submit` enqueues). Add two parity keys: on `up` with an empty input, call `cv.pane.unstageLastQueued()` and, if it returns a message, `m.input.SetValue(msg)` (pop the last queued back for editing); and have the `esc`-closes-the-page path also `cv.pane.clearQueue()` so closing `/c` drops pending messages. Keep these minimal — they match `d808952`'s `unstageLastQueued` / esc-drops-queue behavior.

- [ ] **Step 5: Run tests + build the CLI**

Run: `cd source/clients/cli && go test ./internal/ui/ -count=1 && go build ./... && go test ./... -count=1`
Expected: PASS (new pane-route tests + all prior UI tests); clean CLI build; full CLI suite green.

- [ ] **Step 6: Commit**

```bash
git add source/clients/cli/internal/ui/context_view.go source/clients/cli/internal/ui/model.go source/clients/cli/internal/ui/context_view_route_test.go
git commit -m "feat(cli): /c hosts a chatPane — context edits feel like a second agent chat"
```

---

## Self-Review

**Spec coverage:**
- §1 chatPane component (entries, status line, scroll, agent-agnostic) → Task 1.
- §2 ChatDriver + event protocol (status/assistant/confirm/done/error) → Task 1.
- §3 context-manager driver (propose→confirm→delete→done; error/empty) → Task 2.
- §4 /c integration (pane fed by prompt bar; turns + chat log; confirm via shared gate; reload after delete) → Task 3.
- §5 main-chat capability → interface kept agent-agnostic (no context-edit fields in pane/driver iface/events); not migrated (Phase 2).
- Error/edge (propose/delete error, empty proposal, busy-ignore, confirm gate) → Tasks 2 + 3.
- Testing → Tasks 1–3.

**Type consistency:** `ChatDriver`, `chatPane` (`Submit`/`Apply`/`Busy`/`appendAssistant`/`clearBusy`/`View`), the `chat*Msg` events, `contextManagerDriver` (`Submit`/`proposalToMsg`/`deleteCmd`/`onDeleted`), and the model `routeChatMsg`/`handleContextViewKey` are used identically across tasks.

**Placeholder scan:** code shown for every step. The "reconcile key-helper/renderEntry/theme-field names against real code" and "swap to richer renderEntry in Task 3 if desired" notes are deliberate reconciliations with the live UI (the implementer matches real names and the existing progress-tick re-render), not unresolved requirements. The richer per-entry rendering is explicitly optional for v1 — the binding requirement is the reused animated status line.
