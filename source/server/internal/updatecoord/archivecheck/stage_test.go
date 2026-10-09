package archivecheck

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stagePolicy is the trusted staging policy used by the fixture tests: the
// same trusted member names as testLayout's executables, fixed policy modes,
// and a staging-directory name prefix. It contains no default HOME, no
// environment lookups and no paths outside the caller-provided parent.
func stagePolicy() StageOptions {
	return StageOptions{
		FileMode:       0o644,
		ExecutableMode: 0o755,
		DirMode:        0o755,
		Executables:    []string{"bin/cercano", "bin/cercano-cli"},
		StagingPattern: "cercano-stage-",
	}
}

// writeArchiveFile writes the fixture bytes to a regular file (as the
// acquisition boundary would have received them) and returns its path.
func writeArchiveFile(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "update-archive")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// countParentEntries returns the number of entries left in the staging
// parent after a staging attempt.
func countParentEntries(t *testing.T, parent string) int {
	t.Helper()
	es, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	return len(es)
}

// assertStagedTree verifies the staged directory against the fixture
// bodies and the TRUSTED POLICY modes: executables 0755, documents 0644,
// directories 0755 — never the archive's own mode bits — and that every
// staged file's SHA-256 equals the preflight manifest hash bound to the
// archive bytes.
func assertStagedTree(t *testing.T, st *Staged) {
	t.Helper()
	if st == nil || st.Dir == "" {
		t.Fatal("staged result is missing its directory")
	}
	if st.Manifest == nil || len(st.Manifest.Members) != 4 {
		t.Fatalf("staged manifest members = %v, want 4", st.Manifest)
	}
	manByName := map[string]Member{}
	for _, m := range st.Manifest.Members {
		manByName[m.Name] = m
	}
	want := []struct {
		rel  string // under the staging directory
		body string
		mode fs.FileMode
	}{
		{filepath.Join(testRoot, "bin", "cercano"), agentBody, 0o755},
		{filepath.Join(testRoot, "bin", "cercano-cli"), cliBody, 0o755},
		{filepath.Join(testRoot, "LICENSE"), licenseBody, 0o644},
		{filepath.Join(testRoot, "README.txt"), readmeBody, 0o644},
	}
	for _, w := range want {
		full := filepath.Join(st.Dir, w.rel)
		data, err := os.ReadFile(full)
		if err != nil {
			t.Fatalf("reading staged member %q: %v", w.rel, err)
		}
		if string(data) != w.body {
			t.Errorf("staged member %q content = %q, want %q", w.rel, data, w.body)
		}
		fi, err := os.Lstat(full)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode() != w.mode {
			t.Errorf("staged member %q mode = %v, want the policy mode %v (never the archive's bits)", w.rel, fi.Mode(), w.mode)
		}
		sum := sha256.Sum256(data)
		m := manByName[strings.ReplaceAll(w.rel, string(filepath.Separator), "/")]
		if m.SHA256 != hex.EncodeToString(sum[:]) || m.Size != int64(len(data)) {
			t.Errorf("staged member %q does not match its preflight manifest entry %+v", w.rel, m)
		}
	}
	// The staged directories exist with the policy mode.
	for _, d := range []string{testRoot, filepath.Join(testRoot, "bin")} {
		fi, err := os.Lstat(filepath.Join(st.Dir, d))
		if err != nil || !fi.IsDir() {
			t.Fatalf("staged directory %q missing: %v", d, err)
		}
		if fi.Mode().Perm() != 0o755 {
			t.Errorf("staged directory %q mode = %v, want 0755", d, fi.Mode().Perm())
		}
	}
	// The staging directory contains nothing beyond the staged tree: no
	// extra files, no symlinks, nothing followed or smuggled in.
	var seen int
	err := filepath.WalkDir(st.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type() != 0 && !d.IsDir() {
			return fmt.Errorf("non-regular staged object %q (type %v)", p, d.Type())
		}
		if !d.IsDir() {
			seen++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != 4 {
		t.Errorf("staged directory holds %d regular files, want exactly the 4 manifest members", seen)
	}
}

func TestStageFileTarGzStagesApprovedMembersWithPolicyModes(t *testing.T) {
	// The archive's own mode bits are deliberately loose (0o777 documents,
	// 0o666 executables): staging must ignore them and apply the policy.
	entries := []tarEntry{
		{name: testRoot + "/", typ: tar.TypeDir, mode: 0o777},
		{name: testRoot + "/bin/", typ: tar.TypeDir, mode: 0o777},
		{name: testRoot + "/bin/cercano", body: agentBody, typ: tar.TypeReg, mode: 0o666},
		{name: testRoot + "/bin/cercano-cli", body: cliBody, typ: tar.TypeReg, mode: 0o666},
		{name: testRoot + "/LICENSE", body: licenseBody, typ: tar.TypeReg, mode: 0o777},
		{name: testRoot + "/README.txt", body: readmeBody, typ: tar.TypeReg, mode: 0o777},
	}
	archivePath := writeArchiveFile(t, buildTarGz(t, entries))
	parent := t.TempDir()
	st, err := StageFile(context.Background(), archivePath, parent, tarOpts(), stagePolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertStagedTree(t, st)
	if c := countParentEntries(t, parent); c != 1 {
		t.Errorf("staging parent holds %d entries, want exactly the one staging directory", c)
	}
}

func TestStageFileZipStagesApprovedMembersWithPolicyModes(t *testing.T) {
	// Same as the tar case, with the Windows-distribution zip container:
	// archive mode bits again ignored in favor of the trusted policy.
	entries := []zipEntry{
		{name: testRoot + "/", mode: fs.ModeDir | 0o777},
		{name: testRoot + "/bin/", mode: fs.ModeDir | 0o777},
		{name: testRoot + "/bin/cercano", body: agentBody, mode: 0o666},
		{name: testRoot + "/bin/cercano-cli", body: cliBody, mode: 0o666},
		{name: testRoot + "/LICENSE", body: licenseBody, mode: 0o777},
		{name: testRoot + "/README.txt", body: readmeBody, mode: 0o777},
	}
	archivePath := writeArchiveFile(t, buildZip(t, entries))
	parent := t.TempDir()
	st, err := StageFile(context.Background(), archivePath, parent, zipOpts(), stagePolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertStagedTree(t, st)
	if c := countParentEntries(t, parent); c != 1 {
		t.Errorf("staging parent holds %d entries, want exactly the one staging directory", c)
	}
}

func TestStageFileUnicodeMemberPaths(t *testing.T) {
	// Unicode member names flow through the same validated portable
	// namespace and are staged verbatim on disk.
	const dirName = "日本語-ünïcode"
	const fileName = dirName + "/Σφραγίδα.txt"
	const body = "unicode-body-🚀"
	layout := Layout{Required: []string{fileName}}
	opts := Options{Format: TarGz, Layout: layout, Bounds: testBounds()}
	policy := StageOptions{FileMode: 0o644, ExecutableMode: 0o755, DirMode: 0o755, StagingPattern: "stage-"}
	entries := []tarEntry{
		{name: dirName + "/", typ: tar.TypeDir, mode: 0o755},
		{name: fileName, body: body, typ: tar.TypeReg, mode: 0o644},
	}
	archivePath := writeArchiveFile(t, buildTarGz(t, entries))
	parent := t.TempDir()
	st, err := StageFile(context.Background(), archivePath, parent, opts, policy)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(st.Dir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != body {
		t.Errorf("staged unicode member content = %q, want %q", data, body)
	}
	if st.Manifest.Members[0].Name != fileName {
		t.Errorf("manifest member = %q, want %q", st.Manifest.Members[0].Name, fileName)
	}
}

func TestStageFileBadArchiveRejectedBeforeAnyFileCreation(t *testing.T) {
	// A structurally bad archive is refused by the FULL preflight over the
	// immutable snapshot — before the staging directory even exists, so
	// the parent stays empty. No partial member file can ever appear.
	cases := []struct {
		name     string
		opts     Options
		data     func(t *testing.T) []byte
		contains string
	}{
		{
			name: "missing required tar member",
			opts: tarOpts(),
			data: func(t *testing.T) []byte {
				entries := []tarEntry{{name: testRoot + "/bin/cercano", body: agentBody, typ: tar.TypeReg, mode: 0o755}}
				return buildTarGz(t, entries)
			},
			contains: "missing required member",
		},
		{
			name:     "corrupt zip bytes",
			opts:     zipOpts(),
			data:     func(t *testing.T) []byte { return []byte("not a zip archive at all") },
			contains: "zip central directory rejected",
		},
		{
			name: "truncated tar stream",
			opts: tarOpts(),
			data: func(t *testing.T) []byte {
				data := buildTarGz(t, goodTarEntries())
				return data[:len(data)-40]
			},
			contains: "tar stream",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			archivePath := writeArchiveFile(t, tc.data(t))
			parent := t.TempDir()
			_, err := StageFile(context.Background(), archivePath, parent, tc.opts, stagePolicy())
			wantError(t, err, tc.contains)
			if c := countParentEntries(t, parent); c != 0 {
				t.Errorf("staging parent holds %d entries after a refused archive, want 0 (nothing was staged)", c)
			}
		})
	}
}

func TestStageFileReceiptIdentityMismatchRejectedBeforeAnyFileCreation(t *testing.T) {
	data := buildTarGz(t, goodTarEntries())
	archivePath := writeArchiveFile(t, data)
	opts := tarOpts()
	opts.Identity = &Identity{Length: int64(len(data)), SHA256: make([]byte, sha256.Size)}
	parent := t.TempDir()
	_, err := StageFile(context.Background(), archivePath, parent, opts, stagePolicy())
	wantError(t, err, "do not match the SHA-256 of the verified receipt")
	if c := countParentEntries(t, parent); c != 0 {
		t.Errorf("staging parent holds %d entries after an identity mismatch, want 0", c)
	}
}

func TestStageFileParentMustBeCallerValidatedDirectory(t *testing.T) {
	archivePath := writeArchiveFile(t, buildTarGz(t, goodTarEntries()))
	// A symlinked parent is refused: staging must never resolve links the
	// caller did not validate.
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "parent-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := StageFile(context.Background(), archivePath, link, tarOpts(), stagePolicy())
	wantError(t, err, "is a symlink")
	if c := countParentEntries(t, real); c != 0 {
		t.Errorf("caller directory was touched through the symlink: %d entries", c)
	}
	// A missing parent is refused as well.
	_, err = StageFile(context.Background(), archivePath, filepath.Join(t.TempDir(), "missing"), tarOpts(), stagePolicy())
	wantError(t, err, "staging parent")
}

func TestStageOptionsRefusedWhenInconsistent(t *testing.T) {
	cases := []struct {
		name     string
		policy   StageOptions
		contains string
	}{
		{
			name:     "FileMode with execute bits",
			policy:   StageOptions{FileMode: 0o755, ExecutableMode: 0o755, DirMode: 0o755, StagingPattern: "s-"},
			contains: "FileMode must not carry execute bits",
		},
		{
			name:     "ExecutableMode without execute bits",
			policy:   StageOptions{FileMode: 0o644, ExecutableMode: 0o644, DirMode: 0o755, StagingPattern: "s-"},
			contains: "ExecutableMode must carry execute bits",
		},
		{
			name:     "empty staging pattern",
			policy:   StageOptions{FileMode: 0o644, ExecutableMode: 0o755, DirMode: 0o755},
			contains: "StagingPattern is required",
		},
		{
			name: "executable not in the trusted layout",
			policy: StageOptions{FileMode: 0o644, ExecutableMode: 0o755, DirMode: 0o755,
				Executables: []string{"bin/not-a-member"}, StagingPattern: "s-"},
			contains: "not a member of the expected layout",
		},
		{
			name: "duplicate executables",
			policy: StageOptions{FileMode: 0o644, ExecutableMode: 0o755, DirMode: 0o755,
				Executables: []string{"bin/cercano", "bin/cercano"}, StagingPattern: "s-"},
			contains: "duplicate",
		},
	}
	archivePath := writeArchiveFile(t, buildTarGz(t, goodTarEntries()))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			_, err := StageFile(context.Background(), archivePath, parent, tarOpts(), tc.policy)
			wantError(t, err, tc.contains)
			if c := countParentEntries(t, parent); c != 0 {
				t.Errorf("staging parent holds %d entries, want 0", c)
			}
		})
	}
}

// newUnitStagingRoot creates an exclusive staging directory under a private
// parent (like StageFile does) and opens its directory handle for direct
// stageExtract-level tests that need to inject objects first.
func newUnitStagingRoot(t *testing.T) (parent, stageDir string, root *os.Root) {
	t.Helper()
	parent = t.TempDir()
	var err error
	stageDir, err = os.MkdirTemp(parent, "cercano-stage-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = os.OpenRoot(stageDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return parent, stageDir, root
}

// stageFixture stages fixture bytes directly through stageExtract (the
// extraction pass StageFile runs after its full preflight), so tests can
// control the exact archive readers and the pre-injected staging state.
// The fixture must first pass the same full preflight Check, run against a
// fresh Archive value (checkA); the staging pass then consumes stageA.
func stageFixture(t *testing.T, ctx context.Context, opts Options, policy StageOptions, checkA, stageA Archive, root *os.Root) (error, *stageLedger) {
	t.Helper()
	man, err := Check(ctx, checkA, opts)
	if err != nil {
		t.Fatalf("fixture must pass preflight before staging: %v", err)
	}
	ledger := newStageLedger()
	serr := stageExtract(ctx, stageA, opts, policy, man, root, ledger)
	return serr, ledger
}

// readerAtCancelsOnNthLowRead cancels ctx on the n-th ReadAt served at an
// offset below tail. The first low read is the zip EOCD backward-scan window
// (offset 0); the next one is the first member's local header during the
// copy phase — so cancellation lands after member files start being created.
type readerAtCancelsOnNthLowRead struct {
	ra   io.ReaderAt
	tail int64
	n    int
	canc context.CancelFunc
	lows int
}

func (r *readerAtCancelsOnNthLowRead) ReadAt(p []byte, off int64) (int, error) {
	if off < r.tail {
		r.lows++
		if r.lows == r.n {
			r.canc()
		}
	}
	return r.ra.ReadAt(p, off)
}

func TestStageCanceledMidCopyCleansUpCreatedFilesAndPreservesUnknownObjects(t *testing.T) {
	// A stored-zip fixture: cancellation is delivered during the member
	// copy phase. Cleanup must remove ONLY the files this staging created
	// (identity-matching, partial but accounted), never recursing, and
	// must PRESERVE unknown injected objects it did not create, reporting
	// the retained staging directory.
	entries := []zipEntry{
		{name: testRoot + "/bin/cercano", body: agentBody, mode: 0o755},
		{name: testRoot + "/bin/cercano-cli", body: cliBody, mode: 0o755},
		{name: testRoot + "/LICENSE", body: licenseBody, mode: 0o644},
		{name: testRoot + "/README.txt", body: readmeBody, mode: 0o644},
	}
	data := buildZip(t, entries)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, stageDir, root := newUnitStagingRoot(t)
	// Unknown injected objects cleanup must preserve.
	if err := os.WriteFile(filepath.Join(stageDir, "unknown-file"), []byte("keep-me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stageDir, "unknown-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The canceling reader is only handed to the STAGING pass; preflight
	// runs against plain immutable bytes (as StageFile would).
	canceling := &readerAtCancelsOnNthLowRead{ra: bytes.NewReader(data), tail: int64(len(data)) - 512, n: 2, canc: cancel}
	serr, ledger := stageFixture(t, ctx, zipOpts(), stagePolicy(),
		Archive{ReaderAt: bytes.NewReader(data), Size: int64(len(data))},
		Archive{ReaderAt: canceling, Size: int64(len(data))},
		root)
	if serr == nil || !(errors.Is(serr, context.Canceled) || strings.Contains(serr.Error(), "context canceled")) {
		t.Fatalf("expected a cancellation error while staging members, got %v", serr)
	}
	if len(ledger.fileOrd) == 0 {
		t.Fatal("expected at least one member file to have been created (and accounted) when cancellation hit")
	}
	retained, keepStage := cleanupStaging(root, stageDir, ledger)
	// Everything the staging created is gone — including the partial
	// member file, whose byte count was accounted.
	for _, name := range ledger.fileOrd {
		if _, err := root.Lstat(name); err == nil {
			t.Errorf("created file %q was left behind by cleanup", name)
		}
	}
	// The unknown objects are preserved untouched.
	if got, err := os.ReadFile(filepath.Join(stageDir, "unknown-file")); err != nil || string(got) != "keep-me" {
		t.Errorf("unknown injected file was not preserved verbatim: %v %q", err, got)
	}
	if fi, err := os.Lstat(filepath.Join(stageDir, "unknown-dir")); err != nil || !fi.IsDir() {
		t.Errorf("unknown injected directory was not preserved: %v", err)
	}
	// The staging directory itself must be reported as retained, because
	// the preserved unknown objects keep it non-empty.
	if len(retained) != 0 {
		t.Errorf("expected no created objects retained, got %v", retained)
	}
	if !keepStage {
		t.Error("expected the staging directory to be kept while unknown objects remain in it")
	}
}

func TestStageCopyMemberCanceledMidCopyRemovesPartialFile(t *testing.T) {
	// Deterministic mid-copy cancellation: the member's first read serves
	// two bytes, then the context is canceled. The partial file is fully
	// accounted in the ledger and removed by cleanup.
	_, stageDir, root := newUnitStagingRoot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ledger := newStageLedger()
	s := &staging{
		opts: Options{Bounds: testBounds()}, stage: stagePolicy(), root: root,
		expected: map[string]Member{"cercano": {Name: "cercano", Size: 6, SHA256: hex.EncodeToString(make([]byte, 32))}},
		v:        newValidator(Options{Bounds: testBounds()}), ledger: ledger,
	}
	r := &cancelAfterReader{r: strings.NewReader("abcdef"), after: 2, ctx: ctx, cancel: cancel}
	serr := s.copyMember(ctx, r, "cercano", 6)
	if serr == nil || !(errors.Is(serr, context.Canceled) || strings.Contains(serr.Error(), "context canceled")) {
		t.Fatalf("expected a cancellation error mid-copy, got %v", serr)
	}
	if len(ledger.fileOrd) != 1 || ledger.fileOrd[0] != "cercano" {
		t.Fatalf("ledger = %v, want the partial member accounted", ledger.fileOrd)
	}
	retained, keepStage := cleanupStaging(root, stageDir, ledger)
	if len(retained) != 0 || keepStage {
		t.Errorf("cleanup retained %v keepStage=%v, want a fully cleaned empty staging directory", retained, keepStage)
	}
	if _, err := os.Lstat(stageDir); !os.IsNotExist(err) {
		t.Errorf("staging directory should have been removed once empty, got %v", err)
	}
}

func TestStageOverwriteForbiddenInjectedFilePreserved(t *testing.T) {
	// A file injected at a member's path is never overwritten (O_EXCL),
	// never followed, and cleanup preserves it and reports it retained.
	data := buildZip(t, []zipEntry{
		{name: "cercano", body: agentBody, mode: 0o755},
		{name: "cercano-cli", body: cliBody, mode: 0o755},
	})
	opts := Options{Format: Zip, Layout: Layout{Required: []string{"cercano", "cercano-cli"}}, Bounds: testBounds()}
	policy := StageOptions{FileMode: 0o644, ExecutableMode: 0o755, DirMode: 0o755, StagingPattern: "s-"}
	_, stageDir, root := newUnitStagingRoot(t)
	injected := filepath.Join(stageDir, "cercano-cli")
	if err := os.WriteFile(injected, []byte("injected-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	serr, ledger := stageFixture(t, context.Background(), opts, policy,
		Archive{ReaderAt: bytes.NewReader(data), Size: int64(len(data))},
		Archive{ReaderAt: bytes.NewReader(data), Size: int64(len(data))},
		root)
	wantError(t, serr, "exclusively")
	retained, keepStage := cleanupStaging(root, stageDir, ledger)
	if !keepStage {
		t.Error("staging directory holding a preserved injected object must be kept")
	}
	if len(retained) != 1 || retained[0] != "cercano-cli" {
		t.Errorf("retained = %v, want exactly [\"cercano-cli\"] (the injected file, never removed)", retained)
	}
	// The injected file is preserved verbatim, never overwritten.
	got, err := os.ReadFile(injected)
	if err != nil || string(got) != "injected-content" {
		t.Errorf("injected file was not preserved verbatim: %v %q", err, got)
	}
	fi, err := os.Lstat(injected)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("injected file was modified: %v %v", err, fi)
	}
	// The member the staging DID create is removed by cleanup.
	if _, err := root.Lstat("cercano"); err == nil {
		t.Error("created member file was left behind by cleanup")
	}
}

func TestStageReplacedDirectoryObjectPreserved(t *testing.T) {
	// A regular file squatting on a directory member's path is never
	// followed or overwritten; staging refuses and cleanup preserves it
	// (it is not in the creation ledger) while reporting the retained
	// staging directory.
	entries := []tarEntry{
		{name: "bin/", typ: tar.TypeDir, mode: 0o755},
		{name: "bin/cercano", body: agentBody, typ: tar.TypeReg, mode: 0o755},
		{name: "bin/cercano-cli", body: cliBody, typ: tar.TypeReg, mode: 0o755},
	}
	data := buildTarGz(t, entries)
	opts := Options{Format: TarGz, Layout: Layout{Required: []string{"bin/cercano", "bin/cercano-cli"}}, Bounds: testBounds()}
	policy := StageOptions{FileMode: 0o644, ExecutableMode: 0o755, DirMode: 0o755, StagingPattern: "s-"}
	_, stageDir, root := newUnitStagingRoot(t)
	squatter := filepath.Join(stageDir, "bin")
	if err := os.WriteFile(squatter, []byte("do-not-touch"), 0o644); err != nil {
		t.Fatal(err)
	}
	serr, ledger := stageFixture(t, context.Background(), opts, policy,
		Archive{Reader: bytes.NewReader(data)},
		Archive{Reader: bytes.NewReader(data)},
		root)
	wantError(t, serr, "refusing to overwrite or follow it")
	retained, keepStage := cleanupStaging(root, stageDir, ledger)
	if !keepStage {
		t.Error("staging directory holding a preserved squatter object must be kept")
	}
	if len(retained) != 0 {
		t.Errorf("retained = %v, want none from the staging itself (no member file was created)", retained)
	}
	// The squatter is preserved verbatim.
	got, err := os.ReadFile(squatter)
	if err != nil || string(got) != "do-not-touch" {
		t.Errorf("squatter was not preserved verbatim: %v %q", err, got)
	}
}

func TestStageSymlinkMemberRejectedBeforeAnyFileCreation(t *testing.T) {
	// Preflight rejects a symlink member before anything is staged; the
	// staging pass would refuse it again. The parent stays empty.
	entries := []tarEntry{
		{name: testRoot + "/bin/cercano", body: agentBody, typ: tar.TypeReg, mode: 0o755},
		{name: testRoot + "/bin/cercano-cli", link: "cercano", typ: tar.TypeSymlink, mode: 0o777},
	}
	archivePath := writeArchiveFile(t, buildTarGz(t, entries))
	parent := t.TempDir()
	_, err := StageFile(context.Background(), archivePath, parent, tarOpts(), stagePolicy())
	wantError(t, err, "links, devices and FIFOs are refused")
	if c := countParentEntries(t, parent); c != 0 {
		t.Errorf("staging parent holds %d entries after a refused archive, want 0", c)
	}
}

func TestStageSymlinkAtMemberPathNeverFollowedOrOverwritten(t *testing.T) {
	// A symlink injected at a member's path inside the staging directory:
	// O_EXCL refuses to create through it, cleanup does not follow it, and
	// both the symlink and its referent are preserved.
	data := buildZip(t, []zipEntry{
		{name: "cercano", body: agentBody, mode: 0o755},
		{name: "cercano-cli", body: cliBody, mode: 0o755},
	})
	opts := Options{Format: Zip, Layout: Layout{Required: []string{"cercano", "cercano-cli"}}, Bounds: testBounds()}
	policy := StageOptions{FileMode: 0o644, ExecutableMode: 0o755, DirMode: 0o755, StagingPattern: "s-"}
	_, stageDir, root := newUnitStagingRoot(t)
	referent := filepath.Join(t.TempDir(), "referent")
	if err := os.WriteFile(referent, []byte("referent-body"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(stageDir, "cercano-cli")
	if err := os.Symlink(referent, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	serr, ledger := stageFixture(t, context.Background(), opts, policy,
		Archive{ReaderAt: bytes.NewReader(data), Size: int64(len(data))},
		Archive{ReaderAt: bytes.NewReader(data), Size: int64(len(data))},
		root)
	wantError(t, serr, "exclusively")
	retained, keepStage := cleanupStaging(root, stageDir, ledger)
	if !keepStage {
		t.Error("staging directory holding a preserved symlink must be kept")
	}
	if len(retained) != 1 || retained[0] != "cercano-cli" {
		t.Errorf("retained = %v, want exactly [\"cercano-cli\"]", retained)
	}
	// The symlink still points where it did; the referent is untouched.
	if got, err := os.Readlink(link); err != nil || got != referent {
		t.Errorf("symlink was not preserved verbatim: %v %q", err, got)
	}
	if got, err := os.ReadFile(referent); err != nil || string(got) != "referent-body" {
		t.Errorf("referent was not preserved verbatim: %v %q", err, got)
	}
}

func TestStageBoundsEnforcedAgainDuringCopy(t *testing.T) {
	// The extraction pass re-enforces the per-member and total bounds
	// against the bytes it ACTUALLY copies — here a declared 4-byte member
	// whose stream tries to hand over 6 bytes under a 4-byte total bound.
	// The partial file is accounted in the ledger and removed by cleanup.
	_, stageDir, root := newUnitStagingRoot(t)
	opts := Options{
		Format: TarGz,
		Layout: Layout{Required: []string{"small"}},
		Bounds: Bounds{MaxMembers: 4, MaxCompressedBytes: 1 << 16, MaxUncompressedBytes: 4, MaxMemberBytes: 4},
	}
	policy := StageOptions{FileMode: 0o644, ExecutableMode: 0o755, DirMode: 0o755, StagingPattern: "s-"}
	ctx := context.Background()
	man, err := Check(ctx, Archive{Reader: bytes.NewReader(buildTarGz(t, []tarEntry{
		{name: "small", body: "abcd", typ: tar.TypeReg, mode: 0o644},
	}))}, opts)
	if err != nil {
		t.Fatal(err)
	}
	ledger := newStageLedger()
	s := &staging{
		opts: opts, stage: policy, root: root,
		expected: map[string]Member{"small": man.Members[0]},
		v:        newValidator(opts), ledger: ledger,
	}
	serr := s.copyMember(ctx, strings.NewReader("abcdef"), "small", 4)
	wantError(t, serr, "exceeds the member or total uncompressed bounds")
	if len(ledger.fileOrd) != 1 {
		t.Fatalf("ledger = %v, want the failed member accounted", ledger.fileOrd)
	}
	retained, keepStage := cleanupStaging(root, stageDir, ledger)
	if len(retained) != 0 || keepStage {
		t.Errorf("cleanup retained %v keepStage=%v, want a fully cleaned empty staging directory", retained, keepStage)
	}
	if fi, err := os.Lstat(stageDir); err == nil {
		_ = fi
		t.Error("staging directory should have been removed once empty")
	}
}

func TestStageFileRequiresContextAndPaths(t *testing.T) {
	if _, err := StageFile(nil, "a", "b", tarOpts(), stagePolicy()); err == nil {
		t.Error("nil context must be refused")
	}
	archivePath := writeArchiveFile(t, buildTarGz(t, goodTarEntries()))
	if _, err := StageFile(context.Background(), "", t.TempDir(), tarOpts(), stagePolicy()); err == nil {
		t.Error("empty archive path must be refused")
	}
	if _, err := StageFile(context.Background(), archivePath, "", tarOpts(), stagePolicy()); err == nil {
		t.Error("empty parent path must be refused")
	}
}
