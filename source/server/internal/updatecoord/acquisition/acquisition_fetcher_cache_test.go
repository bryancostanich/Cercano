package acquisition

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// countingTransport records round trips so tests can prove a refusal
// happened before any HTTP request was made (or that exactly the bounded
// number of attempts was made).
type countingTransport struct {
	calls   int
	respond func(r *http.Request) (*http.Response, error)
}

func (t *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls++
	return t.respond(r)
}

func httpResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// TestBoundedFetcherRefusesNonFiniteLengthBoundsBeforeFetch proves that a
// per-fetch length bound coming from signed metadata is refused before any
// HTTP request is issued or body byte read: negative values and MaxInt64
// (whose +1 read-limit increment overflows int64 to a negative number that
// disables the LimitReader bound) are rejected outright, as is anything
// above the global finite ceiling.
func TestBoundedFetcherRefusesNonFiniteLengthBoundsBeforeFetch(t *testing.T) {
	for _, maxLength := range []int64{
		-1,
		math.MinInt64,
		math.MaxInt64,
		fetchBytesCeiling + 1,
	} {
		transport := &countingTransport{respond: func(*http.Request) (*http.Response, error) {
			return httpResponse(http.StatusOK, "must not be fetched"), nil
		}}
		fetcher := &boundedFetcher{
			ctx:    context.Background(),
			client: &http.Client{Transport: transport},
		}
		data, err := fetcher.DownloadFile("https://example.invalid/file.bin", maxLength, 0)
		if err == nil {
			t.Fatalf("maxLength %d: fetcher accepted a non-finite length bound (data: %q)", maxLength, data)
		}
		var bound *errFetchLengthBound
		if !errors.As(err, &bound) {
			t.Fatalf("maxLength %d: refusal %v is not a size-bound refusal", maxLength, err)
		}
		if transport.calls != 0 {
			t.Fatalf("maxLength %d: %d HTTP request(s) were made before the size refusal; refusal must precede any fetch",
				maxLength, transport.calls)
		}
	}
}

// TestBoundedFetcherFiniteCeilingAdmitsValidBounds proves the ceiling does
// not reject legitimate bounds: a small bound fetches normally, and the
// ceiling value itself passes the pre-read check (its +1 read-limit
// increment stays positive, so the LimitReader bound stays effective) and
// reaches the HTTP layer. No huge-allocation fetch is performed.
func TestBoundedFetcherFiniteCeilingAdmitsValidBounds(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	fetcher := &boundedFetcher{ctx: context.Background(), client: insecureTestClient()}
	// A small in-bounds fetch must succeed unchanged.
	data, err := fetcher.DownloadFile(fx.server.URL+"/targets/"+regressTarget, 64, 0)
	if err != nil {
		t.Fatalf("in-bounds fetch failed: %v", err)
	}
	if want := fx.targets[regressTarget]; string(data) != string(want) {
		t.Fatalf("fetched bytes = %q, want %q", data, want)
	}

	// The ceiling value itself passes the pre-read length check: it is
	// refused only by the server's 404, never by the bound. (A fetch at the
	// ceiling would also be refused by ErrDownloadLengthMismatch if a body
	// exceeded it — the point here is that the ceiling is a usable finite
	// bound, not that huge downloads are attempted.)
	transport := &countingTransport{respond: func(*http.Request) (*http.Response, error) {
		return httpResponse(http.StatusNotFound, ""), nil
	}}
	atCeiling := &boundedFetcher{ctx: context.Background(), client: &http.Client{Transport: transport}}
	_, err = atCeiling.DownloadFile("https://example.invalid/file.bin", fetchBytesCeiling, 0)
	var httpStatus *metadata.ErrDownloadHTTP
	if !errors.As(err, &httpStatus) || httpStatus.StatusCode != http.StatusNotFound {
		t.Fatalf("fetch at the finite ceiling failed with %v, want an HTTP 404 status error proving the bound passed pre-read validation", err)
	}
	if transport.calls != 1 {
		t.Fatalf("fetch at the ceiling made %d HTTP request(s), want exactly 1", transport.calls)
	}
}

// TestBoundedFetcherRetriesTransientTransportFailureOnly pins the bounded
// transient classification: transport-level errors are retried up to the
// bounded budget; a deterministic HTTP status response is never retried.
func TestBoundedFetcherRetriesTransientTransportFailureOnly(t *testing.T) {
	// A transient transport error that clears on the final attempt succeeds,
	// and exactly fetchRetryAttempts round trips are made.
	calls := 0
	transient := &countingTransport{respond: func(*http.Request) (*http.Response, error) {
		calls++
		if calls < fetchRetryAttempts {
			return nil, errors.New("transient connection reset")
		}
		return httpResponse(http.StatusOK, "recovered"), nil
	}}
	data, err := (&boundedFetcher{ctx: context.Background(), client: &http.Client{Transport: transient}}).
		DownloadFile("https://example.invalid/file.bin", 64, 0)
	if err != nil || string(data) != "recovered" {
		t.Fatalf("transient failure recovery failed: data=%q err=%v", data, err)
	}
	if calls != fetchRetryAttempts {
		t.Fatalf("transient fetch made %d attempts, want %d", calls, fetchRetryAttempts)
	}

	// An HTTP 404 is the server's deterministic answer — and the very signal
	// go-tuf's root-version walk uses as its expected probe terminator — so
	// it must not consume the retry budget.
	deterministic := &countingTransport{respond: func(*http.Request) (*http.Response, error) {
		return httpResponse(http.StatusNotFound, ""), nil
	}}
	_, err = (&boundedFetcher{ctx: context.Background(), client: &http.Client{Transport: deterministic}}).
		DownloadFile("https://example.invalid/file.bin", 64, 0)
	var httpStatus *metadata.ErrDownloadHTTP
	if !errors.As(err, &httpStatus) {
		t.Fatalf("404 fetch returned %v, want an HTTP status error", err)
	}
	if deterministic.calls != 1 {
		t.Fatalf("404 was retried: %d attempts, want exactly 1", deterministic.calls)
	}
}

// TestAcquireCacheLeafSymlinkRefusedExternalFileUnchanged plants a symlink
// leaf at exactly the name the library would use for the cached target and
// proves the package refuses before the library's os.WriteFile can write
// through it: the external file is unchanged, the symlink itself is left in
// place (no delete/reset/rewrite), and no fetch happens at all.
func TestAcquireCacheLeafSymlinkRefusedExternalFileUnchanged(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	cacheDir, outputDir := t.TempDir(), t.TempDir()
	targetsDir := filepath.Join(cacheDir, cacheTargetsSubdir)
	if err := os.MkdirAll(targetsDir, 0o700); err != nil {
		t.Fatalf("seeding targets cache dir: %v", err)
	}
	external := filepath.Join(t.TempDir(), "external.txt")
	const sentinel = "external user data, must survive acquisition"
	if err := os.WriteFile(external, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("seeding external file: %v", err)
	}
	leaf := filepath.Join(targetsDir, url.PathEscape(regressTarget))
	if err := os.Symlink(external, leaf); err != nil {
		t.Skipf("symlinks are unavailable on this platform: %v", err)
	}

	_, err := acquire(context.Background(), fixtureOptions(fx, cacheDir, outputDir), regressTarget, insecureTestClient())
	if err == nil {
		t.Fatal("acquisition accepted a symlinked cache leaf in the library-owned targets directory")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("refusal %v does not identify the unsafe cache leaf", err)
	}
	// The external file must be byte-for-byte unchanged.
	got, rerr := os.ReadFile(external)
	if rerr != nil || string(got) != sentinel {
		t.Fatalf("external file was modified through the symlinkmed cache leaf: read err=%v content=%q", rerr, got)
	}
	// The symlink itself must be untouched: no delete, reset, or rewrite.
	fi, rerr := os.Lstat(leaf)
	if rerr != nil {
		t.Fatalf("cache leaf was removed or replaced: %v", rerr)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("cache leaf %q is no longer a symlink; the cache was rewritten", leaf)
	}
	// Refusal happens before the updater runs, so nothing is fetched and
	// nothing is published.
	if fx.targetRequests != 0 {
		t.Fatalf("target bytes were fetched %d time(s) before the cache refusal", fx.targetRequests)
	}
	if names := outputEntries(t, outputDir); len(names) != 0 {
		t.Fatalf("refused acquisition left files in the output directory: %v", names)
	}
}

// TestAcquireCacheMetadataLeafSymlinkRefused plants a symlink leaf where the
// library reads cached metadata with os.ReadFile — a read through it would
// return attacker-chosen bytes (and a FIFO would block unboundedly) — and
// proves the refusal precedes any such read.
func TestAcquireCacheMetadataLeafSymlinkRefused(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	cacheDir, outputDir := t.TempDir(), t.TempDir()
	metadataDir := filepath.Join(cacheDir, cacheMetadataSubdir)
	if err := os.MkdirAll(metadataDir, 0o700); err != nil {
		t.Fatalf("seeding metadata cache dir: %v", err)
	}
	external := filepath.Join(t.TempDir(), "fake-timestamp.json")
	if err := os.WriteFile(external, []byte("{\"signed\":{\"version\":2}}"), 0o600); err != nil {
		t.Fatalf("seeding external file: %v", err)
	}
	leaf := filepath.Join(metadataDir, "timestamp.json")
	if err := os.Symlink(external, leaf); err != nil {
		t.Skipf("symlinks are unavailable on this platform: %v", err)
	}

	_, err := acquire(context.Background(), fixtureOptions(fx, cacheDir, outputDir), regressTarget, insecureTestClient())
	if err == nil {
		t.Fatal("acquisition accepted a symlinked metadata cache leaf")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("refusal %v does not identify the unsafe cache leaf", err)
	}
	got, rerr := os.ReadFile(external)
	if rerr != nil || string(got) != "{\"signed\":{\"version\":2}}" {
		t.Fatalf("external file was modified through the symlinkmed metadata leaf: read err=%v content=%q", rerr, got)
	}
	if fi, rerr := os.Lstat(leaf); rerr != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("metadata cache leaf was removed or replaced: lstat err=%v mode=%v", rerr, fi.Mode())
	}
	if names := outputEntries(t, outputDir); len(names) != 0 {
		t.Fatalf("refused acquisition left files in the output directory: %v", names)
	}
}

// TestAcquireCacheLeafDirectoryRefused covers a non-regular leaf that needs
// no symlink privilege (a nested directory), including on Windows.
func TestAcquireCacheLeafDirectoryRefused(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	cacheDir, outputDir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(cacheDir, cacheTargetsSubdir, "nested"), 0o700); err != nil {
		t.Fatalf("seeding nested directory in targets cache: %v", err)
	}

	_, err := acquire(context.Background(), fixtureOptions(fx, cacheDir, outputDir), regressTarget, insecureTestClient())
	if err == nil {
		t.Fatal("acquisition accepted a directory as a cache leaf in the library-owned targets directory")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("refusal %v does not identify the unsafe cache leaf", err)
	}
	if fi, rerr := os.Lstat(filepath.Join(cacheDir, cacheTargetsSubdir, "nested")); rerr != nil || !fi.IsDir() {
		t.Fatalf("nested directory was removed or replaced: lstat err=%v mode=%v", rerr, fi.Mode())
	}
	if fx.targetRequests != 0 {
		t.Fatalf("target bytes were fetched %d time(s) before the cache refusal", fx.targetRequests)
	}
}

// TestAcquireCacheRegularLeavesAccepted proves stale but regular library-
// owned leaves are accepted (the library legitimately overwrites them) —
// the new leaf checks must not refuse the package's own cache format.
func TestAcquireCacheRegularLeavesAccepted(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	cacheDir, outputDir := t.TempDir(), t.TempDir()
	metadataDir := filepath.Join(cacheDir, cacheMetadataSubdir)
	targetsDir := filepath.Join(cacheDir, cacheTargetsSubdir)
	if err := os.MkdirAll(metadataDir, 0o700); err != nil {
		t.Fatalf("seeding metadata cache dir: %v", err)
	}
	if err := os.MkdirAll(targetsDir, 0o700); err != nil {
		t.Fatalf("seeding targets cache dir: %v", err)
	}
	// Stale regular files exactly where the library keeps its state.
	if err := os.WriteFile(filepath.Join(metadataDir, "root.json"), []byte("stale root"), 0o600); err != nil {
		t.Fatalf("seeding stale root.json: %v", err)
	}
	targetLeaf := filepath.Join(targetsDir, url.PathEscape(regressTarget))
	if err := os.WriteFile(targetLeaf, []byte("stale target bytes"), 0o600); err != nil {
		t.Fatalf("seeding stale target leaf: %v", err)
	}

	receipt, err := acquire(context.Background(), fixtureOptions(fx, cacheDir, outputDir), regressTarget, insecureTestClient())
	if err != nil {
		t.Fatalf("acquisition refused regular-file cache leaves left by earlier library runs: %v", err)
	}
	// The cached target leaf must now hold the verified bytes under the
	// same flat name (proving the library's own cache layout survived).
	cached, err := os.ReadFile(targetLeaf)
	if err != nil {
		t.Fatalf("cached target leaf unreadable after acquisition: %v", err)
	}
	if want := fx.targets[regressTarget]; string(cached) != string(want) {
		t.Fatalf("cached target leaf = %q, want verified %q", cached, want)
	}
	published, err := os.ReadFile(receipt.OutputPath)
	if err != nil || string(published) != string(fx.targets[regressTarget]) {
		t.Fatalf("published bytes unreadable or wrong: err=%v", err)
	}
}

// TestValidateCacheLayoutEntryCap proves the leaf scan traversal is bounded:
// a cache holding more than the validation cap is refused rather than
// walking an unbounded, caller-populated directory.
func TestValidateCacheLayoutEntryCap(t *testing.T) {
	cacheDir := t.TempDir()
	metadataDir := filepath.Join(cacheDir, cacheMetadataSubdir)
	if err := os.MkdirAll(metadataDir, 0o700); err != nil {
		t.Fatalf("seeding metadata cache dir: %v", err)
	}
	for i := 0; i <= maxCacheSubtreeEntries; i++ {
		if err := os.WriteFile(filepath.Join(metadataDir, fmt.Sprintf("f%04x%02x", i, i)), nil, 0o600); err != nil {
			t.Fatalf("seeding entry %d: %v", i, err)
		}
	}
	err := validateCacheLayout(cacheDir)
	if err == nil {
		t.Fatal("validateCacheLayout validated a cache above the bounded entry cap")
	}
	if !strings.Contains(err.Error(), "cannot be validated safely") {
		t.Fatalf("refusal %v does not explain the bounded traversal cap", err)
	}
}
