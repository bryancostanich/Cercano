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
	out, ok, err := runValidator(ctx, nodeValidateTimeout, workDir, "npm", "run", "build", "--silent")
	if err != nil {
		return Failed, fmt.Errorf("npm run build failed: %w\n%s", err, out)
	}
	if !ok {
		return Failed, fmt.Errorf("npm run build failed:\n%s", out)
	}
	return Passed, nil
}
