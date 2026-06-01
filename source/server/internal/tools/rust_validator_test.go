package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"cercano/source/server/internal/testfixtures"
)

func skipIfNoCargo(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo not in PATH; skipping integration test")
	}
}

func TestRustValidator_PassesOnValidProject(t *testing.T) {
	skipIfNoCargo(t)
	// Use Copy: cargo build writes Cargo.lock + target/ into the project.
	dir := testfixtures.Copy(t, "rust/valid")
	v := NewRustValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision != Passed {
		t.Errorf("got decision %s, want passed", decision)
	}
}

func TestRustValidator_FailsOnBrokenProject(t *testing.T) {
	skipIfNoCargo(t)
	dir := testfixtures.Copy(t, "rust/broken")
	v := NewRustValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Errorf("got decision %s, want failed", decision)
	}
}

func TestRustValidator_MissingBinaryReturnsFailedWithHint(t *testing.T) {
	t.Setenv("PATH", "")
	v := NewRustValidator()
	decision, err := v.Validate(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Errorf("got decision %s, want failed", decision)
	}
	if !strings.Contains(err.Error(), "validator.command") {
		t.Errorf("err = %q, want it to contain 'validator.command'", err.Error())
	}
}
