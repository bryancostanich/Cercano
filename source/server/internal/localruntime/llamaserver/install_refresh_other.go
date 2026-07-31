//go:build !windows

package llamaserver

// defaultRefreshPATH is a no-op outside Windows: macOS's brew symlinks into a
// directory that's already on PATH, and no other platform has a managed
// install path (install.go) that could leave PATH stale for a running
// process.
func defaultRefreshPATH() {}
