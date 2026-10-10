//go:build darwin || linux

package imagecheck

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCheckStagedRefusesFIFOWithoutWaiting(t *testing.T) {
	s := stagedFixture(t)
	p := filepath.Join(s.Dir, "bin", "cercano")
	if e := os.Remove(p); e != nil {
		t.Fatal(e)
	}
	if e := unix.Mkfifo(p, 0600); e != nil {
		t.Fatal(e)
	}
	if e := CheckStaged(context.Background(), s, []string{"bin/cercano"}, "linux", "amd64"); !errors.Is(e, ErrStage) {
		t.Fatal(e)
	}
	// The lower-level opener is nonblocking even if a regular entry is replaced
	// by a FIFO between regularMember and openMember.
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	f, e := openMember(root, "bin/cercano")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatal("fixture not a FIFO", e)
	}
}
