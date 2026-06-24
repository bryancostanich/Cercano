# `/c` Edit Rework — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Drive `/c` context edits from the main prompt bar (drop the embedded input), and factor `y`/`n` confirmation into a reusable `confirmRequest` primitive shared by the tool-permission gate and the context-edit delete.

**Architecture:** Generalize the tool-specific `pendingConfirm` into a generic `confirmRequest{onYes,onNo,extras}`; migrate the tool gate onto it. Slim `contextView` to a display surface. Route keystrokes to `m.input` while `/c` is active; enter → `ProposeContextEdit`; the proposal raises a `confirmRequest` for the delete.

**Tech Stack:** Go, Bubble Tea (`charm.land/bubbletea/v2`), the existing `internal/ui` model.

## Global Constraints

- CLI module only: `cd source/clients/cli && go build ./... && go test ./... -count=1`.
- Tool-permission confirm behavior must be UNCHANGED (same keys `y`/`n`/`d`, same Allow/Deny/invoke effects, same scrollback lines) — this is a refactor, not a behavior change, for that path.
- While `/c` is active the prompt bar is the only text input; `esc` (empty input) closes `/c`; `q` is a text character (NOT a close key) in this mode; `PgUp`/`PgDn`/`Ctrl+U`/`Ctrl+D` scroll the turn list.
- A pending confirm gates keys before content-page routing (existing model.go:655 order) — do not change that ordering.
- Commit messages MUST NOT contain the word "Claude". No Co-Authored-By trailer.

Reference shapes (already defined):

```go
// internal/ui/model.go
type pendingToolCall struct { ToolUseID, Name, Args, Permission string }      // keep as a render/transient holder
func (m Model) renderConfirmPrompt(p *pendingToolCall) string                  // keep
// raise sites: local /tool ~:1156 ; PermissionRequired stream ~:1289 ; resolver ~:1931
// keypress handler ~:652 ; content-page routing ~:670 ; submit(text) ~:981 ; preparePromptInput() exists
// agentclient: ProposeContextEdit(ctx, convID, instr) (Proposal, error) ; DeleteConversationTurns(ctx, convID, ids) (int, error)
// contextView (Task 4) currently has: input textinput.Model, mode contextViewMode (cvBrowse/cvEditing/cvProposal),
//   proposal agentclient.Proposal, applyProposal/cancelProposal/markedForDelete, proposeCmd/deleteCmd, scroll methods.
```

---

### Task 1: Generalize the confirm into `confirmRequest`

Refactor the tool-specific confirm into a reusable primitive; migrate the tool gate onto it with identical behavior.

**Files:**
- Modify: `source/clients/cli/internal/ui/model.go` (`pendingConfirm` field type, `resolveConfirmKey`, the two raise sites)
- Test: `source/clients/cli/internal/ui/confirm_test.go` (update + add a primitive test)

**Interfaces:**
- Produces: `type confirmRequest struct { onYes, onNo func(Model) (Model, tea.Cmd); extras map[string]func(Model) (Model, tea.Cmd) }`; `m.pendingConfirm *confirmRequest`; generic `resolveConfirmKey`.

- [ ] **Step 1: Write the failing primitive test**

Add to `confirm_test.go`:

```go
func TestResolveConfirmKey_Generic(t *testing.T) {
	yes, no, diff := false, false, false
	mk := func() Model {
		m := minimalModel()
		m.pendingConfirm = &confirmRequest{
			onYes:  func(m Model) (Model, tea.Cmd) { yes = true; m.pendingConfirm = nil; return m, nil },
			onNo:   func(m Model) (Model, tea.Cmd) { no = true; m.pendingConfirm = nil; return m, nil },
			extras: map[string]func(Model) (Model, tea.Cmd){"d": func(m Model) (Model, tea.Cmd) { diff = true; return m, nil }},
		}
		return m
	}
	// y → onYes, clears
	m := mk(); m, _ = m.resolveConfirmKey("y")
	if !yes || m.pendingConfirm != nil { t.Errorf("y: yes=%v pending=%v", yes, m.pendingConfirm != nil) }
	// n → onNo, clears
	yes = false; m = mk(); m, _ = m.resolveConfirmKey("n")
	if !no || m.pendingConfirm != nil { t.Errorf("n: no=%v pending=%v", no, m.pendingConfirm != nil) }
	// d (extra) → handler, does NOT clear
	m = mk(); m, _ = m.resolveConfirmKey("d")
	if !diff || m.pendingConfirm == nil { t.Errorf("d: diff=%v pending=%v", diff, m.pendingConfirm == nil) }
	// unknown key → ignored, still pending
	m = mk(); m, _ = m.resolveConfirmKey("x")
	if m.pendingConfirm == nil { t.Error("unknown key cleared the confirm") }
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestResolveConfirmKey_Generic -count=1`
Expected: FAIL — `confirmRequest` undefined and `pendingConfirm` is the old type.

- [ ] **Step 3: Add the type + change the field**

Add near `pendingToolCall` (model.go ~:187):

```go
// confirmRequest is a generic confirmation gate. Any feature raises one; the
// model routes y / n / esc (and optional extra keys) to it. onYes/onNo resolve
// and should clear m.pendingConfirm; extras run without resolving.
type confirmRequest struct {
	onYes  func(Model) (Model, tea.Cmd)
	onNo   func(Model) (Model, tea.Cmd)
	extras map[string]func(Model) (Model, tea.Cmd)
}
```

Change the field declaration (model.go ~:170) from `pendingConfirm *pendingToolCall` to `pendingConfirm *confirmRequest`.

- [ ] **Step 4: Rewrite `resolveConfirmKey` generic**

Replace the whole `resolveConfirmKey` (model.go ~:1931) with:

```go
func (m Model) resolveConfirmKey(key string) (Model, tea.Cmd) {
	c := m.pendingConfirm
	if c == nil {
		return m, nil
	}
	switch key {
	case "y", "Y":
		return c.onYes(m)
	case "n", "N", "esc", "ctrl+c":
		return c.onNo(m)
	default:
		if fn, ok := c.extras[key]; ok {
			return fn(m)
		}
		return m, nil
	}
}
```

- [ ] **Step 5: Migrate the two tool raise sites**

The PermissionRequired stream raise (model.go ~:1289) becomes (build the transient `pendingToolCall` only to render the prompt, then a `confirmRequest` closing over its data):

```go
	case agentclient.TypePermissionRequired:
		tc := &pendingToolCall{ToolUseID: sm.ToolUseID, Name: sm.ToolName, Args: sm.ArgsJSON, Permission: sm.Tier}
		m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: m.renderConfirmPrompt(tc)})
		m.pendingConfirm = toolConfirm(tc)
```

The local `/tool` raise (model.go ~:1156) becomes:

```go
		tc := &pendingToolCall{Name: res.ToolName, Args: res.ToolArgs, Permission: perm}
		m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: m.renderConfirmPrompt(tc)})
		m.pendingConfirm = toolConfirm(tc)
```

(Adjust to whatever the existing local raise pushes for its prompt entry — keep that line; only the `pendingConfirm =` assignment changes.) Add the shared builder near `resolveConfirmKey`:

```go
// toolConfirm builds the confirm gate for a tool-permission decision, preserving
// the prior behavior: y approves (Allow RPC for a stream-origin call, else local
// invoke), n denies (Deny RPC for stream-origin), d reveals the args JSON.
func toolConfirm(tc *pendingToolCall) *confirmRequest {
	return &confirmRequest{
		onYes: func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: m.styles.Accent.Render("✓ approved — running…")})
			m.refreshViewport()
			if tc.ToolUseID != "" {
				ag, id := m.agent, tc.ToolUseID
				if ag != nil {
					go func() { _ = ag.AllowToolCall(context.Background(), id) }()
				}
				return m, nil
			}
			return m, invokeToolCmd(m.agent, tc.Name, tc.Args)
		},
		onNo: func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: m.styles.Muted.Render("canceled.")})
			m.refreshViewport()
			if tc.ToolUseID != "" {
				ag, id := m.agent, tc.ToolUseID
				if ag != nil {
					go func() { _ = ag.DenyToolCall(context.Background(), id) }()
				}
			}
			return m, nil
		},
		extras: map[string]func(Model) (Model, tea.Cmd){
			"d": func(m Model) (Model, tea.Cmd) {
				m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: "args:\n```json\n" + tc.Args + "\n```"})
				m.refreshViewport()
				return m, nil
			},
			"D": func(m Model) (Model, tea.Cmd) {
				m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: "args:\n```json\n" + tc.Args + "\n```"})
				m.refreshViewport()
				return m, nil
			},
		},
	}
}
```

- [ ] **Step 6: Reconcile existing confirm tests**

Existing `confirm_test.go` tests that set `m.pendingConfirm = &pendingToolCall{...}` and call `resolveConfirmKey` must switch to `m.pendingConfirm = toolConfirm(&pendingToolCall{...})`. Tests asserting `renderConfirmPrompt` output are unaffected (it still takes `*pendingToolCall`). Update any that drive the resolver. Run the whole UI suite.

- [ ] **Step 7: Run tests + build**

Run: `cd source/clients/cli && go test ./internal/ui/ -count=1 && go build ./...`
Expected: PASS (primitive test + migrated confirm tests); clean build.

- [ ] **Step 8: Commit**

```bash
git add source/clients/cli/internal/ui/model.go source/clients/cli/internal/ui/confirm_test.go
git commit -m "refactor(cli): reusable confirmRequest gate; migrate tool-permission confirm"
```

---

### Task 2: Slim `contextView` to a display surface

Remove the embedded input + edit-mode state added in the first 3b cut; keep proposal display + commands.

**Files:**
- Modify: `source/clients/cli/internal/ui/context_view.go`
- Test: `source/clients/cli/internal/ui/context_view_edit_test.go`

**Interfaces:**
- Produces (unchanged signatures): `applyProposal(agentclient.Proposal)`, `cancelProposal()`, `markedForDelete(id string) bool`, `proposeCmd(instruction string) tea.Cmd`, `deleteCmd(ids []string) tea.Cmd`. Removed: `input`, `mode`/`contextViewMode`/`cvEditing`/`cvProposal` (replace with a `showingProposal bool`).

- [ ] **Step 1: Adjust the edit tests to the slimmer shape**

In `context_view_edit_test.go`, keep `TestContextView_ProposalMarksTurnsAndRationale` and `TestContextView_CancelProposalClears` (they call `applyProposal`/`markedForDelete`/`cancelProposal`, which survive). Remove the `mode`/`input` field from `newEditTestView`'s struct literal (it should construct only the surviving fields). If a test referenced `cvEditing`/`input`, delete that assertion.

- [ ] **Step 2: Run to verify it fails (compile)**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestContextView -count=1`
Expected: FAIL to compile once Step 3 removes the fields the tests still mention — so do Step 1 and Step 3 together, then this run goes green.

- [ ] **Step 3: Remove input/mode; add `showingProposal`**

In `context_view.go`:
- Delete the `input textinput.Model`, `mode contextViewMode`, `editErr string` struct fields; delete the `contextViewMode`/`cvBrowse`/`cvEditing`/`cvProposal` declarations; remove the `textinput` import if now unused.
- Add `showingProposal bool` to the struct.
- `applyProposal(p)` sets `c.proposal = p; c.showingProposal = true`. `cancelProposal()` sets `c.proposal = agentclient.Proposal{}; c.showingProposal = false`. `markedForDelete(id)` returns false unless `c.showingProposal`, else membership in `c.proposal.DeleteIDs`.
- `Update(msg)` reduces to: scroll keys (`pgup`/`pgdown`/`ctrl+b`/`ctrl+f`/`ctrl+u`/`ctrl+d`) → scroll; `esc`/`q` → close (`return nil, true`); `r` → reload. (Model-level routing in Task 3 will intercept most keys before this for `/c`, but keep `Update` valid for the `contentPage` contract.)
- Keep `renderTurn` marking via `markedForDelete`; in `View`, when `showingProposal`, render the rationale + a `[y] delete  [n] cancel` footer line.
- Keep `proposeCmd`/`deleteCmd` and the `contextEditProposalMsg`/`contextEditDeletedMsg` types. Remove any `onProposal`/`onDeleted` methods that manipulated `mode`/`input` — the model will own proposal→confirm wiring (Task 3).

- [ ] **Step 4: Run tests + build**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestContextView -count=1 -v && go build ./...`
Expected: PASS (marking + cancel tests); clean build. (Build may fail until Task 3 updates `model.go` references to removed methods — if so, note it and proceed; Task 3 fixes the model side. To keep this task self-contained, temporarily comment nothing — instead, if `model.go` references a removed `contextView` method, that reference is updated here minimally to compile, and Task 3 completes the routing.)

- [ ] **Step 5: Commit**

```bash
git add source/clients/cli/internal/ui/context_view.go source/clients/cli/internal/ui/context_view_edit_test.go
git commit -m "refactor(cli): slim contextView — drop embedded input/edit mode"
```

---

### Task 3: Prompt-bar routing + propose/confirm wiring

Route `/c` keystrokes to the prompt bar; enter proposes; the proposal raises a `confirmRequest` (Task 1) for the delete.

**Files:**
- Modify: `source/clients/cli/internal/ui/model.go` (keypress content-page branch; the two async msg handlers; new `handleContextViewKey`)
- Test: `source/clients/cli/internal/ui/context_view_route_test.go`

**Interfaces:**
- Consumes: `confirmRequest` (Task 1); `contextView.applyProposal`/`cancelProposal`/`proposeCmd`/`deleteCmd` (Task 2); `agentclient.ProposeContextEdit`/`DeleteConversationTurns`.

- [ ] **Step 1: Write the failing routing test**

Create `source/clients/cli/internal/ui/context_view_route_test.go`:

```go
package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func modelWithContextView() Model {
	m := Model{palette: theme.Cracker(), styles: theme.NewStyles(theme.Cracker()), convID: "c1"}
	m.input = newPromptInput()
	cv := &contextView{width: 80, height: 24, palette: m.palette, styles: m.styles, convID: "c1"}
	m.content = cv
	return m
}

func TestContextViewRoute_TypingEditsPromptBar(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	next, _ := m.handleContextViewKey(cv, tea.KeyPressMsg{Code: 'h', Text: "h"})
	if next.input.Value() != "h" {
		t.Errorf("typing did not reach the prompt bar: %q", next.input.Value())
	}
}

func TestContextViewRoute_EnterProposes(t *testing.T) {
	m := modelWithContextView()
	m.input.SetValue("drop the tangent")
	cv := m.content.(*contextView)
	next, cmd := m.handleContextViewKey(cv, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil { t.Error("enter with text should return a propose cmd") }
	if next.input.Value() != "" { t.Errorf("input not cleared after submit: %q", next.input.Value()) }
}

func TestContextViewRoute_ProposalRaisesConfirm(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)
	cv.snapshot = contextSnapshot{Turns: []agentclient.ContextTurn{{ID: "a", Role: "user", Preview: "x"}}}
	m2, _ := m.onContextProposal(contextEditProposalMsg{p: agentclient.Proposal{DeleteIDs: []string{"a"}, Rationale: "r"}})
	if m2.pendingConfirm == nil { t.Error("proposal should raise a pendingConfirm") }
	if !cv.markedForDelete("a") { t.Error("proposal should mark turn a") }
}
```

(Match the `tea.KeyPressMsg` construction to how other UI tests build key messages — check an existing `_test.go` that sends keys, e.g. `prompt_test.go`/`cursor_test.go`, and mirror its field usage; the `Code`/`Text` form above is illustrative.)

- [ ] **Step 2: Run to verify it fails**

Run: `cd source/clients/cli && go test ./internal/ui/ -run TestContextViewRoute -count=1`
Expected: FAIL — `handleContextViewKey`/`onContextProposal` undefined.

- [ ] **Step 3: Add `handleContextViewKey` + the proposal/delete handlers**

Add to `model.go`:

```go
// handleContextViewKey owns the keyboard while the /c context viewer is the
// active page: typing edits the main prompt bar, enter submits an edit
// instruction (ProposeContextEdit), scroll keys move the turn list, and esc on
// an empty bar closes the page.
func (m Model) handleContextViewKey(cv *contextView, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.input.Value() != "" {
			m.input.SetValue("")
			return m, nil
		}
		m.content = nil
		m.contentScrollbarDragging = false
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		m.input.SetValue("")
		return m, cv.proposeCmd(text)
	case "pgup", "ctrl+b":
		cv.ScrollBy(-dashboardContentHeight(cv.height)); return m, nil
	case "pgdown", "ctrl+f":
		cv.ScrollBy(dashboardContentHeight(cv.height)); return m, nil
	case "ctrl+u":
		cv.ScrollBy(-maxInt(1, dashboardContentHeight(cv.height)/2)); return m, nil
	case "ctrl+d":
		cv.ScrollBy(maxInt(1, dashboardContentHeight(cv.height)/2)); return m, nil
	}
	// Everything else edits the prompt bar.
	m = m.preparePromptInput()
	var cmd tea.Cmd
	prev := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != prev {
		m.relayout()
	}
	return m, cmd
}

// onContextProposal applies a proposal to the /c view and raises a confirm gate
// whose y deletes the proposed turns and n cancels.
func (m Model) onContextProposal(msg contextEditProposalMsg) (Model, tea.Cmd) {
	cv, ok := m.content.(*contextView)
	if !ok {
		return m, nil
	}
	if msg.err != nil {
		m.entries = append(m.entries, &Entry{Role: RoleSystem, Content: m.styles.Muted.Render("couldn't interpret that — try rephrasing")})
		m.refreshViewport()
		return m, nil
	}
	cv.applyProposal(msg.p)
	ids := msg.p.DeleteIDs
	m.pendingConfirm = &confirmRequest{
		onYes: func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			return m, cv.deleteCmd(ids)
		},
		onNo: func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			cv.cancelProposal()
			return m, nil
		},
	}
	return m, nil
}

// onContextDeleted clears the proposal and reloads the /c snapshot.
func (m Model) onContextDeleted(msg contextEditDeletedMsg) (Model, tea.Cmd) {
	if cv, ok := m.content.(*contextView); ok {
		cv.cancelProposal()
		cv.snapshot = loadContextSnapshot(cv.agent, cv.convID)
	}
	return m, nil
}
```

- [ ] **Step 4: Wire the keypress branch + msg cases**

In the keypress handler, replace the generic content-page branch (model.go ~:670 `if m.content != nil { ... }`) so `*contextView` is special-cased BEFORE the generic page handling, but still AFTER the `pendingConfirm` gate (so a raised confirm intercepts y/n):

```go
	if m.content != nil {
		if cv, ok := m.content.(*contextView); ok {
			return m.handleContextViewKey(cv, msg)
		}
		pageID := m.content.ID()
		cmd, closed := m.content.Update(msg)
		if closed {
			m.content = nil
			m.contentScrollbarDragging = false
			if pageID == contentPageConfig {
				return m, fetchConfigCmd(m.agent)
			}
		}
		return m, cmd
	}
```

Replace the existing `contextEditProposalMsg`/`contextEditDeletedMsg` top-level msg cases (added in the first 3b cut) with:

```go
	case contextEditProposalMsg:
		return m.onContextProposal(msg)
	case contextEditDeletedMsg:
		return m.onContextDeleted(msg)
```

(If the first-cut cases called `cv.onProposal`/`cv.onDeleted`, delete those — the logic now lives on the model.)

- [ ] **Step 5: Run tests + build the CLI**

Run: `cd source/clients/cli && go test ./internal/ui/ -count=1 && go build ./... && go test ./... -count=1`
Expected: PASS (routing tests + all prior UI tests); clean CLI build; full CLI suite green.

- [ ] **Step 6: Manual-smoke note (no code)**

Confirm the intended flow by reading the wiring: `/c` open → typing edits `m.input` → enter fires `proposeCmd` → `contextEditProposalMsg` → `onContextProposal` raises `pendingConfirm` + marks turns → `y` (resolveConfirmKey → onYes) fires `deleteCmd` → `contextEditDeletedMsg` → `onContextDeleted` reloads. `esc` on empty bar closes `/c`.

- [ ] **Step 7: Commit**

```bash
git add source/clients/cli/internal/ui/model.go source/clients/cli/internal/ui/context_view_route_test.go
git commit -m "feat(cli): /c edits via the prompt bar; proposal uses the confirm gate"
```

---

## Self-Review

**Spec coverage:**
- Part A reusable confirm (generalize + migrate tool gate) → Task 1.
- Part B slim contextView (drop embedded input/mode) → Task 2.
- Part B prompt-bar routing + enter-proposes + proposal→confirm→delete→reload → Task 3.
- Error/edge: empty-bar enter no-op, propose error line, confirm gate ordering, delete reload → Task 3 (`handleContextViewKey`, `onContextProposal`, `onContextDeleted`).
- Out of scope (soft-exclude, compaction) → not here.

**Type consistency:** `confirmRequest{onYes,onNo,extras}`, `toolConfirm(*pendingToolCall) *confirmRequest`, `handleContextViewKey(cv, msg)`, `onContextProposal`/`onContextDeleted`, `contextView.{applyProposal,cancelProposal,markedForDelete,proposeCmd,deleteCmd,showingProposal}` are used identically across tasks.

**Placeholder scan:** code shown for every step. The "match key-msg construction / existing local-raise prompt line / adjust if first-cut used cv.onProposal" notes are deliberate reconciliations with real existing code (the implementer matches the live shapes), not unresolved requirements. The tea key-message construction in the Task 3 test MUST be reconciled against an existing UI key test before running (flagged in the step).
