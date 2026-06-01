package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// DotnetValidator runs `dotnet build` in workDir.
type DotnetValidator struct{}

func NewDotnetValidator() *DotnetValidator { return &DotnetValidator{} }

func (v *DotnetValidator) Validate(ctx context.Context, workDir string) (Decision, error) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		return Failed, errors.New("dotnet validator: command 'dotnet' not found in PATH — install .NET SDK or set validator.command in .cercano/config.yaml to override")
	}
	cmd := exec.CommandContext(ctx, "dotnet", "build", "--nologo", "-clp:NoSummary")
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Failed, fmt.Errorf("dotnet build failed:\n%s", cleanOutput(string(out)))
	}
	return Passed, nil
}
