package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"cercano/source/server/internal/updatecoord/oneshot"
)

// digestOfLengthOne returns a syntactically valid digest (not the content of
// anything here) so request validation passes and the type check is what
// must refuse the source.
func digestOfLengthOne(t *testing.T) string {
	t.Helper()
	sum := sha256.Sum256([]byte{1})
	return hex.EncodeToString(sum[:])
}

// resetHooks clears the package test seams; every test that sets a hook
// defers this.
func resetHooks(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		testHookChunk = nil
		testHookAfterCopy = nil
		testHookVerifyRead = nil
	})
}

// makeSource writes a deterministic multi-chunk image and returns its path,
// digest and length.
func makeSource(t *testing.T, dir, name string, size int) (string, string, int64) {
	t.Helper()
	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i % 251)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatalf("writing source: %v", err)
	}
	sum := sha256.Sum256(content)
	return path, hex.EncodeToString(sum[:]), int64(len(content))
}

// validRequest builds a fully valid request for the given source, with the
// REAL sibling version tree (which must exist) as the forbidden root.
func validRequest(t *testing.T, stateRoot, forbidden, srcPath, digest string, length int64) Request {
	t.Helper()
	if _, err := os.Stat(forbidden); err != nil {
		t.Fatalf("fixture forbidden root %s must be real: %v", forbidden, err)
	}
	return Request{
		Oneshot:        oneshotRequest(),
		Source:         Source{Path: srcPath, ExpectedSHA256: digest, ExpectedLength: length},
		StateRoot:      stateRoot,
		ForbiddenRoots: []string{forbidden},
	}
}

func oneshotRequest() oneshot.Request {
	return oneshot.Request{InstallID: "install-1", OperationID: 42}
}

// stateRootFixture creates an isolated per-install state root plus a real
// sibling version tree that staging must stay out of. The returned versions
// path is the forbidden root callers name.
func stateRootFixture(t *testing.T) (stateRoot, versions string) {
	t.Helper()
	base := t.TempDir()
	stateRoot = filepath.Join(base, "state", "install-1")
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatalf("creating state root: %v", err)
	}
	versions = filepath.Join(base, "versions")
	if err := os.MkdirAll(filepath.Join(versions, "1.2.3"), 0o755); err != nil {
		t.Fatalf("creating version tree: %v", err)
	}
	return stateRoot, versions
}

// decoys plants marker files whose survival proves failure cleanup never
// touched pre-existing data, and returns a function asserting they all
// still exist with unchanged content.
func decoys(t *testing.T, stateRoot, versions string) func() {
	t.Helper()
	markers := map[string]string{
		filepath.Join(stateRoot, "keep-me.db"):          "state data",
		filepath.Join(versions, "1.2.3", "cercano.bin"): "old version image",
	}
	for p, c := range markers {
		if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
			t.Fatalf("writing decoy %s: %v", p, err)
		}
	}
	return func() {
		t.Helper()
		for p, c := range markers {
			got, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("decoy %s damaged: %v", p, err)
			}
			if string(got) != c {
				t.Fatalf("decoy %s modified", p)
			}
		}
	}
}

// assertNoStagingLeft asserts the state root holds no staging directories.
func assertNoStagingLeft(t *testing.T, stateRoot string) {
	t.Helper()
	entries, err := os.ReadDir(stateRoot)
	if err != nil {
		t.Fatalf("reading state root: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), stagingPrefix) {
			t.Fatalf("staging directory %q left behind after failure", e.Name())
		}
	}
}

func TestPrepareVerifiesAndStagesCopy(t *testing.T) {
	stateRoot, versions := stateRootFixture(t)
	srcDir := t.TempDir()
	srcPath, digest, length := makeSource(t, srcDir, "cercano", 2*copyChunkSize+7)
	before, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("reading source: %v", err)
	}
	beforeInfo, err := os.Stat(srcPath)
	if err != nil {
		t.Fatalf("stating source: %v", err)
	}

	req := validRequest(t, stateRoot, versions, srcPath, digest, length)
	img, err := Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if img.InstallID() != req.Oneshot.InstallID || img.OperationID() != req.Oneshot.OperationID {
		t.Fatalf("receipt identifiers %q/%d do not match request", img.InstallID(), img.OperationID())
	}
	if img.SHA256() != digest || img.Length() != length {
		t.Fatalf("receipt digest/length %s/%d do not match expected %s/%d", img.SHA256(), img.Length(), digest, length)
	}
	if img.Path() != filepath.Join(img.StagingDir(), preparedCopyName()) {
		t.Fatalf("receipt path %q is not the prepared copy inside staging dir %q", img.Path(), img.StagingDir())
	}
	if !filepath.IsAbs(img.Path()) || filepath.Base(img.StagingDir()) == "." {
		t.Fatalf("receipt paths not absolute: %q / %q", img.Path(), img.StagingDir())
	}
	if !strings.HasPrefix(filepath.Base(img.StagingDir()), stagingPrefix) {
		t.Fatalf("staging dir %q lacks the exclusive prefix", img.StagingDir())
	}
	if !withinPath(req.StateRoot, img.StagingDir()) {
		t.Fatalf("staging dir %q is outside the allowed state root", img.StagingDir())
	}

	got, err := os.ReadFile(img.Path())
	if err != nil {
		t.Fatalf("reading prepared copy: %v", err)
	}
	if !bytes.Equal(got, before) {
		t.Fatal("prepared copy content differs from source")
	}
	if runtime.GOOS != "windows" {
		dirInfo, err := os.Stat(img.StagingDir())
		if err != nil {
			t.Fatalf("stating staging dir: %v", err)
		}
		if dirInfo.Mode().Perm() != 0o700 {
			t.Fatalf("staging dir perm %v, want 0700", dirInfo.Mode().Perm())
		}
		fileInfo, err := os.Stat(img.Path())
		if err != nil {
			t.Fatalf("stating prepared copy: %v", err)
		}
		if fileInfo.Mode().Perm() != 0o700 {
			t.Fatalf("prepared copy perm %v, want 0700", fileInfo.Mode().Perm())
		}
	}

	// The source was never modified: same content and metadata.
	after, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("re-reading source: %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("source content was modified")
	}
	afterInfo, err := os.Stat(srcPath)
	if err != nil {
		t.Fatalf("re-stating source: %v", err)
	}
	if afterInfo.Size() != beforeInfo.Size() || !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatal("source metadata was modified")
	}
}

func TestPrepareRejectsDigestMismatchAndLeavesNothing(t *testing.T) {
	stateRoot, versions := stateRootFixture(t)
	assertDecoys := decoys(t, stateRoot, versions)
	srcPath, _, length := makeSource(t, t.TempDir(), "cercano", copyChunkSize+3)
	wrong := strings.Repeat("0", sha256.Size*2)

	req := validRequest(t, stateRoot, versions, srcPath, wrong, length)
	if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Prepare error %v, want ErrDigestMismatch", err)
	}
	assertNoStagingLeft(t, stateRoot)
	assertDecoys()
}

func TestPrepareValidatesSizeBeforeAnyWrite(t *testing.T) {
	stateRoot, versions := stateRootFixture(t)
	srcPath, digest, _ := makeSource(t, t.TempDir(), "cercano", 100)

	for _, claimed := range []int64{99, 101} {
		req := validRequest(t, stateRoot, versions, srcPath, digest, claimed)
		if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrInvalidSource) {
			t.Fatalf("Prepare with claimed length %d: error %v, want ErrInvalidSource", claimed, err)
		}
	}
	entries, err := os.ReadDir(stateRoot)
	if err != nil {
		t.Fatalf("reading state root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("size validation wrote %d entries before refusing", len(entries))
	}
}

func TestPrepareRejectsSymlinkSource(t *testing.T) {
	stateRoot, versions := stateRootFixture(t)
	srcPath, digest, length := makeSource(t, t.TempDir(), "cercano", 10)
	link := filepath.Join(t.TempDir(), "link-to-cercano")
	if err := os.Symlink(srcPath, link); err != nil {
		t.Fatalf("symlinking source: %v", err)
	}
	req := validRequest(t, stateRoot, versions, link, digest, length)
	if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("Prepare on symlink source: error %v, want ErrInvalidSource", err)
	}
	assertNoStagingLeft(t, stateRoot)
}

func TestPrepareRejectsNonRegularSources(t *testing.T) {
	stateRoot, versions := stateRootFixture(t)
	digest := hex.EncodeToString(bytes.Repeat([]byte{0}, sha256.Size))
	oneByte := int64(1)

	// A directory is not a copyable image.
	dirSrc := filepath.Join(t.TempDir(), "adir")
	if err := os.MkdirAll(dirSrc, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	req := validRequest(t, stateRoot, versions, dirSrc, digest, oneByte)
	if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("Prepare on directory source: error %v, want ErrInvalidSource", err)
	}

	// A missing source.
	req.Source.Path = filepath.Join(t.TempDir(), "missing")
	if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("Prepare on missing source: error %v, want ErrInvalidSource", err)
	}

	assertNoStagingLeft(t, stateRoot)
}

func TestPrepareRejectsInvalidRequests(t *testing.T) {
	stateRoot, versions := stateRootFixture(t)
	srcPath, digest, length := makeSource(t, t.TempDir(), "cercano", 10)
	base := validRequest(t, stateRoot, versions, srcPath, digest, length)

	cases := map[string]func(*Request){
		"uppercase install id":  func(r *Request) { r.Oneshot.InstallID = "Bad" },
		"empty install id":      func(r *Request) { r.Oneshot.InstallID = "" },
		"zero operation id":     func(r *Request) { r.Oneshot.OperationID = 0 },
		"negative operation id": func(r *Request) { r.Oneshot.OperationID = -3 },
		"relative source path":  func(r *Request) { r.Source.Path = "cercano" },
		"relative state root":   func(r *Request) { r.StateRoot = "state" },
		"empty state root":      func(r *Request) { r.StateRoot = "" },
		"missing state root":    func(r *Request) { r.StateRoot = filepath.Join(stateRoot, "nope") },
		"no forbidden roots":    func(r *Request) { r.ForbiddenRoots = nil },
		"relative forbidden":    func(r *Request) { r.ForbiddenRoots = []string{"versions"} },
		"uppercase digest":      func(r *Request) { r.Source.ExpectedSHA256 = strings.ToUpper(digest) },
		"short digest":          func(r *Request) { r.Source.ExpectedSHA256 = digest[:63] },
		"non-hex digest":        func(r *Request) { r.Source.ExpectedSHA256 = strings.Replace(digest, "a", "z", 1) },
		"zero expected length":  func(r *Request) { r.Source.ExpectedLength = 0 },
		"huge expected length":  func(r *Request) { r.Source.ExpectedLength = MaxImageBytes + 1 },
	}
	for name, mutate := range cases {
		req := base
		mutate(&req)
		if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: Prepare error %v, want ErrInvalidRequest", name, err)
		}
	}

	// A symlinked state root is refused by type: staging must not follow a
	// link the caller did not resolve.
	linked := filepath.Join(t.TempDir(), "linked-state")
	if err := os.Symlink(stateRoot, linked); err != nil {
		t.Fatalf("symlinking state root: %v", err)
	}
	req := base
	req.StateRoot = linked
	if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Prepare on symlinked state root: error %v, want ErrInvalidRequest", err)
	}

	assertNoStagingLeft(t, stateRoot)
}

func TestPrepareRefusesPlacementInsideForbiddenRoots(t *testing.T) {
	base := t.TempDir()
	versions := filepath.Join(base, "versions", "1.2.3")
	if err := os.MkdirAll(versions, 0o755); err != nil {
		t.Fatalf("creating version tree: %v", err)
	}
	// The source is a syntactically valid but nonexistent path: the
	// placement refusal must happen during request validation, before any
	// source I/O. It must be absolute on EVERY platform — a Unix-only
	// literal like "/x" is not absolute on Windows (no volume), so the
	// request would be refused as invalid before the placement boundary
	// was ever examined.
	absent := filepath.Join(base, "cercano")
	// A caller-provided state root that IS the version root...
	req := Request{
		Oneshot:        oneshotRequest(),
		Source:         Source{Path: absent, ExpectedSHA256: hex.EncodeToString(bytes.Repeat([]byte{1}, sha256.Size)), ExpectedLength: 1},
		StateRoot:      versions,
		ForbiddenRoots: []string{versions},
	}
	if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrUnsafePlacement) {
		t.Fatalf("Prepare with state root == forbidden root: error %v, want ErrUnsafePlacement", err)
	}
	// ...or lies inside it.
	inside := filepath.Join(versions, "state")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatalf("creating inner state root: %v", err)
	}
	req.StateRoot = inside
	if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrUnsafePlacement) {
		t.Fatalf("Prepare with state root inside forbidden root: error %v, want ErrUnsafePlacement", err)
	}
}

func TestPrepareRejectsSourceGrownDuringCopy(t *testing.T) {
	resetHooks(t)
	stateRoot, versions := stateRootFixture(t)
	assertDecoys := decoys(t, stateRoot, versions)
	srcPath, digest, length := makeSource(t, t.TempDir(), "cercano", 2*copyChunkSize)
	fh, err := os.OpenFile(srcPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("opening source for append: %v", err)
	}
	defer fh.Close() //nolint:errcheck // test fixture

	testHookChunk = func(copied int64) {
		if copied == copyChunkSize {
			if _, err := fh.Write(bytes.Repeat([]byte{9}, copyChunkSize)); err != nil {
				t.Errorf("appending to source mid-copy: %v", err)
			}
		}
	}

	req := validRequest(t, stateRoot, versions, srcPath, digest, length)
	if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("Prepare with growing source: error %v, want ErrSourceChanged", err)
	}
	assertNoStagingLeft(t, stateRoot)
	assertDecoys()
}

func TestPrepareRejectsSourceMutatedAfterCopy(t *testing.T) {
	resetHooks(t)
	stateRoot, versions := stateRootFixture(t)
	assertDecoys := decoys(t, stateRoot, versions)
	srcPath, digest, length := makeSource(t, t.TempDir(), "cercano", copyChunkSize)
	fh, err := os.OpenFile(srcPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening source for truncate: %v", err)
	}
	defer fh.Close() //nolint:errcheck // test fixture

	testHookAfterCopy = func() {
		if err := fh.Truncate(1); err != nil {
			t.Errorf("truncating source after copy: %v", err)
		}
	}

	req := validRequest(t, stateRoot, versions, srcPath, digest, length)
	if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("Prepare with post-copy mutation: error %v, want ErrSourceChanged", err)
	}
	assertNoStagingLeft(t, stateRoot)
	assertDecoys()
}

// TestPrepareRefusesSymlinkAliasedPlacementIntoForbiddenRoot: a purely
// lexical containment check is blind to symlink aliases, so a state path
// that travels through a symlink into an existing forbidden root (its leaf
// a regular directory) must be refused after canonicalizing both the state
// path and every forbidden root — and nothing may be written.
func TestPrepareRefusesSymlinkAliasedPlacementIntoForbiddenRoot(t *testing.T) {
	srcPath, digest, length := makeSource(t, t.TempDir(), "cercano", 64)

	t.Run("state path through alias into forbidden root", func(t *testing.T) {
		base := t.TempDir()
		forbidden := filepath.Join(base, "versions")
		if err := os.MkdirAll(filepath.Join(forbidden, "1.2.3"), 0o755); err != nil {
			t.Fatalf("creating forbidden root: %v", err)
		}
		alias := filepath.Join(base, "alias")
		if err := os.Symlink(forbidden, alias); err != nil {
			t.Fatalf("creating alias symlink: %v", err)
		}
		stateRoot := filepath.Join(alias, "state") // leaf is a regular dir
		if err := os.MkdirAll(stateRoot, 0o700); err != nil {
			t.Fatalf("creating aliased state root: %v", err)
		}

		req := validRequest(t, stateRoot, forbidden, srcPath, digest, length)
		if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrUnsafePlacement) {
			t.Fatalf("Prepare with aliased state root: error %v, want ErrUnsafePlacement", err)
		}
		entries, err := os.ReadDir(stateRoot)
		if err != nil {
			t.Fatalf("reading aliased state root: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("staging was written into the forbidden root via the alias: %v", entries)
		}
	})

	t.Run("forbidden root named through alias", func(t *testing.T) {
		base := t.TempDir()
		forbidden := filepath.Join(base, "versions")
		if err := os.MkdirAll(forbidden, 0o755); err != nil {
			t.Fatalf("creating forbidden root: %v", err)
		}
		alias := filepath.Join(base, "alias")
		if err := os.Symlink(forbidden, alias); err != nil {
			t.Fatalf("creating alias symlink: %v", err)
		}
		// The state root is a REAL path inside the forbidden tree, but the
		// caller names the forbidden root through its alias, so a purely
		// lexical check sees no containment.
		stateRoot := filepath.Join(forbidden, "state")
		if err := os.MkdirAll(stateRoot, 0o700); err != nil {
			t.Fatalf("creating inner state root: %v", err)
		}

		req := validRequest(t, stateRoot, alias, srcPath, digest, length)
		if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrUnsafePlacement) {
			t.Fatalf("Prepare with aliased forbidden root: error %v, want ErrUnsafePlacement", err)
		}
		entries, err := os.ReadDir(stateRoot)
		if err != nil {
			t.Fatalf("reading inner state root: %v", err)
		}
		if len(entries) != 0 {
			t.Fatalf("staging was written into the forbidden root despite the alias: %v", entries)
		}
	})
}

// TestPrepareRejectsUnresolvableForbiddenRoots: a forbidden boundary that
// cannot be resolved (missing, dangling symlink, symlink loop) is never
// guessed at — the request is refused instead.
func TestPrepareRejectsUnresolvableForbiddenRoots(t *testing.T) {
	stateRoot, versions := stateRootFixture(t)
	srcPath, digest, length := makeSource(t, t.TempDir(), "cercano", 10)
	base := validRequest(t, stateRoot, versions, srcPath, digest, length)

	missing := filepath.Join(t.TempDir(), "no-such-root")
	dangling := filepath.Join(t.TempDir(), "dangling")
	if err := os.Symlink(filepath.Join(t.TempDir(), "nowhere"), dangling); err != nil {
		t.Fatalf("creating dangling symlink: %v", err)
	}
	loop := filepath.Join(t.TempDir(), "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatalf("creating symlink loop: %v", err)
	}

	for name, forbidden := range map[string]string{
		"missing forbidden root":     missing,
		"dangling forbidden symlink": dangling,
		"looping forbidden symlink":  loop,
	} {
		req := base
		req.ForbiddenRoots = []string{forbidden}
		if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: Prepare error %v, want ErrInvalidRequest", name, err)
		}
	}
	assertNoStagingLeft(t, stateRoot)
}

func TestPrepareConcurrentSameOperationStagesDistinctDirs(t *testing.T) {
	stateRoot, versions := stateRootFixture(t)
	srcPath, digest, length := makeSource(t, t.TempDir(), "cercano", copyChunkSize)
	req := validRequest(t, stateRoot, versions, srcPath, digest, length)

	const n = 8
	imgs := make([]*PreparedImage, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			imgs[i], errs[i] = Prepare(context.Background(), req)
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("concurrent Prepare %d failed: %v", i, errs[i])
		}
		if seen[imgs[i].StagingDir()] {
			t.Fatalf("concurrent preparations collided on staging dir %q", imgs[i].StagingDir())
		}
		seen[imgs[i].StagingDir()] = true
		if imgs[i].SHA256() != digest || imgs[i].Length() != length {
			t.Fatalf("concurrent receipt %d not verified", i)
		}
	}
}

func TestPrepareCanceledMidCopyCleansUp(t *testing.T) {
	resetHooks(t)
	stateRoot, versions := stateRootFixture(t)
	assertDecoys := decoys(t, stateRoot, versions)
	srcPath, digest, length := makeSource(t, t.TempDir(), "cercano", 2*copyChunkSize)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	testHookChunk = func(copied int64) {
		if copied == copyChunkSize {
			cancel()
		}
	}

	req := validRequest(t, stateRoot, versions, srcPath, digest, length)
	if _, err := Prepare(ctx, req); !errors.Is(err, ErrCanceled) {
		t.Fatalf("Prepare under cancellation: error %v, want ErrCanceled", err)
	}
	assertNoStagingLeft(t, stateRoot)
	assertDecoys()
}

func TestPrepareNilContextRefused(t *testing.T) {
	stateRoot, versions := stateRootFixture(t)
	srcPath, digest, length := makeSource(t, t.TempDir(), "cercano", 10)
	req := validRequest(t, stateRoot, versions, srcPath, digest, length)
	if _, err := Prepare(nil, req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Prepare(nil, ...): error %v, want ErrInvalidRequest", err)
	}
	assertNoStagingLeft(t, stateRoot)
}

func TestPrepareHandlesUTF8Paths(t *testing.T) {
	base := t.TempDir()
	stateRoot := filepath.Join(base, "состояние-日本", "install-1")
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatalf("creating UTF-8 state root: %v", err)
	}
	versions := filepath.Join(base, "версии")
	if err := os.MkdirAll(filepath.Join(versions, "1.2.3"), 0o755); err != nil {
		t.Fatalf("creating UTF-8 version tree: %v", err)
	}
	srcDir := filepath.Join(base, "источник-🎉")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("creating UTF-8 source dir: %v", err)
	}
	srcPath, digest, length := makeSource(t, srcDir, "агент-🎉.bin", copyChunkSize)

	req := validRequest(t, stateRoot, versions, srcPath, digest, length)
	img, err := Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare with UTF-8 paths: %v", err)
	}
	got, err := os.ReadFile(img.Path())
	if err != nil {
		t.Fatalf("reading prepared copy: %v", err)
	}
	want, _ := os.ReadFile(srcPath)
	if !bytes.Equal(got, want) {
		t.Fatal("prepared copy content differs from UTF-8-named source")
	}
}

// TestPrepareRejectsSourcePathnameReplacedDuringCopy: an opened handle
// alone cannot notice that the source pathname was renamed away or that a
// different file was bound to it mid-copy. The pathname must be compared
// against the opened identity before and after the copy, and a bound
// replacement must never be modified.
//
// On Windows the mutation may be prevented by the OS itself: openSourceFile
// holds the source without FILE_SHARE_DELETE, so the rename fails with a
// sharing violation or access denied while the copy proceeds on the
// unchanged original. The test branches on the observed outcome instead of
// pretending the mutation succeeded: a real mutation must yield
// ErrSourceChanged; a prevented mutation must leave a positively verified
// copy of the unchanged source; anything else fails.
func TestPrepareRejectsSourcePathnameReplacedDuringCopy(t *testing.T) {
	replacementBytes := []byte("not the trusted image")

	t.Run("replaced with another file", func(t *testing.T) {
		resetHooks(t)
		stateRoot, versions := stateRootFixture(t)
		assertDecoys := decoys(t, stateRoot, versions)
		srcDir := t.TempDir()
		srcPath, digest, length := makeSource(t, srcDir, "cercano", 2*copyChunkSize)
		before, beforeInfo := snapshotSource(t, srcPath)
		replacement := filepath.Join(srcDir, "replacement")
		if err := os.WriteFile(replacement, replacementBytes, 0o755); err != nil {
			t.Fatalf("writing replacement: %v", err)
		}
		var renameErr error
		testHookChunk = func(copied int64) {
			if copied == copyChunkSize {
				renameErr = os.Rename(replacement, srcPath)
			}
		}

		req := validRequest(t, stateRoot, versions, srcPath, digest, length)
		img, err := Prepare(context.Background(), req)
		switch {
		case renameErr == nil:
			// The pathname really was re-bound mid-copy.
			if !errors.Is(err, ErrSourceChanged) {
				t.Fatalf("Prepare with replaced pathname: error %v, want ErrSourceChanged", err)
			}
			// The replacement file bound to the pathname was not modified.
			got, rerr := os.ReadFile(srcPath)
			if rerr != nil {
				t.Fatalf("re-reading replaced pathname: %v", rerr)
			}
			if !bytes.Equal(got, replacementBytes) {
				t.Fatal("the replacement bound to the source pathname was modified")
			}
			assertNoStagingLeft(t, stateRoot)
		case mutationPreventedByOS(renameErr):
			// The OS refused the rebind before Prepare could observe it.
			if err != nil {
				t.Fatalf("Prepare after prevented pathname replacement: %v", err)
			}
			assertSourceUnchanged(t, srcPath, before, beforeInfo)
			assertCopyVerified(t, img, digest, length, before)
			if got, rerr := os.ReadFile(replacement); rerr != nil || !bytes.Equal(got, replacementBytes) {
				t.Fatalf("replacement file must be untouched where it is; read error %v", rerr)
			}
		default:
			t.Fatalf("binding replacement to source pathname: unexpected error %v", renameErr)
		}
		assertDecoys()
	})

	t.Run("renamed away", func(t *testing.T) {
		resetHooks(t)
		stateRoot, versions := stateRootFixture(t)
		assertDecoys := decoys(t, stateRoot, versions)
		srcDir := t.TempDir()
		srcPath, digest, length := makeSource(t, srcDir, "cercano", 2*copyChunkSize)
		before, beforeInfo := snapshotSource(t, srcPath)
		moved := filepath.Join(srcDir, "cercano.gone")
		var renameErr error
		testHookChunk = func(copied int64) {
			if copied == copyChunkSize {
				renameErr = os.Rename(srcPath, moved)
			}
		}

		req := validRequest(t, stateRoot, versions, srcPath, digest, length)
		img, err := Prepare(context.Background(), req)
		switch {
		case renameErr == nil:
			// The pathname really was renamed away mid-copy.
			if !errors.Is(err, ErrSourceChanged) {
				t.Fatalf("Prepare with renamed-away pathname: error %v, want ErrSourceChanged", err)
			}
			if _, serr := os.Stat(srcPath); !os.IsNotExist(serr) {
				t.Fatalf("original pathname should be gone, stat error: %v", serr)
			}
			assertNoStagingLeft(t, stateRoot)
		case mutationPreventedByOS(renameErr):
			// The OS kept the pathname bound to the opened original, so the
			// copy of the unchanged source must have completed and verified.
			if err != nil {
				t.Fatalf("Prepare after prevented rename-away: %v", err)
			}
			assertSourceUnchanged(t, srcPath, before, beforeInfo)
			assertCopyVerified(t, img, digest, length, before)
			if _, serr := os.Stat(moved); !os.IsNotExist(serr) {
				t.Fatalf("moved pathname should not exist when the rename was prevented, stat error: %v", serr)
			}
		default:
			t.Fatalf("renaming source pathname away: unexpected error %v", renameErr)
		}
		assertDecoys()
	})
}

// snapshotSource captures the source's content and metadata for later
// positive proof that an adversarial mutation was prevented.
func snapshotSource(t *testing.T, srcPath string) ([]byte, os.FileInfo) {
	t.Helper()
	content, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("reading source: %v", err)
	}
	info, err := os.Stat(srcPath)
	if err != nil {
		t.Fatalf("stating source: %v", err)
	}
	return content, info
}

// assertSourceUnchanged proves the source pathname is still bound to the
// original file: identical content, size and modification time.
func assertSourceUnchanged(t *testing.T, srcPath string, before []byte, beforeInfo os.FileInfo) {
	t.Helper()
	got, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("re-reading source after prevented mutation: %v", err)
	}
	if !bytes.Equal(got, before) {
		t.Fatalf("source content changed despite prevented mutation: read %d bytes, original had %d", len(got), len(before))
	}
	info, err := os.Stat(srcPath)
	if err != nil {
		t.Fatalf("re-stating source after prevented mutation: %v", err)
	}
	if info.Size() != beforeInfo.Size() || !info.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("source metadata changed despite prevented mutation: size %d mtime %v, want size %d mtime %v",
			info.Size(), info.ModTime(), beforeInfo.Size(), beforeInfo.ModTime())
	}
}

// assertCopyVerified proves the prepared copy of the unchanged source is
// intact: the receipt matches the expected digest and length, and the copy
// on disk matches the source byte for byte.
func assertCopyVerified(t *testing.T, img *PreparedImage, digest string, length int64, srcContent []byte) {
	t.Helper()
	if img == nil {
		t.Fatal("no prepared image returned after prevented mutation")
	}
	if img.SHA256() != digest || img.Length() != length {
		t.Fatalf("receipt digest/length %s/%d do not match expected %s/%d", img.SHA256(), img.Length(), digest, length)
	}
	got, err := os.ReadFile(img.Path())
	if err != nil {
		t.Fatalf("reading prepared copy after prevented mutation: %v", err)
	}
	if !bytes.Equal(got, srcContent) {
		t.Fatal("prepared copy content differs from the unchanged source")
	}
}

// findStagingDir returns the staging directory the running Prepare created
// under stateRoot; used by failure-injection hooks.
func findStagingDir(t *testing.T, stateRoot string) string {
	t.Helper()
	entries, err := os.ReadDir(stateRoot)
	if err != nil {
		t.Fatalf("reading state root: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), stagingPrefix) && e.IsDir() {
			return filepath.Join(stateRoot, e.Name())
		}
	}
	t.Fatal("no staging directory found under state root")
	return ""
}

// TestFailureCleanupPreservesUnknownInjectedFile: failure cleanup must not
// be a recursive delete. An unknown file injected into the staging
// directory must survive; only this call's own created image may be
// removed, the kept staging directory must be reported, and the caller's
// state root is never touched.
func TestFailureCleanupPreservesUnknownInjectedFile(t *testing.T) {
	resetHooks(t)
	stateRoot, versions := stateRootFixture(t)
	assertDecoys := decoys(t, stateRoot, versions)
	srcPath, _, length := makeSource(t, t.TempDir(), "cercano", 2*copyChunkSize)
	wrong := strings.Repeat("0", sha256.Size*2)

	var injected string
	testHookChunk = func(copied int64) {
		if copied == copyChunkSize {
			staging := findStagingDir(t, stateRoot)
			injected = filepath.Join(staging, "injected")
			if err := os.WriteFile(injected, []byte("not ours"), 0o600); err != nil {
				t.Errorf("injecting unknown file: %v", err)
			}
		}
	}

	req := validRequest(t, stateRoot, versions, srcPath, wrong, length)
	_, err := Prepare(context.Background(), req)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Prepare error %v, want ErrDigestMismatch", err)
	}
	// The incomplete cleanup must be reported alongside the failure.
	if !strings.Contains(err.Error(), "cleanup") {
		t.Fatalf("error %v does not report incomplete cleanup", err)
	}
	// The injected unknown file survives untouched.
	got, rerr := os.ReadFile(injected)
	if rerr != nil {
		t.Fatalf("injected unknown file was deleted: %v", rerr)
	}
	if string(got) != "not ours" {
		t.Fatalf("injected unknown file was modified: %q", got)
	}
	// Our own created image was removed; only the unknown remains.
	entries, rerr := os.ReadDir(filepath.Dir(injected))
	if rerr != nil {
		t.Fatalf("reading kept staging dir: %v", rerr)
	}
	if len(entries) != 1 || entries[0].Name() != "injected" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("kept staging dir holds %v, want only the injected unknown", names)
	}
	assertDecoys()
}

// TestFailureCleanupKeepsReplacedStagingDir: if the staging directory path
// no longer names the directory this call created, cleanup can prove
// nothing and keeps everything rather than deleting an unrelated
// replacement.
func TestFailureCleanupKeepsReplacedStagingDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("renaming a directory that contains an open file is not permitted on Windows")
	}
	resetHooks(t)
	stateRoot, versions := stateRootFixture(t)
	srcPath, _, length := makeSource(t, t.TempDir(), "cercano", 2*copyChunkSize)
	wrong := strings.Repeat("0", sha256.Size*2)

	var staging string
	testHookChunk = func(copied int64) {
		if copied == copyChunkSize && staging == "" {
			staging = findStagingDir(t, stateRoot)
			if err := os.Rename(staging, staging+"-moved"); err != nil {
				t.Errorf("moving our staging dir: %v", err)
			}
			// An unrelated directory takes over the staging path.
			if err := os.MkdirAll(filepath.Join(staging, "nested"), 0o755); err != nil {
				t.Errorf("creating replacement dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(staging, "unrelated"), []byte("someone else's"), 0o600); err != nil {
				t.Errorf("planting unrelated file: %v", err)
			}
		}
	}

	req := validRequest(t, stateRoot, versions, srcPath, wrong, length)
	_, err := Prepare(context.Background(), req)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Prepare error %v, want ErrDigestMismatch", err)
	}
	if !strings.Contains(err.Error(), "cleanup") {
		t.Fatalf("error %v does not report incomplete cleanup", err)
	}
	// The replacement directory and its contents survive untouched.
	got, rerr := os.ReadFile(filepath.Join(staging, "unrelated"))
	if rerr != nil {
		t.Fatalf("replacement staging dir was deleted: %v", rerr)
	}
	if string(got) != "someone else's" {
		t.Fatal("unrelated file inside the replacement staging dir was modified")
	}
	if _, rerr = os.Stat(filepath.Join(staging, "nested")); rerr != nil {
		t.Fatalf("nested dir inside the replacement staging dir was removed: %v", rerr)
	}
}

// TestVerifyCopyReadIsBounded: re-verification must never read beyond the
// expected length plus one byte, so a copy that grows while it is being
// re-read cannot cause unbounded reading.
func TestVerifyCopyReadIsBounded(t *testing.T) {
	resetHooks(t)
	stateRoot, versions := stateRootFixture(t)
	srcPath, digest, length := makeSource(t, t.TempDir(), "cercano", copyChunkSize)

	var maxTotal int64
	testHookVerifyRead = func(path string, total int64) {
		if total > maxTotal {
			maxTotal = total
		}
		if total == length {
			// Grow the copy AFTER the pre-read size check, during the
			// verify read loop.
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				t.Errorf("opening copy for append: %v", err)
				return
			}
			defer f.Close() //nolint:errcheck // test fixture
			if _, err := f.Write([]byte("appended after the verify stat")); err != nil {
				t.Errorf("appending to copy mid-verify: %v", err)
			}
		}
	}

	req := validRequest(t, stateRoot, versions, srcPath, digest, length)
	if _, err := Prepare(context.Background(), req); !errors.Is(err, ErrCopyVerification) {
		t.Fatalf("Prepare with growing copy: error %v, want ErrCopyVerification", err)
	}
	if maxTotal > length+1 {
		t.Fatalf("verify read %d bytes, more than the expected length %d plus one", maxTotal, length)
	}
	assertNoStagingLeft(t, stateRoot)
}

// TestPreparedCopyNameUsableWithoutLaunch checks — without launching
// anything — that the prepared copy's name is one the platform's process
// creation can actually use: Windows CreateProcess requires an executable
// extension.
func TestPreparedCopyNameUsableWithoutLaunch(t *testing.T) {
	stateRoot, versions := stateRootFixture(t)
	srcPath, digest, length := makeSource(t, t.TempDir(), "cercano.bin", 64)

	req := validRequest(t, stateRoot, versions, srcPath, digest, length)
	img, err := Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if filepath.Base(img.Path()) != preparedCopyName() {
		t.Fatalf("prepared copy named %q, want %q", filepath.Base(img.Path()), preparedCopyName())
	}
	if runtime.GOOS == "windows" && filepath.Ext(img.Path()) != ".exe" {
		t.Fatalf("prepared copy %q cannot be used by CreateProcess without an .exe extension", img.Path())
	}
	if _, err := os.Stat(img.Path()); err != nil {
		t.Fatalf("prepared copy missing: %v", err)
	}
}

func TestWithinPathPlacementSemantics(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Case-insensitive containment is exercised by the same logic.
		if !withinPath(`C:\Versions`, `c:\versions\1.0`) {
			t.Fatal("windows containment must be case-insensitive")
		}
	}
	if !withinPath("/a", "/a") {
		t.Fatal("a root must contain itself")
	}
	if !withinPath("/a", "/a/b/c") {
		t.Fatal("containment must be transitive")
	}
	if withinPath("/a", "/ab") {
		t.Fatal("sibling prefixes must not count as containment")
	}
	if withinPath("/a/b", "/a") {
		t.Fatal("parent must not be considered inside child")
	}
	if withinPath("/a/b", "/a/c") {
		t.Fatal("disjoint trees must not be considered inside")
	}
}
