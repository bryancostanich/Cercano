//go:build darwin

package config

import (
	"os/exec"
	"strconv"
	"strings"
)

func detectPhysicalMemoryBytes() uint64 {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return v
}
