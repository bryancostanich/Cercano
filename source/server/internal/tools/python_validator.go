package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// PythonValidator runs `python -m compileall .` (or `python3` fallback) in workDir.
// Compile-only: walks the tree, compiles every .py file, reports syntax errors.
// Does not need a venv, does not run tests, does not install anything.
type PythonValidator struct{}

func NewPythonValidator() *PythonValidator { return &PythonValidator{} }

func (v *PythonValidator) Validate(ctx context.Context, workDir string) (Decision, error) {
	bin := ""
	if _, err := exec.LookPath("python3"); err == nil {
		bin = "python3"
	} else if _, err := exec.LookPath("python"); err == nil {
		bin = "python"
	} else {
		return Failed, errors.New("python validator: neither 'python3' nor 'python' found in PATH — install Python 3 or set validator.command in .cercano/config.yaml to override")
	}
	cmd := exec.CommandContext(ctx, bin, "-m", "compileall", "-q", ".")
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Failed, fmt.Errorf("python compile failed:\n%s", cleanOutput(string(out)))
	}
	return Passed, nil
}
