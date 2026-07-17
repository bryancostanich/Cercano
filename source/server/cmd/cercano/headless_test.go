package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestFinalizeHeadlessExit_NoAssistantOutputFails(t *testing.T) {
	var stderr bytes.Buffer
	got := finalizeHeadlessExit(0, false, &stderr)
	if got == 0 {
		t.Fatalf("finalizeHeadlessExit returned success for a turn with no assistant output")
	}
	if !strings.Contains(stderr.String(), "no assistant output") {
		t.Fatalf("stderr = %q, want actionable no-output message", stderr.String())
	}
}

func TestFinalizeHeadlessExit_KeepsPriorErrorCode(t *testing.T) {
	var stderr bytes.Buffer
	got := finalizeHeadlessExit(3, false, &stderr)
	if got != 3 {
		t.Fatalf("finalizeHeadlessExit preserved code = %d, want 3", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no extra no-output message for prior error", stderr.String())
	}
}

func TestFinalizeHeadlessExit_TextSuccess(t *testing.T) {
	var stderr bytes.Buffer
	got := finalizeHeadlessExit(0, true, &stderr)
	if got != 0 {
		t.Fatalf("finalizeHeadlessExit with text = %d, want 0", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}
