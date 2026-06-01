package tools

import (
	"context"
	"strings"
	"testing"
)

func TestCustomValidator_PassesOnZeroExit(t *testing.T) {
	v := NewCustomValidator("true")
	decision, err := v.Validate(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision != Passed {
		t.Fatalf("got decision %s, want passed", decision)
	}
}

func TestCustomValidator_FailsOnNonZeroExit(t *testing.T) {
	v := NewCustomValidator("echo boom >&2; exit 1")
	decision, err := v.Validate(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Fatalf("got decision %s, want failed", decision)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %q, want it to contain stderr output 'boom'", err.Error())
	}
}
