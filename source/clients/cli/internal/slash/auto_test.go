package slash

import (
	"strings"
	"testing"
)

func TestRegisterAuto_RegistersCommand(t *testing.T) {
	r := New()
	RegisterAuto(r, nil)
	if _, ok := r.cmds["auto"]; !ok {
		t.Fatal("missing /auto")
	}
}

func TestSlash_AutoWithGoalSubmitsBriefDraftPrompt(t *testing.T) {
	r := New()
	RegisterAuto(r, nil)
	res, _ := r.Dispatch("/auto fix reconnect approval recovery")
	if res.Kind != ResultSubmitPrompt {
		t.Fatalf("/auto kind = %v, want ResultSubmitPrompt", res.Kind)
	}
	for _, want := range []string{"fix reconnect approval recovery", "Draft a concise autonomous run brief", "call suggest_autonomous", "Do not enter autonomous mode until"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("/auto prompt missing %q:\n%s", want, res.Text)
		}
	}
}

func TestSlash_AutoWithoutGoalAsksToDefineBrief(t *testing.T) {
	r := New()
	RegisterAuto(r, nil)
	res, _ := r.Dispatch("/auto")
	if res.Kind != ResultSubmitPrompt {
		t.Fatalf("/auto kind = %v, want ResultSubmitPrompt", res.Kind)
	}
	for _, want := range []string{"Help me define", "autonomous run brief", "call suggest_autonomous"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("/auto prompt missing %q:\n%s", want, res.Text)
		}
	}
}
