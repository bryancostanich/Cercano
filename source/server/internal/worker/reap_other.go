//go:build !unix

package worker

// Orphan reaping relies on POSIX process groups and pgrep to safely identify
// a leftover worker before killing it (see reap.go's identity guard). Neither
// exists here, so there is no safe way to positively identify a stale pid.
// realReapSeams therefore treats every recorded group as already dead: no
// process is ever killed, but reapOnePidfile still sweeps the stale pidfile
// and socket files, which is the only cleanup this platform needs — a
// hard-killed host doesn't leave orphaned child processes behind the way a
// SIGKILLed process-group leader does on POSIX.
func realReapSeams() reapSeams {
	return reapSeams{
		groupAlive:    func(pgid int) bool { return false },
		identifyGroup: func(pgid int) bool { return false },
		pidAlive:      func(pid int) bool { return false },
		killGroup:     func(pgid int) {},
	}
}
