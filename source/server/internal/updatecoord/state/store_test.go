package state

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func dbFile(root, id string) string {
	return filepath.Join(root, orgComponent(), "updater", id, "state.db")
}
func TestStoreReopenAndTwoHandles(t *testing.T) {
	root := t.TempDir()
	s, e := Open(root, "test-install")
	if e != nil {
		t.Fatal(e)
	}
	id, e := s.AllocateOperationID(context.Background())
	if e != nil || id != 1 {
		t.Fatalf("id=%d err=%v", id, e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(root, "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	other, e := Open(root, "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	seen := map[int64]bool{1: true}
	for _, store := range []*Store{s, other, s, other} {
		id, e = store.AllocateOperationID(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if seen[id] {
			t.Fatal("reissued id", id)
		}
		seen[id] = true
	}
}
func TestConcurrentAllocationOneHandle(t *testing.T) {
	s, e := Open(t.TempDir(), "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	const n = 24
	start := make(chan struct{})
	var wg sync.WaitGroup
	ids := make(chan int64, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			id, e := s.AllocateOperationID(context.Background())
			if e != nil {
				errs <- e
			} else {
				ids <- id
			}
		}()
	}
	close(start)
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Errorf("concurrent allocation: %v", e)
	}
	seen := map[int64]bool{}
	for id := range ids {
		if seen[id] {
			t.Error("duplicate id", id)
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Fatalf("allocated=%d want%d", len(seen), n)
	}
}
func TestDatabaseModePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode assertion is not an ACL test")
	}
	root := t.TempDir()
	s, e := Open(root, "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	info, e := os.Stat(dbFile(root, "test-install"))
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("database accessible outside owner: %o", info.Mode().Perm())
	}
}
func TestTruncatedExistingDatabaseIsNotReinitialized(t *testing.T) {
	root := t.TempDir()
	p := dbFile(root, "test-install")
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, nil, 0600); e != nil {
		t.Fatal(e)
	}
	s, e := Open(root, "test-install")
	if e == nil {
		s.Close()
		t.Fatal("existing empty/truncated DB silently initialized")
	}
	b, e := os.ReadFile(p)
	if e != nil || len(b) != 0 {
		t.Fatalf("changed truncated database: %v", e)
	}
}
func TestFutureForeignAndCorruptDatabaseUnchanged(t *testing.T) {
	for _, kind := range []string{"future", "foreign", "corrupt"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			p := dbFile(root, "test-install")
			if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
				t.Fatal(e)
			}
			if kind == "corrupt" {
				if e := os.WriteFile(p, []byte("not sqlite"), 0600); e != nil {
					t.Fatal(e)
				}
			} else {
				d, e := sql.Open("sqlite", p)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = d.Exec("CREATE TABLE unrelated (x TEXT)"); e != nil {
					t.Fatal(e)
				}
				if kind == "future" {
					if _, e = d.Exec("PRAGMA application_id=1129464654; PRAGMA user_version=999"); e != nil {
						t.Fatal(e)
					}
				}
				if e = d.Close(); e != nil {
					t.Fatal(e)
				}
				if e = os.Chmod(p, 0600); e != nil {
					t.Fatal(e)
				}
			}
			before, e := os.ReadFile(p)
			if e != nil {
				t.Fatal(e)
			}
			s, e := Open(root, "test-install")
			if e == nil {
				s.Close()
				t.Fatal("unexpected acceptance")
			}
			after, e := os.ReadFile(p)
			if e != nil || !bytes.Equal(before, after) {
				t.Fatal("refused database modified")
			}
			for _, suffix := range []string{"-wal", "-shm", "-journal"} {
				if _, e = os.Stat(p + suffix); !os.IsNotExist(e) {
					t.Fatalf("sidecar created on refusal: %s %v", suffix, e)
				}
			}
		})
	}
}
func TestSymlinkDatabaseRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires Windows symlink capability; native ACL tests pending")
	}
	root := t.TempDir()
	p := dbFile(root, "test-install")
	os.MkdirAll(filepath.Dir(p), 0700)
	target := filepath.Join(root, "other")
	os.WriteFile(target, []byte("untouched"), 0600)
	if e := os.Symlink(target, p); e != nil {
		t.Fatal(e)
	}
	s, e := Open(root, "test-install")
	if e == nil {
		s.Close()
		t.Fatal("symlink accepted")
	}
	if !errors.Is(e, ErrUnsafePath) {
		t.Fatalf("wrong refusal: %v", e)
	}
	b, _ := os.ReadFile(target)
	if string(b) != "untouched" {
		t.Fatal("symlink target changed")
	}
}
func TestStatePathsRejectAliasesAndTraversal(t *testing.T) {
	for _, id := range []string{"..", "x/y", "CON", "foo.", "foo?", "foo*"} {
		if e := ValidateInstallID(id); e == nil {
			t.Errorf("unsafe identifier accepted: %q", id)
		}
	}
	for _, c := range []struct{ platform, home, local, xdg string }{
		{"linux", "/home/me", "", "/"}, {"linux", "/home/me", "", "/tmp/../other"},
		{"windows", "", `\\?\C:\Users\me`, ""}, {"windows", "", `C:\Users\..\other`, ""},
	} {
		if p, e := StatePath(c.platform, c.home, c.local, c.xdg, "test-install"); e == nil {
			t.Errorf("unsafe root accepted: %q", p)
		}
	}
}

func TestBlockedWriteHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	s, e := Open(root, "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	other, e := Open(root, "test-install")
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	conn, e := s.db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	if _, e = conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); e != nil {
		t.Fatal(e)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, e = other.AllocateOperationID(ctx)
	if e == nil {
		t.Fatal("write lock ignored")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancellation delayed by busy handler: %s %v", elapsed, e)
	}
}
func TestStatePathLocations(t *testing.T) {
	for _, c := range []struct{ platform, home, local, xdg, want string }{
		{"linux", "/home/me", "", "", "/home/me/.local/state/cercano/updater/test-install/state.db"},
		{"linux", "/home/me", "", "/custom/state", "/custom/state/cercano/updater/test-install/state.db"},
		{"darwin", "/Users/Ada Lovelace", "", "", "/Users/Ada Lovelace/Library/Application Support/Cercano/updater/test-install/state.db"},
		{"windows", "", `C:\Users\Ada Lovelace\AppData\Local`, "", `C:\Users\Ada Lovelace\AppData\Local\Cercano\updater\test-install\state.db`},
	} {
		got, e := StatePath(c.platform, c.home, c.local, c.xdg, "test-install")
		if e != nil || got != c.want {
			t.Errorf("%s: %q want %q err %v", c.platform, got, c.want, e)
		}
	}
}
