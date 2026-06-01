package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"cercano/source/server/internal/testfixtures"
)

func skipIfNoNpm(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not in PATH; skipping integration test")
	}
}

func TestNodeValidator_PassesOnTrivialBuild(t *testing.T) {
	skipIfNoNpm(t)
	// Use Copy: npm may write a package-lock.json or similar artifacts.
	dir := testfixtures.Copy(t, "node/valid")
	v := NewNodeValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision != Passed {
		t.Errorf("got decision %s, want passed", decision)
	}
}

func TestNodeValidator_FailsOnFailingBuild(t *testing.T) {
	skipIfNoNpm(t)
	dir := testfixtures.Copy(t, "node/broken")
	v := NewNodeValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Errorf("got decision %s, want failed", decision)
	}
}

func TestNodeValidator_MissingBinaryReturnsFailedWithHint(t *testing.T) {
	t.Setenv("PATH", "")
	v := NewNodeValidator()
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
