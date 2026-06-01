package tools

import (
	"context"
	"fmt"
	"os/exec"
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
	cmd := exec.CommandContext(ctx, "sh", "-c", v.command)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Failed, fmt.Errorf("custom validator failed: %s\n%s", err, cleanOutput(string(out)))
	}
	return Passed, nil
}
