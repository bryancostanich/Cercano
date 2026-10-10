package activationtxn

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/updatecoord/archivecheck"
	"cercano/source/server/internal/updatecoord/privdir"
	"cercano/source/server/internal/updatecoord/state"
)

// Inert Linux header: these fixtures never execute the staged bytes. Platform
// policy is deliberately explicit and independent of the host running tests.
func fixtureELF() []byte {
	d := make([]byte, 256)
	b := binary.LittleEndian
	copy(d, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	b.PutUint16(d[16:], 2)
	b.PutUint16(d[18:], 62)
	b.PutUint32(d[20:], 1)
	b.PutUint64(d[24:], 0x400080)
	b.PutUint64(d[32:], 64)
	b.PutUint16(d[52:], 64)
	b.PutUint16(d[54:], 56)
	b.PutUint16(d[56:], 1)
	p := d[64:]
	b.PutUint32(p, 1)
	b.PutUint32(p[4:], 5)
	b.PutUint64(p[16:], 0x400000)
	b.PutUint64(p[32:], 256)
	b.PutUint64(p[40:], 256)
	b.PutUint64(p[48:], 4096)
	return d
}

// fixtureImages reconstructs a trusted fixture manifest, without reading or
// modifying staged files. Subprocess recovery must inspect persisted bytes.
func fixtureImages(dir string) *TargetImages {
	data := fixtureELF()
	sum := sha256.Sum256(data)
	members := []archivecheck.Member{}
	for _, name := range []string{"bin/cercano", "bin/cercano-cli"} {
		members = append(members, archivecheck.Member{Name: name, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
	}
	return &TargetImages{VersionsDirectory: filepath.Join(dir, "versions"), Manifest: &archivecheck.Manifest{ArchiveSHA256: testArtifact, Members: members}, AgentMember: "bin/cercano", ClientMember: "bin/cercano-cli", GOOS: "linux", GOARCH: "amd64"}
}
func provisionFixtureImages(t *testing.T, dir string) {
	t.Helper()
	root := filepath.Join(dir, "versions")
	for _, p := range []string{root, filepath.Join(root, testStagedID), filepath.Join(root, testStagedID, "bin")} {
		if _, e := privdir.Ensure(p); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"cercano", "cercano-cli"} {
		if e := os.WriteFile(filepath.Join(root, testStagedID, "bin", name), fixtureELF(), 0700); e != nil {
			t.Fatal(e)
		}
	}
}
func TestSwitchRefusesUnverifiedImagesBeforeIntent(t *testing.T) {
	for _, kind := range []string{"missing-policy", "digest", "platform", "missing-file", "tampered", "directory", "member-policy"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t, false)
			r := f.request()
			path := filepath.Join(r.Images.VersionsDirectory, testStagedID, "bin", "cercano")
			switch kind {
			case "missing-policy":
				r.Images = nil
			case "digest":
				r.Images.Manifest.ArchiveSHA256 = digestChar('f')
			case "platform":
				r.Images.GOOS = "windows"
			case "missing-file":
				if e := os.Remove(path); e != nil {
					t.Fatal(e)
				}
			case "tampered":
				d := fixtureELF()
				d[255] = 1
				if e := os.WriteFile(path, d, 0700); e != nil {
					t.Fatal(e)
				}
			case "directory":
				r.Images.VersionsDirectory = t.TempDir()
			case "member-policy":
				r.Images.ClientMember = r.Images.AgentMember
			}
			if _, e := Switch(context.Background(), r); !errors.Is(e, ErrTargetImages) {
				t.Fatalf("bad images accepted: %v", e)
			}
			f.requireJournal(state.JournalPrepared, 1)
			if _, e := os.Lstat(f.selectionPath()); !os.IsNotExist(e) {
				t.Fatalf("selection was published: %v", e)
			}
		})
	}
}
func TestSwitchRechecksImagesAfterIntent(t *testing.T) {
	f := newFixture(t, false)
	r := f.request()
	testHookAfterIntent = func() error {
		return os.WriteFile(filepath.Join(r.Images.VersionsDirectory, testStagedID, "bin", "cercano"), []byte("changed"), 0700)
	}
	t.Cleanup(func() { testHookAfterIntent = nil })
	if _, e := Switch(context.Background(), r); !errors.Is(e, ErrTargetImages) {
		t.Fatal(e)
	}
	f.requireJournal(state.JournalSwitchIntent, 2)
	if _, e := os.Lstat(f.selectionPath()); !os.IsNotExist(e) {
		t.Fatalf("selection was published: %v", e)
	}
}
func TestSwitchRechecksImagesBeforeAcknowledgement(t *testing.T) {
	f := newFixture(t, false)
	r := f.request()
	testHookAfterPublish = func() error {
		return os.WriteFile(filepath.Join(r.Images.VersionsDirectory, testStagedID, "bin", "cercano-cli"), []byte("changed"), 0700)
	}
	t.Cleanup(func() { testHookAfterPublish = nil })
	result, e := Switch(context.Background(), r)
	if !errors.Is(e, ErrTargetImages) || !result.Published || result.Acknowledged || result.AwaitHealth {
		t.Fatalf("incorrect postpublication result: %+v %v", result, e)
	}
	f.requireJournal(state.JournalSwitchIntent, 2)
	testHookAfterPublish = nil
	if _, e = Switch(context.Background(), r); !errors.Is(e, ErrTargetImages) {
		t.Fatal("resume acknowledged changed images", e)
	}
	f.requireJournal(state.JournalSwitchIntent, 2)
}
func TestSwitchSelectedReentryRechecksImages(t *testing.T) {
	f := newFixture(t, false)
	r := f.request()
	if _, e := Switch(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(filepath.Join(r.Images.VersionsDirectory, testStagedID, "bin", "cercano-cli")); e != nil {
		t.Fatal(e)
	}
	if result, e := Switch(context.Background(), r); !errors.Is(e, ErrTargetImages) || result.AwaitHealth {
		t.Fatalf("stale image observation: %+v %v", result, e)
	}
	f.requireJournal(state.JournalSelected, 3)
}

// Exercise real archive preflight and extraction, not a caller-invented manifest.
// Publisher authentication remains outside this fixture's inert-byte scope.
func TestSwitchBindsExtractedArchiveDigest(t *testing.T) {
	f := newFixtureNoJournal(t)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"bin/cercano", "bin/cercano-cli"} {
		w, e := zw.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(fixtureELF()); e != nil {
			t.Fatal(e)
		}
	}
	if e := zw.Close(); e != nil {
		t.Fatal(e)
	}
	archive := filepath.Join(t.TempDir(), "release.zip")
	if e := os.WriteFile(archive, buf.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	root := fixtureImages(f.dir).VersionsDirectory
	staged, e := archivecheck.StageFile(context.Background(), archive, root, archivecheck.Options{
		Format: archivecheck.Zip, Layout: archivecheck.Layout{Required: []string{"bin/cercano", "bin/cercano-cli"}},
		Bounds: archivecheck.Bounds{MaxMembers: 8, MaxCompressedBytes: 1 << 20, MaxUncompressedBytes: 1 << 20, MaxMemberBytes: 1 << 20},
	}, archivecheck.StageOptions{StagingPattern: "verified-stage-", FileMode: 0600, ExecutableMode: 0700, DirMode: 0700, Executables: []string{"bin/cercano", "bin/cercano-cli"}})
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(buf.Bytes())
	if staged.Manifest.ArchiveSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("archive digest not retained")
	}
	j := preparedJournal(f.opID)
	j.VerifiedArtifactSHA256 = staged.Manifest.ArchiveSHA256
	j.StagedVersionDir = filepath.Base(staged.Dir)
	if _, e = f.store.SaveActivationJournal(context.Background(), 0, j); e != nil {
		t.Fatal(e)
	}
	r := f.request()
	r.Images.Manifest = staged.Manifest
	res, e := Switch(context.Background(), r)
	if e != nil || !res.Published || !res.Acknowledged || res.Target.VerifiedArtifactSHA256 != staged.Manifest.ArchiveSHA256 {
		t.Fatalf("extracted target switch: %+v %v", res, e)
	}
}

func TestSwitchRefusesSymlinkedVersionAncestor(t *testing.T) {
	f := newFixtureNoJournal(t)
	r := f.request()
	root := r.Images.VersionsDirectory
	// The same genuine fixture bytes are reachable through an alias. A matching
	// digest must not make this an acceptable version-namespace binding.
	if e := os.Symlink(root, filepath.Join(root, "alias")); e != nil {
		t.Skipf("symlink fixture unavailable: %v", e)
	}
	j := preparedJournal(f.opID)
	j.StagedVersionDir = "alias/" + testStagedID
	if _, e := f.store.SaveActivationJournal(context.Background(), 0, j); e != nil {
		t.Fatal(e)
	}
	if _, e := Switch(context.Background(), r); !errors.Is(e, ErrTargetImages) {
		t.Fatalf("version alias accepted: %v", e)
	}
	f.requireJournal(state.JournalPrepared, 1)
	if _, e := os.Lstat(f.selectionPath()); !os.IsNotExist(e) {
		t.Fatal("selection was published", e)
	}
}
