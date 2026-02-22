package agent

import (
	"testing"

	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func TestMapEventToProgress_NilEvent(t *testing.T) {
	if got := MapEventToProgress(nil); got != "" {
		t.Errorf("expected empty string for nil event, got %q", got)
	}
}

func TestMapEventToProgress_GeneratorAuthor(t *testing.T) {
	ev := session.NewEvent("inv-1")
	ev.Author = "generator"
	ev.LLMResponse.Content = genai.NewContentFromText("some code", genai.RoleModel)

	if got := MapEventToProgress(ev); got != "Generating code..." {
		t.Errorf("expected %q, got %q", "Generating code...", got)
	}
}

func TestMapEventToProgress_ValidatorSuccess(t *testing.T) {
	ev := session.NewEvent("inv-1")
	ev.Author = "validator"
	ev.Actions.Escalate = true

	if got := MapEventToProgress(ev); got != "Validation passed." {
		t.Errorf("expected %q, got %q", "Validation passed.", got)
	}
}

func TestMapEventToProgress_ValidatorFailure(t *testing.T) {
	ev := session.NewEvent("inv-1")
	ev.Author = "validator"
	ev.Actions.Escalate = false

	if got := MapEventToProgress(ev); got != "Validation failed. Retrying..." {
		t.Errorf("expected %q, got %q", "Validation failed. Retrying...", got)
	}
}

func TestMapEventToProgress_UnknownAuthor(t *testing.T) {
	ev := session.NewEvent("inv-1")
	ev.Author = "unknown_agent"

	if got := MapEventToProgress(ev); got != "" {
		t.Errorf("expected empty string for unknown author, got %q", got)
	}
}
