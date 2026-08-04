package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

func newTestAddForm() *mcpAddForm {
	return newMcpAddForm(theme.Palette{}, theme.Styles{})
}

func typeRunes(f *mcpAddForm, s string) {
	for _, r := range s {
		f.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestMcpAddForm_FieldNavigation(t *testing.T) {
	f := newTestAddForm()
	if f.focus != mcpFieldName {
		t.Fatalf("initial focus = %d, want name", f.focus)
	}
	f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if f.focus != mcpFieldCommand {
		t.Fatalf("after tab focus = %d, want command", f.focus)
	}
	f.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if f.focus != mcpFieldName {
		t.Fatalf("after shift+tab focus = %d, want name", f.focus)
	}
	// Wrap backward from name to env.
	f.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if f.focus != mcpFieldEnv {
		t.Fatalf("wrap focus = %d, want env", f.focus)
	}
}

func TestMcpAddForm_ValidationRejectsEmpty(t *testing.T) {
	f := newTestAddForm()
	// Enter with no name → not submitted, error set, focus on name.
	_, _, sub := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if sub != nil {
		t.Fatal("submitted with empty name")
	}
	if f.errMsg == "" || f.focus != mcpFieldName {
		t.Fatalf("expected name error, got err=%q focus=%d", f.errMsg, f.focus)
	}
	// Name only, no command → rejected on command.
	typeRunes(f, "github")
	_, _, sub = f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if sub != nil {
		t.Fatal("submitted with empty command")
	}
	if f.focus != mcpFieldCommand {
		t.Fatalf("expected command focus, got %d", f.focus)
	}
}

func TestMcpAddForm_SubmitParsesArgsAndEnv(t *testing.T) {
	f := newTestAddForm()
	typeRunes(f, "github")
	f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // → command
	typeRunes(f, "npx")
	f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // → args
	typeRunes(f, "-y server-github")
	f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // → env
	typeRunes(f, "TOKEN=abc BAD FOO=bar")

	_, _, sub := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if sub == nil {
		t.Fatal("expected submit")
	}
	if sub.name != "github" || sub.command != "npx" {
		t.Fatalf("name/command = %q/%q", sub.name, sub.command)
	}
	if len(sub.args) != 2 || sub.args[0] != "-y" || sub.args[1] != "server-github" {
		t.Fatalf("args = %v", sub.args)
	}
	if sub.env["TOKEN"] != "abc" || sub.env["FOO"] != "bar" {
		t.Fatalf("env = %v", sub.env)
	}
	if _, ok := sub.env["BAD"]; ok {
		t.Fatalf("malformed env token was kept: %v", sub.env)
	}
}

func TestMcpAddForm_EscCancels(t *testing.T) {
	f := newTestAddForm()
	_, closed, sub := f.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !closed || sub != nil {
		t.Fatalf("esc: closed=%v sub=%v, want closed no-submit", closed, sub)
	}
}

func TestMcpAddForm_Backspace(t *testing.T) {
	f := newTestAddForm()
	typeRunes(f, "abc")
	f.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if f.values[mcpFieldName] != "ab" {
		t.Fatalf("after backspace = %q, want ab", f.values[mcpFieldName])
	}
}
