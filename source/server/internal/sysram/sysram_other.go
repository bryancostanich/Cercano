//go:build !darwin && !linux

package sysram

// Total returns 0 on platforms without a probe implementation; callers
// render "unknown" instead of a fit verdict.
func Total() int64 {
	return 0
}
