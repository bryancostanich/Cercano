package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// GoValidator runs 'go test' or 'go build' in the specified directory.
type GoValidator struct{}

// NewGoValidator creates a new validator for Go projects.
func NewGoValidator() *GoValidator {
	return &GoValidator{}
}

// Validate runs 'go test' if tests exist, or 'go build' otherwise.
func (v *GoValidator) Validate(ctx context.Context, dir string) (Decision, error) {
	cmd := exec.CommandContext(ctx, "go", "test", "-c", "-o", "/dev/null")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()

	if err != nil {
		outStr := string(output)
		if strings.Contains(outStr, "no test files") {
			buildCmd := exec.CommandContext(ctx, "go", "build", "-o", "/dev/null", "./...")
			buildCmd.Dir = dir
			buildOutput, buildErr := buildCmd.CombinedOutput()
			if buildErr != nil {
				return Failed, fmt.Errorf("build failed:\n%s", cleanOutput(string(buildOutput)))
			}
			return Passed, nil
		}
		return Failed, fmt.Errorf("compilation failed:\n%s", cleanOutput(outStr))
	}

	cmdRun := exec.CommandContext(ctx, "go", "test", "-v")
	cmdRun.Dir = dir
	outputRun, err := cmdRun.CombinedOutput()
	if err != nil {
		return Failed, fmt.Errorf("tests failed:\n%s", cleanOutput(string(outputRun)))
	}

	return Passed, nil
}

// cleanOutput trims whitespace and standardizes error messages for the LLM.
func cleanOutput(out string) string {
	return strings.TrimSpace(out)
}
