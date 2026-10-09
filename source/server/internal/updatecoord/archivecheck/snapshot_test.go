package archivecheck

// Byte-binding regression tests: PreflightFile must hash and parse the
// exact same bytes — one bounded immutable in-memory snapshot — so the
// verified-receipt digest always binds the parsed archive, even when the
// underlying source is mutable (a file changed on disk between passes, or
// a ReaderAt serving different bytes per call, as the old two-pass design
// was shown to accept).

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildStoredZipVariant builds a Store-method zip fixture (deterministic
// lengths) whose agent member carries agent and whose optional README
// carries readme.
func buildStoredZipVariant(t *testing.T, agent, readme string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries := []zipEntry{
		{name: testRoot + "/", mode: fs.ModeDir | 0o755},
		{name: testRoot + "/bin/", mode: fs.ModeDir | 0o755},
		{name: testRoot + "/bin/cercano", body: agent, mode: 0o755},
		{name: testRoot + "/bin/cercano-cli", body: cliBody, mode: 0o755},
		{name: testRoot + "/README.txt", body: readme, mode: 0o644},
	}
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Store}
		h.SetMode(e.mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatalf("fixture zip header %q: %v", e.name, err)
		}
		if _, err = w.Write([]byte(e.body)); err != nil {
			t.Fatalf("fixture zip body %q: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// swappingReaderAt is the controlled mutable ReaderAt: it serves `first`
// until that archive has been fully consumed once (the snapshot pass) and
// `second` on every later call (where a second-pass parse would read).
type swappingReaderAt struct {
	first, second []byte
	served        int64
}

func (s *swappingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	src := s.first
	if s.served >= int64(len(s.first)) {
		src = s.second
	}
	n := copy(p, src[off:])
	s.served += int64(n)
	return n, nil
}

// recordingReaderAt counts calls and the highest exclusive end offset ever
// requested, proving reads stay inside the advertised length.
type recordingReaderAt struct {
	ra     io.ReaderAt
	calls  int
	maxEnd int64
}

func (r *recordingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	r.calls++
	if end := off + int64(len(p)); end > r.maxEnd {
		r.maxEnd = end
	}
	return r.ra.ReadAt(p, off)
}

// cancelingReaderAt cancels cancel on its first call and otherwise serves ra.
type cancelingReaderAt struct {
	ra     io.ReaderAt
	cancel context.CancelFunc
}

func (c *cancelingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	c.cancel()
	return c.ra.ReadAt(p, off)
}

func memberDigest(t *testing.T, body string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// TestPreflightSnapshotHashesAndParsesSameImmutableBytes drives the exact
// composition PreflightFile now uses with the controlled ReaderAt that
// reproduced the old two-pass defect: archive A while being snapshotted, a
// different valid same-length archive B afterwards. The manifest must bind
// A's bytes, and B — though it parses fine on its own — must never be
// accepted under A's receipt digest nor appear in the parsed result.
func TestPreflightSnapshotHashesAndParsesSameImmutableBytes(t *testing.T) {
	a := buildStoredZipVariant(t, "agent-binary-bytes", strings.Repeat("r", 200<<10))
	b := buildStoredZipVariant(t, "bgent-binary-bytes", strings.Repeat("r", 200<<10))
	if len(a) != len(b) {
		t.Fatalf("Store-method fixtures differ in length: %d vs %d", len(a), len(b))
	}
	if len(a) <= 64<<10 {
		t.Fatalf("fixture too small to exercise chunked snapshot reads: %d", len(a))
	}
	// B alone is a fully valid archive for the same layout.
	bOpts := zipOpts()
	if _, err := Check(context.Background(), Archive{ReaderAt: bytes.NewReader(b), Size: int64(len(b))}, bOpts); err != nil {
		t.Fatalf("fixture B must be a valid archive: %v", err)
	}

	aSum := sha256.Sum256(a)
	bSum := sha256.Sum256(b)
	aOpts := zipOpts()
	aOpts.Identity = &Identity{Length: int64(len(a)), SHA256: aSum[:]}
	bOpts.Identity = &Identity{Length: int64(len(b)), SHA256: bSum[:]}

	rec := &recordingReaderAt{ra: &swappingReaderAt{first: a, second: b}}
	snap, man, err := preflightSnapshot(context.Background(), rec, int64(len(a)), aOpts)
	if err != nil {
		t.Fatalf("preflight of the snapshotted archive A failed: %v", err)
	}
	if len(snap) != len(a) {
		t.Fatalf("snapshot is %d bytes, want the archive's %d", len(snap), len(a))
	}
	for _, m := range man.Members {
		if m.Name == testRoot+"/bin/cercano" && m.SHA256 != memberDigest(t, "agent-binary-bytes") {
			t.Fatalf("parsed member binds bytes other than the snapshotted archive A: %+v", m)
		}
	}
	if rec.maxEnd != int64(len(a)) {
		t.Fatalf("snapshot reads reached past the advertised length: max end %d, want %d", rec.maxEnd, len(a))
	}

	// The same mutable source serving B while being snapshotted must be
	// refused under A's receipt: the digest binds the parsed bytes.
	rec = &recordingReaderAt{ra: &swappingReaderAt{first: b, second: a}}
	_, _, err = preflightSnapshot(context.Background(), rec, int64(len(a)), aOpts)
	wantError(t, err, "do not match the SHA-256")
}

// TestPreflightSnapshotBindsTarGzToo repeats the mutable-source proof for
// the TarGz format. B is a valid archive but need not share A's length:
// after the one snapshot it is never read, and a valid B is exactly what a
// regressed second-pass parse would silently accept.
func TestPreflightSnapshotBindsTarGzToo(t *testing.T) {
	build := func(agent string) []byte {
		entries := []tarEntry{
			{name: testRoot + "/", typ: tar.TypeDir, mode: 0o755},
			{name: testRoot + "/bin/", typ: tar.TypeDir, mode: 0o755},
			{name: testRoot + "/bin/cercano", body: agent, typ: tar.TypeReg, mode: 0o755},
			{name: testRoot + "/bin/cercano-cli", body: cliBody, typ: tar.TypeReg, mode: 0o755},
		}
		return buildTarGz(t, entries)
	}
	a := build("agent-binary-bytes")
	b := build("bgent-binary-bytes")
	bOpts := tarOpts()
	if _, err := Check(context.Background(), Archive{Reader: bytes.NewReader(b)}, bOpts); err != nil {
		t.Fatalf("fixture B must be a valid archive: %v", err)
	}

	aSum := sha256.Sum256(a)
	aOpts := tarOpts()
	aOpts.Identity = &Identity{Length: int64(len(a)), SHA256: aSum[:]}
	rec := &recordingReaderAt{ra: &swappingReaderAt{first: a, second: b}}
	snap, man, err := preflightSnapshot(context.Background(), rec, int64(len(a)), aOpts)
	if err != nil {
		t.Fatalf("preflight of the snapshotted archive A failed: %v", err)
	}
	if len(snap) != len(a) {
		t.Fatalf("snapshot is %d bytes, want the archive's %d", len(snap), len(a))
	}
	for _, m := range man.Members {
		if m.Name == testRoot+"/bin/cercano" && m.SHA256 != memberDigest(t, "agent-binary-bytes") {
			t.Fatalf("parsed member binds bytes other than the snapshotted archive A: %+v", m)
		}
	}
	if rec.maxEnd != int64(len(a)) {
		t.Fatalf("snapshot reads reached past the advertised length: max end %d, want %d", rec.maxEnd, len(a))
	}
}

// TestSnapshotRefusesOversizedAdvertisedLengthBeforeReadAt proves the hard
// finite allocation ceiling is checked BEFORE any allocation or read: an
// advertised length above it is refused without a single ReadAt call, so
// caller bounds near math.MaxInt64 can never become an allocation.
func TestSnapshotRefusesOversizedAdvertisedLengthBeforeReadAt(t *testing.T) {
	for _, size := range []int64{snapshotBytesCeiling + 1, math.MaxInt64} {
		rec := &recordingReaderAt{ra: bytes.NewReader(nil)}
		_, err := snapshotReaderAt(context.Background(), rec, size)
		wantError(t, err, "exceeds the immutable-snapshot ceiling")
		if rec.calls != 0 {
			t.Fatalf("advertised length %d: %d ReadAt calls before the ceiling refusal", size, rec.calls)
		}

		// The same refusal holds through the full preflight composition.
		rec = &recordingReaderAt{ra: bytes.NewReader(nil)}
		_, _, err = preflightSnapshot(context.Background(), rec, size, zipOpts())
		wantError(t, err, "exceeds the immutable-snapshot ceiling")
		if rec.calls != 0 {
			t.Fatalf("advertised length %d via preflight: %d ReadAt calls before the ceiling refusal", size, rec.calls)
		}
	}
}

// TestSnapshotReadsExactAdvertisedLengthOnly proves the snapshot consumes
// exactly the advertised length: every byte is read once, no byte past the
// length is ever requested, and a source that advertises more than it
// provides (truncated underneath the stat) is refused.
func TestSnapshotReadsExactAdvertisedLengthOnly(t *testing.T) {
	data := buildStoredZipVariant(t, "agent-binary-bytes", strings.Repeat("r", 200<<10))
	rec := &recordingReaderAt{ra: bytes.NewReader(data)}
	snap, err := snapshotReaderAt(context.Background(), rec, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snap, data) {
		t.Fatal("snapshot bytes differ from the source bytes")
	}
	if rec.maxEnd != int64(len(data)) {
		t.Fatalf("reads reached past the advertised length: max end %d, want %d", rec.maxEnd, len(data))
	}

	// A truncated source: it provides ten bytes fewer than advertised.
	short := data[:len(data)-10]
	_, err = snapshotReaderAt(context.Background(), bytes.NewReader(short), int64(len(data)))
	wantError(t, err, "truncated input is refused")
}

// TestSnapshotHonorsCancellation proves a canceled context fails the
// snapshot closed: before any read when already canceled, and mid-read
// when cancellation lands between bounded chunks.
func TestSnapshotHonorsCancellation(t *testing.T) {
	data := buildStoredZipVariant(t, "agent-binary-bytes", strings.Repeat("r", 200<<10))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := &recordingReaderAt{ra: bytes.NewReader(data)}
	_, err := snapshotReaderAt(ctx, rec, int64(len(data)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled context: got %v, want context.Canceled", err)
	}
	if rec.calls != 0 {
		t.Fatalf("pre-canceled context still made %d ReadAt calls", rec.calls)
	}

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	_, err = snapshotReaderAt(ctx, &cancelingReaderAt{ra: bytes.NewReader(data), cancel: cancel}, int64(len(data)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-snapshot cancellation: got %v, want context.Canceled", err)
	}
}

// TestPreflightFileRefusesEmptyArchive proves a stat length of 0 is legal
// input to the snapshot step (no allocation, no read) yet still fails
// closed in the format checks — even under a receipt that technically
// authorizes zero bytes.
func TestPreflightFileRefusesEmptyArchive(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := PreflightFile(context.Background(), empty, tarOpts())
	wantError(t, err, "not a valid gzip archive")
	_, err = PreflightFile(context.Background(), empty, zipOpts())
	wantError(t, err, "positive Archive.Size")

	zeroSum := sha256.Sum256(nil)
	opts := tarOpts()
	opts.Identity = &Identity{Length: 0, SHA256: zeroSum[:]}
	_, err = PreflightFile(context.Background(), empty, opts)
	wantError(t, err, "not a valid gzip archive")
}
