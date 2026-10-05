// Package tufproof exercises the real go-tuf client against ephemeral local
// repositories. All signing keys are generated in memory for each test. This
// is not a production publisher, key policy, or update implementation.
package tufproof

import (
	"crypto"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/theupdateframework/go-tuf/v2/metadata/config"
	"github.com/theupdateframework/go-tuf/v2/metadata/updater"
)

const targetPath = "stable/windows/amd64/1.2.3/cercano.zip"

var targetBytes = []byte("inert archive fixture: never extracted or executed")

type repository struct {
	t         *testing.T
	mu        sync.Mutex
	files     map[string][]byte
	keys      map[string]ed25519.PrivateKey
	root      *metadata.Metadata[metadata.RootType]
	bootstrap []byte
	server    *httptest.Server
	revision  int64
	expires   time.Time
}

func key(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, k, e := ed25519.GenerateKey(nil)
	if e != nil {
		t.Fatal(e)
	}
	return k
}
func signed[T metadata.Roles](t *testing.T, m *metadata.Metadata[T], keys ...ed25519.PrivateKey) []byte {
	t.Helper()
	m.Signatures = nil
	for _, k := range keys {
		s, e := signature.LoadSigner(k, crypto.Hash(0))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = m.Sign(s); e != nil {
			t.Fatal(e)
		}
	}
	b, e := m.ToBytes(false)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func newRepo(t *testing.T) *repository {
	t.Helper()
	r := &repository{t: t, files: map[string][]byte{}, keys: map[string]ed25519.PrivateKey{}, revision: 1, expires: time.Now().UTC().Add(time.Hour)}
	r.root = metadata.Root(time.Now().UTC().Add(24 * time.Hour))
	for _, role := range []string{"root", "targets", "snapshot", "timestamp"} {
		r.keys[role] = key(t)
		k, e := metadata.KeyFromPublicKey(r.keys[role].Public())
		if e != nil {
			t.Fatal(e)
		}
		if e = r.root.Signed.AddKey(k, role); e != nil {
			t.Fatal(e)
		}
	}
	r.bootstrap = signed(t, r.root, r.keys["root"])
	r.files["/metadata/1.root.json"] = r.bootstrap
	r.publish()
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		r.mu.Lock()
		b, ok := r.files[q.URL.Path]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, q)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *repository) publish() {
	targets := metadata.Targets(r.expires)
	targets.Signed.Version = r.revision
	tf, e := metadata.TargetFile().FromBytes(targetPath, targetBytes, "sha256")
	if e != nil {
		r.t.Fatal(e)
	}
	targets.Signed.Targets[targetPath] = tf
	tb := signed(r.t, targets, r.keys["targets"])
	snapshot := metadata.Snapshot(r.expires)
	snapshot.Signed.Version = r.revision
	snapshot.Signed.Meta["targets.json"] = metadata.MetaFile(r.revision)
	sb := signed(r.t, snapshot, r.keys["snapshot"])
	timestamp := metadata.Timestamp(r.expires)
	timestamp.Signed.Version = r.revision
	timestamp.Signed.Meta["snapshot.json"] = metadata.MetaFile(r.revision)
	ts := signed(r.t, timestamp, r.keys["timestamp"])
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files[fmt.Sprintf("/metadata/%d.targets.json", r.revision)] = tb
	r.files[fmt.Sprintf("/metadata/%d.snapshot.json", r.revision)] = sb
	// Publish the timestamp last; targets are hash-prefixed with consistent snapshots.
	prefix := hex.EncodeToString(tf.Hashes["sha256"])
	r.files["/targets/stable/windows/amd64/1.2.3/"+prefix+".cercano.zip"] = targetBytes
	r.files["/metadata/timestamp.json"] = ts
}

func (r *repository) client(cache string) *updater.Updater {
	r.t.Helper()
	cfg, e := config.New(r.server.URL+"/metadata", r.bootstrap)
	if e != nil {
		r.t.Fatal(e)
	}
	cfg.RemoteTargetsURL = r.server.URL + "/targets"
	cfg.LocalMetadataDir = filepath.Join(cache, "metadata")
	cfg.LocalTargetsDir = filepath.Join(cache, "targets")
	if e = cfg.EnsurePathsExist(); e != nil {
		r.t.Fatal(e)
	}
	if e = cfg.SetDefaultFetcherHTTPClient(&http.Client{Timeout: time.Second}); e != nil {
		r.t.Fatal(e)
	}
	if e = cfg.SetDefaultFetcherRetry(time.Millisecond, 1); e != nil {
		r.t.Fatal(e)
	}
	u, e := updater.New(cfg)
	if e != nil {
		r.t.Fatal(e)
	}
	return u
}
func (r *repository) set(path string, b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b == nil {
		delete(r.files, path)
	} else {
		r.files[path] = b
	}
}
func (r *repository) get(path string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte{}, r.files[path]...)
}
func failIfNil(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("unsafe operation unexpectedly succeeded")
	}
	t.Log(err)
}

func TestBootstrapAndVerifiedTarget(t *testing.T) {
	r := newRepo(t)

	u := r.client(t.TempDir())
	if e := u.Refresh(); e != nil {
		t.Fatal(e)
	}
	info, e := u.GetTargetInfo(targetPath)
	if e != nil {
		t.Fatal(e)
	}
	_, b, e := u.DownloadTarget(info, "", "")
	if e != nil {
		t.Fatal(e)
	}
	if string(b) != string(targetBytes) {
		t.Fatal("wrong verified bytes")
	}
}
func TestWrongPlatformTargetNotAuthorized(t *testing.T) {
	r := newRepo(t)
	u := r.client(t.TempDir())
	if e := u.Refresh(); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"stable/linux/amd64/1.2.3/cercano.zip", "stable/windows/arm64/1.2.3/cercano.zip", "beta/windows/amd64/1.2.3/cercano.zip"} {
		_, e := u.GetTargetInfo(path)
		failIfNil(t, e)
	}
}
func TestTamperedTargetFailsWithoutInstalling(t *testing.T) {
	r := newRepo(t)
	cache := t.TempDir()
	u := r.client(cache)
	if e := u.Refresh(); e != nil {
		t.Fatal(e)
	}
	info, e := u.GetTargetInfo(targetPath)
	if e != nil {
		t.Fatal(e)
	}
	p := "/targets/stable/windows/amd64/1.2.3/" + hex.EncodeToString(info.Hashes["sha256"]) + ".cercano.zip"
	bad := append([]byte{}, targetBytes...)
	bad[0] ^= 1
	r.set(p, bad)
	_, _, e = u.DownloadTarget(info, "", "")
	var mismatch *metadata.ErrLengthOrHashMismatch
	if !errors.As(e, &mismatch) {
		t.Fatalf("wanted actual hash failure, not other error: %v", e)
	}
	entries, e := os.ReadDir(filepath.Join(cache, "targets"))
	if e != nil {
		t.Fatal(e)
	}
	if len(entries) != 0 {
		t.Fatal("tampered target persisted")
	}
}
func TestTimestampWrongSignerRefused(t *testing.T) {
	r := newRepo(t)
	m := metadata.Timestamp(r.expires)
	m.Signed.Meta["snapshot.json"] = metadata.MetaFile(1)
	r.set("/metadata/timestamp.json", signed(t, m, key(t)))
	failIfNil(t, r.client(t.TempDir()).Refresh())
}
func TestExpiryFailsClosed(t *testing.T) {
	r := newRepo(t)
	u := r.client(t.TempDir())
	// Test-only clock injection; no production clock bypass is being added.
	u.UnsafeSetRefTime(r.expires.Add(time.Minute))
	failIfNil(t, u.Refresh())
}
func TestRootExpiryFailsClosed(t *testing.T) {
	r := newRepo(t)
	u := r.client(t.TempDir())
	u.UnsafeSetRefTime(r.root.Signed.Expires.Add(time.Minute))
	failIfNil(t, u.Refresh())
}
func TestRollbackAfterCachedNewerMetadata(t *testing.T) {
	r := newRepo(t)
	old := r.get("/metadata/timestamp.json")
	r.revision = 2
	r.publish()
	cache := t.TempDir()
	if e := r.client(cache).Refresh(); e != nil {
		t.Fatal(e)
	}
	r.set("/metadata/timestamp.json", old)
	failIfNil(t, r.client(cache).Refresh())
}
func TestOfflineFailureKeepsInstalledBytes(t *testing.T) {
	r := newRepo(t)
	cache := t.TempDir()
	if e := r.client(cache).Refresh(); e != nil {
		t.Fatal(e)
	}
	installed := filepath.Join(t.TempDir(), "installed")
	if e := os.WriteFile(installed, []byte("old version"), 0600); e != nil {
		t.Fatal(e)
	}
	r.server.Close()
	failIfNil(t, r.client(cache).Refresh())
	b, e := os.ReadFile(installed)
	if e != nil || string(b) != "old version" {
		t.Fatal("installed bytes changed")
	}
}
func TestInterruptedMetadataRefreshRecovers(t *testing.T) {
	for _, path := range []string{"/metadata/timestamp.json", "/metadata/1.snapshot.json", "/metadata/1.targets.json"} {
		t.Run(path, func(t *testing.T) {
			r := newRepo(t)
			cache := t.TempDir()
			saved := r.get(path)
			r.set(path, nil)
			failIfNil(t, r.client(cache).Refresh())
			r.set(path, saved)
			if e := r.client(cache).Refresh(); e != nil {
				t.Fatalf("retry did not recover: %v", e)
			}
		})
	}
}
func (r *repository) rotate() {
	old := r.keys["root"]
	oldKey, e := metadata.KeyFromPublicKey(old.Public())
	if e != nil {
		r.t.Fatal(e)
	}
	r.keys["root"] = key(r.t)
	newKey, e := metadata.KeyFromPublicKey(r.keys["root"].Public())
	if e != nil {
		r.t.Fatal(e)
	}
	oldID, e := oldKey.ID()
	if e != nil {
		r.t.Fatal(e)
	}
	if e = r.root.Signed.RevokeKey(oldID, "root"); e != nil {
		r.t.Fatal(e)
	}
	if e = r.root.Signed.AddKey(newKey, "root"); e != nil {
		r.t.Fatal(e)
	}
	r.root.Signed.Version++
	b := signed(r.t, r.root, old, r.keys["root"])
	r.set(fmt.Sprintf("/metadata/%d.root.json", r.root.Signed.Version), b)
}
func TestSequentialRootRotation(t *testing.T) {
	r := newRepo(t)
	r.rotate()
	r.rotate()
	u := r.client(t.TempDir())
	if e := u.Refresh(); e != nil {
		t.Fatal(e)
	}
	if v := u.GetTrustedMetadataSet().Root.Signed.Version; v != 3 {
		t.Fatalf("root version=%d", v)
	}
}
func TestMissingIntermediateRootCannotBeSkipped(t *testing.T) {
	r := newRepo(t)
	r.rotate()
	r.rotate()
	r.set("/metadata/2.root.json", nil)
	u := r.client(t.TempDir())
	if e := u.Refresh(); e != nil {
		t.Fatal(e)
	}
	if v := u.GetTrustedMetadataSet().Root.Signed.Version; v != 1 {
		t.Fatalf("skipped missing trust link: %d", v)
	}
	// Previously authorized, fresh metadata remains usable; withholding rotations
	// cannot be called immediate revocation without an explicit expiry policy.
}
func TestIncompletePublicationFailsThenRecovers(t *testing.T) {
	r := newRepo(t)
	cache := t.TempDir()
	if e := r.client(cache).Refresh(); e != nil {
		t.Fatal(e)
	}
	r.revision = 2
	r.publish()
	path := "/metadata/2.targets.json"
	saved := r.get(path)
	r.set(path, nil)
	failIfNil(t, r.client(cache).Refresh())
	r.set(path, saved)
	if e := r.client(cache).Refresh(); e != nil {
		t.Fatal(e)
	}
}

// This models the required storage precondition on the actual served timestamp.
// A production host must separately prove an equivalent conditional-write API.
func (r *repository) commitTimestamp(expected, next int64, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, err := metadata.Timestamp(time.Time{}).FromBytes(r.files["/metadata/timestamp.json"])
	if err != nil {
		return err
	}
	candidate, err := metadata.Timestamp(time.Time{}).FromBytes(data)
	if err != nil {
		return err
	}
	if current.Signed.Version != expected || next <= expected || candidate.Signed.Version != next {
		return fmt.Errorf("stale or regressive publisher")
	}
	r.files["/metadata/timestamp.json"] = append([]byte{}, data...)
	return nil
}
func TestConcurrentPublisherContract(t *testing.T) {
	r := newRepo(t)
	old := r.get("/metadata/timestamp.json")
	r.revision = 2
	r.publish()
	next := r.get("/metadata/timestamp.json")
	// Immutable version-2 data is uploaded before the timestamp is switched.
	r.set("/metadata/timestamp.json", old)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- r.commitTimestamp(1, 2, next) }()
	}
	successes := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent commits succeeded=%d", successes)
	}
	failIfNil(t, r.commitTimestamp(2, 1, old))
	u := r.client(t.TempDir())
	if err := u.Refresh(); err != nil {
		t.Fatal(err)
	}
	if u.GetTrustedMetadataSet().Timestamp.Signed.Version != 2 {
		t.Fatal("served metadata regressed")
	}
}
func TestExpiredBootstrapCanRecoverThroughValidRotation(t *testing.T) {
	r := newRepo(t)
	r.root.Signed.Expires = time.Now().UTC().Add(-time.Hour)
	r.bootstrap = signed(t, r.root, r.keys["root"])
	r.root.Signed.Expires = time.Now().UTC().Add(24 * time.Hour)
	r.rotate()
	u := r.client(t.TempDir())
	if err := u.Refresh(); err != nil {
		t.Fatalf("valid root rotation recovery failed: %v", err)
	}
	if u.GetTrustedMetadataSet().Root.Signed.Version != 2 {
		t.Fatal("did not recover root")
	}
}
func TestUnsignedRootRotationRefused(t *testing.T) {
	r := newRepo(t)
	r.rotate()
	r.root.Signatures = nil
	r.set("/metadata/2.root.json", signed(t, r.root, key(t)))
	failIfNil(t, r.client(t.TempDir()).Refresh())
}
func TestTamperedTargetsMetadataRefused(t *testing.T) {
	r := newRepo(t)
	targets := metadata.Targets(r.expires)
	r.set("/metadata/1.targets.json", signed(t, targets, key(t)))
	failIfNil(t, r.client(t.TempDir()).Refresh())
}
