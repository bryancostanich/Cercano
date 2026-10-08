//go:build !windows

package launch

import "os"

func readFixtureFile(path string) ([]byte, error) { return os.ReadFile(path) }
