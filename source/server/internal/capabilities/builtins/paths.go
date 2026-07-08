package builtins

import "path/filepath"

// resolvePath resolves p against workDir when p is relative; an absolute p is
// returned unchanged, and an empty workDir leaves p as-is (process-cwd
// fallback). Replaces the process-global os.Chdir the turn handler did, so
// concurrent turns in different workspaces never share a cwd.
func resolvePath(workDir, p string) string {
	if p == "" || workDir == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workDir, p)
}
