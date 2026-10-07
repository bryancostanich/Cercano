//go:build !windows && !darwin && !linux

package exclusion

import (
	"errors"
	"os"
)

func tryLock(*os.File, Mode) error { return errors.New("unsupported exclusion platform") }
func unlock(*os.File) error        { return nil }
