//go:build darwin

// Package sysram reports the machine's total physical memory. On Apple
// Silicon this is the unified memory pool — the same budget the GPU
// draws from, which is what makes it the right denominator for "will
// this model fit?" verdicts.
package sysram

import "golang.org/x/sys/unix"

// Total returns total physical memory in bytes, or 0 if the probe
// fails (callers render "unknown" rather than a wrong verdict).
func Total() int64 {
	v, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return int64(v)
}
