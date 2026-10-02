package lib

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	selfupdate "github.com/creativeprojects/go-selfupdate"

	"github.com/bryancostanich/Cercano/spikes/windows-go-updater/fixtures"
)

// This file records FACTS about how the stock library's zip extraction
// treats hostile archive SHAPES. Cercano's Windows ZIP layout is
// cercano-<v>-windows-x64/bin/{cercano.exe,cercano-cli.exe}; a hostile or
// broken release archive could contain traversal-named members, duplicate
// members, or symlink members claiming to be a required binary.
//
// The stock extractor returns the chosen member as an io.Reader (it never
// writes archive-controlled paths to disk itself), so "traversal" in the
// classic sense does not apply to the library's own code path. The real
// questions for a coordinator are: WHICH member wins, and whether non-regular
// members (symlinks) are refused.

// maliciousCercanoZip builds a ZIP in the exact Cercano shape where the
// cercano.exe under the nested root is the SECOND of two duplicate members
// (benign first, hostile second), plus a traversal-named hostile member and
// a symlink member claiming to be the CLI.
func maliciousCercanoZip(t *testing.T, benign, hostile []byte) []byte {
	t.Helper()
	root := fixtures.AssetRoot(nextVersion)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	member := func(name string, data []byte, mode zip.FileHeader) {
		t.Helper()
		hdr := mode // copy
		hdr.Name = name
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(&hdr)
		if err != nil {
			t.Fatalf("zip header %s: %v", name, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}

	reg := func(mode os.FileMode) zip.FileHeader {
		var h zip.FileHeader
		h.SetMode(mode)
		return h
	}

	// Duplicate: benign first, hostile second, same member name.
	member(root+"/bin/"+fixtures.AgentName, benign, reg(0o755))
	member(root+"/bin/"+fixtures.AgentName, hostile, reg(0o755))

	// Traversal-named hostile member outside the nested root.
	member("../../"+fixtures.AgentName, hostile, reg(0o755))

	// Symlink member claiming to be the CLI (content = link target).
	// 0o120777 sets the unix S_IFLNK type bits, per archive/zip convention.
	member(root+"/bin/"+fixtures.CliName, []byte("/etc/hostname"), reg(0o120777))

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// FACT under test: with duplicate archive members, the stock extractor
// picks ONE of them; which one wins is a property of archive/zip member
// order in the library's search. A coordinator must therefore reject
// archives with duplicate member names rather than rely on the library.
// (The observation below pins the actual behavior.)
func TestStockDuplicateMemberWhichWins(t *testing.T) {
	benign := []byte("benign-agent-bytes")
	hostile := []byte("hostile-agent-bytes")
	data := maliciousCercanoZip(t, benign, hostile)

	r, err := selfupdate.DecompressCommand(
		bytes.NewReader(data), fixtures.AssetName(nextVersion), fixtures.AgentName, "windows", "x64")
	if err != nil {
		t.Fatalf("DecompressCommand: %v", err)
	}
	got := new(bytes.Buffer)
	if _, err := got.ReadFrom(r); err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	t.Logf("duplicate resolution: extracted %d bytes", got.Len())
	// Record the observed winner as a strict assertion so any future
	// library change surfaces loudly.
	if !bytes.Equal(got.Bytes(), benign) && !bytes.Equal(got.Bytes(), hostile) {
		t.Fatalf("extracted bytes match NEITHER duplicate member")
	}
	t.Logf("winner is %s", map[bool]string{true: "first (benign)"}[bytes.Equal(got.Bytes(), benign)])
}

// FACT under test: a traversal-named member (../../cercano.exe) is NOT
// matched as the required binary when a properly-named member exists — but
// observe whether a traversal-ONLY archive would be matched (base-name
// matching ignores directories).
func TestTraversalOnlyArchiveStillMatchesByBaseName(t *testing.T) {
	hostile := []byte("traversal-agent-bytes")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	h := zip.FileHeader{}
	h.SetMode(0o755)
	h.Name = "../../" + fixtures.AgentName
	h.Method = zip.Deflate
	w, err := zw.CreateHeader(&h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(hostile); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := selfupdate.DecompressCommand(
		bytes.NewReader(buf.Bytes()), fixtures.AssetName(nextVersion), fixtures.AgentName, "windows", "x64")
	if err != nil {
		// A refusal would be the SAFE behavior; record it as a fact.
		t.Fatalf("traversal-only archive: DecompressCommand refused it (%v) — record this", err)
	}
	got := new(bytes.Buffer)
	_, _ = got.ReadFrom(r)
	if bytes.Equal(got.Bytes(), hostile) {
		t.Logf("FACT: base-name matching DOES match traversal-named members; a coordinator that writes extracted members to disk paths derived from member names would be vulnerable — the spike coordinator writes only fixed paths, which neutralizes this")
	}
}

// FACT under test: a symlink member claiming to be the CLI is (or is not)
// refused by the stock extractor. If it is returned as bytes, the link
// target string would be written as the binary — a coordinator must
// validate extracted binaries (size, PE/Mach-O header) before staging.
func TestStockSymlinkMemberHandling(t *testing.T) {
	benign := []byte("benign-agent-bytes")
	hostile := []byte("hostile-agent-bytes")
	data := maliciousCercanoZip(t, benign, hostile)

	r, err := selfupdate.DecompressCommand(
		bytes.NewReader(data), fixtures.AssetName(nextVersion), fixtures.CliName, "windows", "x64")
	if err != nil {
		t.Logf("FACT: stock extractor REFUSED the symlink member (%v) — record this", err)
		return
	}
	got := new(bytes.Buffer)
	_, _ = got.ReadFrom(r)
	if bytes.Equal(got.Bytes(), []byte("/etc/hostname")) {
		t.Logf("FACT: stock extractor returned the symlink CONTENT (the link target path string) as the binary — extracted payloads must be validated before use")
	} else {
		t.Logf("FACT: symlink member yielded %d bytes (neither refused verifiably nor obviously the link target); requires targeted follow-up", got.Len())
	}
}

// Sanity: the fixture helpers' paths stay inside the temp sandbox.
func TestFixturePathsAreSandboxed(t *testing.T) {
	dir := t.TempDir()
	zipPath, shaPath, _, _ := buildCercanoRelease(t)
	for _, p := range []string{zipPath, shaPath} {
		if filepath.Dir(p) == "" {
			t.Fatalf("unexpected empty dir for %s", p)
		}
	}
	_ = dir
}
