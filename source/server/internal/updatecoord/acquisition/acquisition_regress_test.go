package acquisition

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// insecureTestClient returns the owned TLS-fixture client used by the other
// tests: the local httptest TLS server uses a self-signed certificate.
func insecureTestClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

func fixtureOptions(fx *fixture, cacheDir, outputDir string) Options {
	return Options{
		TrustedRoot:     fx.root,
		MetadataBaseURL: fx.server.URL + "/metadata/",
		TargetBaseURL:   fx.server.URL + "/targets/",
		CacheDir:        cacheDir,
		OutputDir:       outputDir,
	}
}

// outputEntries lists every entry in dir (used to prove nothing is left
// behind on failure and nothing unowned was touched).
func outputEntries(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading output dir: %v", err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

const regressTarget = "cercano/app/1.0.0.zip"

func TestAcquireOutputCollisionDoesNotOverwriteUserData(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	cacheDir := t.TempDir()
	outputDir := t.TempDir()
	// A user file that collides with the target's basename. The current
	// implementation publishes by renaming onto this exact name, destroying
	// the user's data.
	preexisting := filepath.Join(outputDir, "1.0.0.zip")
	const sentinel = "preexisting user data, must survive acquisition"
	if err := os.WriteFile(preexisting, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("seeding user file: %v", err)
	}

	receipt, err := acquire(context.Background(), fixtureOptions(fx, cacheDir, outputDir), regressTarget, insecureTestClient())
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if receipt.OutputPath == preexisting {
		t.Fatalf("acquisition published onto the preexisting user filename %q", preexisting)
	}
	got, err := os.ReadFile(preexisting)
	if err != nil {
		t.Fatalf("preexisting user file removed: %v", err)
	}
	if string(got) != sentinel {
		t.Fatalf("preexisting user file overwritten; content = %q", got)
	}
	published, err := os.ReadFile(receipt.OutputPath)
	if err != nil {
		t.Fatalf("published file unreadable: %v", err)
	}
	if !strings.HasPrefix(receipt.OutputPath, filepath.Join(outputDir, ".cercano-acquire-")) {
		t.Fatalf("published path %q is not an owned name inside the output directory", receipt.OutputPath)
	}
	if want := fx.targets[regressTarget]; string(published) != string(want) {
		t.Fatalf("published bytes = %q, want %q", published, want)
	}
}

func TestAcquireOversizeRefusedBeforeFetch(t *testing.T) {
	bigLength := int64(1024 * 1024)
	fx := newFixtureSpecs(t, time.Now().AddDate(1, 0, 0), nil,
		targetSpec{path: regressTarget, data: []byte("tiny bytes"), length: &bigLength})
	defer fx.server.Close()

	cacheDir := t.TempDir()
	outputDir := t.TempDir()
	opts := fixtureOptions(fx, cacheDir, outputDir)
	opts.MaxTargetBytes = 4096

	_, err := acquire(context.Background(), opts, regressTarget, insecureTestClient())
	if err == nil {
		t.Fatal("acquisition accepted a signed length above the configured bound")
	}
	if fx.targetRequests != 0 {
		t.Fatalf("target bytes were fetched %d time(s) before the size refusal; refusal must precede any fetch", fx.targetRequests)
	}
	if names := outputEntries(t, outputDir); len(names) != 0 {
		t.Fatalf("refused acquisition left files in the output directory: %v", names)
	}
}

func TestAcquireMaxTargetBytesCappedAtHardLimit(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	opts := fixtureOptions(fx, t.TempDir(), t.TempDir())
	opts.MaxTargetBytes = hardMaxTargetBytes + 1
	if _, err := acquire(context.Background(), opts, regressTarget, insecureTestClient()); err == nil {
		t.Fatal("options may not raise the target size bound above the hard in-memory cap")
	}
}

func TestAcquireNoSHA256RefusedBeforeFetch(t *testing.T) {
	fx := newFixtureSpecs(t, time.Now().AddDate(1, 0, 0), nil,
		targetSpec{path: regressTarget, data: []byte("payload signed with sha512 only"), hashes: []string{"sha512"}})
	defer fx.server.Close()

	cacheDir := t.TempDir()
	outputDir := t.TempDir()

	_, err := acquire(context.Background(), fixtureOptions(fx, cacheDir, outputDir), regressTarget, insecureTestClient())
	if err == nil {
		t.Fatal("acquisition accepted a target without a signed sha256 digest")
	}
	if fx.targetRequests != 0 {
		t.Fatalf("target bytes were fetched %d time(s) before the digest refusal; refusal must precede any fetch", fx.targetRequests)
	}
	if names := outputEntries(t, outputDir); len(names) != 0 {
		t.Fatalf("refused acquisition left files in the output directory: %v", names)
	}
}

func TestAcquireRedirectToHTTPRefused(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	// A plain-HTTP mirror serving the same payload: if the redirect were
	// followed, acquisition would silently succeed over cleartext.
	var plainPayload = fx.targets[regressTarget]
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(plainPayload)
	}))
	defer plain.Close()

	fx.onTargetRequest = func(w http.ResponseWriter, r *http.Request, relPath string) bool {
		http.Redirect(w, r, plain.URL+"/mirror/"+relPath, http.StatusFound)
		return true
	}

	cacheDir := t.TempDir()
	outputDir := t.TempDir()

	_, err := acquire(context.Background(), fixtureOptions(fx, cacheDir, outputDir), regressTarget, insecureTestClient())
	if err == nil {
		t.Fatal("acquisition followed an https-to-http redirect downgrade")
	}
	if fx.targetRequests != 1 {
		t.Fatalf("expected exactly the initial target request, got %d", fx.targetRequests)
	}
	if names := outputEntries(t, outputDir); len(names) != 0 {
		t.Fatalf("refused acquisition left files in the output directory: %v", names)
	}
}

func TestAcquireRedirectWithCredentialsRefused(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	fx.onTargetRequest = func(w http.ResponseWriter, r *http.Request, relPath string) bool {
		u := strings.TrimPrefix(fx.server.URL, "https://")
		http.Redirect(w, r, "https://user:pass@"+u+"/targets/"+relPath, http.StatusFound)
		return true
	}

	cacheDir := t.TempDir()
	outputDir := t.TempDir()

	_, err := acquire(context.Background(), fixtureOptions(fx, cacheDir, outputDir), regressTarget, insecureTestClient())
	if err == nil {
		t.Fatal("acquisition followed a credential-bearing redirect destination")
	}
	if fx.targetRequests != 1 {
		t.Fatalf("expected exactly the initial target request, got %d", fx.targetRequests)
	}
}

func TestAcquireDoesNotMutateProvidedClient(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	cacheDir := t.TempDir()
	outputDir := t.TempDir()

	client := insecureTestClient()
	before := client.Transport
	if _, err := acquire(context.Background(), fixtureOptions(fx, cacheDir, outputDir), regressTarget, client); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if client.Transport != before {
		t.Fatal("acquisition mutated the caller-provided http.Client transport")
	}
}

func TestAcquireCancelDuringDownloadPublishesNothing(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	cacheDir := t.TempDir()
	outputDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel while the verified bytes are already in flight: the transport
	// cancels the context just before returning the successful target
	// response (the fixture serves consistent-snapshot hash-prefixed target
	// URLs, so match on path prefix), so the only remaining publish step
	// must observe the cancellation.
	client := &http.Client{Transport: &cancelingTransport{base: insecureTestClient().Transport, cancel: cancel}}

	_, err := acquire(ctx, fixtureOptions(fx, cacheDir, outputDir), regressTarget, client)
	if err == nil {
		t.Fatal("acquisition succeeded despite a canceled context")
	}
	if names := outputEntries(t, outputDir); len(names) != 0 {
		t.Fatalf("canceled acquisition left files in the output directory: %v", names)
	}
}

func TestAcquireCloseFailureCleansUpOwnedFileOnly(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	cacheDir := t.TempDir()
	outputDir := t.TempDir()
	preexisting := filepath.Join(outputDir, "keep-me.txt")
	if err := os.WriteFile(preexisting, []byte("user data"), 0o600); err != nil {
		t.Fatalf("seeding user file: %v", err)
	}

	origClose := osClose
	osClose = func(f *os.File) error { return errors.New("forced close failure") }
	defer func() { osClose = origClose }()

	_, err := acquire(context.Background(), fixtureOptions(fx, cacheDir, outputDir), regressTarget, insecureTestClient())
	osClose = origClose
	if err == nil {
		t.Fatal("acquisition succeeded despite a failing close")
	}
	ents := outputEntries(t, outputDir)
	if len(ents) != 1 || ents[0] != "keep-me.txt" {
		t.Fatalf("failed close left owned files behind (or removed user files): %v", ents)
	}
}

func TestAcquireCacheChildSymlinkRefusedNoChange(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	cacheDir := t.TempDir()
	outside := t.TempDir() // must remain untouched
	if err := os.Symlink(outside, filepath.Join(cacheDir, cacheMetadataSubdir)); err != nil {
		t.Fatalf("planting cache child symlink: %v", err)
	}
	outputDir := t.TempDir()

	if _, err := acquire(context.Background(), fixtureOptions(fx, cacheDir, outputDir), regressTarget, insecureTestClient()); err == nil {
		t.Fatal("acquisition accepted a symlinked cache metadata child directory")
	}
	ents := outputEntries(t, outside)
	if len(ents) != 0 {
		t.Fatalf("library wrote through the symlinked cache child: %v", ents)
	}
	if names := outputEntries(t, outputDir); len(names) != 0 {
		t.Fatalf("refused acquisition left files in the output directory: %v", names)
	}
}

func TestAcquireRejectsAliasedAndRelativeDirectories(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	realOutput := t.TempDir()
	aliased := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(realOutput, aliased); err != nil {
		t.Fatalf("planting alias: %v", err)
	}

	cases := []struct {
		name string
		mut  func(o *Options)
	}{
		{"output dir symlink alias", func(o *Options) { o.OutputDir = aliased }},
		{"cache dir symlink alias", func(o *Options) { o.CacheDir = aliased }},
		{"relative output dir", func(o *Options) { o.OutputDir = "relative/output" }},
		{"relative cache dir", func(o *Options) { o.CacheDir = "relative/cache" }},
		{"nonexistent output dir", func(o *Options) { o.OutputDir = filepath.Join(t.TempDir(), "missing") }},
	}
	for _, tc := range cases {
		opts := fixtureOptions(fx, t.TempDir(), t.TempDir())
		tc.mut(&opts)
		if _, err := acquire(context.Background(), opts, regressTarget, insecureTestClient()); err == nil {
			t.Fatalf("%s: acquisition accepted an unsafe directory", tc.name)
		}
	}
}

func TestValidateTargetPathUnsafeEncodings(t *testing.T) {
	for _, p := range []string{
		"%2e%2e/escape",      // URL-encoded traversal
		"a%2fb",              // URL-encoded separator
		"file\u0000name.zip", // NUL control byte
		"file\nname.zip",     // newline control byte
		"file\u007fname.zip", // DEL control byte
		"name:ads.zip",       // Windows alternate data stream
		"C:/x/name.zip",      // drive-letter ambiguity
		"cercano/app/a.",     // trailing dot stripped by Windows
		"cercano/app/a ",     // trailing space stripped by Windows
	} {
		if err := validateTargetPath(p); err == nil {
			t.Errorf("validateTargetPath(%q) accepted an ambiguous path", p)
		}
	}
	for _, p := range []string{
		"cercano/app/1.0.0.zip",
		"a/b-c_d.e/f~g.txt",
	} {
		if err := validateTargetPath(p); err != nil {
			t.Errorf("validateTargetPath(%q) rejected a valid path: %v", p, err)
		}
	}
}

func TestAcquireRejectsNilContextAndBadTimeouts(t *testing.T) {
	fx := newFixture(t)
	defer fx.server.Close()

	opts := fixtureOptions(fx, t.TempDir(), t.TempDir())

	// Nil contexts must be rejected outright, not panic inside net/http.
	if _, err := acquire(nil, opts, regressTarget, insecureTestClient()); err == nil {
		t.Fatal("package-private acquire accepted a nil context")
	}
	if _, err := Acquire(nil, opts, regressTarget); err == nil {
		t.Fatal("Acquire accepted a nil context")
	}

	cases := []struct {
		name    string
		timeout time.Duration
	}{
		{"negative timeout", -time.Second},
		{"excessive timeout", maxRequestTimeout + time.Minute},
	}
	for _, tc := range cases {
		o := opts
		o.RequestTimeout = tc.timeout
		if _, err := acquire(context.Background(), o, regressTarget, insecureTestClient()); err == nil {
			t.Fatalf("%s: acquisition accepted it", tc.name)
		}
		if _, err := Acquire(context.Background(), o, regressTarget); err == nil {
			t.Fatalf("%s: exported Acquire accepted it", tc.name)
		}
	}

	// The unexported shortcut must not bypass the overall bound: a tiny
	// deadline must fail the acquisition the same way it would through
	// the exported API.
	o := opts
	o.RequestTimeout = time.Nanosecond
	if _, err := acquire(context.Background(), o, regressTarget, insecureTestClient()); err == nil {
		t.Fatal("package-private acquire bypassed the overall timeout bound")
	}
}

// cancelingTransport cancels a context once a target request is seen, then
// returns the real (fixture) response.
type cancelingTransport struct {
	base   http.RoundTripper
	cancel context.CancelFunc
}

func (t *cancelingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.Path, "/targets/") {
		t.cancel()
	}
	return t.base.RoundTrip(req)
}

func TestValidateTargetPathPositiveUnicodeAndVersions(t *testing.T) {
	for _, p := range []string{
		"cercano/app/1.0.0.zip",
		"cercano/app/v2.3.4-beta.1+build.5.pkg",
		"cercano/app/приложение-1.0.0.zip", // Cyrillic
		"cercano/app/日本語-1.0.0.zip",        // CJK
		"cercano/app/emoji-🚀-1.0.0.zip",    // astral Unicode
		"cercano/app/sale-100%-off.zip",    // literal percent, not an escape
		"a/b-c_d.e/f~g.txt",
	} {
		if err := validateTargetPath(p); err != nil {
			t.Errorf("validateTargetPath(%q) rejected a valid path: %v", p, err)
		}
	}
}

func TestValidateTargetPathWindowsReservedAndTraversalForms(t *testing.T) {
	for _, p := range []string{
		"cercano/app/CON.zip", // reserved device, with extension
		"cercano/app/con",     // reserved device, case-insensitive
		"cercano/Aux.tmp",     // reserved device, mixed case
		"aux/cercano.zip",     // reserved device as a directory segment
		"cercano/COM1.zip",    // serial device
		"cercano/lpt9.zip",    // printer device
		"cercano/conin$.zip",  // console device
		"..",                  // bare traversal
		"a//b",                // empty segment (not clean)
		"a/./b",               // not clean
		"a%2e%2eb",            // percent-encoded dot-dot without a slash
		"a%5Cb",               // percent-encoded backslash
		"\x01/a.zip",          // low control byte
	} {
		if err := validateTargetPath(p); err == nil {
			t.Errorf("validateTargetPath(%q) accepted an ambiguous path", p)
		}
	}
}

// TestAcquireUnicodeTargetPath proves a signed Unicode filename survives
// validation, signing, fetching, and publication end to end.
func TestAcquireUnicodeTargetPath(t *testing.T) {
	const uniTarget = "cercano/app/приложение-1.0.0.zip"
	fx := newFixtureSpecs(t, time.Now().AddDate(1, 0, 0), nil,
		targetSpec{path: uniTarget, data: []byte("unicode verified payload")})
	defer fx.server.Close()

	receipt, err := acquire(context.Background(),
		fixtureOptions(fx, t.TempDir(), t.TempDir()), uniTarget, insecureTestClient())
	if err != nil {
		t.Fatalf("acquire failed for a Unicode target path: %v", err)
	}
	got, err := os.ReadFile(receipt.OutputPath)
	if err != nil {
		t.Fatalf("published file unreadable: %v", err)
	}
	if want := fx.targets[uniTarget]; string(got) != string(want) {
		t.Fatalf("published bytes = %q, want %q", got, want)
	}
}

// TestAcquireSameBasenameIndependentTargets proves two signed targets that
// share a basename publish as two distinct library-owned files, each with
// its own verified bytes.
func TestAcquireSameBasenameIndependentTargets(t *testing.T) {
	fx := newFixtureSpecs(t, time.Now().AddDate(1, 0, 0), nil,
		targetSpec{path: "stable/1.0.0.zip", data: []byte("stable payload")},
		targetSpec{path: "beta/1.0.0.zip", data: []byte("beta payload")},
	)
	defer fx.server.Close()

	cacheDir, outputDir := t.TempDir(), t.TempDir()
	opts := fixtureOptions(fx, cacheDir, outputDir)

	first, err := acquire(context.Background(), opts, "stable/1.0.0.zip", insecureTestClient())
	if err != nil {
		t.Fatalf("first acquisition failed: %v", err)
	}
	second, err := acquire(context.Background(), opts, "beta/1.0.0.zip", insecureTestClient())
	if err != nil {
		t.Fatalf("second acquisition failed: %v", err)
	}
	if first.OutputPath == second.OutputPath {
		t.Fatalf("same-basename targets published onto one file %q", first.OutputPath)
	}
	for path, want := range map[string][]byte{
		first.OutputPath:  fx.targets["stable/1.0.0.zip"],
		second.OutputPath: fx.targets["beta/1.0.0.zip"],
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("published file %q unreadable: %v", path, err)
		}
		if string(got) != string(want) {
			t.Fatalf("published bytes for %q = %q, want %q", path, got, want)
		}
	}
	if names := outputEntries(t, outputDir); len(names) != 2 {
		t.Fatalf("expected exactly two published files, got %v", names)
	}
}

func TestCheckBaseURLSourcePolicy(t *testing.T) {
	for _, raw := range []string{
		"https://user:pass@host/metadata/", // embedded credentials
		"https://host/metadata/?q=1",       // query
		"https://host/metadata/#frag",      // fragment
		"https://host/me%ta/",              // invalid percent escape
		"https://host/me%2fta/",            // percent-encoded separator
		"https://host/me%5cta/",            // percent-encoded backslash
		"https://host/../metadata/",        // traversal segment
		"https://host/a%00b/",              // percent-encoded control byte
		"https://host\\metadata/",          // backslash in path
		"http://host/metadata/",            // wrong scheme (exported policy)
		"",                                 // required
	} {
		if err := checkBaseURL(raw, "metadata", true); err == nil {
			t.Errorf("checkBaseURL(%q) accepted an unsafe source URL", raw)
		}
	}
	// The plain fixture-style URL must remain accepted.
	if err := checkBaseURL("https://host/metadata/", "metadata", true); err != nil {
		t.Errorf("checkBaseURL rejected a plain source URL: %v", err)
	}
	// Errors must never echo the raw URL: a configured source may embed
	// credentials, and denials must not leak them.
	err := checkBaseURL("https://user:pass@host/metadata/", "metadata", true)
	if err == nil {
		t.Fatal("credential-bearing source URL accepted")
	}
	for _, secret := range []string{"user:pass@host", "pass", "user"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("checkBaseURL error leaked credential material %q: %s", secret, err)
		}
	}
}
