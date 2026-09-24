// Package brewrestart contains safety checks for the Homebrew restart coordinator.
package brewrestart

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Identity is obtained from the kernel, not from mutable argv[0] or PATH.
// Start time distinguishes a process from a later reuse of the same PID.
type Identity struct {
	PID               int
	UID               uint32
	Executable        string
	StartSeconds      uint64
	StartMicroseconds uint64
}

func (p Identity) SameProcess(other Identity) bool {
	return p.PID == other.PID && p.UID == other.UID &&
		p.StartSeconds == other.StartSeconds && p.StartMicroseconds == other.StartMicroseconds &&
		p.Executable == other.Executable
}

// FormulaRoot accepts only the canonical layout installed by the formula.
// The caller resolves the NEW binary's symlinks; a running process's kernel
// path must never be resolved through the now-repointed prefix symlink.
func FormulaRoot(executable string) (string, error) {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || filepath.Base(executable) != "cercano" {
		return "", fmt.Errorf("not an absolute canonical cercano executable: %q", executable)
	}
	bin := filepath.Dir(executable)
	version := filepath.Dir(bin)
	formula := filepath.Dir(version)
	if filepath.Base(bin) != "bin" || filepath.Base(formula) != "cercano" || filepath.Base(filepath.Dir(formula)) != "Cellar" || filepath.Base(version) == "" {
		return "", fmt.Errorf("executable is not in Cellar/cercano/<version>/bin: %q", executable)
	}
	return formula, nil
}

// CheckOwned fails closed for another user, an unknown process identity, a
// development binary, or a different Homebrew installation. It does not signal
// the process or establish that it owns the agent's listening socket.
func CheckOwned(process Identity, newExecutable string, uid uint32) error {
	if process.PID <= 0 || process.StartSeconds == 0 || process.UID != uid {
		return fmt.Errorf("process identity is incomplete or belongs to another user")
	}
	newRoot, err := FormulaRoot(newExecutable)
	if err != nil {
		return err
	}
	oldRoot, err := FormulaRoot(process.Executable)
	if err != nil {
		return err
	}
	if oldRoot != newRoot || !strings.HasPrefix(process.Executable, newRoot+string(filepath.Separator)) {
		return fmt.Errorf("process belongs to another installation: %q", process.Executable)
	}
	return nil
}
