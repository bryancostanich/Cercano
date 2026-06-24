package server

import (
	"strings"
	"testing"
)

func TestBuildToolLoopSystem(t *testing.T) {
	// Names the working directory so the model doesn't hunt the filesystem.
	s := buildToolLoopSystem("/home/u/proj", "")
	if !strings.Contains(s, "/home/u/proj") {
		t.Errorf("system prompt must name the working directory, got: %q", s)
	}

	// Includes project context when present.
	s2 := buildToolLoopSystem("/home/u/proj", "Cercano is a local-first AI dev tool.")
	if !strings.Contains(s2, "Cercano is a local-first AI dev tool.") {
		t.Errorf("system prompt must include project context, got: %q", s2)
	}

	// No working dir → still a usable prompt, no empty-path artifacts.
	s3 := buildToolLoopSystem("", "")
	if strings.Contains(s3, "working directory is \n") || strings.Contains(s3, "working directory is .") {
		t.Errorf("empty workDir should be omitted cleanly, got: %q", s3)
	}
}
