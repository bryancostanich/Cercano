package ui

import (
	"cercano/source/clients/cli/internal/form"
	tea "charm.land/bubbletea/v2"
	"testing"
)

func TestSettingsPasteIntoEditingText(t *testing.T) {
	for _, masked := range []bool{false, true} {
		name := "plain"
		if masked {
			name = "api-key"
		}
		t.Run(name, func(t *testing.T) {
			field := form.NewText("value", "Value", "", "")
			if masked {
				field = form.NewMasked("cloud-api-key", "API key", false)
			}
			f := form.New([]form.Section{{Fields: []form.Field{field}}})
			sp := &settingsPage{form: f}
			p, ok := any(sp).(pasteConsumingPage)
			if !ok {
				t.Fatal("settings page cannot receive terminal paste events")
			}
			if p.handlePaste("ignored") {
				t.Fatal("paste accepted outside edit mode")
			}
			commits := 0
			value := ""
			f.OnCommit = func(key, v string) (string, tea.Cmd, error) { commits++; value = v; return "", nil, nil }
			f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if !p.handlePaste("synthetic-token") {
				t.Fatal("paste declined in edit mode")
			}
			if commits != 0 || !field.Editing() {
				t.Fatal("paste committed or ended editing")
			}
			f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if commits != 1 || value != "synthetic-token" {
				t.Fatalf("commits=%d; pasted value mismatch=%v", commits, value != "synthetic-token")
			}
		})
	}
}

func TestSettingsPasteTopLevelDoesNotReachPrompt(t *testing.T) {
	m := New(nil, false)
	field := form.NewMasked("cloud-api-key", "API key", false)
	f := form.New([]form.Section{{Fields: []form.Field{field}}})
	m.content = &settingsPage{form: f}
	m.input.SetValue("existing prompt")
	commits := 0
	f.OnCommit = func(key, value string) (string, tea.Cmd, error) {
		commits++
		if value != "synthetic-token" {
			t.Fatal("pasted key mismatch")
		}
		return "", nil, nil
	}
	// Inactive fields must not leak paste into the prompt either.
	next, _ := m.Update(tea.PasteMsg{Content: "ignored"})
	m = next.(Model)
	f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	next, _ = m.Update(tea.PasteMsg{Content: "synthetic-token"})
	m = next.(Model)
	if m.input.Value() != "existing prompt" {
		t.Fatal("settings paste changed prompt")
	}
	if commits != 0 {
		t.Fatal("paste committed key")
	}
	f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if commits != 1 {
		t.Fatal("Enter did not commit key")
	}
}
