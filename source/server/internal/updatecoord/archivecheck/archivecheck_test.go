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
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures mirror the actual layout produced by scripts/build-macos-unsigned.sh:
// one version-named root directory containing bin/cercano and bin/cercano-cli
// (required) plus LICENSE and README.txt (approved optional members).
const (
	testRoot    = "cercano-1.2.3-darwin-arm64-unsigned"
	agentBody   = "agent-binary-bytes"
	cliBody     = "cli-binary-bytes"
	licenseBody = "license-body"
	readmeBody  = "readme-body"
)

func testLayout() Layout {
	return Layout{
		Root:     testRoot,
		Required: []string{"bin/cercano", "bin/cercano-cli"},
		Optional: []string{"LICENSE", "README.txt"},
	}
}

func testBounds() Bounds {
	return Bounds{MaxMembers: 16, MaxCompressedBytes: 1 << 20, MaxUncompressedBytes: 1 << 20, MaxMemberBytes: 512 << 10}
}

type tarEntry struct {
	name, body, link string
	typ              byte
	mode             int64
}

func writeTarEntries(t *testing.T, tw *tar.Writer, entries []tarEntry) {
	t.Helper()
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, Typeflag: e.typ, Linkname: e.link}
		if e.typ == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		} else {
			hdr.Devmajor, hdr.Devminor = 1, 3
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("fixture tar header %q: %v", e.name, err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatalf("fixture tar body %q: %v", e.name, err)
		}
	}
}

func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	writeTarEntries(t, tw, entries)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	writeTarEntries(t, tw, entries)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func gzipBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var gz bytes.Buffer
	gzw := gzip.NewWriter(&gz)
	if _, err := gzw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}
	return gz.Bytes()
}

type zipEntry struct {
	name string
	body string
	mode fs.FileMode
}

func buildZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		h.SetMode(e.mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatalf("fixture zip header %q: %v", e.name, err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatalf("fixture zip body %q: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func checkTarGzFixture(t *testing.T, data []byte, opts Options) (*Manifest, error) {
	t.Helper()
	return Check(context.Background(), Archive{Reader: bytes.NewReader(data)}, opts)
}

func checkZipFixture(t *testing.T, data []byte, opts Options) (*Manifest, error) {
	t.Helper()
	return Check(context.Background(), Archive{ReaderAt: bytes.NewReader(data), Size: int64(len(data))}, opts)
}

func tarOpts() Options {
	return Options{Format: TarGz, Layout: testLayout(), Bounds: testBounds()}
}

func zipOpts() Options {
	return Options{Format: Zip, Layout: testLayout(), Bounds: testBounds()}
}

func wantError(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got success", contains)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("expected error containing %q, got %q", contains, err.Error())
	}
}

func goodTarEntries() []tarEntry {
	return []tarEntry{
		{name: testRoot + "/", typ: tar.TypeDir, mode: 0o755},
		{name: testRoot + "/bin/", typ: tar.TypeDir, mode: 0o755},
		{name: testRoot + "/bin/cercano", body: agentBody, typ: tar.TypeReg, mode: 0o755},
		{name: testRoot + "/bin/cercano-cli", body: cliBody, typ: tar.TypeReg, mode: 0o755},
		{name: testRoot + "/LICENSE", body: licenseBody, typ: tar.TypeReg, mode: 0o644},
		{name: testRoot + "/README.txt", body: readmeBody, typ: tar.TypeReg, mode: 0o644},
	}
}

func goodZipEntries() []zipEntry {
	return []zipEntry{
		{name: testRoot + "/", mode: fs.ModeDir | 0o755},
		{name: testRoot + "/bin/", mode: fs.ModeDir | 0o755},
		{name: testRoot + "/bin/cercano", body: agentBody, mode: 0o755},
		{name: testRoot + "/bin/cercano-cli", body: cliBody, mode: 0o755},
		{name: testRoot + "/LICENSE", body: licenseBody, mode: 0o644},
		{name: testRoot + "/README.txt", body: readmeBody, mode: 0o644},
	}
}

func TestGoodTarGzMatchesBuildScriptLayout(t *testing.T) {
	data := buildTarGz(t, goodTarEntries())
	man, err := checkTarGzFixture(t, data, tarOpts())
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ name, body string }{
		{testRoot + "/bin/cercano", agentBody},
		{testRoot + "/bin/cercano-cli", cliBody},
		{testRoot + "/LICENSE", licenseBody},
		{testRoot + "/README.txt", readmeBody},
	}
	if len(man.Members) != len(want) {
		t.Fatalf("manifest has %d members, want %d", len(man.Members), len(want))
	}
	for i, w := range want {
		m := man.Members[i]
		sum := sha256.Sum256([]byte(w.body))
		if m.Name != w.name || m.Size != int64(len(w.body)) || m.SHA256 != hex.EncodeToString(sum[:]) {
			t.Errorf("member %d = %+v, want name=%q size=%d sha256=%s",
				i, m, w.name, len(w.body), hex.EncodeToString(sum[:]))
		}
	}
}

func TestGoodZipWithAndWithoutDirectoryEntries(t *testing.T) {
	withDirs := goodZipEntries()
	if _, err := checkZipFixture(t, buildZip(t, withDirs), zipOpts()); err != nil {
		t.Fatal(err)
	}
	noDirs := []zipEntry{}
	for _, e := range withDirs {
		if !strings.HasSuffix(e.name, "/") {
			noDirs = append(noDirs, e)
		}
	}
	man, err := checkZipFixture(t, buildZip(t, noDirs), zipOpts())
	if err != nil {
		t.Fatal(err)
	}
	if len(man.Members) != 4 {
		t.Fatalf("manifest members = %d, want 4", len(man.Members))
	}
}

func TestOptionalMembersMayBeAbsent(t *testing.T) {
	entries := []tarEntry{}
	for _, e := range goodTarEntries() {
		switch {
		case strings.HasSuffix(e.name, "/"),
			strings.HasSuffix(e.name, "bin/cercano"),
			strings.HasSuffix(e.name, "bin/cercano-cli"):
			entries = append(entries, e)
		}
	}
	if _, err := checkTarGzFixture(t, buildTarGz(t, entries), tarOpts()); err != nil {
		t.Fatal(err)
	}
}

// TestEmptyRootLayoutAccepted is the regression for Layout.Root == "": the
// documented no-root layout, whose required members appear at the
// archive's top level, must work — and a rooted archive must not satisfy
// it.
func TestEmptyRootLayoutAccepted(t *testing.T) {
	noRoot := Layout{Root: "", Required: []string{"bin/cercano", "bin/cercano-cli"}}
	opts := Options{Format: TarGz, Layout: noRoot, Bounds: testBounds()}
	entries := []tarEntry{
		{name: "bin/", typ: tar.TypeDir, mode: 0o755},
		{name: "bin/cercano", body: agentBody, typ: tar.TypeReg, mode: 0o755},
		{name: "bin/cercano-cli", body: cliBody, typ: tar.TypeReg, mode: 0o755},
	}
	man, err := checkTarGzFixture(t, buildTarGz(t, entries), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(man.Members) != 2 {
		t.Fatalf("manifest members = %d, want 2", len(man.Members))
	}

	zopts := Options{Format: Zip, Layout: noRoot, Bounds: testBounds()}
	zentries := []zipEntry{
		{name: "bin/", mode: fs.ModeDir | 0o755},
		{name: "bin/cercano", body: agentBody, mode: 0o755},
		{name: "bin/cercano-cli", body: cliBody, mode: 0o755},
	}
	if _, err = checkZipFixture(t, buildZip(t, zentries), zopts); err != nil {
		t.Fatal(err)
	}

	// A rooted archive does not satisfy the no-root layout.
	_, err = checkTarGzFixture(t, buildTarGz(t, goodTarEntries()), opts)
	wantError(t, err, "allowed only for needed parent paths")
}

func TestMissingRequiredMemberRejected(t *testing.T) {
	entries := []tarEntry{}
	for _, e := range goodTarEntries() {
		if e.name != testRoot+"/bin/cercano-cli" {
			entries = append(entries, e)
		}
	}
	_, err := checkTarGzFixture(t, buildTarGz(t, entries), tarOpts())
	wantError(t, err, "missing required member")
}

func TestUnknownMemberRejectedNotDropped(t *testing.T) {
	_, err := checkTarGzFixture(t, buildTarGz(t, append(goodTarEntries(), tarEntry{
		name: testRoot + "/evil.sh", body: "x", typ: tar.TypeReg, mode: 0o755,
	})), tarOpts())
	wantError(t, err, "not in the expected layout")

	_, err = checkZipFixture(t, buildZip(t, append(goodZipEntries(), zipEntry{
		name: testRoot + "/evil.dll", body: "x", mode: 0o755,
	})), zipOpts())
	wantError(t, err, "not in the expected layout")
}

func TestUnneededDirectoryEntryRejected(t *testing.T) {
	_, err := checkTarGzFixture(t, buildTarGz(t, append(goodTarEntries(), tarEntry{
		name: testRoot + "/extra/", typ: tar.TypeDir, mode: 0o755,
	})), tarOpts())
	wantError(t, err, "allowed only for needed parent paths")

	_, err = checkZipFixture(t, buildZip(t, append(goodZipEntries(), zipEntry{
		name: testRoot + "/extra/", mode: fs.ModeDir | 0o755,
	})), zipOpts())
	wantError(t, err, "allowed only for needed parent paths")
}

func TestRequiredMemberThatIsADirectoryRejected(t *testing.T) {
	entries := []tarEntry{}
	for _, e := range goodTarEntries() {
		if e.name == testRoot+"/bin/cercano" {
			e = tarEntry{name: testRoot + "/bin/cercano/", typ: tar.TypeDir, mode: 0o755}
		}
		entries = append(entries, e)
	}
	_, err := checkTarGzFixture(t, buildTarGz(t, entries), tarOpts())
	wantError(t, err, testRoot+"/bin/cercano")
}

func TestUnsafeTarEntryTypesRejected(t *testing.T) {
	for _, typ := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeFifo, tar.TypeChar, tar.TypeBlock} {
		entries := append(goodTarEntries(), tarEntry{
			name: testRoot + "/bin/evil", typ: typ, mode: 0o644, link: "/etc/passwd",
		})
		_, err := checkTarGzFixture(t, buildTarGz(t, entries), tarOpts())
		wantError(t, err, "not a regular file or directory")
	}
	// A GNU sparse member is refused by archive/tar itself before the
	// entry-type switch ever sees it: refused either way, fail-closed.
	entries := append(goodTarEntries(), tarEntry{
		name: testRoot + "/bin/evil", typ: tar.TypeGNUSparse, mode: 0o644, link: "/etc/passwd",
	})
	_, err := checkTarGzFixture(t, buildTarGz(t, entries), tarOpts())
	wantError(t, err, "tar stream rejected")
}

func TestUnsafeZipEntryTypesRejected(t *testing.T) {
	for _, mode := range []fs.FileMode{
		fs.ModeSymlink | 0o777, fs.ModeNamedPipe | 0o600, fs.ModeDevice | 0o600,
	} {
		entries := append(goodZipEntries(), zipEntry{name: testRoot + "/bin/evil", mode: mode})
		_, err := checkZipFixture(t, buildZip(t, entries), zipOpts())
		wantError(t, err, "unsafe entry type")
	}
}

func TestSetuidSetgidModesRejected(t *testing.T) {
	for _, mode := range []int64{0o4755, 0o2755} {
		entries := []tarEntry{}
		for _, e := range goodTarEntries() {
			if e.typ == tar.TypeReg {
				e.mode = mode
			}
			entries = append(entries, e)
		}
		_, err := checkTarGzFixture(t, buildTarGz(t, entries), tarOpts())
		wantError(t, err, "setuid/setgid mode bits are refused")
	}
	// Note: archive/zip silently drops setuid/setgid bits on write, so a
	// stdlib-built zip fixture cannot reach the equivalent zip-side mode
	// check; it remains as defense-in-depth for real-world archives.
}

func TestUnsafeMemberNamesRejected(t *testing.T) {
	bad := []string{
		"", "/etc/passwd", "../evil", testRoot + "/bin/../../evil", "root\\bin\\x",
		"C:evil", testRoot + "/file:ads", testRoot + "/nul.txt", testRoot + "/bin/con",
		testRoot + "/x.", testRoot + "/x ", testRoot + "/\x01x", testRoot + "//bin",
		"./" + testRoot + "/bin/cercano", testRoot + "/com1",
	}
	for _, name := range bad {
		entries := append(goodTarEntries(), tarEntry{name: name, body: "x", typ: tar.TypeReg, mode: 0o644})
		_, err := checkTarGzFixture(t, buildTarGz(t, entries), tarOpts())
		wantError(t, err, fmt.Sprintf("%q", name))
	}
}

func TestDuplicateMemberRejected(t *testing.T) {
	entries := append(goodTarEntries(), tarEntry{
		name: testRoot + "/LICENSE", body: "dup", typ: tar.TypeReg, mode: 0o644,
	})
	_, err := checkTarGzFixture(t, buildTarGz(t, entries), tarOpts())
	wantError(t, err, "duplicate member")
}

func TestCaseFoldCollisionRejected(t *testing.T) {
	entries := append(goodTarEntries(), tarEntry{
		name: testRoot + "/bin/CERCANO", body: "collide", typ: tar.TypeReg, mode: 0o644,
	})
	_, err := checkTarGzFixture(t, buildTarGz(t, entries), tarOpts())
	wantError(t, err, "collides case-insensitively")
}

func TestTruncatedTarGzRejected(t *testing.T) {
	data := buildTarGz(t, goodTarEntries())
	_, err := checkTarGzFixture(t, data[:len(data)-40], tarOpts())
	wantError(t, err, "tar stream rejected")
}

func TestTruncatedMemberContentRejected(t *testing.T) {
	// A tar member whose header declares more content than the stream carries.
	raw := buildTar(t, []tarEntry{
		{name: testRoot + "/bin/cercano", body: strings.Repeat("a", 200), typ: tar.TypeReg, mode: 0o755},
		{name: testRoot + "/bin/cercano-cli", body: "x", typ: tar.TypeReg, mode: 0o755},
	})
	data := gzipBytes(t, raw[:512+150])
	_, err := checkTarGzFixture(t, data, tarOpts())
	wantError(t, err, "reading content failed")
}

func TestTruncatedZipRejected(t *testing.T) {
	data := buildZip(t, goodZipEntries())
	_, err := checkZipFixture(t, data[:len(data)-30], zipOpts())
	wantError(t, err, "zip central directory rejected")
}

// Direct readMember unit tests: the archive/tar and archive/zip readers
// already refuse lying header sizes at the container layer, so these exercise
// the package's own while-reading enforcement with plain readers.

func newTestValidator(opts Options) *validator {
	v := newValidator(opts)
	v.begin() // one member being read
	return v
}

func TestReadMemberRejectsHeaderLyingAboutDeclaredSize(t *testing.T) {
	v := newTestValidator(tarOpts())
	_, err := v.readMember(context.Background(), strings.NewReader("abcd"), "m", 10)
	wantError(t, err, "header declares 10 bytes but content is 4 bytes")
}

func TestReadMemberBoundsEnforcedWhileReadingDecompressedBytes(t *testing.T) {
	// Declared size beyond the per-member bound is refused before reading.
	opts := tarOpts()
	opts.Bounds.MaxMemberBytes = 100
	v := newTestValidator(opts)
	_, err := v.readMember(context.Background(), strings.NewReader(strings.Repeat("a", 200)), "m", 200)
	wantError(t, err, "declared size 200 exceeds the member or total uncompressed bounds")

	// Declared size beyond the remaining total budget is refused before
	// reading, so totals can never cross the bound mid-read.
	opts = tarOpts()
	opts.Bounds.MaxMemberBytes = 4096
	opts.Bounds.MaxUncompressedBytes = 150
	v = newTestValidator(opts)
	if _, err = v.readMember(context.Background(), strings.NewReader(strings.Repeat("a", 100)), "m1", 100); err != nil {
		t.Fatal(err)
	}
	v.begin()
	_, err = v.readMember(context.Background(), strings.NewReader(strings.Repeat("a", 100)), "m2", 100)
	wantError(t, err, "declared size 100 exceeds the member or total uncompressed bounds")

	// A reader that lies by never ending is cut off mid-read by the
	// while-reading per-member bound, not just by the declared size.
	infinite := func(p []byte) (int, error) { copy(p, bytes.Repeat([]byte("a"), len(p))); return len(p), nil }
	opts = tarOpts()
	opts.Bounds.MaxMemberBytes = 10
	v = newTestValidator(opts)
	_, err = v.readMember(context.Background(), readerFunc(infinite), "m", 7)
	wantError(t, err, "content exceeds the member or total uncompressed bounds")
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestReadMemberHonorsCancellationWhileReading(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v := newTestValidator(tarOpts())
	_, err := v.readMember(ctx, strings.NewReader("content"), "m", 7)
	if err == nil || !(errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled")) {
		t.Fatalf("expected cancellation error while reading, got %v", err)
	}
}

func TestOversizedDeclaredMemberRejectedBeforeReading(t *testing.T) {
	entries := []tarEntry{
		{name: testRoot + "/bin/cercano", body: strings.Repeat("a", 64*1024), typ: tar.TypeReg, mode: 0o755},
		{name: testRoot + "/bin/cercano-cli", body: "x", typ: tar.TypeReg, mode: 0o755},
	}
	opts := tarOpts()
	opts.Bounds.MaxUncompressedBytes = 32 * 1024
	_, err := checkTarGzFixture(t, buildTarGz(t, entries), opts)
	wantError(t, err, "exceeds the member or total uncompressed bounds")
}

func TestCompressedBoundRejectedWhileReadingGzipBomb(t *testing.T) {
	entries := []tarEntry{
		{name: testRoot + "/bin/cercano", body: strings.Repeat("\x00", 4<<20), typ: tar.TypeReg, mode: 0o755},
		{name: testRoot + "/bin/cercano-cli", body: "x", typ: tar.TypeReg, mode: 0o755},
	}
	opts := tarOpts()
	opts.Bounds.MaxMemberBytes = 16 << 20
	opts.Bounds.MaxUncompressedBytes = 16 << 20
	opts.Bounds.MaxCompressedBytes = 2 * 1024
	_, err := checkTarGzFixture(t, buildTarGz(t, entries), opts)
	wantError(t, err, "MaxCompressedBytes")
}

func TestMemberCountBoundRejected(t *testing.T) {
	opts := tarOpts()
	opts.Bounds.MaxMembers = 3
	_, err := checkTarGzFixture(t, buildTarGz(t, goodTarEntries()), opts)
	wantError(t, err, "exceeds MaxMembers 3")
}

// cancelAfterReader cancels ctx once at least `after` bytes have been read,
// so a Check that keeps reading past that point fails closed mid-stream.
type cancelAfterReader struct {
	r      io.Reader
	after  int64
	n      int64
	ctx    context.Context
	cancel context.CancelFunc
}

func (c *cancelAfterReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n >= c.after {
		c.cancel()
	}
	return n, err
}

func TestCancellationHonoredWhileReadingDecompressedBytes(t *testing.T) {
	data := buildTarGz(t, goodTarEntries())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &cancelAfterReader{r: bytes.NewReader(data), after: 16, ctx: ctx, cancel: cancel}
	_, err := Check(ctx, Archive{Reader: r}, tarOpts())
	if err == nil || !(errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled")) {
		t.Fatalf("expected context cancellation error, got %v", err)
	}
}

func TestMissingRequiredMemberReportedPerFormat(t *testing.T) {
	entries := []zipEntry{
		{name: testRoot + "/", mode: fs.ModeDir | 0o755},
		{name: testRoot + "/bin/", mode: fs.ModeDir | 0o755},
		{name: testRoot + "/bin/cercano", body: agentBody, mode: 0o755},
	}
	_, err := checkZipFixture(t, buildZip(t, entries), zipOpts())
	wantError(t, err, "missing required member")
}

func TestFileThenDirectoryConflictRejected(t *testing.T) {
	entries := append(goodTarEntries(), tarEntry{
		name: testRoot + "/LICENSE/", typ: tar.TypeDir, mode: 0o755,
	})
	_, err := checkTarGzFixture(t, buildTarGz(t, entries), tarOpts())
	wantError(t, err, "collides case-insensitively")
}

func TestGzipTrailerCRCorruptedRejected(t *testing.T) {
	data := buildTarGz(t, goodTarEntries())
	// The gzip trailer's CRC-32 of the decompressed bytes is the 8th byte
	// from the end; flipping it must fail the whole archive even though
	// every tar entry already parsed successfully.
	data[len(data)-8] ^= 0xff
	_, err := checkTarGzFixture(t, data, tarOpts())
	wantError(t, err, "gzip stream rejected while verifying its trailer")
}

func TestGzipTrailerTruncatedRejected(t *testing.T) {
	data := buildTarGz(t, goodTarEntries())
	_, err := checkTarGzFixture(t, data[:len(data)-8], tarOpts())
	wantError(t, err, "rejected")
}

func TestSmuggledEntryAfterTarEndMarkerRejected(t *testing.T) {
	// A well-formed tar followed by a smuggled entry written PAST the
	// two-block end-of-archive marker: archive/tar stops at the marker, so
	// only the tail tracker and the trailer drain can catch the hidden
	// payload. The smuggled header must be refused, not silently skipped.
	raw := buildTar(t, goodTarEntries())
	smuggled := &tar.Header{Name: testRoot + "/bin/smuggled", Mode: 0o755, Size: int64(len("hidden-payload")), Typeflag: tar.TypeReg}
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	if err := tw.WriteHeader(smuggled); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("hidden-payload")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	full := append(append([]byte{}, raw...), b.Bytes()...)
	_, err := checkTarGzFixture(t, gzipBytes(t, full), tarOpts())
	wantError(t, err, "end-of-archive marker")
}

func TestZipMemberChecksumErrorEnforced(t *testing.T) {
	// A Store-method member so corrupting one data byte deterministically
	// surfaces as a CRC-32 checksum failure when the member is streamed.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range goodZipEntries() {
		h := &zip.FileHeader{Name: e.name, Method: zip.Store}
		h.SetMode(e.mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	i := bytes.Index(data, []byte(agentBody))
	if i < 0 {
		t.Fatal("agent body not found in stored zip fixture")
	}
	data[i] ^= 0xff
	_, err := checkZipFixture(t, data, zipOpts())
	wantError(t, err, "checksum")
}

func TestPreflightFileRejectsNonRegularInputs(t *testing.T) {
	dir := t.TempDir()

	// A symlink to a perfectly valid archive must be refused: the input
	// itself must be the verified regular file.
	good := filepath.Join(dir, "good.tar.gz")
	if err := os.WriteFile(good, buildTarGz(t, goodTarEntries()), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.tar.gz")
	if err := os.Symlink(good, link); err != nil {
		t.Skipf("cannot create a symlink on this platform: %v", err)
	}
	_, err := PreflightFile(context.Background(), link, tarOpts())
	wantError(t, err, "is a symlink")

	_, err = PreflightFile(context.Background(), dir, tarOpts())
	wantError(t, err, "not a regular file")

	_, err = PreflightFile(context.Background(), filepath.Join(dir, "absent.tar.gz"), tarOpts())
	wantError(t, err, "absent.tar.gz")
}

func TestPreflightFileBindsBytesToVerifiedReceipt(t *testing.T) {
	dir := t.TempDir()

	tarData := buildTarGz(t, goodTarEntries())
	tarPath := filepath.Join(dir, "cercano.tar.gz")
	if err := os.WriteFile(tarPath, tarData, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(tarData)
	identity := &Identity{Length: int64(len(tarData)), SHA256: sum[:]}

	opts := tarOpts()
	opts.Identity = identity
	man, err := PreflightFile(context.Background(), tarPath, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(man.Members) != 4 {
		t.Fatalf("manifest members = %d, want 4", len(man.Members))
	}

	zipData := buildZip(t, goodZipEntries())
	zipPath := filepath.Join(dir, "cercano.zip")
	if err := os.WriteFile(zipPath, zipData, 0o600); err != nil {
		t.Fatal(err)
	}
	zsum := sha256.Sum256(zipData)
	zopts := zipOpts()
	zopts.Identity = &Identity{Length: int64(len(zipData)), SHA256: zsum[:]}
	if _, err = PreflightFile(context.Background(), zipPath, zopts); err != nil {
		t.Fatal(err)
	}

	// Wrong hash: the bytes are not the receipt's bytes.
	bad := tarOpts()
	bad.Identity = &Identity{Length: int64(len(tarData)), SHA256: make([]byte, sha256.Size)}
	_, err = PreflightFile(context.Background(), tarPath, bad)
	wantError(t, err, "do not match the SHA-256")

	// Wrong length.
	bad = tarOpts()
	bad.Identity = &Identity{Length: int64(len(tarData)) + 1, SHA256: sum[:]}
	_, err = PreflightFile(context.Background(), tarPath, bad)
	wantError(t, err, "verified receipt authorizes")

	// Malformed identity: not a raw 32-byte digest.
	bad = tarOpts()
	bad.Identity = &Identity{Length: int64(len(tarData)), SHA256: sum[:31]}
	_, err = PreflightFile(context.Background(), tarPath, bad)
	wantError(t, err, "raw 32-byte SHA-256")
}
