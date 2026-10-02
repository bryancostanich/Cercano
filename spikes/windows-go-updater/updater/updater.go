// Package updater is a THROWAWAY prototype coordinator demonstrating the
// "external updater + versioned immutable directories + active-version
// manifest commit" architecture for the spike question:
//
//	Q: Can a coordinator stage BOTH Cercano binaries together and make the
//	   activation — a single active-version manifest write — atomic across
//	   both, with rollback to the previously active version?
//
// Scope caveats (do NOT read more into this package than it demonstrates):
//
//   - It demonstrates STAGING + COMMIT-POINT + ROLLBACK of a versioned
//     directory strategy with the real go-selfupdate detection/download/
//     validation/extract APIs. It is NOT a production transaction runtime:
//     it does not pause the agent, does not verify the new version actually
//     runs before commit, and does not own the app lifecycle.
//   - "Restart/health failure after activation restores the old manifest" is
//     demonstrated by the explicit RestorePrevious() (WatchdogRollback) entry
//     point plus tests; nothing here watches the app automatically.
//
// Layout:
//
//	installDir/
//	  versions/v<version>/bin/cercano(.exe), cercano-cli(.exe)
//	  active.json                  (active-version manifest; ONE atomic file
//	                                 write is the commit point)
//	  versions/staged-v<version>/  (staging dir; renamed on activation)
//	  .update.lock                 (OS-level lock; only one updater runs)
package updater

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	selfupdate "github.com/creativeprojects/go-selfupdate"
)

const (
	// manifestName is the active-version manifest file name. Its atomic
	// replace is the COMMIT POINT for a version flip.
	manifestName = "active.json"
	// lockName is the coordinator's mutual-exclusion lock file name.
	lockName = ".update.lock"
	// versionsDirName is the directory holding one immutable sub-directory
	// per version.
	versionsDirName = "versions"
)

// ActiveManifest is the one-file source of truth for which versioned
// directory is live. Agents read this file (and nothing else) at startup to
// locate their binaries.
type ActiveManifest struct {
	Version   string    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BinaryNames returns the production Windows binary names for this spike
// (kept here so the coordinator owns the exact current Cercano names).
func BinaryNames() (agent, cli string) {
	return "cercano.exe", "cercano-cli.exe"
}

// Options configures a Coordinator run.
type Options struct {
	// Repository is the slug to list releases from.
	Repository selfupdate.Repository
	// Source serves the release assets (HTTP or a test fake).
	Source selfupdate.Source
	// Validator, when set, is applied EXPLICITLY to the archive before
	// anything is staged. Nil means NO validation (documented fact: the
	// library default performs none).
	Validator selfupdate.Validator
	// OS/Arch for asset matching. For current Cercano releases this must be
	// "windows" + "x64" (the asset name embeds windows-x64, NOT amd64) — or
	// Filters can be used instead.
	OS      string
	Arch    string
	Filters []string
	// InstallDir is the root containing versions/, active.json, the lock.
	InstallDir string
	// Binaries are the required command base names (e.g. cercano.exe,
	// cercano-cli.exe). All must be present in the archive or the run fails
	// before any staging.
	Binaries []string
	// ForceVersion, when non-empty, updates to that version instead of the
	// detected latest.
	ForceVersion string
}

// RunResult reports what a Coordinator run did.
type RunResult struct {
	// Activated is true when the version was staged AND committed (manifest
	// flipped and staging dir renamed to its final versioned name).
	Activated bool
	// Version is the version that was staged (and activated, if so).
	Version string
	// Release is the underlying selfupdate.Release (for AssetName etc.).
	Release *selfupdate.Release
}

// StageResult is what Stage() produced: a fully validated staging directory.
type StageResult struct {
	// StagingDir holds the staged, validated binaries under versions/.
	StagingDir string
	// Version that was staged.
	Version string
}

// Stage is the plan-and-prepare step: detect (or take a forced) version,
// fetch the EXACT current Cercano archive from the source, validate it if a
// validator is configured, extract EVERY required binary into a temporary
// staging directory under versions/ (in a try-then-rename pattern so a
// failed run never leaves a partial versioned directory), verify both
// binaries are present, and check they are not yet running (so activation
// does not hit a locked file).
//
// Stage is idempotent and side-effect-free on the active manifest: if
// anything here fails, the currently active version is untouched.
func Stage(ctx context.Context, o Options) (*StageResult, *selfupdate.Release, error) {
	if o.Validator == nil {
		return nil, nil, errors.New("validator is required")
	}
	if o.InstallDir == "" || len(o.Binaries) == 0 {
		return nil, nil, errors.New("InstallDir and Binaries are required")
	}
	if o.OS == "" || o.Arch == "" {
		return nil, nil, errors.New("OS and Arch are required (use windows/x64 for current Cercano releases)")
	}

	up, err := selfupdate.NewUpdater(selfupdate.Config{
		Source:    o.Source,
		Validator: o.Validator,
		OS:        o.OS,
		Arch:      o.Arch,
		Filters:   o.Filters,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build updater: %w", err)
	}

	var rel *selfupdate.Release
	if o.ForceVersion != "" {
		rel, _, err = up.DetectVersion(ctx, o.Repository, o.ForceVersion)
		if err != nil {
			return nil, nil, fmt.Errorf("detect version %s: %w", o.ForceVersion, err)
		}
	} else {
		rel, _, err = up.DetectLatest(ctx, o.Repository)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("detect latest: %w", err)
	}
	if rel == nil {
		return nil, nil, errors.New("no matching release found")
	}

	// Download + validate (explicit; nil validator = none) + extract ONE
	// archive into a local byte buffer. The library's DecompressCommand
	// wants the FULL archive once per binary — that fact is demonstrated in
	// lib/extract_test.go; buffering it here avoids re-downloading.
	data, err := fetchValidated(ctx, o.Source, o.Validator, rel)
	if err != nil {
		return nil, nil, err
	}

	if err := validateArchiveStructure(data, rel.AssetName, o.Binaries); err != nil {
		return nil, nil, fmt.Errorf("archive validation failed: %w", err)
	}

	versionsDir := filepath.Join(o.InstallDir, versionsDirName)
	if err := os.MkdirAll(versionsDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create versions dir: %w", err)
	}

	// Staging dir: temp first, then rename to staged-v<version> so a crash
	// mid-extract cannot be mistaken for a real version directory.
	stagingFinal := filepath.Join(versionsDir, "staged-v"+rel.Version())
	stagingTmp := stagingFinal + ".tmp"

	// Start clean (previous failed attempts of the same version).
	_ = os.RemoveAll(stagingTmp)
	if err := os.RemoveAll(stagingFinal); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("clean prior staging: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(stagingTmp, "bin"), 0o755); err != nil {
		return nil, nil, fmt.Errorf("create staging dir: %w", err)
	}

	for _, b := range o.Binaries {
		r, err := selfupdate.DecompressCommand(
			bytes.NewReader(data), rel.AssetName, b, o.OS, o.Arch)
		if err != nil {
			_ = os.RemoveAll(stagingTmp)
			return nil, nil, fmt.Errorf("extract %s: %w", b, err)
		}
		if err := writeExecutableFile(filepath.Join(stagingTmp, "bin", b), r); err != nil {
			_ = os.RemoveAll(stagingTmp)
			return nil, nil, err
		}
	}

	// Verify every required binary is present and non-empty before staging
	// is considered successful (missing-binary archives are rejected here,
	// not at activation time).
	for _, b := range o.Binaries {
		st, err := os.Stat(filepath.Join(stagingTmp, "bin", b))
		if err != nil || st.Size() == 0 {
			_ = os.RemoveAll(stagingTmp)
			return nil, nil, fmt.Errorf("staged archive missing binary %s: %v", b, err)
		}
	}

	// If any binary is currently RUNNING from the active dir, activation
	// would hit a locked file; bail out here instead (POSIX semantics would
	// silently succeed; Windows semantics would fail — Stage must behave the
	// same on both and leave the manifest untouched on this path).
	if running, which := anyBinaryRunning(o); running {
		_ = os.RemoveAll(stagingTmp)
		return nil, rel, fmt.Errorf("binary %s is currently running from the active version; not staging", which)
	}

	if err := os.Rename(stagingTmp, stagingFinal); err != nil {
		_ = os.RemoveAll(stagingTmp)
		return nil, nil, fmt.Errorf("publish staging dir: %w", err)
	}
	return &StageResult{StagingDir: stagingFinal, Version: rel.Version()}, rel, nil
}

// Activate commits a staged version: it renames the staging directory to its
// final immutable name versions/v<version>/ and atomically flips the
// active-version manifest active.json. The manifest write is the COMMIT
// POINT: before it, nothing changed for readers; after it, every reader that
// resolves binaries through the manifest sees the new version.
//
// Activate MUST be called while holding the coordinator lock (RunWithLock).
func Activate(o Options, staged *StageResult) error {
	versionsDir := filepath.Join(o.InstallDir, versionsDirName)
	final := filepath.Join(versionsDir, "v"+staged.Version)
	if err := os.Rename(staged.StagingDir, final); err != nil {
		return fmt.Errorf("activate version dir: %w", err)
	}
	if err := writeManifestAtomic(filepath.Join(o.InstallDir, manifestName), ActiveManifest{
		Version:   staged.Version,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		// Manifest write failed: the version directory exists but nothing
		// references it. Roll the rename back so the tree matches the still
		// OLD manifest.
		if rb := os.Rename(final, staged.StagingDir); rb != nil {
			return fmt.Errorf("manifest write failed (%v) and rollback of version dir also failed (%v); version dir %s is orphaned", err, rb, final)
		}
		return fmt.Errorf("write active manifest: %w", err)
	}
	return nil
}

// Abort discards a staged version without touching the active manifest.
func Abort(o Options, staged *StageResult) error {
	if staged == nil {
		return nil
	}
	if err := os.RemoveAll(staged.StagingDir); err != nil {
		return fmt.Errorf("abort staging: %w", err)
	}
	return nil
}

// RunResult for RunWithLock is RunResult above.

// RunWithLock runs fn under the OS-level coordinator lock (a single
// lock-owner across processes; persistent lock file). fn receives
// nothing; it closes over its Options. The OS releases the lock when a holder exits.
func RunWithLock(o Options, fn func() (*RunResult, error)) (*RunResult, error) {
	lockPath := filepath.Join(o.InstallDir, lockName)
	if err := os.MkdirAll(o.InstallDir, 0o755); err != nil {
		return nil, fmt.Errorf("create install dir: %w", err)
	}
	fd, err := lockfileCreate(lockPath, false /*blocking*/, 5*time.Minute /*stale*/)
	if err != nil {
		return nil, fmt.Errorf("acquire update lock %s: %w", lockPath, err)
	}
	defer func() {
		_ = lockfileClose(fd)
	}()
	return fn()
}

// UpdateToLatest is the full coordinator sequence under the lock:
// Stage → (already-active check) → Activate. It returns what happened.
//
// Injection point used by tests: o.Validator / fake source failures abort
// BEFORE Activate (staging discarded, manifest untouched); manifest-write
// failures roll back the version-directory rename (see Activate).
func UpdateToLatest(ctx context.Context, o Options) (*RunResult, error) {
	return RunWithLock(o, func() (*RunResult, error) {
		staged, rel, err := Stage(ctx, o)
		if err != nil {
			return nil, err
		}
		cur, merr := ReadActiveManifest(o.InstallDir)
		if merr == nil && cur.Version == staged.Version {
			// Idempotent no-op: the staged version is already active; discard
			// staging and report Activated=false without error.
			_ = Abort(o, staged)
			return &RunResult{Activated: false, Version: staged.Version, Release: rel}, nil
		}
		if err := Activate(o, staged); err != nil {
			_ = Abort(o, staged)
			return nil, err
		}
		return &RunResult{Activated: true, Version: staged.Version, Release: rel}, nil
	})
}

// RestorePrevious is the WATCHDOG ROLLBACK entry point: after activation, if
// the new version fails to start or fails its health check, a supervisor
// calls this to switch the active manifest back to a previous version that
// still exists on disk. It does NOT claim the new process is dead — that is
// the supervisor's job (recorded as an explicit constraint).
func RestorePrevious(o Options, version string) error {
	if version == "" {
		return errors.New("no previous version to restore")
	}
	dir := filepath.Join(o.InstallDir, versionsDirName, "v"+version)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("previous version %s not present on disk: %w", version, err)
	}
	return writeManifestAtomic(filepath.Join(o.InstallDir, manifestName), ActiveManifest{
		Version:   version,
		UpdatedAt: time.Now().UTC(),
	})
}

// ReadActiveManifest loads active.json (or reports that none exists).
func ReadActiveManifest(installDir string) (*ActiveManifest, error) {
	data, err := os.ReadFile(filepath.Join(installDir, manifestName))
	if err != nil {
		return nil, err
	}
	var m ActiveManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ActiveBinaryPath resolves a binary through the active manifest — this is
// how the app should locate its executables.
func ActiveBinaryPath(installDir, binary string) (string, error) {
	m, err := ReadActiveManifest(installDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(installDir, versionsDirName, "v"+m.Version, "bin", binary), nil
}

// writeExecutableFile writes an extracted binary with fixed exec perms and
// fsyncs it (stage must not vaporize on a crash).
func writeExecutableFile(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	return f.Close()
}

// writeManifestAtomic writes the manifest to a temp file in the same
// directory and renames it over the target — one atomic flip.
func writeManifestAtomic(path string, m ActiveManifest) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create manifest tmp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write manifest tmp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync manifest tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// fetchValidated downloads the archive once and, when a validator is
// configured, applies the library's real Validator interface
// (Validate(filename, release, asset)) against the release and its sidecar
// asset. The library's own UpdateTo would re-download the archive per
// binary; the coordinator instead validates once and stages from the local
// bytes — so nothing but the validated bytes is ever staged.
func fetchValidated(ctx context.Context, src selfupdate.Source, v selfupdate.Validator, rel *selfupdate.Release) ([]byte, error) {
	rc0, err := rc(ctx, src, rel, rel.AssetID)
	if err != nil {
		return nil, fmt.Errorf("download archive: %w", err)
	}
	defer rc0.Close()
	data, err := readBounded(rc0, maxArchiveBytes)
	if err != nil {
		return nil, fmt.Errorf("download archive: %w", err)
	}
	if v != nil {
		if rel.ValidationAssetID < 0 {
			return nil, fmt.Errorf("%w: %s", selfupdate.ErrValidationAssetNotFound, v.GetValidationAssetName(rel.AssetName))
		}
		side, err := rc(ctx, src, rel, rel.ValidationAssetID)
		if err != nil {
			return nil, fmt.Errorf("download validation asset: %w", err)
		}
		defer side.Close()
		sidedata, err := readBounded(side, 64*1024)
		if err != nil {
			return nil, fmt.Errorf("download validation asset: %w", err)
		}
		if err := v.Validate(rel.AssetName, data, sidedata); err != nil {
			return nil, fmt.Errorf("validate %s: %w", rel.AssetName, err)
		}
	}
	return data, nil
}

// rc opens a single asset from the source (archive or validation sidecar).
func rc(ctx context.Context, src selfupdate.Source, rel *selfupdate.Release, assetID int64) (io.ReadCloser, error) {
	r, err := src.DownloadReleaseAsset(ctx, rel, assetID)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errors.New("source returned nil asset reader")
	}
	return r, nil
}

// anyBinaryRunning reports whether any required binary in the ACTIVE version
// dir has a running process image (host-only fact, best effort on POSIX).
func anyBinaryRunning(o Options) (bool, string) {
	if runtime.GOOS != "windows" {
		return false, ""
	}
	return anyBinaryRunningHost(o)
}
