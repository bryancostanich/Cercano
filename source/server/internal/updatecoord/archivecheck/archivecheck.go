// Package archivecheck inspects the STRUCTURE of an already-verified update
// archive against a caller-provided expected layout. It is the strict
// pre-staging archive gate required by the approved cross-platform update
// plan (efforts/cross-platform-updates/plan.md, Phase 5): reject traversal,
// link/device/FIFO entries, duplicate or unexpected members, missing
// binaries and oversized content BEFORE anything is staged or executed.
//
// Trust boundary: authenticity of the archive bytes belongs to the
// acquisition boundary (TUF, internal/updatecoord/acquisition). This
// package performs no signature or publisher verification. The caller may
// bind the inspected bytes to the already-verified acquisition receipt via
// Options.Identity (exact archive length and whole-archive SHA-256);
// PreflightFile re-hashes the bytes it actually opens against it, which is
// an integrity binding, never publisher authentication. The expected layout
// comes only from the trusted caller (build/publish configuration), never
// from the archive; nothing is inferred from PATH, platform or environment,
// and no member filename is ever interpreted as a program or argument.
//
// Check never touches the filesystem. PreflightFile opens only the
// caller-supplied archive path, read-only, and requires it to be an
// existing regular file that is not a symlink. Neither extracts, writes,
// or executes anything, and neither has default paths or default state.
//
// The returned Manifest is a preflight result, not extraction authority:
// any future extraction must independently revalidate these same member
// rules and bounds against the bytes it actually reads and must not treat
// a preflight pass as permission to materialize anything.
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
	"os"
	"strings"
)

// Format identifies the archive container. It is explicit caller input; the
// bytes are never sniffed and the format is never guessed.
type Format int

const (
	// TarGz is a gzip-compressed tar archive (the layout produced by
	// scripts/build-macos-unsigned.sh: one version-named root directory).
	TarGz Format = iota + 1
	// Zip is the Windows-distribution container format.
	Zip
)

// Layout is the trusted caller's explicit expected member set, derived from
// the actual build scripts — never discovered from the archive itself.
type Layout struct {
	// Root is the single expected top-level directory name, or "" when the
	// archive has no root directory (e.g. "cercano-1.2.3-darwin-arm64-unsigned").
	Root string
	// Required are regular files that must be present, e.g. the agent and
	// CLI entrypoints "bin/cercano" and "bin/cercano-cli". It must not be empty.
	Required []string
	// Optional are additional regular files permitted but not demanded
	// (e.g. "LICENSE", "README.txt").
	Optional []string
}

// Bounds are the finite limits enforced while reading. Every bound must be
// positive; there are no defaults and non-finite values are refused up front.
type Bounds struct {
	// MaxMembers caps the number of member entries (files and directories).
	MaxMembers int
	// MaxCompressedBytes caps bytes read from the archive container itself.
	MaxCompressedBytes int64
	// MaxUncompressedBytes caps total decompressed member content.
	MaxUncompressedBytes int64
	// MaxMemberBytes caps one member's decompressed content size.
	MaxMemberBytes int64
}

// Archive supplies the opened, already-verified archive bytes. For TarGz,
// Reader must be set; for Zip, ReaderAt and Size must be set (Size is the
// exact byte length of the archive data).
type Archive struct {
	Reader   io.Reader
	ReaderAt io.ReaderAt
	Size     int64
}

// Options configures one Check.
type Options struct {
	Format Format
	Layout Layout
	Bounds Bounds
	// Identity optionally binds the inspected bytes to the caller's
	// already-verified acquisition receipt: the exact archive length and
	// the raw whole-archive SHA-256 that the acquisition boundary verified
	// against signed TUF metadata. It is used only by PreflightFile to
	// re-hash the bytes actually opened; Check ignores it because its
	// bytes arrive already bound by the caller. Binding is integrity, not
	// publisher authentication.
	Identity *Identity
}

// Identity is the caller-supplied receipt binding for one archive: the
// exact archive length and the raw 32-byte whole-archive SHA-256 from the
// verified acquisition receipt (internal/updatecoord/acquisition). The
// types are duplicated here so this package does not depend on the
// acquisition package; the caller copies the values from its receipt.
type Identity struct {
	// Length is the exact archive size in bytes.
	Length int64
	// SHA256 is the raw 32-byte SHA-256 digest of the whole archive.
	SHA256 []byte
}

// PreflightFile inspects a caller-provided, already-verified local archive
// file. archivePath must be an existing regular file that is not a symlink;
// the path itself (not a parent) is checked, because the caller provisions
// the private directory holding it. When opts.Identity is set, the bytes of
// the file actually opened are streamed, hashed and size-compared against
// the verified receipt before the structure is checked, so the inspected
// bytes are exactly the receipt's bytes. The file is opened read-only and
// is never modified.
func PreflightFile(ctx context.Context, archivePath string, opts Options) (*Manifest, error) {
	if ctx == nil {
		return nil, errors.New("archivecheck: context is required")
	}
	if archivePath == "" {
		return nil, errors.New("archivecheck: archive path is required")
	}
	if err := validateOptionValues(opts); err != nil {
		return nil, err
	}
	fi, err := os.Lstat(archivePath)
	if err != nil {
		return nil, fmt.Errorf("archivecheck: archive %q: %w", archivePath, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("archivecheck: archive %q is a symlink; the verified archive itself must be a regular file", archivePath)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("archivecheck: archive %q is not a regular file", archivePath)
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("archivecheck: archive %q: %w", archivePath, err)
	}
	defer f.Close()
	// Stat the open descriptor, not the pre-open path: every check below
	// then describes the bytes actually being read, not a snapshot.
	sfi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("archivecheck: archive %q: %w", archivePath, err)
	}
	if !sfi.Mode().IsRegular() {
		return nil, fmt.Errorf("archivecheck: archive %q is not a regular file", archivePath)
	}
	if sfi.Size() > opts.Bounds.MaxCompressedBytes {
		return nil, fmt.Errorf("archivecheck: archive size %d exceeds MaxCompressedBytes %d",
			sfi.Size(), opts.Bounds.MaxCompressedBytes)
	}
	if id := opts.Identity; id != nil {
		if id.Length < 0 || len(id.SHA256) != sha256.Size {
			return nil, errors.New("archivecheck: Identity must carry an exact non-negative length and a raw 32-byte SHA-256")
		}
		if sfi.Size() != id.Length {
			return nil, fmt.Errorf("archivecheck: archive is %d bytes but the verified receipt authorizes %d bytes",
				sfi.Size(), id.Length)
		}
		h := sha256.New()
		// Bounded by the receipt length already compared against the open
		// file's size; a file that grows mid-read is refused by the limit.
		if _, err = io.Copy(h, &ctxLimitedReader{ctx: ctx, r: f, max: id.Length}); err != nil {
			return nil, fmt.Errorf("archivecheck: hashing archive %q failed: %w", archivePath, err)
		}
		if !bytes.Equal(h.Sum(nil), id.SHA256) {
			return nil, fmt.Errorf("archivecheck: archive bytes do not match the SHA-256 of the verified receipt")
		}
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("archivecheck: archive %q: %w", archivePath, err)
		}
	}
	if opts.Format == Zip {
		return Check(ctx, Archive{ReaderAt: f, Size: sfi.Size()}, opts)
	}
	// The structural pass reads exactly the hashed bytes: the section
	// reader caps the stream at the open descriptor's size.
	return Check(ctx, Archive{Reader: io.NewSectionReader(f, 0, sfi.Size())}, opts)
}

// Member is one validated regular-file member. Name is the archive-internal
// slash path only; no filesystem path outside the archive ever appears here.
type Member struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest is the compact result of a fully validated archive.
type Manifest struct {
	Members []Member `json:"members"`
}

// Check validates the archive's structure and content bounds and returns a
// manifest. Any violation fails the whole archive: bad members are never
// silently dropped. Cancellation is honored while decompressed bytes are
// being read.
func Check(ctx context.Context, a Archive, opts Options) (*Manifest, error) {
	if ctx == nil {
		return nil, errors.New("archivecheck: context is required")
	}
	if err := validateOptions(opts, a); err != nil {
		return nil, err
	}
	if opts.Format == TarGz {
		return checkTarGz(ctx, a.Reader, opts)
	}
	return checkZip(ctx, a, opts)
}

func validateOptions(opts Options, a Archive) error {
	if err := validateOptionValues(opts); err != nil {
		return err
	}
	if opts.Format == TarGz && a.Reader == nil {
		return errors.New("archivecheck: TarGz requires Archive.Reader")
	}
	if opts.Format == Zip {
		if a.ReaderAt == nil || a.Size <= 0 {
			return errors.New("archivecheck: Zip requires Archive.ReaderAt and a positive Archive.Size")
		}
		if a.Size > opts.Bounds.MaxCompressedBytes {
			return fmt.Errorf("archivecheck: archive size %d exceeds MaxCompressedBytes %d", a.Size, opts.Bounds.MaxCompressedBytes)
		}
	}
	return nil
}

// validateOptionValues validates the format, bounds and layout of one Check
// independently of the archive's input form.
func validateOptionValues(opts Options) error {
	if opts.Format != TarGz && opts.Format != Zip {
		return fmt.Errorf("archivecheck: format must be an explicit TarGz or Zip, not %d", opts.Format)
	}
	b := opts.Bounds
	if b.MaxMembers <= 0 || b.MaxCompressedBytes <= 0 || b.MaxUncompressedBytes <= 0 || b.MaxMemberBytes <= 0 {
		return errors.New("archivecheck: every bound must be a positive finite value; there are no defaults")
	}
	l := opts.Layout
	if len(l.Required) == 0 {
		return errors.New("archivecheck: Layout.Required must name the required agent and CLI members")
	}
	if err := validateEntryName(l.Root, true); err != nil {
		return fmt.Errorf("archivecheck: Layout.Root: %w", err)
	}
	seen := map[string]bool{}
	for _, role := range []struct {
		name  string
		names []string
	}{{"Required", l.Required}, {"Optional", l.Optional}} {
		for _, n := range role.names {
			if err := validateEntryName(n, false); err != nil {
				return fmt.Errorf("archivecheck: Layout.%s %q: %w", role.name, n, err)
			}
			key := strings.ToLower(n)
			if seen[key] {
				return fmt.Errorf("archivecheck: Layout contains duplicate or case-colliding member %q", n)
			}
			seen[key] = true
		}
	}
	return nil
}

// validateEntryName accepts only a relative, slash-separated member name with
// no cross-platform ambiguity. It rejects: empty names, absolute paths,
// empty, "." and ".." segments, backslashes, colons (drive letters and NTFS
// alternate data streams), control bytes, Windows reserved device names in
// any segment, and segments ending in a dot or space. Directory names may
// carry one trailing slash, which is trimmed before validation.
func validateEntryName(name string, isDir bool) error {
	if isDir {
		name = strings.TrimSuffix(name, "/")
	}
	if name == "" {
		return errors.New("empty member name")
	}
	if strings.HasPrefix(name, "/") {
		return fmt.Errorf("member name %q must be relative", name)
	}
	if strings.ContainsRune(name, '\\') {
		return fmt.Errorf("member name %q must not contain a backslash", name)
	}
	if strings.ContainsRune(name, ':') {
		return fmt.Errorf("member name %q must not contain a colon (drive-letter or alternate-data-stream ambiguity)", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("member name %q contains a control byte", name)
		}
	}
	for _, seg := range strings.Split(name, "/") {
		switch {
		case seg == "", seg == ".", seg == "..":
			return fmt.Errorf("member name %q must not contain empty or traversal segments", name)
		case strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " "):
			return fmt.Errorf("member name %q has a segment ending in a dot or space, which Windows strips", name)
		case windowsReserved[strings.ToLower(strings.SplitN(seg, ".", 2)[0])]:
			return fmt.Errorf("member name %q uses the Windows reserved device name %q", name, seg)
		}
	}
	return nil
}

var windowsReserved = func() map[string]bool {
	m := map[string]bool{"con": true, "prn": true, "aux": true, "nul": true, "conin$": true, "conout$": true}
	for i := 1; i <= 9; i++ {
		m[fmt.Sprintf("com%d", i)] = true
		m[fmt.Sprintf("lpt%d", i)] = true
	}
	return m
}()

// validator carries per-Check state: the allowlist built from the trusted
// layout, collision detection, and the enforced bounds.
type validator struct {
	opts  Options
	files map[string]bool   // allowed regular-file member names
	dirs  map[string]bool   // permitted directory names (needed parent paths only)
	seen  map[string]string // lower-cased member name -> exact first-seen name
	kind  map[string]bool   // lower-cased member name -> is directory
	found map[string]bool   // required files encountered
	count int
	total int64 // decompressed bytes read so far
	man   *Manifest
}

func newValidator(opts Options) *validator {
	v := &validator{
		opts: opts, files: map[string]bool{}, dirs: map[string]bool{},
		seen: map[string]string{}, kind: map[string]bool{}, found: map[string]bool{},
		man: &Manifest{},
	}
	for _, n := range append(append([]string{}, opts.Layout.Required...), opts.Layout.Optional...) {
		f := joinRoot(opts.Layout.Root, n)
		v.files[f] = true
		// Every ancestor directory of an allowed file is a needed parent path.
		for d := f; ; {
			i := strings.LastIndexByte(d, '/')
			if i < 0 {
				break
			}
			d = d[:i]
			v.dirs[d] = true
		}
	}
	return v
}

func joinRoot(root, name string) string {
	if root == "" {
		return name
	}
	return root + "/" + name
}

// begin admits one more member entry under the count bound.
func (v *validator) begin() error {
	v.count++
	if v.count > v.opts.Bounds.MaxMembers {
		return fmt.Errorf("archivecheck: archive exceeds MaxMembers %d", v.opts.Bounds.MaxMembers)
	}
	return nil
}

// entry validates one member name against the trusted layout before any
// content is read. Duplicates, case-fold collisions and unknown members are
// rejected; nothing is dropped silently.
func (v *validator) entry(name string, isDir bool) error {
	if strings.HasSuffix(name, "/") && !isDir {
		return fmt.Errorf("archivecheck: member %q: a file entry must not have a directory name", name)
	}
	if isDir {
		name = strings.TrimSuffix(name, "/")
	}
	if err := validateEntryName(name, isDir); err != nil {
		return fmt.Errorf("archivecheck: member %q: %w", name, err)
	}
	key := strings.ToLower(name)
	if prev, dup := v.seen[key]; dup {
		if prev == name && v.kind[key] == isDir {
			return fmt.Errorf("archivecheck: member %q: duplicate member", name)
		}
		return fmt.Errorf("archivecheck: member %q collides case-insensitively with member %q", name, prev)
	}
	v.seen[key] = name
	v.kind[key] = isDir
	if isDir {
		if !v.dirs[name] {
			return fmt.Errorf("archivecheck: member %q: directory entries are allowed only for needed parent paths", name)
		}
		return nil
	}
	if !v.files[name] {
		return fmt.Errorf("archivecheck: member %q is not in the expected layout; unknown members are refused", name)
	}
	v.found[name] = true
	return nil
}

// readMember streams one member's decompressed bytes, enforcing the
// per-member and total uncompressed bounds WHILE reading, honoring context
// cancellation while reading, hashing content, and verifying the header's
// declared size against the actual bytes.
func (v *validator) readMember(ctx context.Context, r io.Reader, name string, declared int64) (Member, error) {
	b := v.opts.Bounds
	if declared < 0 || declared > b.MaxMemberBytes || v.total+declared > b.MaxUncompressedBytes {
		return Member{}, fmt.Errorf("archivecheck: member %q: declared size %d exceeds the member or total uncompressed bounds", name, declared)
	}
	h := sha256.New()
	buf := make([]byte, 32*1024)
	var actual int64
	for {
		if err := ctx.Err(); err != nil {
			return Member{}, fmt.Errorf("archivecheck: member %q: %w", name, err)
		}
		n, err := r.Read(buf)
		if n > 0 {
			actual += int64(n)
			v.total += int64(n)
			h.Write(buf[:n])
			if actual > b.MaxMemberBytes || v.total > b.MaxUncompressedBytes {
				return Member{}, fmt.Errorf("archivecheck: member %q: content exceeds the member or total uncompressed bounds", name)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return Member{}, fmt.Errorf("archivecheck: member %q: reading content failed: %w", name, err)
		}
	}
	if actual != declared {
		return Member{}, fmt.Errorf("archivecheck: member %q: header declares %d bytes but content is %d bytes", name, declared, actual)
	}
	return Member{Name: name, Size: actual, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// finish refuses an archive missing any required member.
func (v *validator) finish() (*Manifest, error) {
	for _, req := range v.opts.Layout.Required {
		full := joinRoot(v.opts.Layout.Root, req)
		if !v.found[full] {
			return nil, fmt.Errorf("archivecheck: archive is missing required member %q", full)
		}
	}
	return v.man, nil
}

// ctxLimitedReader bounds and context-checks the compressed container stream.
// One byte past max stays readable so a strict overflow is detected instead
// of being truncated into acceptance.
type ctxLimitedReader struct {
	ctx context.Context
	r   io.Reader
	n   int64
	max int64
}

func (l *ctxLimitedReader) Read(p []byte) (int, error) {
	if err := l.ctx.Err(); err != nil {
		return 0, err
	}
	limit := l.max + 1
	if l.n >= limit {
		return 0, fmt.Errorf("archivecheck: compressed stream exceeds MaxCompressedBytes %d", l.max)
	}
	if int64(len(p)) > limit-l.n {
		p = p[:limit-l.n]
	}
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.n > l.max {
		return n, fmt.Errorf("archivecheck: compressed stream exceeds MaxCompressedBytes %d", l.max)
	}
	return n, err
}

// ctxLimitedReaderAt bounds and context-checks random access into the zip
// container: any read whose end offset passes the compressed bound is refused.
type ctxLimitedReaderAt struct {
	ctx context.Context
	ra  io.ReaderAt
	max int64
}

func (l *ctxLimitedReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if err := l.ctx.Err(); err != nil {
		return 0, err
	}
	if off < 0 || off > l.max || int64(len(p)) > l.max-off {
		return 0, fmt.Errorf("archivecheck: zip container access exceeds MaxCompressedBytes %d", l.max)
	}
	return l.ra.ReadAt(p, off)
}

// checkTarGz validates a gzip-compressed tar archive.
func checkTarGz(ctx context.Context, r io.Reader, opts Options) (*Manifest, error) {
	gz, err := gzip.NewReader(&ctxLimitedReader{ctx: ctx, r: r, max: opts.Bounds.MaxCompressedBytes})
	if err != nil {
		return nil, fmt.Errorf("archivecheck: not a valid gzip archive: %w", err)
	}
	defer gz.Close()
	// The tail tracker sees every decompressed byte archive/tar pulls —
	// including read-ahead buffered inside archive/tar that a post-EOF
	// drain would never observe — so content hidden after the tar
	// end-of-archive marker is detected instead of silently skipped.
	tail := &tarTail{r: gz}
	tr := tar.NewReader(tail)
	v := newValidator(opts)
	for {
		if err = ctx.Err(); err != nil {
			return nil, fmt.Errorf("archivecheck: %w", err)
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("archivecheck: tar stream rejected: %w", err)
		}
		if err = v.begin(); err != nil {
			return nil, err
		}
		if hdr.Mode&0o6000 != 0 {
			return nil, fmt.Errorf("archivecheck: member %q: setuid/setgid mode bits are refused", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err = v.entry(hdr.Name, true); err != nil {
				return nil, err
			}
		case tar.TypeReg:
			if err = v.entry(hdr.Name, false); err != nil {
				return nil, err
			}
			m, rerr := v.readMember(ctx, tr, hdr.Name, hdr.Size)
			if rerr != nil {
				return nil, rerr
			}
			v.man.Members = append(v.man.Members, m)
		default:
			return nil, fmt.Errorf("archivecheck: member %q: entry type %q is not a regular file or directory; links, devices and FIFOs are refused", hdr.Name, rune(hdr.Typeflag))
		}
	}
	if err = tail.requireEndMarker(); err != nil {
		return nil, err
	}
	// archive/tar stops at the two-block end-of-archive marker, which
	// normally leaves the gzip trailer unread: compress/gzip verifies the
	// CRC-32 and ISIZE trailer only when the stream is read to EOF.
	// Draining the remainder enforces that trailer, refuses non-NUL bytes
	// past the marker, and is itself bounded.
	if err = drainGzipTail(ctx, gz); err != nil {
		return nil, err
	}
	return v.finish()
}

// tarTail wraps the decompressed stream, tracking the total bytes read and
// the position of the last non-zero byte, so the archive can be proven to
// end with the 1024-byte end-of-archive marker followed by nothing but NUL
// padding.
type tarTail struct {
	r      io.Reader
	n      int64
	lastNZ int64 // index of the last non-zero byte; -1 when none
}

func (t *tarTail) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	for i := 0; i < n; i++ {
		if p[i] != 0 {
			t.lastNZ = t.n + int64(i)
		}
	}
	t.n += int64(n)
	return n, err
}

// requireEndMarker refuses a decompressed stream that does not end with the
// two zero blocks of the tar end-of-archive marker: a shorter zero run means
// the archive is truncated or carries hidden content past its end.
func (t *tarTail) requireEndMarker() error {
	if t.n-(t.lastNZ+1) < 1024 {
		return errors.New("archivecheck: tar stream does not end with the two-block end-of-archive marker; hidden or truncated content past the archive end is refused")
	}
	return nil
}

// maxTrailingPadding bounds the decompressed bytes that may trail the tar
// end-of-archive marker. Conforming writers pad to at most one 10240-byte
// record; 64 KiB is a generous ceiling, and anything larger is treated as
// hidden content, not padding.
const maxTrailingPadding = 64 << 10

// drainGzipTail reads the gzip stream to its end, forcing compress/gzip to
// verify the CRC-32/ISIZE trailer, and refuses any non-NUL byte or any
// trailing run longer than maxTrailingPadding: tar stops at the
// end-of-archive marker, so a second payload hidden after it (including in
// a concatenated gzip member) must be refused rather than silently skipped.
func drainGzipTail(ctx context.Context, gz *gzip.Reader) error {
	buf := make([]byte, 4*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("archivecheck: %w", err)
		}
		n, err := gz.Read(buf)
		total += int64(n)
		if total > maxTrailingPadding {
			return fmt.Errorf("archivecheck: gzip stream carries more than %d bytes past the tar end-of-archive marker", maxTrailingPadding)
		}
		for i := 0; i < n; i++ {
			if buf[i] != 0 {
				return errors.New("archivecheck: non-NUL bytes follow the tar end-of-archive marker; hidden content past the archive end is refused")
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("archivecheck: gzip stream rejected while verifying its trailer: %w", err)
		}
	}
}

// checkZip validates a zip container archive.
func checkZip(ctx context.Context, a Archive, opts Options) (*Manifest, error) {
	lim := &ctxLimitedReaderAt{ctx: ctx, ra: a.ReaderAt, max: opts.Bounds.MaxCompressedBytes}
	zr, err := zip.NewReader(lim, a.Size)
	if err != nil {
		return nil, fmt.Errorf("archivecheck: zip central directory rejected: %w", err)
	}
	var declaredCompressed int64
	for _, f := range zr.File {
		declaredCompressed += int64(f.CompressedSize64)
	}
	if declaredCompressed > opts.Bounds.MaxCompressedBytes {
		return nil, fmt.Errorf("archivecheck: zip members declare %d compressed bytes, above MaxCompressedBytes %d", declaredCompressed, opts.Bounds.MaxCompressedBytes)
	}
	v := newValidator(opts)
	for _, f := range zr.File {
		if err = v.begin(); err != nil {
			return nil, err
		}
		mode := f.Mode()
		if mode&(fs.ModeSetuid|fs.ModeSetgid) != 0 {
			return nil, fmt.Errorf("archivecheck: member %q: setuid/setgid mode bits are refused", f.Name)
		}
		isDir := strings.HasSuffix(f.Name, "/")
		if typ := mode & fs.ModeType; typ != 0 && !(isDir && typ == fs.ModeDir) {
			return nil, fmt.Errorf("archivecheck: member %q: unsafe entry type in its mode bits; links, devices and FIFOs are refused", f.Name)
		}
		if err = v.entry(f.Name, isDir); err != nil {
			return nil, err
		}
		if isDir {
			continue
		}
		rc, oerr := f.Open()
		if oerr != nil {
			return nil, fmt.Errorf("archivecheck: member %q: cannot open: %w", f.Name, oerr)
		}
		m, rerr := v.readMember(ctx, rc, f.Name, int64(f.UncompressedSize64))
		rc.Close()
		if rerr != nil {
			return nil, rerr
		}
		v.man.Members = append(v.man.Members, m)
	}
	return v.finish()
}
