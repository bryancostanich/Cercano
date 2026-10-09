package selection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"cercano/source/server/internal/updatecoord/activation"
	"cercano/source/server/internal/updatecoord/exclusion"
	"cercano/source/server/internal/updatecoord/privdir"
)

// These tests use ONLY temporary private directories they provision
// themselves via privdir, the REAL exclusion lock, and package-private
// failure-point hooks. Durability here means the measured protocol
// (sync/flush boundary, confirmed re-read); no power-loss guarantee is
// or can be tested from unit tests.

var (
	errTestPreCommit  = errors.New("test: failure injected before commit")
	errTestPostCommit = errors.New("test: failure injected after commit")
)

func testDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func testSelection(gen int64, version string) activation.Selection {
	return activation.Selection{
		SchemaVersion:          activation.SelectionSchemaVersion,
		InstallID:              "install-a1b2c3d4",
		Generation:             gen,
		SelectedVersion:        version,
		StagedVersionDir:       "versions/" + version,
		VerifiedArtifactSHA256: testDigest("artifact-" + version),
	}
}

func newPrivateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	created, err := privdir.Ensure(dir)
	if err != nil || !created {
		t.Fatalf("provision private dir = created=%v err=%v; want created, nil", created, err)
	}
	return dir
}

func acquireUpdateLock(t *testing.T, dir string) *exclusion.Handle {
	t.Helper()
	h, err := exclusion.Acquire(context.Background(), dir, exclusion.Update)
	if err != nil {
		t.Fatalf("acquire update exclusion: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func canonicalBytes(t *testing.T, sel activation.Selection) []byte {
	t.Helper()
	b, err := json.Marshal(sel)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// writeFixture arranges a current selection file OUTSIDE Publish (a prior
// cooperating publish's durable result) and returns its raw-bytes digest.
func writeFixture(t *testing.T, dir string, sel activation.Selection) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, FileName), canonicalBytes(t, sel), 0o600); err != nil {
		t.Fatal(err)
	}
	return digestOf(canonicalBytes(t, sel))
}

func readDest(t *testing.T, dir string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertNoStagingLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), stagingPrefix) {
			t.Fatalf("staging tempfile %q left behind in %s", e.Name(), dir)
		}
	}
}

func withHooks(t *testing.T, before, after func() error) {
	t.Helper()
	prevBefore, prevAfter := testHookBeforeCommit, testHookAfterCommit
	testHookBeforeCommit, testHookAfterCommit = before, after
	t.Cleanup(func() { testHookBeforeCommit, testHookAfterCommit = prevBefore, prevAfter })
}

// TestPublishFirstCreationFromProvenAbsence proves the absent-initial
// publish: first creation commits, is durable-confirmed, and is exactly
// what the activation reader observes afterwards.
func TestPublishFirstCreationFromProvenAbsence(t *testing.T) {
	dir := newPrivateDir(t)
	lock := acquireUpdateLock(t, dir)
	_ = lock
	next := testSelection(1, "1.0.1")
	res, err := Publish(context.Background(), dir, lock, Expected{Absent: true}, next)
	if err != nil {
		t.Fatalf("Publish(first) err = %v; want nil", err)
	}
	if res.State != CommitConfirmed {
		t.Fatalf("Publish(first) state = %v; want CommitConfirmed", res.State)
	}
	want := canonicalBytes(t, next)
	if got := readDest(t, dir); string(got) != string(want) {
		t.Fatalf("file after first publish = %q; want canonical %q", got, want)
	}
	if res.Digest != digestOf(want) {
		t.Fatalf("result digest = %q; want %q", res.Digest, digestOf(want))
	}
	observed, rerr := activation.ReadSelection(filepath.Join(dir, FileName))
	if rerr != nil || observed != next {
		t.Fatalf("ReadSelection after publish = (%+v, %v); want (%+v, nil)", observed, rerr, next)
	}
	assertNoStagingLeftovers(t, dir)
}

// TestPublishReplacesExactExpectedSelection proves the correct-expected
// replace path, chained twice, with the returned digest reused as the next
// expectation.
func TestPublishReplacesExactExpectedSelection(t *testing.T) {
	dir := newPrivateDir(t)
	lock := acquireUpdateLock(t, dir)
	gen1 := testSelection(1, "1.0.1")
	d1 := writeFixture(t, dir, gen1)
	gen2 := testSelection(2, "1.0.2")
	res, err := Publish(context.Background(), dir, lock, Expected{Selection: gen1, Digest: d1}, gen2)
	if err != nil {
		t.Fatalf("Publish(replace) err = %v; want nil", err)
	}
	if res.State != CommitConfirmed {
		t.Fatalf("Publish(replace) state = %v; want CommitConfirmed", res.State)
	}
	if got := readDest(t, dir); string(got) != string(canonicalBytes(t, gen2)) {
		t.Fatalf("file after replace = %q; want canonical gen2", got)
	}
	gen3 := testSelection(3, "1.0.3")
	res, err = Publish(context.Background(), dir, lock, Expected{Selection: gen2, Digest: res.Digest}, gen3)
	if err != nil || res.State != CommitConfirmed {
		t.Fatalf("Publish(chained) = (%+v, %v); want CommitConfirmed, nil", res, err)
	}
	assertNoStagingLeftovers(t, dir)
}

// TestPublishStaleExpectedRefusedUnchanged proves the stale-expectation
// conflict: a cooperating writer moved first, the re-read notices, and
// the destination is left exactly as that writer left it.
func TestPublishStaleExpectedRefusedUnchanged(t *testing.T) {
	dir := newPrivateDir(t)
	lock := acquireUpdateLock(t, dir)
	gen1 := testSelection(1, "1.0.1")
	writeFixture(t, dir, gen1)
	// A cooperating writer advanced to gen2 after this caller's expectation was formed.
	gen2 := testSelection(2, "1.0.2")
	writeFixture(t, dir, gen2)
	gen3 := testSelection(3, "1.0.3")
	_, err := Publish(context.Background(), dir, lock, Expected{Selection: gen1, Digest: digestOf(canonicalBytes(t, gen1))}, gen3)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Publish(stale expected) err = %v; want ErrConflict", err)
	}
	if got := readDest(t, dir); string(got) != string(canonicalBytes(t, gen2)) {
		t.Fatalf("destination changed after stale-expectation refusal: %q", got)
	}
	assertNoStagingLeftovers(t, dir)
}

// TestPublishRefusesMalformedForeignAndWrongDigest covers present-but-
// refused content: malformed bytes, a foreign installation's descriptor,
// and a matching descriptor with the wrong raw digest are all conflicts
// that leave the file unchanged.
func TestPublishRefusesMalformedForeignAndWrongDigest(t *testing.T) {
	own := testSelection(1, "1.0.1")
	cases := []struct {
		name     string
		bytes    []byte
		expected activation.Selection
		digest   string
	}{
		{"malformed bytes", []byte("not a selection"), own, digestOf(canonicalBytes(t, own))},
		{"foreign installation", nil, own, digestOf(canonicalBytes(t, own))},
		{"wrong digest", canonicalBytes(t, own), own, digestOf([]byte("different bytes"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := newPrivateDir(t)
			lock := acquireUpdateLock(t, dir)
			foreign := testSelection(1, "1.0.1")
			foreign.InstallID = "install-other-5678"
			payload := tc.bytes
			if tc.name == "foreign installation" {
				payload = canonicalBytes(t, foreign)
			}
			if err := os.WriteFile(filepath.Join(dir, FileName), payload, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Publish(context.Background(), dir, lock, Expected{Selection: tc.expected, Digest: tc.digest}, testSelection(2, "1.0.2"))
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("Publish err = %v; want ErrConflict", err)
			}
			if tc.name == "malformed bytes" && !errors.Is(err, activation.ErrSelectionMalformed) {
				t.Fatalf("malformed case err = %v; want wrapped activation.ErrSelectionMalformed", err)
			}
			if got := readDest(t, dir); string(got) != string(payload) {
				t.Fatalf("destination changed: %q; want %q", got, payload)
			}
			assertNoStagingLeftovers(t, dir)
		})
	}
}

// TestPublishRefusesInvalidRequests proves request validation refuses
// before anything is observed or staged.
func TestPublishRefusesInvalidRequests(t *testing.T) {
	dir := newPrivateDir(t)
	lock := acquireUpdateLock(t, dir)
	gen1 := testSelection(1, "1.0.1")
	d1 := writeFixture(t, dir, gen1)
	gen2 := testSelection(2, "1.0.2")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	// The publication directory hosts the exclusion lock file, so a
	// correctly-bound handle cannot exist for an absent path or a regular
	// file: those rows would only ever exercise the guarded-use binding
	// refusal (covered by TestPublishRefusesNonLiveExclusionHandles), not
	// the privdir classification. The genuinely-bound unsafe-directory
	// refusals are covered by TestPublishRefusesAbsentDirectoryWith
	// BoundHandle and the publish_unix_test.go rework.
	cases := []struct {
		name     string
		ctx      context.Context
		dir      string
		lock     *exclusion.Handle
		expected Expected
		next     activation.Selection
		want     error
	}{
		{"canceled context", canceled, dir, lock, Expected{Absent: true}, gen1, ErrCanceled},
		{"nil exclusion handle", context.Background(), dir, nil, Expected{Absent: true}, gen1, ErrInvalidRequest},
		{"relative directory", context.Background(), "relative/dir", lock, Expected{Absent: true}, gen1, ErrInvalidRequest},
		{"handle not bound to the publication directory", context.Background(), newPrivateDir(t), lock, Expected{Absent: true}, gen1, ErrInvalidRequest},
		{"invalid next selection", context.Background(), dir, lock, Expected{Absent: true}, func() activation.Selection {
			bad := gen1
			bad.SchemaVersion = 99
			return bad
		}(), ErrInvalidRequest},
		{"expected absent plus descriptor", context.Background(), dir, lock, Expected{Absent: true, Selection: gen1, Digest: d1}, gen2, ErrInvalidRequest},
		{"expected descriptor invalid", context.Background(), dir, lock, Expected{Selection: func() activation.Selection {
			bad := gen1
			bad.VerifiedArtifactSHA256 = "not-a-digest"
			return bad
		}(), Digest: d1}, gen2, ErrInvalidRequest},
		{"expected digest not lowercase hex sha256", context.Background(), dir, lock, Expected{Selection: gen1, Digest: "not-a-digest"}, gen2, ErrInvalidRequest},
		{"next generation not increasing", context.Background(), dir, lock, Expected{Selection: gen1, Digest: d1}, gen1, ErrInvalidRequest},
		{"next names another installation", context.Background(), dir, lock, Expected{Selection: gen1, Digest: d1}, func() activation.Selection {
			foreign := gen2
			foreign.InstallID = "install-other-5678"
			return foreign
		}(), ErrInvalidRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Publish(tc.ctx, tc.dir, tc.lock, tc.expected, tc.next)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Publish err = %v; want %v", err, tc.want)
			}
		})
	}
	if got := readDest(t, dir); string(got) != string(canonicalBytes(t, gen1)) {
		t.Fatalf("destination changed by refused requests: %q", got)
	}
}

// TestPublishRefusesAbsentDirectoryWithBoundHandle proves the absent
// publication directory is refused, never provisioned, with the handle
// correctly bound: the lock is acquired while the directory exists and
// the directory is then removed, so the only thing Publish can observe is
// the verify-only privdir classification of an absent path.
func TestPublishRefusesAbsentDirectoryWithBoundHandle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory holding an open lock file cannot be removed on Windows")
	}
	dir := newPrivateDir(t)
	lock := acquireUpdateLock(t, dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Skipf("cannot remove the locked fixture directory: %v", err)
	}
	_, err := Publish(context.Background(), dir, lock, Expected{Absent: true}, testSelection(1, "1.0.1"))
	if !errors.Is(err, ErrUnsafeDirectory) {
		t.Fatalf("Publish(absent dir, bound handle) err = %v; want ErrUnsafeDirectory", err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Publish(absent dir) err = %v; want the refusal to wrap fs.ErrNotExist", err)
	}
	if _, lerr := os.Lstat(dir); !errors.Is(lerr, fs.ErrNotExist) {
		t.Fatalf("absent publication directory was provisioned: %v", lerr)
	}
}

// TestPublishFailurePoints proves the two fixed failure points: a
// pre-commit failure changes nothing and cleans staging; a post-commit
// failure never rolls back and reports durability-uncertain with the
// OBSERVED committed value in place.
func TestPublishFailurePoints(t *testing.T) {
	t.Run("before commit leaves destination unchanged", func(t *testing.T) {
		dir := newPrivateDir(t)
		lock := acquireUpdateLock(t, dir)
		gen1 := testSelection(1, "1.0.1")
		d1 := writeFixture(t, dir, gen1)
		withHooks(t, func() error { return errTestPreCommit }, nil)
		_, err := Publish(context.Background(), dir, lock, Expected{Selection: gen1, Digest: d1}, testSelection(2, "1.0.2"))
		if !errors.Is(err, errTestPreCommit) {
			t.Fatalf("Publish err = %v; want injected pre-commit failure", err)
		}
		if got := readDest(t, dir); string(got) != string(canonicalBytes(t, gen1)) {
			t.Fatalf("destination changed by pre-commit failure: %q", got)
		}
		assertNoStagingLeftovers(t, dir)
	})
	t.Run("after commit reports uncertain, never rolls back", func(t *testing.T) {
		dir := newPrivateDir(t)
		lock := acquireUpdateLock(t, dir)
		gen1 := testSelection(1, "1.0.1")
		d1 := writeFixture(t, dir, gen1)
		next := testSelection(2, "1.0.2")
		withHooks(t, nil, func() error { return errTestPostCommit })
		res, err := Publish(context.Background(), dir, lock, Expected{Selection: gen1, Digest: d1}, next)
		if err != nil {
			t.Fatalf("Publish err = %v; want nil (committed)", err)
		}
		if res.State != CommitDurabilityUncertain {
			t.Fatalf("Publish state = %v; want CommitDurabilityUncertain", res.State)
		}
		if !strings.Contains(res.Detail, errTestPostCommit.Error()) {
			t.Fatalf("Detail = %q; want injected post-commit failure explained", res.Detail)
		}
		if got := readDest(t, dir); string(got) != string(canonicalBytes(t, next)) {
			t.Fatalf("observed value after commit = %q; want the committed canonical bytes (no rollback)", got)
		}
		assertNoStagingLeftovers(t, dir)
	})
}

// TestPublishFirstCreationDoesNotClobberUnexpectedEntry proves the
// no-clobber property: an entry that appears at the destination between
// the re-read and the commit makes the first creation fail instead of
// replacing it, and staging is cleaned.
func TestPublishFirstCreationDoesNotClobberUnexpectedEntry(t *testing.T) {
	dir := newPrivateDir(t)
	lock := acquireUpdateLock(t, dir)
	unexpected := []byte("someone else's file")
	withHooks(t, func() error {
		return os.WriteFile(filepath.Join(dir, FileName), unexpected, 0o600)
	}, nil)
	_, err := Publish(context.Background(), dir, lock, Expected{Absent: true}, testSelection(1, "1.0.1"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Publish(first creation with unexpected entry) err = %v; want ErrConflict", err)
	}
	if got := readDest(t, dir); string(got) != string(unexpected) {
		t.Fatalf("unexpected entry clobbered: %q; want %q", got, unexpected)
	}
	assertNoStagingLeftovers(t, dir)
}

// TestConcurrentCooperatingWritersUnderExclusion proves the cooperating
// protocol under the REAL exclusion lock: writers that observe-expect-
// publish inside the lock all commit and chain; writers that race on one
// stale expectation produce exactly one committed publish and clean
// conflicts for everyone else.
func TestConcurrentCooperatingWritersUnderExclusion(t *testing.T) {
	dir := newPrivateDir(t)
	ctx := context.Background()
	const writers = 4

	// Phase 1: each writer holds the lock, observes, and publishes the
	// successor generation. Every publish must commit.
	var committed1 int64
	var wg sync.WaitGroup
	for i := 1; i <= writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, err := exclusion.Acquire(ctx, dir, exclusion.Update)
			if err != nil {
				t.Errorf("writer %d acquire: %v", i, err)
				return
			}
			defer h.Close() //nolint:errcheck // test cleanup
			path := filepath.Join(dir, FileName)
			expected := Expected{Absent: true}
			if data, rerr := os.ReadFile(path); rerr == nil {
				prev, perr := activation.ParseSelection(data)
				if perr != nil {
					t.Errorf("writer %d parse: %v", i, perr)
					return
				}
				expected = Expected{Selection: prev, Digest: digestOf(data)}
			} else if !os.IsNotExist(rerr) {
				t.Errorf("writer %d read: %v", i, rerr)
				return
			}
			next := testSelection(expected.Selection.Generation+1, fmt.Sprintf("1.0.%d", i))
			if expected.Absent {
				next = testSelection(1, fmt.Sprintf("1.0.%d", i))
			}
			res, err := Publish(ctx, dir, h, expected, next)
			if err != nil || res.State != CommitConfirmed {
				t.Errorf("writer %d publish = (%+v, %v); want CommitConfirmed, nil", i, res, err)
				return
			}
			atomic.AddInt64(&committed1, 1)
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt64(&committed1); got != writers {
		t.Fatalf("cooperating writers committed = %d; want %d", got, writers)
	}
	final, rerr := activation.ReadSelection(filepath.Join(dir, FileName))
	if rerr != nil || final.Generation != writers {
		t.Fatalf("final selection = (%+v, %v); want generation %d", final, rerr, writers)
	}

	// Phase 2: every writer races with the SAME (now stale) expectation;
	// exactly one may commit, the rest must conflict cleanly.
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	prev, err := activation.ParseSelection(data)
	if err != nil {
		t.Fatal(err)
	}
	stale := Expected{Selection: prev, Digest: digestOf(data)}
	var committed2, conflicts2 int64
	var mu sync.Mutex
	var winners int
	for i := 1; i <= writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, err := exclusion.Acquire(ctx, dir, exclusion.Update)
			if err != nil {
				t.Errorf("writer %d acquire: %v", i, err)
				return
			}
			defer h.Close() //nolint:errcheck // test cleanup
			res, err := Publish(ctx, dir, h, stale, testSelection(prev.Generation+1, fmt.Sprintf("2.0.%d", i)))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && res.State == CommitConfirmed:
				committed2++
				winners++
			case errors.Is(err, ErrConflict):
				conflicts2++
			default:
				t.Errorf("writer %d publish = (%+v, %v); want committed or conflict", i, res, err)
			}
		}(i)
	}
	wg.Wait()
	if committed2 != 1 || conflicts2 != writers-1 {
		t.Fatalf("stale race: committed=%d conflicts=%d; want 1/%d", committed2, conflicts2, writers-1)
	}
	_ = winners
	final2, rerr := activation.ReadSelection(filepath.Join(dir, FileName))
	if rerr != nil || final2.Generation != prev.Generation+1 {
		t.Fatalf("final selection after race = (%+v, %v); want generation %d", final2, rerr, prev.Generation+1)
	}
	assertNoStagingLeftovers(t, dir)
}
