package tools

import (
	"context"
	"fmt"
)

// CustomValidator runs a user-supplied shell command via 'sh -c' in workDir.
type CustomValidator struct {
	command string
}

// NewCustomValidator returns a validator that runs `sh -c <command>` in workDir.
func NewCustomValidator(command string) *CustomValidator {
	return &CustomValidator{command: command}
}

func (v *CustomValidator) Validate(ctx context.Context, workDir string) (Decision, error) {
	out, ok, err := runValidator(ctx, customValidateTimeout, workDir, "sh", "-c", v.command)
	if err != nil {
		return Failed, fmt.Errorf("custom validator failed: %s\n%s", err, out)
	}
	if !ok {
		return Failed, fmt.Errorf("custom validator failed: %s\n%s", "non-zero exit", out)
	}
	return Passed, nil
}
