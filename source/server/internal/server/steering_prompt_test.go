package server

import (
	"strings"
	"testing"
)

func TestSystemPromptIncludesSteering(t *testing.T) {
	s := &Server{}
	out := s.buildSystemPrompt("")
	if !strings.Contains(out, "plain English") {
		t.Fatal("system prompt missing the plain-English steering rule")
	}
	if !strings.Contains(out, "get_protocol") {
		t.Fatal("system prompt missing the protocol-trigger steering")
	}
	if !strings.HasPrefix(out, "You are Cercano") {
		t.Fatal("persona line must remain first")
	}
	// Wrapper-aware naming steering: the model must be told the mcp__oc__
	// prefix (or similar) is a routing artifact, not a signal that it's
	// running inside a host — and that when it delegates via dispatch or
	// workflow it should use the plain registered names.
	if !strings.Contains(out, "mcp__oc__") {
		t.Fatal("system prompt should acknowledge the mcp__oc__ prefix so the model doesn't misread its identity")
	}
	if !strings.Contains(out, "dispatch") || !strings.Contains(out, "workflow") {
		t.Fatal("naming steering should reference dispatch/workflow so the model knows where plain names matter")
	}
}
