package imagecheck

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/updatecoord/archivecheck"
)

func stagedFixture(t *testing.T) archivecheck.Staged {
	t.Helper()
	root := t.TempDir()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, name := range []string{"bin/cercano", "bin/cercano-cli"} {
		f, e := w.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write(elfFixture(false)); e != nil {
			t.Fatal(e)
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	archive := filepath.Join(root, "release.zip")
	if e := os.WriteFile(archive, b.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	staged, e := archivecheck.StageFile(context.Background(), archive, root, archivecheck.Options{
		Format: archivecheck.Zip, Layout: archivecheck.Layout{Required: []string{"bin/cercano", "bin/cercano-cli"}},
		Bounds: archivecheck.Bounds{MaxMembers: 8, MaxCompressedBytes: 1 << 20, MaxUncompressedBytes: 1 << 20, MaxMemberBytes: 1 << 20},
	}, archivecheck.StageOptions{StagingPattern: "image-test-", FileMode: 0600, ExecutableMode: 0700, DirMode: 0700, Executables: []string{"bin/cercano", "bin/cercano-cli"}})
	if e != nil {
		t.Fatal(e)
	}
	return *staged
}
func TestCheckStagedIntegrationAndTamper(t *testing.T) {
	s := stagedFixture(t)
	names := []string{"bin/cercano", "bin/cercano-cli"}
	if e := CheckStaged(context.Background(), s, names, "linux", "amd64"); e != nil {
		t.Fatal(e)
	}
	if e := CheckStaged(context.Background(), s, names, "windows", "amd64"); !errors.Is(e, ErrImage) {
		t.Fatal(e)
	}
	p := filepath.Join(s.Dir, "bin", "cercano-cli")
	d, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	d[len(d)-1] ^= 1
	if e = os.WriteFile(p, d, 0700); e != nil {
		t.Fatal(e)
	}
	if e = CheckStaged(context.Background(), s, names, "linux", "amd64"); !errors.Is(e, ErrStage) {
		t.Fatal("changed bytes accepted", e)
	}
}
func TestCheckStagedRejectsLinksAndInputs(t *testing.T) {
	s := stagedFixture(t)
	for _, names := range [][]string{nil, {"bin/missing"}, {"../outside"}, {"bin/cercano", "bin/cercano"}} {
		if e := CheckStaged(context.Background(), s, names, "linux", "amd64"); !errors.Is(e, ErrStage) {
			t.Fatalf("accepted names %v: %v", names, e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := CheckStaged(ctx, s, []string{"bin/cercano"}, "linux", "amd64"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	file := filepath.Join(s.Dir, "bin", "cercano")
	other := filepath.Join(t.TempDir(), "target")
	if e := os.Rename(file, other); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(other, file); e != nil {
		t.Skipf("host cannot create symlink fixture: %v", e)
	}
	if e := CheckStaged(context.Background(), s, []string{"bin/cercano"}, "linux", "amd64"); !errors.Is(e, ErrStage) {
		t.Fatal("symlink accepted", e)
	}
}
func TestCheckStagedSizeAndManifestLimits(t *testing.T) {
	s := stagedFixture(t)
	original := s.Manifest.Members[0]
	for _, size := range []int64{-1, 0, original.Size + 1, MaxImageBytes + 1} {
		s.Manifest.Members[0].Size = size
		if e := CheckStaged(context.Background(), s, []string{original.Name}, "linux", "amd64"); !errors.Is(e, ErrStage) {
			t.Fatal("invalid size accepted", size, e)
		}
	}
	s.Manifest.Members[0] = original
	s.Manifest.Members = append(s.Manifest.Members, original)
	if e := CheckStaged(context.Background(), s, []string{original.Name}, "linux", "amd64"); !errors.Is(e, ErrStage) {
		t.Fatal("duplicate manifest accepted", e)
	}
}
func TestCheckStagedRetainsCallerFile(t *testing.T) {
	root := t.TempDir()
	data := elfFixture(false)
	sum := sha256.Sum256(data)
	path := filepath.Join(root, "inert")
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	s := archivecheck.Staged{Dir: root, Manifest: &archivecheck.Manifest{Members: []archivecheck.Member{{Name: "inert", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}}}}
	// A regular unchanged file need not be executable to classify it. Executable
	// permission policy is enforced by staging/launch, not by this byte checker.
	if e := CheckStaged(context.Background(), s, []string{"inert"}, "linux", "amd64"); e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(got, data) {
		t.Fatal("caller file changed", e)
	}
}
