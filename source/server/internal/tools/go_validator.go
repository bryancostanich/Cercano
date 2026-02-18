package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Validator defines the interface for verifying code.
// Moved to interfaces.go

// GoValidator runs 'go test' or 'go build' in the specified directory.
type GoValidator struct{}

// NewGoValidator creates a new validator for Go projects.
func NewGoValidator() *GoValidator {
	return &GoValidator{}
}

// Validate runs 'go test' if tests exist, or 'go build' otherwise.
func (v *GoValidator) Validate(ctx context.Context, dir string) error {
	// For this track, we'll try 'go test -c' first which is more comprehensive if tests exist.
	// If it fails with "no test files", we'll fallback to 'go build'.
	
	cmd := exec.CommandContext(ctx, "go", "test", "-c", "-o", "/dev/null")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	
	if err != nil {
		outStr := string(output)
		if strings.Contains(outStr, "no test files") {
			// Fallback to go build
			buildCmd := exec.CommandContext(ctx, "go", "build", "-o", "/dev/null", "./...")
			buildCmd.Dir = dir
			buildOutput, buildErr := buildCmd.CombinedOutput()
			if buildErr != nil {
				return fmt.Errorf("build failed:\n%s", cleanOutput(string(buildOutput)))
			}
			return nil
		}
		return fmt.Errorf("compilation failed:\n%s", cleanOutput(outStr))
	}

	// Step 2: Run Tests (go test) if it's a test package
	cmdRun := exec.CommandContext(ctx, "go", "test", "-v")
	cmdRun.Dir = dir
	outputRun, err := cmdRun.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tests failed:\n%s", cleanOutput(string(outputRun)))
	}

	return nil
}

// cleanOutput trims whitespace and standardizes error messages for the LLM.
func cleanOutput(out string) string {
	return strings.TrimSpace(out)
}
