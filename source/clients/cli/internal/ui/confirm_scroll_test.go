package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// buildConfirmScrollModel returns a Model with an overflowing chat viewport and
// a pending tool-confirm gate. onYes/onNo are wired so a resolve keystroke
// doesn't nil-deref.
func buildConfirmScrollModel() Model {
	m := New(nil, false)
	m.width = 80
	m.height = 24
	m.relayout()
	setChatTestContent(m.mainChat(), strings.Repeat("chat\n", 80))
	m.mainChat().SetYOffset(0)
	m.pendingConfirm = &confirmRequest{
		onYes: func(m Model) (Model, tea.Cmd) { m.pendingConfirm = nil; return m, nil },
		onNo:  func(m Model) (Model, tea.Cmd) { m.pendingConfirm = nil; return m, nil },
	}
	return m
}

// The reported bug: while a y/n confirm prompt is up, you can't scroll. Both
// the mouse wheel and the PgDn key must scroll the scrollback while the confirm
// stays pending, so the user can review context before answering.
func TestConfirmPending_MouseWheelScrollsScrollback(t *testing.T) {
	m := buildConfirmScrollModel()
	next, _ := m.Update(tea.MouseWheelMsg{X: 2, Y: m.scrollbarTop, Button: tea.MouseWheelDown})
	got := next.(Model)
	if got.mainChat().YOffset() == 0 {
		t.Fatal("mouse wheel should scroll scrollback while a confirm is pending")
	}
	if got.pendingConfirm == nil {
		t.Fatal("scrolling must not resolve the pending confirm")
	}
}

func TestConfirmPending_PgDnScrollsScrollback(t *testing.T) {
	m := buildConfirmScrollModel()
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	got := next.(Model)
	if got.mainChat().YOffset() == 0 {
		t.Fatal("PgDn should scroll scrollback while a confirm is pending")
	}
	if got.pendingConfirm == nil {
		t.Fatal("scrolling must not resolve the pending confirm")
	}
}

// The confirm answers must still work — a scroll-key pass-through must not
// swallow y/n. 'y' resolves (clears pendingConfirm); a scroll key does not.
func TestConfirmPending_YStillResolves(t *testing.T) {
	m := buildConfirmScrollModel()
	next, _ := m.Update(tea.KeyPressMsg{Code: 'y'})
	got := next.(Model)
	if got.pendingConfirm != nil {
		t.Fatal("'y' should resolve the pending confirm")
	}
}
