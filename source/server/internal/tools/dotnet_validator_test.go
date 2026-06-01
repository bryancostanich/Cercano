package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"cercano/source/server/internal/testfixtures"
)

func skipIfNoDotnet(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet not in PATH; skipping integration test")
	}
}

func TestDotnetValidator_PassesOnValidProject(t *testing.T) {
	skipIfNoDotnet(t)
	// Use Copy: dotnet build writes bin/ and obj/ into the project dir.
	dir := testfixtures.Copy(t, "dotnet/valid")
	v := NewDotnetValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision != Passed {
		t.Errorf("got decision %s, want passed", decision)
	}
}

func TestDotnetValidator_FailsOnBrokenProject(t *testing.T) {
	skipIfNoDotnet(t)
	dir := testfixtures.Copy(t, "dotnet/broken")
	v := NewDotnetValidator()
	decision, err := v.Validate(context.Background(), dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Errorf("got decision %s, want failed", decision)
	}
}

func TestDotnetValidator_MissingBinaryReturnsFailedWithHint(t *testing.T) {
	t.Setenv("PATH", "")
	v := NewDotnetValidator()
	decision, err := v.Validate(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
	if decision != Failed {
		t.Errorf("got decision %s, want failed", decision)
	}
	if !strings.Contains(err.Error(), "validator.command") {
		t.Errorf("err = %q, want it to mention 'validator.command'", err.Error())
	}
}
