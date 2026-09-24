//go:build !darwin || !cgo

package brewrestart

import "fmt"

func Inspect(pid int) (Identity, error) {
	return Identity{}, fmt.Errorf("Homebrew process inspection requires macOS with cgo")
}
