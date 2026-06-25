package ui

// TestContextView_LifecycleOrphanRegression drives a full /c round-trip:
//   submit → chatConfirmMsg → confirm-yes → chatDoneMsg
// and asserts the placeholder-orphan invariants that the step-4 review missed:
//
//   (a) no entry remains with {Streaming:true, Content==""}  — the "working…" orphan
//   (b) rendered /c view contains no "working…" after completion
//   (c) the rationale and done text ARE present (behaviour preserved)
//   (d) busy() spans the confirm gate (true after confirm, false after done)
//
// Before the fix this test failed at (a): chatConfirmMsg appended the rationale
// AFTER the open placeholder instead of filling it, leaving a stray
// {Streaming:true, Content:""} entry that rendered as a frozen "▖ working…" line
// and was never closed by chatDoneMsg (which only closed the LAST entry).

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestContextView_LifecycleOrphanRegression(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)

	// ── 1. submit — opens streaming placeholder, sets busyFlag ──────────────
	var cmd tea.Cmd
	m, cmd = m.submitContextEdit(cv, "drop old turns")
	if cmd == nil {
		t.Fatal("submitContextEdit should return a Cmd")
	}
	if !cv.busy() {
		t.Fatal("(d) busy() must be true after submit")
	}

	// Verify the streaming placeholder exists at this point.
	hasPlaceholder := func() bool {
		for _, e := range cv.chat.Entries() {
			if e.Role == RoleAssistant && e.Streaming && e.Content == "" {
				return true
			}
		}
		return false
	}
	if !hasPlaceholder() {
		t.Fatal("streaming placeholder must be open after submit")
	}

	// ── 2. chatConfirmMsg — fills placeholder with rationale, raises gate ───
	m, _ = m.routeChatMsg(chatConfirmMsg{
		assistant: "I will remove 2 turns.",
		onYes:     func() tea.Msg { return chatDoneMsg{text: "removed 2 turn(s)."} },
		onNo:      func() tea.Msg { return chatDoneMsg{text: "nothing to remove."} },
	})
	if m.pendingConfirm == nil {
		t.Fatal("chatConfirmMsg must raise a pendingConfirm")
	}

	// (a) no orphaned {Streaming:true, Content:""} entry after confirm
	for _, e := range cv.chat.Entries() {
		if e.Role == RoleAssistant && e.Streaming && e.Content == "" {
			t.Error("(a) ORPHAN: stray {Streaming:true, Content:\"\"} entry found after chatConfirmMsg — frozen working… placeholder")
		}
	}

	// (c) rationale present
	rationaleFound := false
	for _, e := range cv.chat.Entries() {
		if strings.Contains(e.Content, "I will remove 2 turns.") {
			rationaleFound = true
		}
	}
	if !rationaleFound {
		t.Error("(c) rationale entry not present after chatConfirmMsg")
	}

	// (d) busy() must still be true during the confirm gate
	if !cv.busy() {
		t.Error("(d) busy() must be true during the confirm gate (after confirm, before done)")
	}

	// ── 3. confirm-yes → chatDoneMsg ─────────────────────────────────────────
	m, _ = m.pendingConfirm.onYes(m)

	// The onYes Cmd returns chatDoneMsg; simulate it flowing back.
	doneCmd := func() tea.Msg { return chatDoneMsg{text: "removed 2 turn(s)."} }
	m, _ = m.routeChatMsg(doneCmd())

	// (a) still no orphan after done
	for _, e := range cv.chat.Entries() {
		if e.Role == RoleAssistant && e.Streaming && e.Content == "" {
			t.Error("(a) ORPHAN: stray {Streaming:true, Content:\"\"} entry found after chatDoneMsg")
		}
	}

	// (b) rendered view must not contain "working…"
	rendered := cv.chat.View()
	if strings.Contains(rendered, "working…") {
		t.Error("(b) rendered /c view still shows 'working…' after completion")
	}

	// (c) done text present
	doneFound := false
	for _, e := range cv.chat.Entries() {
		if strings.Contains(e.Content, "removed 2 turn(s).") {
			doneFound = true
		}
	}
	if !doneFound {
		t.Error("(c) done text 'removed 2 turn(s).' not found in entries after chatDoneMsg")
	}

	// (d) busy() must be false after done
	if cv.busy() {
		t.Error("(d) busy() must be false after chatDoneMsg")
	}
}

// TestContextView_LifecycleOnNo drives the cancel path: submit → confirm → no → done.
// Asserts no orphan and busy cleared on onNo.
func TestContextView_LifecycleOnNo(t *testing.T) {
	m := modelWithContextView()
	cv := m.content.(*contextView)

	m, _ = m.submitContextEdit(cv, "drop old turns")
	if !cv.busy() {
		t.Fatal("(d) busy() must be true after submit")
	}

	m, _ = m.routeChatMsg(chatConfirmMsg{
		assistant: "I will remove 2 turns.",
		onYes:     func() tea.Msg { return chatDoneMsg{text: "removed 2 turn(s)."} },
		onNo:      func() tea.Msg { return chatDoneMsg{text: "nothing to remove."} },
	})

	// (d) busy during gate
	if !cv.busy() {
		t.Error("(d) busy() must be true during the confirm gate")
	}

	// invoke onNo
	m, _ = m.pendingConfirm.onNo(m)

	// (a) no orphan after onNo
	for _, e := range cv.chat.Entries() {
		if e.Role == RoleAssistant && e.Streaming && e.Content == "" {
			t.Error("(a) ORPHAN: stray {Streaming:true, Content:\"\"} found after onNo")
		}
	}

	// (b) no working… in rendered view
	rendered := cv.chat.View()
	if strings.Contains(rendered, "working…") {
		t.Error("(b) rendered view shows 'working…' after onNo")
	}

	// (d) busy cleared by onNo
	if cv.busy() {
		t.Error("(d) busy() must be false after onNo")
	}
}
