//go:build windows

package updater

import (
	"os"
	"path/filepath"
)

// anyBinaryRunningHost is the Windows branch of the "is a required binary
// currently running from the active version dir" check.
//
// Windows locks executable files of running processes (sharing violation on
// rename/replace), so before activating a version the coordinator must
// verify none of the ACTIVE version's binaries is running. The probe: a
// no-op RENAME of each active binary. If the rename fails, the file is in
// use by some process.
//
// SPIKE STATUS: this implementation is Windows-only and unverified on real
// Windows in this spike (it cross-compiles but was not executed locally —
// see README "Remaining Windows-native verification"). The parent task will
// add a credential-free Windows CI probe to exercise it.
func anyBinaryRunningHost(o Options) (bool, string) {
	m, err := ReadActiveManifest(o.InstallDir)
	if err != nil {
		return false, ""
	}
	for _, b := range o.Binaries {
		p := filepath.Join(o.InstallDir, versionsDirName, "v"+m.Version, "bin", b)
		if _, err := os.Stat(p); err != nil {
			continue // active version does not contain this binary
		}
		if err := os.Rename(p, p); err != nil {
			return true, b
		}
	}
	return false, ""
}
