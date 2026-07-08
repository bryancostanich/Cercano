package main

import (
	"errors"
	"strings"
	"testing"
)

// Ollama being unreachable at startup must not prevent the agent from
// serving: the configured runtime may be llama_server and the primary
// route may be cloud, neither of which needs Ollama. The gate produces a
// warning for the log instead of a fatal error.
func TestOllamaStartupWarningWhenUnreachable(t *testing.T) {
	check := func(string) error { return errors.New("connection refused") }

	warn := ollamaStartupWarning(check, "http://localhost:11434")

	if warn == "" {
		t.Fatal("expected a warning when Ollama is unreachable, got empty string")
	}
	if !strings.Contains(warn, "http://localhost:11434") {
		t.Errorf("warning should name the unreachable endpoint, got: %q", warn)
	}
	if !strings.Contains(strings.ToLower(warn), "ollama") {
		t.Errorf("warning should say Ollama-backed features are affected, got: %q", warn)
	}
}

func TestOllamaStartupWarningWhenReachable(t *testing.T) {
	check := func(string) error { return nil }

	if warn := ollamaStartupWarning(check, "http://localhost:11434"); warn != "" {
		t.Fatalf("expected no warning when Ollama is reachable, got: %q", warn)
	}
}
