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
}
