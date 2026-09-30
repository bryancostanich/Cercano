//go:build unix

package builtins

import (
	"os"
	"syscall"
)

// Nonblocking open lets the caller reject FIFOs/devices by fstat without
// hanging before validation, including when the input path is replaced.
func openDiagnosticInput(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
