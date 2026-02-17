package capabilities

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Validator defines the interface for verifying code.
// Moved to interfaces.go

// GoTestValidator runs 'go test' in the specified directory.
type GoTestValidator struct{}

// NewGoTestValidator creates a new validator that runs standard Go tests.
func NewGoTestValidator() *GoTestValidator {
	return &GoTestValidator{}
}

// Validate runs 'go test -c' first (to check compilation), and then 'go test'.
// It returns an error containing the compiler/test output if it fails.
func (v *GoTestValidator) Validate(ctx context.Context, dir string) error {
	// Step 1: Check Compilation (go test -c)
	// -c compiles the test binary but does not run it. This is faster/safer for syntax checks.
	// We use 'go test -c' instead of 'go build' because we are validating test files.
	cmd := exec.CommandContext(ctx, "go", "test", "-c", "-o", "/dev/null")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("compilation failed:\n%s", cleanOutput(string(output)))
	}

	// Step 2: Run Tests (go test)
	// Only run if compilation passed.
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
