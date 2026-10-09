// Staging (Phase 5, bounded): materialize the members of an
// already-preflighted archive into ONE exclusively created staging
// directory, and nothing else.
//
// What this is:
//
//   - StageFile reads the caller's verified archive file ONCE into the same
//     bounded immutable in-memory snapshot PreflightFile uses (identity
//     binding, hard allocation ceiling, chunked context-checked reads).
//     The FULL preflight — receipt binding plus the strict structural
//     Check — runs over that snapshot and must COMPLETE before the staging
//     directory is created; a bad archive is rejected before a single
//     member file exists anywhere. Extraction then decompresses member
//     content from the immutable snapshot only: the mutable on-disk archive
//     is never reopened after the snapshot.
//   - Extraction writes ONLY approved regular files and the directories
//     their archive-internal paths need — every path came through the
//     portable member namespace (validateEntryName), and every member must
//     already be in the preflight manifest. The preflight manifest is NOT
//     trusted as authority: the staging pass independently re-runs the
//     same member rules, type checks, count/compressed/uncompressed bounds
//     and per-member size check against the bytes it actually copies, and
//     every copied member's SHA-256 must match the preflight hash.
//   - Writes are confined by a directory handle (os.Root): names are
//     resolved relative to the staging directory and cannot escape it, and
//     files are created with O_EXCL and no symlink following (Root.OpenFile
//     uses O_NOFOLLOW on the final component and refuses links escaping
//     the root on every component), so nothing is ever followed or
//     overwritten.
//   - Permissions come from the caller's trusted staging policy — document
//     files, executables and directories each get their own fixed mode,
//     enforced through the file handle — never from untrusted archive
//     mode bits. Special entries (links, devices, FIFOs) were already
//     rejected by preflight and are rejected again here.
//   - Each file is flushed (Sync) before it is closed; cancellation is
//     honored on every copy read.
//
// What this deliberately is NOT, so it can be reused safely:
//
//   - No activation, version selection, journal or receipt writing. The
//     returned Staged is only a staged directory plus the preflight
//     manifest; the manifest stays a preflight result, not immutable
//     authority for anything later.
//   - No execution, no signature verification (that belongs to the
//     acquisition boundary), no default paths and no default HOME.
//   - No RemoveAll anywhere. If staging fails, cleanup removes ONLY the
//     files and directories this staging itself created, and only while
//     each object is still the SAME object it was at creation time: every
//     created object's creation handle stays open (pinning its inode) from
//     creation until cleanup, so an original object's identifier can never
//     be recycled by an unlink/recreate (the inode ABA problem — a
//     replacement can otherwise carry the SAME device/inode identity,
//     which native CI on Linux proved happens in practice), and
//     os.SameFile against the FileInfo identity captured through that
//     pinned handle at creation is the primary proof, followed by a
//     Unix-only permission-mode check, the recorded byte count and — for
//     completed files — a hash read bounded to the recorded byte count. A
//     created object whose creation handle could not be opened is never
//     removed: without the pin its identity is not provably stable. A
//     replaced object is retained even when its replacement is
//     byte-identical or an identical empty directory. Unknown, injected
//     and replaced objects are left untouched AND reported, the staging
//     directory is left in place — and is itself removed only when empty
//     and still the same directory (same file identity) this staging
//     created, with the confinement root handle released after the
//     identity check and before the removal (on Windows an open root
//     handle prevents the final directory removal) — and the failure is
//     reported as a *StageError wrapping the original error and carrying
//     the retained directory and the retained object paths. Caller files
//     outside the staging directory are never touched.
//   - Permission mode bits are a Unix concept. On Windows, permissions
//     are ACLs, so no cleanup decision on Windows compares mode bits;
//     object identity, byte counts and bounded hashes decide there.
//
// Caller preconditions (explicit, not checked here):
//
//   - parentPath must be an existing private directory, chosen and
//     validated by the caller, OUTSIDE the live version directory and
//     user data. StageFile only checks it is an existing directory that is
//     not a symlink; it does not audit the caller's choice.
//   - On Windows, privacy is an ACL property of that parent: the caller
//     must ensure the parent's ACL restricts access before staging.
//     StageFile neither sets nor verifies ACLs and makes no claim that the
//     staging directory's mode bits (a Unix concept) provide privacy on
//     Windows.
package archivecheck

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"runtime"
	"strings"
)

// StageOptions is the trusted caller's staging policy. There are no
// defaults: every field must be set explicitly, and the modes must be pure
// permission bits, so untrusted archive mode bits can never leak through.
type StageOptions struct {
	// FileMode is the fixed mode for staged document members (e.g. 0o644).
	// It must not carry execute bits: non-executable members are documents
	// and the staging policy — not the archive — decides their mode.
	FileMode fs.FileMode
	// ExecutableMode is the fixed mode for members listed in Executables
	// (e.g. 0o755). It must carry at least one execute bit.
	ExecutableMode fs.FileMode
	// DirMode is the fixed mode for staged directories (e.g. 0o755).
	DirMode fs.FileMode
	// Executables names the members staged with ExecutableMode, using the
	// same root-relative archive-internal slash names as Layout.Required
	// and Layout.Optional ("bin/cercano"). Every name must be one of the
	// layout's allowed members; duplicates are refused.
	Executables []string
	// StagingPattern is the prefix of the exclusively created staging
	// directory name under parentPath (os.MkdirTemp appends a random
	// suffix). It must be a single portable name segment.
	StagingPattern string
}

// Staged is the result of one successful staging: the staging directory and
// the preflight manifest that describes its regular-file members. It is a
// receipt, not authority: activation must independently revalidate
// anything it later does with these bytes.
type Staged struct {
	// Dir is the staging directory path under parentPath.
	Dir string
	// Manifest is the preflight manifest of the staged members.
	Manifest *Manifest
}

// StageError reports a failed staging whose cleanup could not be completed
// safely. Retained lists the member-relative object paths left in place:
// objects that are no longer the ones this staging created (replaced with
// something else, even byte-identical), objects this staging never created
// (unknown or injected), and objects whose removal failed. StageDir is the
// retained staging directory itself. The caller decides how to surface and
// later collect the retained staging; this package never removes anything
// it did not verifiably create.
type StageError struct {
	// Err is the underlying staging failure.
	Err error
	// StageDir is the staging directory left in place ("" when the
	// staging directory itself was removed).
	StageDir string
	// Retained are member-relative paths left in place under StageDir.
	Retained []string
}

func (e *StageError) Error() string {
	msg := fmt.Sprintf("archivecheck: staging failed: %v", e.Err)
	if e.StageDir != "" {
		msg += fmt.Sprintf("; retained staging directory %q", e.StageDir)
	}
	if len(e.Retained) > 0 {
		msg += "; retained objects: " + strings.Join(e.Retained, ", ")
	}
	return msg
}

func (e *StageError) Unwrap() error { return e.Err }

// StageFile performs the bounded Phase 5 staging of one already-verified
// archive: full preflight of the immutable snapshot FIRST, then extraction
// of the approved members into ONE exclusively created staging directory
// under parentPath. See the package staging comment for the exact trust
// boundary, confinement and cleanup rules. The archive file is opened
// read-only exactly once and is never reopened after its snapshot is taken.
func StageFile(ctx context.Context, archivePath, parentPath string, opts Options, stage StageOptions) (*Staged, error) {
	if ctx == nil {
		return nil, errors.New("archivecheck: context is required")
	}
	if archivePath == "" {
		return nil, errors.New("archivecheck: archive path is required")
	}
	if parentPath == "" {
		return nil, errors.New("archivecheck: staging parent path is required")
	}
	if err := validateOptionValues(opts); err != nil {
		return nil, err
	}
	if err := validateStageOptions(opts, stage); err != nil {
		return nil, err
	}
	// The parent must be an existing directory that is not a symlink. The
	// trust decision on this parent (private, outside live/user data;
	// Windows ACLs) belongs to the caller and is not re-audited here.
	pfi, err := os.Lstat(parentPath)
	if err != nil {
		return nil, fmt.Errorf("archivecheck: staging parent %q: %w", parentPath, err)
	}
	if pfi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("archivecheck: staging parent %q is a symlink; the parent must be the caller's validated directory itself", parentPath)
	}
	if !pfi.IsDir() {
		return nil, fmt.Errorf("archivecheck: staging parent %q is not a directory", parentPath)
	}
	f, size, err := openVerifiedArchive(archivePath, opts.Bounds.MaxCompressedBytes)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// FULL preflight over the one immutable snapshot: receipt binding plus
	// the strict structural Check. Nothing is staged yet, so a rejected
	// archive leaves no member file anywhere.
	snap, man, err := preflightSnapshot(ctx, f, size, opts)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("archivecheck: canceled after preflight, before staging: %w", err)
	}
	// The exclusive staging directory: created fresh and empty with
	// os.MkdirTemp's O_EXCL retry semantics, so nothing pre-exists under
	// it and nothing outside it is touched by staging or cleanup. It stays
	// private (0700 on Unix) until activation; activation owns what the
	// directory finally becomes.
	stageDir, err := os.MkdirTemp(parentPath, stage.StagingPattern)
	if err != nil {
		return nil, fmt.Errorf("archivecheck: creating the exclusive staging directory under %q: %w", parentPath, err)
	}
	// Capture the staging directory's creation identity (its FileInfo,
	// usable with os.SameFile) immediately, so cleanup can never remove a
	// replacement directory that merely occupies this path after a rename.
	stageDirInfo, err := os.Lstat(stageDir)
	if err != nil {
		// The directory is brand new and empty; removing it cannot touch
		// any caller object. This is a direct removal of the empty
		// staging directory, never a RemoveAll.
		os.Remove(stageDir)
		return nil, fmt.Errorf("archivecheck: identifying the exclusive staging directory: %w", err)
	}
	root, err := os.OpenRoot(stageDir)
	if err != nil {
		// The directory is brand new and empty; removing it cannot touch
		// any caller object. This is a direct removal of the empty
		// staging directory, never a RemoveAll.
		os.Remove(stageDir)
		return nil, fmt.Errorf("archivecheck: opening the staging directory handle: %w", err)
	}
	defer root.Close()
	ledger := newStageLedger()
	a := Archive{Reader: bytes.NewReader(snap)}
	if opts.Format == Zip {
		a = Archive{ReaderAt: bytes.NewReader(snap), Size: int64(len(snap))}
	}
	if err := stageExtract(ctx, a, opts, stage, man, root, ledger); err != nil {
		retained, keepStage := cleanupStaging(root, stageDir, stageDirInfo, ledger)
		if len(retained) > 0 || keepStage {
			se := &StageError{Err: err, Retained: retained}
			if keepStage {
				se.StageDir = stageDir
			}
			return nil, se
		}
		return nil, fmt.Errorf("archivecheck: staging failed: %w", err)
	}
	// Success: release every pinned creation handle (files and
	// directories) and the confinement root before handing the staged
	// directory to the caller — on Windows an open handle would otherwise
	// block the caller's own use of the staged tree.
	ledger.closeAll()
	root.Close()
	return &Staged{Dir: stageDir, Manifest: man}, nil
}

// validateStageOptions checks the trusted staging policy for internal
// consistency and rejects anything that could let untrusted archive bits or
// ambiguous names through. There are no defaults.
func validateStageOptions(opts Options, stage StageOptions) error {
	for _, m := range []struct {
		name string
		mode fs.FileMode
	}{{"FileMode", stage.FileMode}, {"ExecutableMode", stage.ExecutableMode}, {"DirMode", stage.DirMode}} {
		if m.mode.Perm()&^fs.FileMode(0o777) != 0 || m.mode.Perm() == 0 {
			return fmt.Errorf("archivecheck: StageOptions.%s must be a non-zero pure permission mode, not %v", m.name, m.mode)
		}
	}
	if stage.FileMode.Perm()&0o111 != 0 {
		return fmt.Errorf("archivecheck: StageOptions.FileMode must not carry execute bits, got %v", stage.FileMode.Perm())
	}
	if stage.ExecutableMode.Perm()&0o111 == 0 {
		return fmt.Errorf("archivecheck: StageOptions.ExecutableMode must carry execute bits, got %v", stage.ExecutableMode.Perm())
	}
	if stage.StagingPattern == "" {
		return errors.New("archivecheck: StageOptions.StagingPattern is required")
	}
	if err := validateEntryName(stage.StagingPattern, false); err != nil {
		return fmt.Errorf("archivecheck: StageOptions.StagingPattern: %w", err)
	}
	// The executables policy must be a duplicate-free subset of the
	// trusted layout's allowed members, in the layout's own root-relative
	// namespace — a policy naming anything else is a misconfiguration.
	allowed := map[string]bool{}
	for _, n := range append(append([]string{}, opts.Layout.Required...), opts.Layout.Optional...) {
		allowed[joinRoot(opts.Layout.Root, n)] = true
	}
	seen := map[string]bool{}
	for _, n := range stage.Executables {
		if err := validateEntryName(n, false); err != nil {
			return fmt.Errorf("archivecheck: StageOptions.Executables %q: %w", n, err)
		}
		full := joinRoot(opts.Layout.Root, n)
		if !allowed[full] {
			return fmt.Errorf("archivecheck: StageOptions.Executables %q is not a member of the expected layout", n)
		}
		if seen[full] {
			return fmt.Errorf("archivecheck: StageOptions.Executables contains duplicate %q", n)
		}
		seen[full] = true
	}
	return nil
}

// stagedFile records the creation identity of one staged file: the policy
// mode it was created with, the FileInfo identity captured through the
// creation file handle, the pinned creation handle itself (open from
// creation until cleanup or success, so the file's inode cannot be
// recycled by an unlink/recreate), whether the staging actually created
// it, how many bytes were verifiably written, and — once the member
// completed — the SHA-256 of those bytes. Cleanup removes a file only
// while the pinned handle and os.SameFile still prove it is the same
// object this staging created.
type stagedFile struct {
	perm     fs.FileMode
	identity fs.FileInfo
	handle   *os.File
	created  bool
	written  int64
	complete bool
	hash     string
}

// close releases the pinned creation handle, exactly once.
func (r *stagedFile) close() {
	if r.handle != nil {
		r.handle.Close()
		r.handle = nil
	}
}

// stagedDir records one staged directory: its member path, the FileInfo
// identity captured at creation, and the pinned directory handle held
// open from creation until cleanup or success — the pin keeps the
// directory's inode alive, so its identity cannot be recycled by an
// rmdir/recreate and cleanup removes only that directory object, never a
// replacement occupying the same path.
type stagedDir struct {
	name     string
	identity fs.FileInfo
	handle   *os.File
}

// stageLedger records everything this staging created inside the staging
// directory, in creation order for directories, so failure cleanup can
// remove exactly those objects and nothing else, non-recursively.
type stageLedger struct {
	files   map[string]*stagedFile // member path -> creation identity
	fileOrd []string               // file creation order
	dirs    []stagedDir            // directory creation order, parent first
	dirSet  map[string]bool
}

func newStageLedger() *stageLedger {
	return &stageLedger{files: map[string]*stagedFile{}, dirSet: map[string]bool{}}
}

func (l *stageLedger) addFile(name string, perm fs.FileMode) *stagedFile {
	rec := &stagedFile{perm: perm}
	l.files[name] = rec
	l.fileOrd = append(l.fileOrd, name)
	return rec
}

func (l *stageLedger) addDir(name string, identity fs.FileInfo, handle *os.File) {
	l.dirs = append(l.dirs, stagedDir{name: name, identity: identity, handle: handle})
	l.dirSet[name] = true
}

// closeAll releases every pinned creation handle this ledger still holds;
// on success it runs before the staging result is returned, and in
// cleanup it guarantees no handle leaks on any path. Closing an
// already-closed (nil) record is a no-op.
func (l *stageLedger) closeAll() {
	for _, name := range l.fileOrd {
		if rec := l.files[name]; rec != nil {
			rec.close()
		}
	}
	for i := range l.dirs {
		if l.dirs[i].handle != nil {
			l.dirs[i].handle.Close()
			l.dirs[i].handle = nil
		}
	}
}

// staging carries one staging pass: the trusted options, the preflight
// manifest (matched against, never trusted as authority), the confinement
// root, the independently revalidating validator and the creation ledger.
type staging struct {
	opts       Options
	stage      StageOptions
	root       *os.Root
	expected   map[string]Member // preflight manifest by member name
	executable map[string]bool   // full member names staged executable
	v          *validator        // independent revalidation while staging
	ledger     *stageLedger
}

// stageExtract materializes the members of the validated archive bytes (the
// immutable snapshot) into root. It re-runs the full member validation and
// bounds against the bytes it actually copies; any failure aborts staging
// (the caller then runs the ledger cleanup).
func stageExtract(ctx context.Context, a Archive, opts Options, stage StageOptions, man *Manifest, root *os.Root, ledger *stageLedger) error {
	expected := make(map[string]Member, len(man.Members))
	for _, m := range man.Members {
		expected[m.Name] = m
	}
	executable := make(map[string]bool, len(stage.Executables))
	for _, n := range stage.Executables {
		executable[joinRoot(opts.Layout.Root, n)] = true
	}
	s := &staging{
		opts: opts, stage: stage, root: root, expected: expected,
		executable: executable, v: newValidator(opts), ledger: ledger,
	}
	if opts.Format == Zip {
		return s.stageZip(ctx, a)
	}
	return s.stageTarGz(ctx, a)
}

// ensureDir creates the directory path name (relative, slash-separated,
// already validated by the portable namespace) and its missing parents
// inside the staging root, refusing anything that already exists — the
// staging directory began empty, so an existing path is an injected or
// replaced object and is never followed or overwritten.
func (s *staging) ensureDir(name string) error {
	if name == "" {
		return nil
	}
	prefix := ""
	for _, seg := range strings.Split(name, "/") {
		prefix = prefix + seg
		if !s.ledger.dirSet[prefix] {
			if _, err := s.root.Lstat(prefix); err == nil {
				return fmt.Errorf("archivecheck: staging: path %q already exists in the empty staging directory; refusing to overwrite or follow it", prefix)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("archivecheck: staging: checking directory %q: %w", prefix, err)
			}
			if err := s.root.Mkdir(prefix, s.stage.DirMode); err != nil {
				if errors.Is(err, fs.ErrExist) {
					return fmt.Errorf("archivecheck: staging: directory %q appeared concurrently; refusing to use it", prefix)
				}
				return fmt.Errorf("archivecheck: staging: creating directory %q: %w", prefix, err)
			}
			// Capture the creation identity before the policy mode is
			// applied, then pin the directory with an open handle: the
			// pin keeps the original inode alive until cleanup, so its
			// identity can never be recycled by an rmdir/recreate, and
			// cleanup can prove the object at this path is still this
			// directory (os.SameFile), never a replacement.
			id, ierr := s.root.Lstat(prefix)
			if ierr != nil {
				return fmt.Errorf("archivecheck: staging: identifying directory %q: %w", prefix, ierr)
			}
			// Normalize past the process umask so the trusted policy,
			// not the caller's environment, decides the directory mode.
			if err := s.root.Chmod(prefix, s.stage.DirMode); err != nil {
				return fmt.Errorf("archivecheck: staging: setting directory %q mode: %w", prefix, err)
			}
			h, oerr := s.root.Open(prefix)
			if oerr != nil {
				// Without the pin this directory's identity is not
				// provably stable; refuse the staging rather than let
				// cleanup reason over a forgable identity.
				return fmt.Errorf("archivecheck: staging: pinning directory %q: %w", prefix, oerr)
			}
			s.ledger.addDir(prefix, id, h)
		}
		prefix += "/"
	}
	return nil
}

// copyMember streams one approved regular member from the immutable archive
// bytes into the staging root. It enforces the per-member and total
// uncompressed bounds AGAIN while copying, honors cancellation on every
// read, creates the file exclusively with O_EXCL (never following or
// overwriting anything), applies the trusted policy mode, and refuses the
// copy unless its size and SHA-256 match the preflight manifest entry.
func (s *staging) copyMember(ctx context.Context, r io.Reader, name string, declared int64) error {
	exp, ok := s.expected[name]
	if !ok {
		return fmt.Errorf("archivecheck: staging: member %q is absent from the preflight manifest", name)
	}
	if declared != exp.Size {
		return fmt.Errorf("archivecheck: staging: member %q declares %d bytes but the preflight manifest recorded %d", name, declared, exp.Size)
	}
	b := s.opts.Bounds
	if declared < 0 || declared > b.MaxMemberBytes || b.MaxUncompressedBytes-s.v.total < declared {
		return fmt.Errorf("archivecheck: staging: member %q: declared size %d exceeds the member or total uncompressed bounds", name, declared)
	}
	perm := s.stage.FileMode
	if s.executable[name] {
		perm = s.stage.ExecutableMode
	}
	rec := s.ledger.addFile(name, perm)
	if err := s.ensureDir(parentDir(name)); err != nil {
		return err
	}
	f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm.Perm())
	if err != nil {
		return fmt.Errorf("archivecheck: staging: creating member file %q exclusively: %w", name, err)
	}
	rec.created = true
	rec.handle = f // pinned from creation until cleanup or success
	// Capture the creation identity through the open handle, so cleanup can
	// prove the object at this path is still this file (os.SameFile) and
	// never deletes an identical replacement. The open handle pins the
	// file's inode: it cannot be recycled by an unlink/recreate while the
	// staging still holds it, which is exactly what makes the identity
	// proof stable.
	if rec.identity, err = f.Stat(); err != nil {
		// Without a captured identity the file can never be proven ours;
		// release the pin here — cleanup retains the path regardless.
		rec.close()
		return fmt.Errorf("archivecheck: staging: identifying member file %q: %w", name, err)
	}
	// Normalize past the process umask through the file handle, so the
	// trusted policy — not the caller's environment — decides the mode.
	// The handle stays open (pinned) on every later path: cleanup closes
	// it after its identity decision, and a successful staging closes all
	// ledger handles before returning.
	if err := f.Chmod(perm); err != nil {
		return fmt.Errorf("archivecheck: staging: setting member %q mode: %w", name, err)
	}
	h := sha256.New()
	buf := make([]byte, 32*1024)
	var actual int64
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("archivecheck: staging: member %q: %w", name, err)
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			// Check before accumulating so no sum can ever wrap.
			if actual > b.MaxMemberBytes-int64(n) || b.MaxUncompressedBytes-s.v.total < int64(n) {
				return fmt.Errorf("archivecheck: staging: member %q: content exceeds the member or total uncompressed bounds at %d bytes", name, actual+int64(n))
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				return fmt.Errorf("archivecheck: staging: member %q: writing content: %w", name, werr)
			}
			h.Write(buf[:n])
			actual += int64(n)
			rec.written = actual
			s.v.total += int64(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("archivecheck: staging: member %q: reading content: %w", name, rerr)
		}
	}
	if actual != declared {
		return fmt.Errorf("archivecheck: staging: member %q: copied %d bytes but the manifest recorded %d", name, actual, declared)
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if digest != exp.SHA256 {
		return fmt.Errorf("archivecheck: staging: member %q: copied content SHA-256 %s does not match the preflight hash %s", name, digest, exp.SHA256)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("archivecheck: staging: member %q: flushing: %w", name, err)
	}
	// The file handle stays pinned in the ledger: a successful staging
	// closes every ledger handle before returning, and a failed staging
	// leaves the closing to cleanup after its identity decision. Every
	// error path above likewise leaves the pinned handle to cleanup, so
	// no path of this function leaks or reaps the handle early.
	rec.complete = true
	rec.hash = digest
	return nil
}

// parentDir returns the archive-internal slash directory containing name.
func parentDir(name string) string {
	if i := strings.LastIndexByte(name, '/'); i > 0 {
		return name[:i]
	}
	return ""
}

// stageZip materializes the members of a validated zip archive. It mirrors
// checkZip's validation on the immutable snapshot bytes — count, compressed
// bounds, setuid/setgid refusal, entry types and the full member namespace —
// while writing the approved members; the preflight pass is never trusted
// as authority for the second pass.
func (s *staging) stageZip(ctx context.Context, a Archive) error {
	lim := &ctxLimitedReaderAt{ctx: ctx, ra: a.ReaderAt, max: s.opts.Bounds.MaxCompressedBytes}
	zr, err := zip.NewReader(lim, a.Size)
	if err != nil {
		return fmt.Errorf("archivecheck: staging: zip central directory rejected: %w", err)
	}
	var declaredCompressed int64
	for _, f := range zr.File {
		// Overflow-safe accumulation, exactly as preflight does.
		if f.CompressedSize64 > math.MaxInt64 || declaredCompressed > math.MaxInt64-int64(f.CompressedSize64) {
			return errors.New("archivecheck: staging: zip members declare compressed sizes above MaxCompressedBytes")
		}
		declaredCompressed += int64(f.CompressedSize64)
	}
	if declaredCompressed > s.opts.Bounds.MaxCompressedBytes {
		return fmt.Errorf("archivecheck: staging: zip members declare %d compressed bytes, above MaxCompressedBytes %d", declaredCompressed, s.opts.Bounds.MaxCompressedBytes)
	}
	for _, f := range zr.File {
		if err = s.v.begin(); err != nil {
			return err
		}
		mode := f.Mode()
		if mode&(fs.ModeSetuid|fs.ModeSetgid) != 0 {
			return fmt.Errorf("archivecheck: staging: member %q: setuid/setgid mode bits are refused", f.Name)
		}
		isDir := strings.HasSuffix(f.Name, "/")
		if typ := mode & fs.ModeType; typ != 0 && !(isDir && typ == fs.ModeDir) {
			return fmt.Errorf("archivecheck: staging: member %q: unsafe entry type in its mode bits; links, devices and FIFOs are refused", f.Name)
		}
		if err = s.v.entry(f.Name, isDir); err != nil {
			return err
		}
		if isDir {
			// Directory entries are needed parent paths only (validated);
			// creating them eagerly keeps the staged tree complete.
			if derr := s.ensureDir(strings.TrimSuffix(f.Name, "/")); derr != nil {
				return derr
			}
			continue
		}
		rc, oerr := f.Open()
		if oerr != nil {
			return fmt.Errorf("archivecheck: staging: member %q: cannot open: %w", f.Name, oerr)
		}
		cerr := s.copyMember(ctx, rc, f.Name, int64(f.UncompressedSize64))
		rc.Close()
		if cerr != nil {
			return cerr
		}
	}
	return s.finish()
}

// stageTarGz materializes the members of a validated gzip-compressed tar
// archive. It mirrors checkTarGz's validation on the immutable snapshot
// bytes — the metered framing, the end-of-archive proof and the gzip
// trailer drain are the same verified machinery — while writing the
// approved members; the preflight pass is never trusted as authority for
// the second pass.
func (s *staging) stageTarGz(ctx context.Context, a Archive) error {
	maxStream, framingBudget, err := decompressedStreamCaps(s.opts.Bounds)
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(&ctxLimitedReader{ctx: ctx, r: a.Reader, max: s.opts.Bounds.MaxCompressedBytes})
	if err != nil {
		return fmt.Errorf("archivecheck: staging: not a valid gzip archive: %w", err)
	}
	defer gz.Close()
	meter := &tarMeter{r: gz, v: s.v, maxStream: maxStream, framingBudget: framingBudget, tailNZ: -1}
	tr := tar.NewReader(meter)
	var lastSize int64
	for {
		if err = ctx.Err(); err != nil {
			return fmt.Errorf("archivecheck: staging: %w", err)
		}
		hdr, nerr := tr.Next()
		if nerr == io.EOF {
			if perr := meter.proveEndMarker(lastSize); perr != nil {
				return perr
			}
			break
		}
		if nerr != nil {
			return fmt.Errorf("archivecheck: staging: tar stream rejected: %w", nerr)
		}
		if err = s.v.begin(); err != nil {
			return err
		}
		if hdr.Mode&0o6000 != 0 {
			return fmt.Errorf("archivecheck: staging: member %q: setuid/setgid mode bits are refused", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err = s.v.entry(hdr.Name, true); err != nil {
				return err
			}
			if derr := s.ensureDir(strings.TrimSuffix(hdr.Name, "/")); derr != nil {
				return derr
			}
			meter.dataEnd = meter.n
			lastSize = 0
		case tar.TypeReg:
			if err = s.v.entry(hdr.Name, false); err != nil {
				return err
			}
			if cerr := s.copyMember(ctx, tr, hdr.Name, hdr.Size); cerr != nil {
				return cerr
			}
			meter.dataEnd = meter.n
			lastSize = hdr.Size
		default:
			return fmt.Errorf("archivecheck: staging: member %q: entry type %q is not a regular file or directory; links, devices and FIFOs are refused", hdr.Name, rune(hdr.Typeflag))
		}
	}
	if err = drainGzipTail(ctx, meter); err != nil {
		return err
	}
	return s.finish()
}

// finish cross-checks the staged member set against the preflight manifest
// and refuses an archive missing any required member, exactly as preflight
// did: the second pass proves the manifest describes the staged bytes.
func (s *staging) finish() error {
	if _, err := s.v.finish(); err != nil {
		return err
	}
	if len(s.ledger.files) != len(s.expected) {
		return fmt.Errorf("archivecheck: staging: %d members staged but the preflight manifest lists %d", len(s.ledger.files), len(s.expected))
	}
	for name := range s.expected {
		if _, ok := s.ledger.files[name]; !ok {
			return fmt.Errorf("archivecheck: staging: member %q from the preflight manifest was not staged", name)
		}
	}
	return nil
}

// cleanupStaging removes, on a failed staging, ONLY what this staging
// itself created — and only while each object is still the SAME object it
// was at creation: the pinned creation handle (open since creation) keeps
// the original inode alive, so an unlink/recreate can never hand the
// ORIGINAL identity to a replacement (the inode ABA problem), and
// os.SameFile against the FileInfo captured at creation is the primary
// proof, so a replacement is retained even when it is byte-identical or
// an identical empty directory. A created object without a pinned handle
// is never removed: without the pin its identity is not provably stable.
// Files must also still be regular and non-symlink, carry the recorded
// byte count, and — for completed members — the recorded content hash,
// read bounded to the recorded byte count so a file that grew after
// creation can never turn cleanup into an unbounded read. The
// permission-mode comparison is Unix-only: Windows permissions are ACLs
// and mode bits there never decide a cleanup. Unknown, injected or
// replaced objects, and removals that fail, are left untouched AND
// reported; there is no RemoveAll, and no object outside the staging
// directory is ever touched. The staging directory itself is removed only
// when empty and still the same directory (os.SameFile against the
// identity captured right after its creation), with the confinement root
// handle released after the identity check and before the removal — on
// Windows an open root handle prevents the final directory removal —
// never a renamed original's replacement at the same path. Every pinned
// handle is reaped on every path (success of the loops here or the
// closeAll sweep), and the caller's trusted-parent assumption covers the
// non-atomic window between a handle release and the removal it enables:
// no claim of protection against a hostile same-user race across
// non-atomic path operations is made. It returns the member-relative
// retained paths (created-but-unremovable, replaced and unknown objects
// alike) and whether the staging directory had to be kept.
func cleanupStaging(root *os.Root, stageDir string, stageDirInfo fs.FileInfo, ledger *stageLedger) (retained []string, keepStageDir bool) {
	defer ledger.closeAll()
	retain := func(path string) {
		retained = append(retained, path)
	}
	// Files, most recently created first. A retained file also blocks the
	// removal of its parent directories and of the staging directory.
	for i := len(ledger.fileOrd) - 1; i >= 0; i-- {
		name := ledger.fileOrd[i]
		rec := ledger.files[name]
		fi, err := root.Lstat(name)
		switch {
		case err == nil:
		case errors.Is(err, fs.ErrNotExist):
			rec.close()
			continue // already gone; nothing of ours to remove
		default:
			rec.close()
			retain(name)
			continue
		}
		if !rec.created || rec.identity == nil || rec.handle == nil {
			// The path was occupied before staging could create the file
			// (O_EXCL refused it), or its creation identity or pin is
			// missing: an object that is never provably ours to remove
			// (an unpinned identity can be forged by inode recycling).
			rec.close()
			retain(name)
			continue
		}
		if fi.Mode()&fs.ModeSymlink != 0 || !fi.Mode().IsRegular() {
			rec.close()
			retain(name) // replaced by a non-regular object
			continue
		}
		if !os.SameFile(rec.identity, fi) {
			rec.close()
			retain(name) // replaced: another object, even byte-identical
			continue
		}
		if runtime.GOOS != "windows" && fi.Mode().Perm() != rec.perm.Perm() {
			// Unix-only mode-bit check; Windows permissions are ACLs.
			rec.close()
			retain(name)
			continue
		}
		if fi.Size() != rec.written {
			rec.close()
			retain(name) // rewritten in place since creation
			continue
		}
		if rec.complete {
			if digest, ok := hashInRoot(root, name, rec.written); !ok || digest != rec.hash {
				rec.close()
				retain(name) // modified in place since creation
				continue
			}
		}
		if err := removePinned(root, name, rec.handle); err != nil {
			rec.handle = nil // reaped by removePinned on both paths
			retain(name)     // never force a removal that failed
			continue
		}
		rec.handle = nil // reaped by removePinned on both paths
	}
	// Directories, children first (creation order is parent-first), each
	// removed only while its pinned handle and os.SameFile still prove it
	// is this staging's empty directory — never an identical-mode empty
	// replacement whose inode was recycled after an rmdir/recreate.
	for i := len(ledger.dirs) - 1; i >= 0; i-- {
		d := &ledger.dirs[i]
		fi, err := root.Lstat(d.name)
		switch {
		case err == nil:
		case errors.Is(err, fs.ErrNotExist):
			continue // already gone; the closeAll sweep reaps the handle
		default:
			retain(d.name)
			continue
		}
		if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
			retain(d.name) // replaced by a non-directory object, or a link we never follow
			continue
		}
		if d.identity == nil || d.handle == nil || !os.SameFile(d.identity, fi) {
			retain(d.name) // replaced (or unpinned): another directory, even identical and empty
			continue
		}
		if err := removePinned(root, d.name, d.handle); err != nil {
			d.handle = nil // reaped by removePinned on both paths
			retain(d.name) // not empty (or removal refused): preserve it
			continue
		}
		d.handle = nil // reaped by removePinned on both paths
	}
	// Anything still present inside the staging root that this staging did
	// not create is an unknown or injected object: never removed, but
	// reported so the cleanup report includes every retained object.
	retained = append(retained, scanUnknownObjects(root, ledger)...)
	if len(retained) > 0 {
		return retained, true
	}
	// The staging directory itself: only when empty, and only while it is
	// still the same directory this staging created (os.SameFile against
	// the identity captured right after creation) — never a replacement
	// occupying the path after a rename. The identity is checked BEFORE
	// the confinement root handle is released; on Windows an open root
	// handle prevents the final removal, so the handle must go first —
	// while it was held no replacement could be created at the path, and
	// the identity is re-checked after the release anyway. This direct
	// removal never recurses into the staging directory.
	sfi, err := os.Lstat(stageDir)
	if err != nil {
		return nil, !errors.Is(err, fs.ErrNotExist)
	}
	if sfi.Mode()&os.ModeSymlink != 0 || !sfi.IsDir() {
		return nil, true
	}
	if stageDirInfo == nil || !os.SameFile(stageDirInfo, sfi) {
		return nil, true // the name no longer names this staging's directory
	}
	root.Close()
	sfi, err = os.Lstat(stageDir)
	if err != nil {
		return nil, !errors.Is(err, fs.ErrNotExist)
	}
	if sfi.Mode()&fs.ModeSymlink != 0 || !sfi.IsDir() || !os.SameFile(stageDirInfo, sfi) {
		return nil, true // swapped between the handle release and here
	}
	if err := os.Remove(stageDir); err != nil {
		// Removal failed (a race or a platform refusal): leave the
		// directory for the caller to inspect and report.
		return nil, true
	}
	return nil, false
}

// removePinned removes the object at name inside root that this staging
// created and still pins with the open creation handle h (its identity
// was proven through that pin by the caller). On Unix the removal happens
// while the handle is open — an unlinked inode stays alive until the
// handle is closed, so the pin also covers the removal — and the handle
// is reaped right after. On Windows an open handle prevents directory
// removal, so the handle is released first: while it was held no
// replacement could occupy the path, and the caller's trusted-parent
// assumption covers the non-atomic window between the release and the
// removal. It never forces and never recurses; the handle is reaped on
// both the success and the failure path.
func removePinned(root *os.Root, name string, h *os.File) error {
	if runtime.GOOS == "windows" {
		h.Close()
		return root.Remove(name)
	}
	err := root.Remove(name)
	h.Close()
	return err
}

// scanUnknownObjects reports, in lexical walk order, every object still
// present inside the staging root that this staging did not create. It is
// strictly read-only, never follows symlinks (fs.WalkDir does not descend
// into them), and exists only so the cleanup report lists unknown
// retained objects; it never removes anything.
func scanUnknownObjects(root *os.Root, ledger *stageLedger) []string {
	var unknown []string
	_ = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			if p != "." {
				unknown = append(unknown, p) // uninspectable: retained, never ours
			}
			return nil // keep scanning what else is inspectable
		case p == ".":
			return nil // the staging directory itself is reported separately
		case ledger.files[p] != nil || ledger.dirSet[p]:
			return nil // one of ours (created, or retained in place)
		default:
			unknown = append(unknown, p)
			return nil
		}
	})
	return unknown
}

// hashInRoot hashes at most max bytes of the content of a regular file at
// name inside root, never following a link that escapes the staging
// directory, and reports whether the file still holds exactly the recorded
// byte count: a file that grew after creation cannot turn this read into
// an unbounded one.
func hashInRoot(root *os.Root, name string, max int64) (string, bool) {
	f, err := root.Open(name)
	if err != nil {
		return "", false
	}
	defer f.Close()
	h := sha256.New()
	// The limit of max+1 bytes proves growth past the recorded count
	// without ever reading more than one byte past it.
	n, err := io.Copy(h, io.LimitReader(f, max+1))
	if err != nil || n != max {
		return "", false
	}
	return hex.EncodeToString(h.Sum(nil)), true
}
