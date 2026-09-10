package ui

import (
	"strings"
	"testing"
)

func TestRenderConfirmPrompt_GitStashAsksPermission(t *testing.T) {
	p := &pendingToolCall{Name: "Bash", Args: `{"cmd":["git","stash","push","--include-untracked","-m","cercano-auto-stash"],"cwd":"/repo"}`, Permission: "W", ToolUseID: "stash-1"}
	out := stripAnsiCSI(minimalModel().renderConfirmPrompt(p))
	for _, want := range []string{"Allow git stash?", "git stash push --include-untracked -m cercano-auto-stash", "[y]es", "[n]o", "[d]etails", "[c]hat"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in prompt: %q", want, out)
		}
	}
}

func TestConfirmPromptTitle_StashDetectionIsNarrow(t *testing.T) {
	for _, p := range []pendingToolCall{
		{Name: "Bash", Args: `{"cmd":["git","status"]}`},
		{Name: "Bash", Args: `{"cmd":["echo","git","stash"]}`},
		{Name: "other", Args: `{"cmd":["git","stash"]}`},
		{Name: "Bash", Args: `{"cmd":["git"]}`},
		{Name: "Bash", Args: `invalid`},
	} {
		if got := confirmPromptTitle(&p); got == "Allow git stash?" {
			t.Errorf("unexpected stash title for %+v", p)
		}
	}
}
