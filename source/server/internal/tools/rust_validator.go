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
	out, ok, err := runValidator(ctx, rustValidateTimeout, workDir, "cargo", "build", "--quiet")
	if err != nil {
		return Failed, fmt.Errorf("cargo build failed: %w\n%s", err, out)
	}
	if !ok {
		return Failed, fmt.Errorf("cargo build failed:\n%s", out)
	}
	return Passed, nil
}
