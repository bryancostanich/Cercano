package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// RustValidator runs `cargo build` in workDir.
type RustValidator struct{}

func NewRustValidator() *RustValidator { return &RustValidator{} }

func (v *RustValidator) Validate(ctx context.Context, workDir string) (Decision, error) {
	if _, err := exec.LookPath("cargo"); err != nil {
		return Failed, errors.New("rust validator: command 'cargo' not found in PATH — install the Rust toolchain or set validator.command in .cercano/config.yaml to override")
	}
	cmd := exec.CommandContext(ctx, "cargo", "build", "--quiet")
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Failed, fmt.Errorf("cargo build failed:\n%s", cleanOutput(string(out)))
	}
	return Passed, nil
}
