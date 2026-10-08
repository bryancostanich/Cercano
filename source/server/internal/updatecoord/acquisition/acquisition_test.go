package acquisition

import (
	"context"
	"crypto"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// fixture is a minimal signed TUF repository served by a local httptest TLS
// server, following the repository fixture pattern of the approved tuf-proof
// (efforts/cross-platform-updates/tuf-proof).
type fixture struct {
	timestampOverride atomic.Value // immutable bytes, safe across HTTP handlers
	makeTimestamp     func(int64) []byte
	server            *httptest.Server
	root              []byte // trusted root metadata bytes handed to the client
	rootPriv          ed25519.PrivateKey
	targets           map[string][]byte
	// targetRequests counts requests received for target bytes. Regression
	// tests assert that digest- and size-refusals happen before any of
	// these, and that refused redirect targets are never fetched.
	targetRequests int
	// onTargetRequest, when set, fully handles one target request and
	// reports whether it did so; tests use it to serve redirect responses.
	onTargetRequest func(w http.ResponseWriter, r *http.Request, relPath string) bool
}

// targetSpec describes one signed target inside a fixture.
type targetSpec struct {
	path string
	data []byte
	// hashes lists the digest algorithms signed for the target
	// (default: sha256 only).
	hashes []string
	// length, when non-nil, overrides the signed target length.
	length *int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureAt(t, time.Now().AddDate(1, 0, 0), nil)
}

// newFixtureAt builds a signed repository with the given metadata expiry and
// a per-test tamper hook for the served target bytes.
func newFixtureAt(t *testing.T, expiry time.Time, tamper func(path string, data []byte) []byte) *fixture {
	t.Helper()
	return newFixtureSpecs(t, expiry, tamper,
		targetSpec{path: "cercano/app/1.0.0.zip", data: []byte("verified update payload 1.0.0")})
}

// newFixtureSpecs builds a signed repository whose targets are described by
// specs, including hash-algorithm and signed-length overrides.
func newFixtureSpecs(t *testing.T, expiry time.Time, tamper func(path string, data []byte) []byte, specs ...targetSpec) *fixture {
	t.Helper()
	priv := make([]ed25519.PrivateKey, 4) // root, targets, snapshot, timestamp
	pub := make([]ed25519.PublicKey, 4)
	for i := range priv {
		pk, sk, err := ed25519.GenerateKey(cryptorand.Reader)
		if err != nil {
			t.Fatalf("generating key: %v", err)
		}
		priv[i], pub[i] = sk, pk
	}
	signer := func(k ed25519.PrivateKey) signature.Signer {
		s, err := signature.LoadSigner(k, crypto.Hash(0))
		if err != nil {
			t.Fatalf("loading signer: %v", err)
		}
		return s
	}
	root := metadata.Root(expiry)
	for i, role := range []string{metadata.ROOT, metadata.TARGETS, metadata.SNAPSHOT, metadata.TIMESTAMP} {
		key, err := metadata.KeyFromPublicKey(pub[i])
		if err != nil {
			t.Fatalf("root key: %v", err)
		}
		if err = root.Signed.AddKey(key, role); err != nil {
			t.Fatalf("adding key for %s: %v", role, err)
		}
	}
	signMeta(t, root, signer(priv[0]))

	fx := &fixture{rootPriv: priv[0], targets: map[string][]byte{}}
	targetsMeta := metadata.Targets(expiry)
	targetsMeta.Signed.Version = 1
	for _, spec := range specs {
		data := spec.data
		if tamper != nil {
			data = tamper(spec.path, data)
		}
		hashes := spec.hashes
		if len(hashes) == 0 {
			hashes = []string{"sha256"}
		}
		tf, err := metadata.TargetFile().FromBytes(spec.path, data, hashes...)
		if err != nil {
			t.Fatalf("target %s: %v", spec.path, err)
		}
		if spec.length != nil {
			tf.Length = *spec.length
		}
		targetsMeta.Signed.Targets[spec.path] = tf
		fx.targets[spec.path] = data
	}
	signMeta(t, targetsMeta, signer(priv[1]))

	// Create snapshot metadata
	snapshotMeta := metadata.Snapshot(expiry)
	snapshotMeta.Signed.Version = 1
	snapshotMeta.Signed.Meta["targets.json"] = metadata.MetaFile(1)
	signMeta(t, snapshotMeta, signer(priv[2]))

	// Create timestamp metadata
	timestampMeta := metadata.Timestamp(expiry)
	timestampMeta.Signed.Version = 1
	timestampMeta.Signed.Meta["snapshot.json"] = metadata.MetaFile(1)
	signMeta(t, timestampMeta, signer(priv[3]))
	fx.makeTimestamp = func(version int64) []byte {
		m := metadata.Timestamp(expiry)
		m.Signed.Version = version
		m.Signed.Meta["snapshot.json"] = metadata.MetaFile(1)
		signMeta(t, m, signer(priv[3]))
		b, e := m.MarshalJSON()
		if e != nil {
			t.Fatal(e)
		}
		return b
	}

	// Create a test server
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/metadata/timestamp.json" {
			if b := fx.timestampOverride.Load(); b != nil {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(b.([]byte))
				return
			}
		}

		if strings.HasPrefix(path, "/metadata/") {
			// Serve metadata - handle both versioned and non-versioned paths
			switch path {
			case "/metadata/root.json":
				w.Header().Set("Content-Type", "application/json")
				rootJSON, _ := root.MarshalJSON()
				w.Write(rootJSON)
			case "/metadata/1.root.json":
				w.Header().Set("Content-Type", "application/json")
				rootJSON, _ := root.MarshalJSON()
				w.Write(rootJSON)
			case "/metadata/targets.json":
				w.Header().Set("Content-Type", "application/json")
				targetsJSON, _ := targetsMeta.MarshalJSON()
				w.Write(targetsJSON)
			case "/metadata/1.targets.json":
				w.Header().Set("Content-Type", "application/json")
				targetsJSON, _ := targetsMeta.MarshalJSON()
				w.Write(targetsJSON)
			case "/metadata/snapshot.json":
				w.Header().Set("Content-Type", "application/json")
				snapshotJSON, _ := snapshotMeta.MarshalJSON()
				w.Write(snapshotJSON)
			case "/metadata/1.snapshot.json":
				w.Header().Set("Content-Type", "application/json")
				snapshotJSON, _ := snapshotMeta.MarshalJSON()
				w.Write(snapshotJSON)
			case "/metadata/timestamp.json":
				w.Header().Set("Content-Type", "application/json")
				timestampJSON, _ := timestampMeta.MarshalJSON()
				w.Write(timestampJSON)
			default:
				w.WriteHeader(404)
			}
		} else if strings.HasPrefix(path, "/targets/") {
			// Serve targets - handle both direct and hashed paths
			targetPath := strings.TrimPrefix(path, "/targets/")
			t.Logf("Requested target path: %s", targetPath)
			fx.targetRequests++

			if fx.onTargetRequest != nil && fx.onTargetRequest(w, r, targetPath) {
				return
			}

			// Direct path lookup first
			if data, ok := fx.targets[targetPath]; ok {
				t.Logf("Serving target directly: %s", targetPath)
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Write(data)
				return
			}

			// If direct lookup fails, try to match by content hash
			for knownPath, data := range fx.targets {
				hash := sha256sum(data)
				hashStr := hex.EncodeToString(hash)
				// Check if the requested path contains our hash
				if strings.Contains(targetPath, hashStr) {
					t.Logf("Serving target by hash: %s -> %s", targetPath, knownPath)
					w.Header().Set("Content-Type", "application/octet-stream")
					w.Write(data)
					return
				}
			}

			t.Logf("Target not found: %s", targetPath)
			w.WriteHeader(404)
		} else {
			w.WriteHeader(404)
		}
	}))

	// Start with TLS to enable HTTPS
	server.StartTLS()

	// Update URLs to use https
	server.URL = strings.Replace(server.URL, "http://", "https://", 1)

	fx.server = server
	rootJSON, _ := root.MarshalJSON()
	fx.root = rootJSON
	return fx
}

func sha256sum(data []byte) []byte {
	hash := sha256.Sum256(data)
	return hash[:]
}

func signMeta[T metadata.Roles](t *testing.T, meta *metadata.Metadata[T], signer signature.Signer) {
	t.Helper()
	if _, err := meta.Sign(signer); err != nil {
		t.Fatalf("signing metadata: %v", err)
	}
}

func TestAcquire(t *testing.T) {
	t.Helper()
	fx := newFixture(t)
	defer fx.server.Close()

	// Create temporary directories
	cacheDir := t.TempDir()
	outputDir := t.TempDir()

	opts := Options{
		TrustedRoot:     fx.root,
		MetadataBaseURL: fx.server.URL + "/metadata/",
		TargetBaseURL:   fx.server.URL + "/targets/",
		CacheDir:        cacheDir,
		OutputDir:       outputDir,
	}

	// Create a custom HTTP client that skips TLS verification for testing
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// Use the acquire function directly with the custom client
	receipt, acquireErr := acquire(context.Background(), opts, "cercano/app/1.0.0.zip", client)
	if acquireErr != nil {
		t.Fatalf("Acquire failed: %v", acquireErr)
	}

	if receipt.TargetPath != "cercano/app/1.0.0.zip" {
		t.Errorf("expected target path %q, got %q", "cercano/app/1.0.0.zip", receipt.TargetPath)
	}
	if receipt.Length != 29 {
		t.Errorf("expected length 29, got %d", receipt.Length)
	}
	if len(receipt.SHA256) != 32 {
		t.Errorf("expected SHA256 length 32, got %d", len(receipt.SHA256))
	}
}

func TestAcquireInvalidTargetPath(t *testing.T) {
	t.Helper()
	fx := newFixture(t)
	defer fx.server.Close()

	cacheDir := t.TempDir()
	outputDir := t.TempDir()

	opts := Options{
		TrustedRoot:     fx.root,
		MetadataBaseURL: fx.server.URL + "/metadata/",
		TargetBaseURL:   fx.server.URL + "/targets/",
		CacheDir:        cacheDir,
		OutputDir:       outputDir,
	}

	_, err := Acquire(context.Background(), opts, "../escape.txt")
	if err == nil {
		t.Fatal("expected error for absolute path")
	}
}

func TestAcquireMissingTarget(t *testing.T) {
	t.Helper()
	fx := newFixture(t)
	defer fx.server.Close()

	cacheDir := t.TempDir()
	outputDir := t.TempDir()

	opts := Options{
		TrustedRoot:     fx.root,
		MetadataBaseURL: fx.server.URL + "/metadata/",
		TargetBaseURL:   fx.server.URL + "/targets/",
		CacheDir:        cacheDir,
		OutputDir:       outputDir,
	}

	_, err := Acquire(context.Background(), opts, "nonexistent/target.txt")
	if err == nil {
		t.Fatal("expected error for missing target")
	}
}

func TestAcquireWrongHash(t *testing.T) {
	t.Helper()
	fx := newFixture(t)
	defer fx.server.Close()

	cacheDir := t.TempDir()
	outputDir := t.TempDir()

	// Create a custom HTTP client that modifies the target content to wrong hash
	client := &http.Client{
		Transport: &wrongHashTransport{
			base:      http.DefaultTransport,
			targetURL: fx.server.URL + "/targets/cercano/app/1.0.0.zip",
		},
	}

	opts := Options{
		TrustedRoot:     fx.root,
		MetadataBaseURL: fx.server.URL + "/metadata/",
		TargetBaseURL:   fx.server.URL + "/targets/",
		CacheDir:        cacheDir,
		OutputDir:       outputDir,
	}

	_, err := acquire(context.Background(), opts, "cercano/app/1.0.0.zip", client)
	if err == nil {
		t.Fatal("expected error for wrong hash")
	}
}

func TestAcquireTruncated(t *testing.T) {
	t.Helper()
	fx := newFixture(t)
	defer fx.server.Close()

	cacheDir := t.TempDir()
	outputDir := t.TempDir()

	// Create a custom HTTP client that returns truncated content
	client := &http.Client{
		Transport: &truncatedTransport{
			base:      http.DefaultTransport,
			targetURL: fx.server.URL + "/targets/cercano/app/1.0.0.zip",
		},
	}

	opts := Options{
		TrustedRoot:     fx.root,
		MetadataBaseURL: fx.server.URL + "/metadata/",
		TargetBaseURL:   fx.server.URL + "/targets/",
		CacheDir:        cacheDir,
		OutputDir:       outputDir,
	}

	_, err := acquire(context.Background(), opts, "cercano/app/1.0.0.zip", client)
	if err == nil {
		t.Fatal("expected error for truncated content")
	}
}

func TestAcquireExpired(t *testing.T) {
	t.Helper()
	// Create a fixture with expired metadata
	fx := newFixtureAt(t, time.Now().Add(-time.Hour), nil)
	defer fx.server.Close()

	cacheDir := t.TempDir()
	outputDir := t.TempDir()

	opts := Options{
		TrustedRoot:     fx.root,
		MetadataBaseURL: fx.server.URL + "/metadata/",
		TargetBaseURL:   fx.server.URL + "/targets/",
		CacheDir:        cacheDir,
		OutputDir:       outputDir,
	}

	_, err := Acquire(context.Background(), opts, "cercano/app/1.0.0.zip")
	if err == nil {
		t.Fatal("expected error for expired metadata")
	}
}

func TestAcquireCancel(t *testing.T) {
	t.Helper()
	fx := newFixture(t)
	defer fx.server.Close()

	cacheDir := t.TempDir()
	outputDir := t.TempDir()

	opts := Options{
		TrustedRoot:     fx.root,
		MetadataBaseURL: fx.server.URL + "/metadata/",
		TargetBaseURL:   fx.server.URL + "/targets/",
		CacheDir:        cacheDir,
		OutputDir:       outputDir,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := Acquire(ctx, opts, "cercano/app/1.0.0.zip")
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

// wrongHashTransport modifies the target content to have a wrong hash
type wrongHashTransport struct {
	base      http.RoundTripper
	targetURL string
}

func (t *wrongHashTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.String() == t.targetURL {
		// Return the wrong content
		body := strings.NewReader("this has the wrong hash")
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(body),
			Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
		}, nil
	}
	return t.base.RoundTrip(req)
}

// truncatedTransport returns truncated content
type truncatedTransport struct {
	base      http.RoundTripper
	targetURL string
}

func (t *truncatedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.String() == t.targetURL {
		// Return truncated content
		body := strings.NewReader("truncated")
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(body),
			Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
		}, nil
	}
	return t.base.RoundTrip(req)
}
