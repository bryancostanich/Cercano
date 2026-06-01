package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const minimalFsproj = `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <OutputType>Library</OutputType>
    <TargetFramework>net8.0</TargetFramework>
  </PropertyGroup>
  <ItemGroup><Compile Include="Lib.fs" /></ItemGroup>
</Project>
`

const validFs = "module Lib\nlet add a b = a + b\n"
const brokenFs = "module Lib\nlet add a b = a + b +\n"

func skipIfNoDotnet(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet not in PATH; skipping integration test")
	}
}

func TestDotnetValidator_PassesOnValidProject(t *testing.T) {
	skipIfNoDotnet(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Lib.fsproj"), []byte(minimalFsproj), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Lib.fs"), []byte(validFs), 0644); err != nil {
		t.Fatal(err)
	}
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
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Lib.fsproj"), []byte(minimalFsproj), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Lib.fs"), []byte(brokenFs), 0644); err != nil {
		t.Fatal(err)
	}
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
