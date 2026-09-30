//go:build !unix

package builtins

import (
	"errors"
	"os"
)

func openDiagnosticInput(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("diagnostic input must be a regular file")
	}
	return os.Open(path)
}
