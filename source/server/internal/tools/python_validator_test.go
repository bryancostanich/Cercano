package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"cercano/source/server/internal/testfixtures"
)

func skipIfNoPython(t *testing.T) {
	if _, err := exec.LookPath("python3"); err == nil {
		return
	}
	if _, err := exec.LookPath("python"); err == nil {
		return
	}
	t.Skip("neither python3 nor python in PATH; skipping integration test")
}

func TestPythonValidator_PassesOnValidProject(t *testing.T) {
	skipIfNoPython(t)
	// Use Copy: compileall writes __pycache__/ directories.
	dir := testfixtures.Copy(t, "python/valid")
	v := NewPythonValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision != Passed {
		t.Errorf("got decision %s, want passed", decision)
	}
}

func TestPythonValidator_FailsOnBrokenProject(t *testing.T) {
	skipIfNoPython(t)
	dir := testfixtures.Copy(t, "python/broken")
	v := NewPythonValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Errorf("got decision %s, want failed", decision)
	}
	if !strings.Contains(err.Error(), "core.py") {
		t.Errorf("err = %q, want it to mention 'core.py' (the broken file)", err.Error())
	}
}

func TestPythonValidator_MissingBinaryReturnsFailedWithHint(t *testing.T) {
	t.Setenv("PATH", "")
	v := NewPythonValidator()
	decision, err := v.Validate(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Errorf("got decision %s, want failed", decision)
	}
	if !strings.Contains(err.Error(), "python3") || !strings.Contains(err.Error(), "validator.command") {
		t.Errorf("err = %q, want it to mention 'python3' and 'validator.command'", err.Error())
	}
}
