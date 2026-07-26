package agent

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

// TestForcedConfirmStashOverridesBypass is the whole point of the feature: a
// WIP-consuming `git stash` run through the shell must trigger a human confirm
// even under ModeBypass, where every other tool call runs silently. If the
// confirm fires and is denied, the loop returns without executing the stash.
func TestForcedConfirmStashOverridesBypass(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "Bash",
				ToolInput: json.RawMessage(`{"cmd":["git","stash"]}`)}},
			{{Type: llm.BlockText, Text: "unreached"}},
		},
		caps: inference.Capabilities{SupportsTools: true},
	}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")
	if err := perms.SetMode(ModeBypass); err != nil {
		t.Fatal(err)
	}

	confirmed := false
	_, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider:    prov,
		Registry:    reg,
		Permissions: perms,
		UserInput:   "tidy up",
		PermissionRequester: func(_ context.Context, _, name string, args json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			confirmed = true
			return false, nil // deny → loop returns without running the stash
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !confirmed {
		t.Fatal("git stash under Bypass must still request a human confirm — the forced-confirm override did not fire")
	}
}

// TestBypassStillSilentForNonStash guards the blast radius: an ordinary shell
// command under Bypass must NOT suddenly start prompting — only the stash shape
// is forced. Regression guard against the override matching too broadly.
func TestBypassStillSilentForNonStash(t *testing.T) {
	prov := &mockProvider{
		scripts: [][]llm.Block{
			{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "Bash",
				ToolInput: json.RawMessage(`{"cmd":["git","status"]}`)}},
			{{Type: llm.BlockText, Text: "done"}},
		},
		caps: inference.Capabilities{SupportsTools: true},
	}
	reg := testDefaultRegistry()
	perms, _ := LoadPermissionStore(t.TempDir() + "/perms.yaml")
	if err := perms.SetMode(ModeBypass); err != nil {
		t.Fatal(err)
	}

	confirmed := false
	res, err := RunToolLoop(t.Context(), ToolLoopInput{
		Provider:    prov,
		Registry:    reg,
		Permissions: perms,
		UserInput:   "check status",
		PermissionRequester: func(_ context.Context, _, _ string, _ json.RawMessage, _ llm.Permission, _ bool) (bool, error) {
			confirmed = true
			return true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed {
		t.Fatal("git status under Bypass must NOT prompt — forced-confirm is over-matching")
	}
	if res.FinalText != "done" {
		t.Fatalf("loop should run through to the terminator, got %q", res.FinalText)
	}
}

func TestIsWIPConsumingStash(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want bool
	}{
		// Consuming — must force a confirm.
		{"bare stash", []string{"git", "stash"}, true},
		{"stash push", []string{"git", "stash", "push"}, true},
		{"stash save", []string{"git", "stash", "save", "wip"}, true},
		{"stash create", []string{"git", "stash", "create"}, true},
		{"stash store", []string{"git", "stash", "store", "deadbeef"}, true},
		{"stash push -u", []string{"git", "stash", "push", "-u"}, true},
		{"bare stash with -u flag", []string{"git", "stash", "-u"}, true},
		{"stash push with message", []string{"git", "stash", "push", "-m", "note"}, true},
		{"shell-wrapped bare stash", []string{"bash", "-lc", "git stash"}, true},
		{"shell-wrapped stash push -u", []string{"bash", "-lc", "git stash push --include-untracked"}, true},
		{"sh -c bare stash", []string{"sh", "-c", "git stash"}, true},

		// Restoring / inspecting — must NOT force a confirm.
		{"stash pop", []string{"git", "stash", "pop"}, false},
		{"stash apply", []string{"git", "stash", "apply"}, false},
		{"stash list", []string{"git", "stash", "list"}, false},
		{"stash show", []string{"git", "stash", "show"}, false},
		{"stash drop", []string{"git", "stash", "drop"}, false},
		{"stash clear", []string{"git", "stash", "clear"}, false},
		{"stash branch", []string{"git", "stash", "branch", "tmp"}, false},
		{"shell-wrapped pop", []string{"bash", "-lc", "git stash pop"}, false},

		// Unrelated git — never.
		{"git status", []string{"git", "status"}, false},
		{"git commit", []string{"git", "commit", "-m", "x"}, false},
		{"git stash-like word", []string{"git", "stashy"}, false},

		// Non-git shell that merely mentions stash — must not trip.
		{"echo stash", []string{"echo", "git stash"}, false},
		{"status then unrelated stash word", []string{"bash", "-lc", "git status; foo stash"}, false},

		// Degenerate.
		{"empty", []string{}, false},
		{"just git", []string{"git"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isWIPConsumingStash(c.argv); got != c.want {
				t.Fatalf("isWIPConsumingStash(%v) = %v, want %v", c.argv, got, c.want)
			}
		})
	}
}

func TestRequiresForcedConfirm(t *testing.T) {
	mk := func(cmd ...string) json.RawMessage {
		b, _ := json.Marshal(struct {
			Cmd []string `json:"cmd"`
		}{Cmd: cmd})
		return b
	}

	cases := []struct {
		name     string
		toolName string
		args     json.RawMessage
		want     bool
	}{
		{"run_command bare stash", "run_command", mk("git", "stash"), true},
		{"Bash alias bare stash", "Bash", mk("git", "stash"), true},
		{"run_command stash push", "run_command", mk("git", "stash", "push"), true},
		{"Bash alias stash pop", "Bash", mk("git", "stash", "pop"), false},
		{"run_command unrelated", "run_command", mk("git", "status"), false},

		// Wrong tool name — a real stash arriving under some other capability
		// name must not match (there is no such path today, but the guard
		// stays scoped to the shell tool).
		{"non-shell tool with stash args", "write_file", mk("git", "stash"), false},

		// Malformed args — fail closed to "no forced confirm" (the normal gate
		// still applies), never panic.
		{"garbage args", "run_command", json.RawMessage(`not json`), false},
		{"empty args", "run_command", json.RawMessage(`{}`), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := requiresForcedConfirm(c.toolName, c.args); got != c.want {
				t.Fatalf("requiresForcedConfirm(%q, %s) = %v, want %v", c.toolName, c.args, got, c.want)
			}
		})
	}
}
