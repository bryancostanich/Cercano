//go:build !darwin && !linux

package llamaserver

import (
	"os"
	"os/exec"
)

func setProcessGroup(cmd *exec.Cmd) {}

func killProcess(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return proc.Kill()
}
