//go:build unix

package worker

import (
	"os/exec"
	"strconv"
	"syscall"
)

// realReapSeams is the production wiring.
func realReapSeams() reapSeams {
	return reapSeams{
		groupAlive:    func(pgid int) bool { return syscall.Kill(-pgid, 0) == nil },
		identifyGroup: groupLooksLikeWorker,
		pidAlive:      func(pid int) bool { return syscall.Kill(pid, 0) == nil },
		killGroup:     killGroupByPgid,
	}
}

// groupLooksLikeWorker reports whether the process group led by pgid has a member
// whose command line is a cercano worker. The worker is spawned as
// `cercano worker --socket <path>` (see spawnWorker), so "cercano worker" is a
// stable substring that identifies it and CANNOT match the host itself
// (`cercano agent` / bare `cercano`) or an unrelated process. This is the
// identity guard: a recycled pid that is not running a cercano worker fails here
// and is never killed.
func groupLooksLikeWorker(pgid int) bool {
	return exec.Command("pgrep", "-f", "-g", strconv.Itoa(pgid), "cercano worker").Run() == nil
}

// killGroupByPgid SIGKILLs the whole process group led by pgid.
func killGroupByPgid(pgid int) {
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
