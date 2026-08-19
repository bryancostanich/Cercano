//go:build !darwin && !linux

package sysram

// Total returns 0 on platforms without a probe implementation; callers
// render "unknown" instead of a fit verdict.
func Total() int64 {
	return 0
}

// NonEvictable reports unknown on platforms without a probe. Callers must
// fall back to their own accounting rather than reading the zero as an
// idle machine, which would permit every spawn.
func NonEvictable() (int64, bool) {
	return 0, false
}
