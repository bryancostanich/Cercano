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

// TestMcpAddForm_SplitsFlatCommandLine covers the round-trip trap: the details
// popover's "copy command" flattens command+args into one shell-style line, and
// a user pastes that whole line into the command field, leaving args empty. The
// form should split it — first token is the executable, the rest are args —
// rather than storing the whole line as `command` (which exec() can't find).
func TestMcpAddForm_SplitsFlatCommandLine(t *testing.T) {
	f := newTestAddForm()
	typeRunes(f, "rekolektion-viz")
	f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // → command
	// The exact line copied from the details pane.
	typeRunes(f, "/Users/b/.dotnet/dotnet run --project /Users/b/rekolektion/tools/viz/src/Rekolektion.Viz.Mcp")

	_, _, sub := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if sub == nil {
		t.Fatal("expected submit")
	}
	if sub.command != "/Users/b/.dotnet/dotnet" {
		t.Fatalf("command = %q, want just the executable path", sub.command)
	}
	want := []string{"run", "--project", "/Users/b/rekolektion/tools/viz/src/Rekolektion.Viz.Mcp"}
	if len(sub.args) != len(want) {
		t.Fatalf("args = %v, want %v", sub.args, want)
	}
	for i := range want {
		if sub.args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, sub.args[i], want[i])
		}
	}
}

// TestMcpAddForm_DoesNotSplitWhenArgsProvided guards the escape hatch: a user
// who deliberately fills both command and args must be left untouched, even if
// the command field happens to contain whitespace.
func TestMcpAddForm_DoesNotSplitWhenArgsProvided(t *testing.T) {
	f := newTestAddForm()
	typeRunes(f, "srv")
	f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // → command
	typeRunes(f, "my cmd")                      // whitespace in command...
	f.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // → args
	typeRunes(f, "--flag")                      // ...but args is non-empty

	_, _, sub := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if sub == nil {
		t.Fatal("expected submit")
	}
	if sub.command != "my cmd" {
		t.Fatalf("command = %q, want left untouched", sub.command)
	}
	if len(sub.args) != 1 || sub.args[0] != "--flag" {
		t.Fatalf("args = %v, want [--flag]", sub.args)
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
