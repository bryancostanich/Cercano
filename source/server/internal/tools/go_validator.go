package tools

import (
	"context"
	"fmt"
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
	outStr, ok, err := runValidator(ctx, goValidateTimeout, dir, "go", "test", "-c", "-o", "/dev/null")
	if err != nil {
		return Failed, fmt.Errorf("compilation failed:\n%s", outStr)
	}
	if !ok {
		if strings.Contains(outStr, "no test files") {
			buildOut, buildOK, buildErr := runValidator(ctx, goValidateTimeout, dir, "go", "build", "-o", "/dev/null", "./...")
			if buildErr != nil {
				return Failed, fmt.Errorf("build failed:\n%s", buildOut)
			}
			if !buildOK {
				return Failed, fmt.Errorf("build failed:\n%s", buildOut)
			}
			return Passed, nil
		}
		return Failed, fmt.Errorf("compilation failed:\n%s", outStr)
	}

	runOut, runOK, err := runValidator(ctx, goValidateTimeout, dir, "go", "test", "-v")
	if err != nil {
		return Failed, fmt.Errorf("tests failed:\n%s", runOut)
	}
	if !runOK {
		return Failed, fmt.Errorf("tests failed:\n%s", runOut)
	}

	return Passed, nil
}

// cleanOutput trims whitespace and standardizes error messages for the LLM.
func cleanOutput(out string) string {
	return strings.TrimSpace(out)
}
