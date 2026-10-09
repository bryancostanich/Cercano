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
// PreflightFile takes ONE bounded immutable in-memory snapshot of the
// opened file, hashes that snapshot against the receipt and parses the
// exact same snapshot bytes, so the receipt digest always binds the
// parsed bytes. That is an integrity binding, never publisher
// authentication. The expected layout
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
//
// Names use one portable namespace contract, applied identically to the
// trusted layout and to archive entries: valid UTF-8 only, no
// Windows-invalid characters (< > | ? * "), no reserved device names
// (including the superscript-digit COM¹/COM²/COM³ and LPT forms), at most
// one trailing slash on directory names, and no two names that fold equal
// under Unicode simple case folding (strings.EqualFold's equivalence, e.g.
// Greek sigma σ/ς/Σ) may coexist as members, implicit parent directories,
// or a file/directory pair — order never matters.
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
	"strings"
	"unicode"
	"unicode/utf8"
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
	// Root is the single expected top-level directory name (e.g.
	// "cercano-1.2.3-darwin-arm64-unsigned"), or "" when the archive has no
	// root directory and its members appear at the archive's top level.
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
	// re-hash the one immutable snapshot taken of the bytes actually
	// opened; Check ignores it because its bytes arrive already bound by
	// the caller. Binding is integrity, not publisher authentication.
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
// the private directory holding it. The file's bytes are read ONCE into a
// bounded immutable in-memory snapshot (a hard finite allocation ceiling is
// checked before any allocation), and that exact snapshot is hashed,
// size-compared against the verified receipt and structurally parsed, so
// the receipt digest always binds the parsed bytes even if the file changes
// on disk afterwards. The file is opened read-only and is never modified;
// the snapshot is never written anywhere and nothing is extracted or
// executed.
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
	// then describes the file actually being opened.
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
	// One immutable snapshot binds the receipt digest to the parsed bytes:
	// hashing and structural checking consume the exact same bytes, and
	// the file is never re-read afterwards.
	return preflightSnapshot(ctx, f, sfi.Size(), opts)
}

// snapshotBytesCeiling is the hard finite allocation ceiling on the
// immutable in-memory archive snapshot PreflightFile takes. The stat'ed
// length is checked against it BEFORE any allocation: caller bounds alone
// are not trusted for allocation, so a MaxCompressedBytes near
// math.MaxInt64 can never become an attempted huge allocation. It mirrors
// the acquisition boundary's hard target cap, so no archive a verified
// receipt could legitimately authorize is refused by it.
const snapshotBytesCeiling = 256 << 20

// preflightSnapshot binds the receipt digest to the parsed bytes. It takes
// ONE bounded immutable in-memory snapshot of exactly size bytes from ra,
// length-checks and hashes that snapshot against opts.Identity when set,
// and parses the exact same snapshot bytes. Nothing is written, extracted
// or executed; a source that serves different bytes per call (or a file
// changed on disk between passes) can never make the digest bind bytes
// other than the ones parsed, because there is no second pass.
func preflightSnapshot(ctx context.Context, ra io.ReaderAt, size int64, opts Options) (*Manifest, error) {
	if id := opts.Identity; id != nil {
		if id.Length < 0 || len(id.SHA256) != sha256.Size {
			return nil, errors.New("archivecheck: Identity must carry an exact non-negative length and a raw 32-byte SHA-256")
		}
		if size != id.Length {
			return nil, fmt.Errorf("archivecheck: archive is %d bytes but the verified receipt authorizes %d bytes",
				size, id.Length)
		}
	}
	snap, err := snapshotReaderAt(ctx, ra, size)
	if err != nil {
		return nil, err
	}
	if id := opts.Identity; id != nil {
		sum := sha256.Sum256(snap)
		if !bytes.Equal(sum[:], id.SHA256) {
			return nil, errors.New("archivecheck: archive bytes do not match the SHA-256 of the verified receipt")
		}
	}
	// Both formats parse the immutable snapshot; the parse never touches
	// the mutable source again.
	br := bytes.NewReader(snap)
	if opts.Format == Zip {
		return Check(ctx, Archive{ReaderAt: br, Size: int64(len(snap))}, opts)
	}
	return Check(ctx, Archive{Reader: br}, opts)
}

// snapshotReaderAt reads exactly size bytes from ra into one immutable
// in-memory snapshot. size is checked against the hard allocation ceiling
// BEFORE any allocation and before any byte is read; the read then runs in
// small bounded chunks that honor ctx cancellation, consumes exactly the
// advertised length (never a byte past it) and refuses a source providing
// fewer bytes than it advertised.
func snapshotReaderAt(ctx context.Context, ra io.ReaderAt, size int64) ([]byte, error) {
	if size < 0 || size > snapshotBytesCeiling {
		return nil, fmt.Errorf("archivecheck: archive length %d is negative or exceeds the immutable-snapshot ceiling %d",
			size, snapshotBytesCeiling)
	}
	snap := make([]byte, size)
	const chunk = 64 << 10 // small chunks keep cancellation checks frequent
	for off := int64(0); off < size; {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("archivecheck: taking the archive snapshot: %w", err)
		}
		end := off + chunk
		if end > size {
			end = size
		}
		n, err := ra.ReadAt(snap[off:end], off)
		off += int64(n)
		if err == io.EOF && off < size {
			return nil, errors.New("archivecheck: archive provided fewer bytes than its length; truncated input is refused")
		}
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("archivecheck: reading the archive snapshot failed: %w", err)
		}
		if n == 0 && err == nil {
			return nil, errors.New("archivecheck: archive source stalled while snapshotting")
		}
	}
	return snap, nil
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
	if b.MaxMembers <= 0 || b.MaxCompressedBytes <= 0 || b.MaxCompressedBytes == math.MaxInt64 || b.MaxUncompressedBytes <= 0 || b.MaxMemberBytes <= 0 {
		return errors.New("archivecheck: every bound must be a positive finite value; there are no defaults")
	}
	l := opts.Layout
	if len(l.Required) == 0 {
		return errors.New("archivecheck: Layout.Required must name the required agent and CLI members")
	}
	// Root "" is the documented no-root layout: members then appear at the
	// archive's top level, so it is the one legal empty directory name.
	if l.Root != "" {
		if err := validateEntryName(l.Root, true); err != nil {
			return fmt.Errorf("archivecheck: Layout.Root: %w", err)
		}
	}
	// The layout must be internally consistent across its whole portable
	// namespace: the root directory, every Required/Optional member and
	// every implicit parent directory those members force. Two names that
	// fold to the same key (strings.EqualFold's simple Unicode case
	// folding), or one name that must be both a file and a directory, make
	// the layout ambiguous on case-insensitive filesystems (Windows,
	// macOS) and are refused regardless of declaration order.
	nsSeen := map[string]string{} // fold key -> exact first-seen name
	nsKind := map[string]bool{}   // fold key -> name is a directory
	addNS := func(name string, isDir bool) error {
		key := foldKey(name)
		if prev, dup := nsSeen[key]; dup {
			switch {
			case prev == name && isDir && nsKind[key]:
				return nil // the same implicit parent, legitimately shared
			case prev == name && !isDir && !nsKind[key]:
				return fmt.Errorf("archivecheck: Layout contains duplicate member %q", name)
			case nsKind[key] != isDir:
				return fmt.Errorf("archivecheck: Layout uses %q and %q as both a regular file and a directory", name, prev)
			default:
				return fmt.Errorf("archivecheck: Layout contains case-colliding names %q and %q", name, prev)
			}
		}
		nsSeen[key] = name
		nsKind[key] = isDir
		return nil
	}
	for _, role := range []struct {
		name  string
		names []string
	}{{"Required", l.Required}, {"Optional", l.Optional}} {
		for _, n := range role.names {
			if err := validateEntryName(n, false); err != nil {
				return fmt.Errorf("archivecheck: Layout.%s %q: %w", role.name, n, err)
			}
			full := joinRoot(l.Root, n)
			if err := addNS(full, false); err != nil {
				return fmt.Errorf("archivecheck: Layout.%s %q: %w", role.name, n, err)
			}
			// Every ancestor directory of a member is part of the namespace.
			for d := full; ; {
				i := strings.LastIndexByte(d, '/')
				if i < 0 {
					break
				}
				d = d[:i]
				if err := addNS(d, true); err != nil {
					return fmt.Errorf("archivecheck: Layout.%s %q: %w", role.name, n, err)
				}
			}
		}
	}
	return nil
}

// validateEntryName accepts only a relative, slash-separated member name
// with no cross-platform ambiguity. It rejects: empty names, absolute
// paths, names that are not valid UTF-8 (Go's range-over-string decodes
// bad bytes to RuneError, which would otherwise be silently accepted),
// empty, "." and ".." segments, backslashes, colons (drive letters and
// NTFS alternate data streams), the characters < > | ? * and " that
// Windows refuses, control bytes, Windows reserved device names in any
// segment, and segments ending in a dot or space. Directory names may
// carry at most ONE trailing slash, which is trimmed before validation; a
// second trailing slash is refused instead of being trimmed into
// acceptance.
func validateEntryName(name string, isDir bool) error {
	if isDir {
		name = strings.TrimSuffix(name, "/")
	}
	if name == "" {
		return errors.New("empty member name")
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("member name %q is not valid UTF-8", name)
	}
	if strings.HasPrefix(name, "/") {
		return fmt.Errorf("member name %q must be relative", name)
	}
	if strings.HasSuffix(name, "/") {
		return fmt.Errorf("member name %q carries more than one trailing slash", name)
	}
	if strings.ContainsRune(name, '\\') {
		return fmt.Errorf("member name %q must not contain a backslash", name)
	}
	if strings.ContainsRune(name, ':') {
		return fmt.Errorf("member name %q must not contain a colon (drive-letter or alternate-data-stream ambiguity)", name)
	}
	if strings.ContainsAny(name, `<>|?*"`) {
		return fmt.Errorf("member name %q contains a Windows-invalid character (< > | ? * \")", name)
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
		case windowsReserved[foldKey(strings.SplitN(seg, ".", 2)[0])]:
			return fmt.Errorf("member name %q uses the Windows reserved device name %q", name, seg)
		}
	}
	return nil
}

// maxFoldOrbit bounds each rune's case-fold orbit traversal. Real Unicode
// orbits are tiny (never more than a handful of runes), so the bound is
// purely defensive.
const maxFoldOrbit = 64

// foldKey returns a canonical case-folding key for name: each rune maps to
// the smallest rune of its unicode.SimpleFold orbit, which yields exactly
// the strings.EqualFold equivalence classes. This catches the Unicode
// folds strings.ToLower misses (Greek sigma σ/ς/Σ, long s ſ, Kelvin sign
// K) without disallowing legitimate non-ASCII names. The per-rune
// traversal is bounded by maxFoldOrbit; invalid UTF-8 must be rejected
// before names ever reach this (range would decode bad bytes to
// RuneError and conflate distinct names).
func foldKey(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		b.WriteRune(foldRune(r))
	}
	return b.String()
}

func foldRune(r rune) rune {
	start, min := r, r
	for i := 0; i < maxFoldOrbit; i++ {
		r = unicode.SimpleFold(r)
		if r == start {
			return min
		}
		if r < min {
			min = r
		}
	}
	return min
}

// windowsReserved lists the device names Windows refuses as file or
// directory names in any case and regardless of an extension, stored as
// fold keys so lookups match across the full EqualFold equivalence.
var windowsReserved = func() map[string]bool {
	names := map[string]bool{"con": true, "prn": true, "aux": true, "nul": true, "conin$": true, "conout$": true}
	for i := 1; i <= 9; i++ {
		names[fmt.Sprintf("com%d", i)] = true
		names[fmt.Sprintf("lpt%d", i)] = true
	}
	// Recent Windows also reserves the superscript-digit device forms
	// COM¹/COM²/COM³ and LPT¹/LPT²/LPT³ (U+00B9, U+00B2, U+00B3).
	for _, sup := range []string{"\u00b9", "\u00b2", "\u00b3"} {
		names["com"+sup] = true
		names["lpt"+sup] = true
	}
	m := make(map[string]bool, len(names))
	for n := range names {
		m[foldKey(n)] = true
	}
	return m
}()

// validator carries per-Check state: the allowlist built from the trusted
// layout, collision detection, and the enforced bounds.
type validator struct {
	opts  Options
	files map[string]bool   // allowed regular-file member names
	dirs  map[string]bool   // permitted directory names (needed parent paths only)
	seen  map[string]string // fold key -> exact first-seen name
	kind  map[string]bool   // fold key -> is directory
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
	root = strings.TrimSuffix(root, "/") // a single trailing slash is legal, "//" is not
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
	// Validate BEFORE trimming so a second trailing slash cannot be
	// trimmed into acceptance.
	if err := validateEntryName(name, isDir); err != nil {
		return fmt.Errorf("archivecheck: member %q: %w", name, err)
	}
	if isDir {
		name = strings.TrimSuffix(name, "/")
	}
	key := foldKey(name)
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
	// Overflow-safe: v.total never exceeds MaxUncompressedBytes, so the
	// remaining budget is computed by subtraction, never by addition that
	// could wrap near math.MaxInt64.
	if declared < 0 || declared > b.MaxMemberBytes || b.MaxUncompressedBytes-v.total < declared {
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
			// Check before accumulating so no sum can ever wrap.
			if actual > b.MaxMemberBytes-int64(n) || b.MaxUncompressedBytes-v.total < int64(n) {
				return Member{}, fmt.Errorf("archivecheck: member %q: content exceeds the member or total uncompressed bounds", name)
			}
			actual += int64(n)
			v.total += int64(n)
			h.Write(buf[:n])
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
	if l.n > l.max {
		return 0, fmt.Errorf("archivecheck: compressed stream exceeds MaxCompressedBytes %d", l.max)
	}
	// One byte past max stays readable so a strict overflow is detected
	// instead of being truncated into acceptance. rem+1 is computed as a
	// guarded increment so a MaxCompressedBytes of math.MaxInt64 cannot
	// overflow into a negative limit.
	rem := l.max - l.n // in [0, math.MaxInt64]
	if rem < math.MaxInt64 {
		rem++
	}
	if int64(len(p)) > rem {
		p = p[:rem]
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

// checkTarGz validates a gzip-compressed tar archive. The stdlib tar reader
// does the parsing (no handrolled tar parser); the tarMeter around it bounds
// and proves the framing, and proves the end-of-archive marker before the
// truncated forms of io.EOF are accepted.
func checkTarGz(ctx context.Context, r io.Reader, opts Options) (*Manifest, error) {
	maxStream, framingBudget, err := decompressedStreamCaps(opts.Bounds)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(&ctxLimitedReader{ctx: ctx, r: r, max: opts.Bounds.MaxCompressedBytes})
	if err != nil {
		return nil, fmt.Errorf("archivecheck: not a valid gzip archive: %w", err)
	}
	defer gz.Close()
	v := newValidator(opts)
	meter := &tarMeter{r: gz, v: v, maxStream: maxStream, framingBudget: framingBudget, tailNZ: -1}
	tr := tar.NewReader(meter)
	var lastSize int64 // declared size of the last accepted member
	for {
		if err = ctx.Err(); err != nil {
			return nil, fmt.Errorf("archivecheck: %w", err)
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			// archive/tar also returns io.EOF when the stream simply ends
			// (no marker, or only one marker block); prove the marker.
			if perr := meter.proveEndMarker(lastSize); perr != nil {
				return nil, perr
			}
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
			// Directory entries carry no data section.
			meter.dataEnd = meter.n
			lastSize = 0
		case tar.TypeReg:
			if err = v.entry(hdr.Name, false); err != nil {
				return nil, err
			}
			member, rerr := v.readMember(ctx, tr, hdr.Name, hdr.Size)
			if rerr != nil {
				return nil, rerr
			}
			v.man.Members = append(v.man.Members, member)
			meter.dataEnd = meter.n
			lastSize = hdr.Size
		default:
			return nil, fmt.Errorf("archivecheck: member %q: entry type %q is not a regular file or directory; links, devices and FIFOs are refused", hdr.Name, rune(hdr.Typeflag))
		}
	}
	// archive/tar stops at the end-of-archive marker, which normally leaves
	// the gzip trailer unread: compress/gzip verifies the CRC-32/ISIZE
	// trailer only when the stream is read to EOF. Draining the remainder
	// enforces that trailer, refuses non-NUL bytes past the marker (a
	// hidden second payload, including one concatenated as a second gzip
	// member) and trailing NUL runs beyond maxTrailingPadding.
	if err = drainGzipTail(ctx, meter); err != nil {
		return nil, err
	}
	return v.finish()
}

// Tar framing constants. They bound the decompressed bytes archive/tar may
// consume for framing — headers, PAX/GNU extension content, padding and the
// end-of-archive marker — in addition to member payloads, and define the
// exact padding limit enforced after the marker.
const (
	// tarBlockSize is the 512-byte tar block size.
	tarBlockSize = 512
	// eoaMarkerLen is the length of the two-block NUL end-of-archive marker
	// that POSIX requires after the last member.
	eoaMarkerLen = 2 * tarBlockSize
	// maxSpecialFileSize mirrors the stdlib archive/tar cap on PAX/GNU
	// extension ("special file") content: stdlib refuses to buffer more, so
	// budgeting per-member framing at this scale cannot be turned into an
	// unbounded allocation by a header bomb.
	maxSpecialFileSize = 1 << 20
	// perMemberFraming is the decompressed framing allowance per member: its
	// 512-byte header block, at most one extension header block plus
	// maxSpecialFileSize bytes of PAX/GNU extension content, up to two
	// block-padding runs, and slack.
	perMemberFraming = maxSpecialFileSize + 8*tarBlockSize
	// maxTrailingPadding is the EXACT padding limit after the end-of-archive
	// marker: conforming tar writers pad the archive to at most one
	// 10240-byte record, so 64 KiB (65536 bytes) of trailing NUL is a
	// generous ceiling. Anything longer, or any non-NUL byte there, is
	// treated as hidden content and refused.
	maxTrailingPadding = 64 << 10
	// framingSlack covers the accounting lag between the meter counting a
	// returned chunk and readMember counting the same chunk as payload; the
	// framing share briefly appears up to one read chunk larger.
	framingSlack = 64 << 10
)

// decompressedStreamCaps derives, from the caller's bounds, the cap on the
// TOTAL decompressed stream (member payloads plus headers, PAX/GNU
// metadata, padding and the marker — everything archive/tar pulls) and the
// framing share of that cap. Every sum is computed with overflow checks;
// combinations that would wrap int64 are refused up front rather than
// mis-enforced.
func decompressedStreamCaps(b Bounds) (maxStream, framingBudget int64, err error) {
	if int64(b.MaxMembers) > math.MaxInt64/perMemberFraming {
		return 0, 0, fmt.Errorf("archivecheck: MaxMembers %d is too large for the per-member framing budget", b.MaxMembers)
	}
	framingBudget = int64(b.MaxMembers)*perMemberFraming + eoaMarkerLen + maxTrailingPadding + framingSlack
	if framingBudget < 0 || framingBudget > math.MaxInt64-1-b.MaxUncompressedBytes {
		return 0, 0, errors.New("archivecheck: MaxUncompressedBytes plus the framing budget overflows; refusing")
	}
	return b.MaxUncompressedBytes + framingBudget, framingBudget, nil
}

// tarMeter instruments the decompressed stream that archive/tar consumes. It
// counts EVERY decompressed byte — headers, PAX/GNU extension content,
// padding and the marker, not only member payloads — enforcing the total
// stream cap and the framing budget WHILE reading, so header and metadata
// bombs fail before the stdlib buffers them. It also records where the last
// accepted member's data section ended and the last non-NUL byte from there
// on, which is what turns archive/tar's io.EOF into an actual end-of-archive
// proof (see proveEndMarker); no tar framing is parsed by hand.
type tarMeter struct {
	r             io.Reader
	v             *validator
	maxStream     int64
	framingBudget int64

	n       int64 // total decompressed bytes observed
	dataEnd int64 // stream position just past the last accepted member's data
	tailNZ  int64 // last non-NUL position at/after dataEnd; -1 when none
	ended   bool  // the decompressed stream reached EOF
}

// Read enforces the total decompressed cap and the framing budget on every
// byte archive/tar pulls, and tracks the non-NUL tail after the last
// member. It never truncates: one byte past the cap stays readable so an
// oversized stream is refused rather than accepted as a prefix.
func (m *tarMeter) Read(p []byte) (int, error) {
	if m.n > m.maxStream {
		return 0, fmt.Errorf("archivecheck: decompressed stream exceeds the total bound %d (member payloads plus bounded framing)", m.maxStream)
	}
	rem := m.maxStream + 1 - m.n // safe: caps guarantee maxStream < math.MaxInt64
	if int64(len(p)) > rem {
		p = p[:rem]
	}
	n, err := m.r.Read(p)
	m.n += int64(n)
	if m.n > m.maxStream {
		return n, fmt.Errorf("archivecheck: decompressed stream exceeds the total bound %d (member payloads plus bounded framing)", m.maxStream)
	}
	if m.n-m.v.total > m.framingBudget {
		return n, fmt.Errorf("archivecheck: tar framing (headers, PAX/GNU metadata or padding) exceeds the budget %d", m.framingBudget)
	}
	start := m.n - int64(n)
	for i := 0; i < n; i++ {
		if p[i] != 0 && start+int64(i) >= m.dataEnd {
			m.tailNZ = start + int64(i)
		}
	}
	if err == io.EOF {
		m.ended = true
	}
	return n, err
}

// blockPadding is the tar block padding following a data section of size.
func blockPadding(size int64) int64 {
	if rem := size % tarBlockSize; rem != 0 {
		return tarBlockSize - rem
	}
	return 0
}

// proveEndMarker turns archive/tar's io.EOF into an actual end-of-archive
// proof. archive/tar returns io.EOF not only for the genuine two-block NUL
// marker but also for a stream that merely ends — with no marker at all, or
// with only one marker block — and it stops reading at the marker, so a
// trailing-1024-zero heuristic can mistake a zero-ending member payload for
// the marker and hide truncation. The meter instead knows the exact stream
// position where the last accepted member's data section ended, so the
// marker must start exactly at dataEnd+blockPadding(lastSize); stdlib itself
// verified that both marker blocks are NUL before returning io.EOF. The
// proof therefore refuses: any non-NUL byte between the last member and the
// marker (smuggled or concatenated hidden entries), a stream that already
// ended with fewer than the full 1024-byte marker, and a stream that ended
// with more than maxTrailingPadding trailing NULs. When the stream has not
// ended, the remaining tail is proven NUL and bounded by drainGzipTail.
func (m *tarMeter) proveEndMarker(lastSize int64) error {
	markerStart := m.dataEnd + blockPadding(lastSize)
	if m.tailNZ >= markerStart {
		return errors.New("archivecheck: non-NUL bytes follow the last member before the end-of-archive marker; hidden content past the archive end is refused")
	}
	if m.ended {
		if m.n < markerStart+eoaMarkerLen {
			return errors.New("archivecheck: tar stream ends without the full two-block end-of-archive marker; missing, one-block or truncated markers are refused")
		}
		if m.n-markerStart-eoaMarkerLen > maxTrailingPadding {
			return fmt.Errorf("archivecheck: gzip stream carries more than %d bytes past the tar end-of-archive marker", maxTrailingPadding)
		}
	}
	return nil
}

// drainGzipTail reads the gzip stream past the marker to its end, forcing
// compress/gzip to verify the CRC-32/ISIZE trailer (it only does so when the
// stream reaches EOF, even when every tar entry already parsed), and
// refuses any non-NUL byte — a second payload hidden after the marker,
// including one smuggled in a concatenated gzip member — and any trailing
// NUL run longer than maxTrailingPadding.
func drainGzipTail(ctx context.Context, m *tarMeter) error {
	buf := make([]byte, 4*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("archivecheck: %w", err)
		}
		n, err := m.Read(buf)
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
	// Overflow-safe accumulation: a declared size above math.MaxInt64 or a
	// running sum that would wrap is refused instead of silently accepted.
	var declaredCompressed int64
	for _, f := range zr.File {
		if f.CompressedSize64 > math.MaxInt64 || declaredCompressed > math.MaxInt64-int64(f.CompressedSize64) {
			return nil, fmt.Errorf("archivecheck: zip members declare compressed sizes above MaxCompressedBytes %d", opts.Bounds.MaxCompressedBytes)
		}
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
