package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// NodeValidator runs `npm run build` in workDir.
type NodeValidator struct{}

func NewNodeValidator() *NodeValidator { return &NodeValidator{} }

func (v *NodeValidator) Validate(ctx context.Context, workDir string) (Decision, error) {
	if _, err := exec.LookPath("npm"); err != nil {
		return Failed, errors.New("node validator: command 'npm' not found in PATH — install Node.js or set validator.command in .cercano/config.yaml to override")
	}
	cmd := exec.CommandContext(ctx, "npm", "run", "build", "--silent")
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Failed, fmt.Errorf("npm run build failed:\n%s", cleanOutput(string(out)))
	}
	return Passed, nil
}
