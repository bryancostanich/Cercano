package state

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSQLiteSidecarSymlinksRefusedBeforeSQLiteOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Windows link permission setup still required")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			root := t.TempDir()
			s, e := Open(root, "test-install")
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			target := filepath.Join(root, "unrelated")
			if e = os.WriteFile(target, []byte("untouched"), 0600); e != nil {
				t.Fatal(e)
			}
			if e = os.Symlink(target, dbFile(root, "test-install")+suffix); e != nil {
				t.Fatal(e)
			}
			s, e = Open(root, "test-install")
			if e == nil {
				s.Close()
			}
			if !errors.Is(e, ErrUnsafePath) {
				t.Fatalf("sidecar link did not fail preflight: %v", e)
			}
			b, e := os.ReadFile(target)
			if e != nil || string(b) != "untouched" {
				t.Fatal("unrelated file modified")
			}
		})
	}
}
