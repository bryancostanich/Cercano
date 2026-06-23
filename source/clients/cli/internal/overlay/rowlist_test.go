package overlay

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

func TestRowList_NavigateAndSelect(t *testing.T) {
	var picked Row
	rows := []Row{
		{Key: "a", Label: "alpha", Value: "1"},
		{Key: "b", Label: "beta", Value: "2"},
		{Key: "c", Label: "gamma", Value: "3"},
	}
	r := New("test", rows, Hooks{
		OnSelect: func(row Row) (string, bool, tea.Cmd) {
			picked = row
			return "selected " + row.Key, true, nil
		},
	})
	styles := theme.NewStyles(theme.Cracker())

	// Down twice → cursor at row 2.
	r, _, closed := r.Update(tea.KeyPressMsg{Code: tea.KeyDown}, styles)
	if closed {
		t.Fatal("unexpected close on first down")
	}
	r, _, closed = r.Update(tea.KeyPressMsg{Code: tea.KeyDown}, styles)
	if r.Cursor() != 2 {
		t.Fatalf("cursor: got %d want 2", r.Cursor())
	}

	// Enter → OnSelect fires with row "c", overlay closes.
	r, _, closed = r.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, styles)
	if !closed {
		t.Fatal("expected overlay to close after select")
	}
	if picked.Key != "c" {
		t.Fatalf("picked: got %q want c", picked.Key)
	}
}

func TestRowList_EditOnEditableRow(t *testing.T) {
	saved := ""
	rows := []Row{
		{Key: "x", Label: "name", Value: "old", Editable: true},
	}
	reloadCalled := false
	r := New("test", rows, Hooks{
		OnEdit: func(row Row, newValue string) (string, error) {
			saved = newValue
			return "ok", nil
		},
		OnReload: func() []Row {
			reloadCalled = true
			return rows
		},
	})
	styles := theme.NewStyles(theme.Cracker())

	// Enter → enter edit mode.
	r, _, _ = r.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, styles)
	if !r.editing {
		t.Fatal("expected editing mode after enter on editable row")
	}

	// Type "new" into the input (simulate keys).
	for _, ch := range "new" {
		r, _, _ = r.Update(tea.KeyPressMsg{Code: ch, Text: string(ch)}, styles)
	}
	// Note: bubbles/textinput pre-populates with "old" + cursor end; our typed
	// runes append. We mostly care that OnEdit fires; let's clear via ctrl+u
	// not available here, so just commit and check OnEdit was invoked.

	r, _, closed := r.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, styles)
	if closed {
		t.Fatal("commit should not close overlay")
	}
	if saved == "" {
		t.Fatal("OnEdit was not invoked")
	}
	if !reloadCalled {
		t.Fatal("OnReload was not invoked after successful save")
	}
}

func TestRowList_ReadOnlyRowIgnoresEnter(t *testing.T) {
	r := New("test", []Row{
		{Key: "x", Label: "info", Value: "v", ReadOnly: true},
	}, Hooks{})
	styles := theme.NewStyles(theme.Cracker())

	r, _, closed := r.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, styles)
	if closed {
		t.Fatal("read-only row should not close")
	}
	if r.editing {
		t.Fatal("read-only row should not enter edit mode")
	}
}

func TestRowList_EscClosesOverlay(t *testing.T) {
	r := New("test", []Row{{Label: "a"}}, Hooks{})
	styles := theme.NewStyles(theme.Cracker())
	_, _, closed := r.Update(tea.KeyPressMsg{Code: tea.KeyEsc}, styles)
	if !closed {
		t.Fatal("esc should close overlay")
	}
}

func TestRowList_SaveErrorShowsStatus(t *testing.T) {
	r := New("test", []Row{
		{Key: "x", Label: "v", Value: "old", Editable: true},
	}, Hooks{
		OnEdit: func(row Row, newValue string) (string, error) {
			return "", errors.New("nope")
		},
	})
	styles := theme.NewStyles(theme.Cracker())

	r, _, _ = r.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, styles)
	r, _, _ = r.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, styles)

	view := r.View(80, theme.Cracker(), styles)
	if !strings.Contains(stripAnsi(view), "save failed: nope") {
		t.Errorf("expected save error in footer, got %q", stripAnsi(view))
	}
}

func stripAnsi(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7E) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
