package visionattach

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func diskStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func TestDiskBackingAndCallerOwnership(t *testing.T) {
	s := diskStore(t)
	data := []byte("pixels")
	a := s.Add("../../not-a-path", "image/png", data).Attachment
	e := s.convs["../../not-a-path"].byID[a.ID]
	if e.metadata.Data != nil || a.Data != nil {
		t.Fatal("store or Add retained payload")
	}
	if filepath.Dir(e.path) != s.dir {
		t.Fatal("file escaped private directory")
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{s.dir: 0700, e.path: 0600} {
			st, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm() != want {
				t.Fatalf("%s permissions: %v", path, st.Mode())
			}
		}
	}
	data[0] = 'X'
	a.ID = "mutated"
	got, ok := s.Lookup("../../not-a-path", e.metadata.ID)
	if !ok || string(got.Data) != "pixels" {
		t.Fatal("caller mutated stored image")
	}
	got.Data[0] = 'X'
	again, ok := s.Lookup("../../not-a-path", e.metadata.ID)
	if !ok || string(again.Data) != "pixels" || e.metadata.Data != nil {
		t.Fatal("lookup retained/shared bytes")
	}
}

func TestMissingFileAndReattach(t *testing.T) {
	s := diskStore(t)
	a := s.Add("c", "image/png", []byte("pixels")).Attachment
	if err := os.Remove(s.convs["c"].byID[a.ID].path); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Lookup("c", a.ID); ok {
		t.Fatal("missing file resolved")
	}
	if _, _, ok, ambiguous := s.LookupAny(a.ID); ok || ambiguous {
		t.Fatal("missing file resolved globally")
	}
	b := s.Add("c", "image/png", []byte("pixels"))
	if b.Rejected || !b.Deduped || b.Attachment.ID != a.ID {
		t.Fatalf("reattach: %+v", b)
	}
	if got, ok := s.Lookup("c", a.ID); !ok || string(got.Data) != "pixels" {
		t.Fatal("reattach did not restore image")
	}
}

func TestClearAndCloseRemoveOwnedFiles(t *testing.T) {
	s := diskStore(t)
	a := s.Add("a", "image/png", []byte("same")).Attachment
	b := s.Add("b", "image/png", []byte("same")).Attachment
	path := s.convs["a"].byID[a.ID].path
	root := s.dir
	s.Clear("a")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Clear left file: %v", err)
	}
	if _, ok := s.Lookup("b", b.ID); !ok {
		t.Fatal("Clear removed other conversation")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("Close left directory: %v", err)
	}
	if s.Count("b") != 0 {
		t.Fatal("Close retained metadata")
	}
	if !s.Add("c", "image/png", []byte("new")).Rejected {
		t.Fatal("closed store accepted image")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTemporaryStorageFailures(t *testing.T) {
	t.Run("creation", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")
		for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
			t.Setenv(key, missing)
		}
		s := diskStore(t)
		if r := s.Add("c", "image/png", []byte("pixels")); !r.Rejected || r.Attachment != nil {
			t.Fatalf("creation failure: %+v", r)
		}
		if s.Count("c") != 0 {
			t.Fatal("failed creation published metadata")
		}
	})
	t.Run("write", func(t *testing.T) {
		s := diskStore(t)
		a := s.Add("c", "image/png", []byte("pixels")).Attachment
		// Replace the owned directory with a file: deterministic on Windows and Unix,
		// unlike permission-based tests when running as root.
		if err := os.RemoveAll(s.dir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(s.dir, []byte("blocked"), 0600); err != nil {
			t.Fatal(err)
		}
		if r := s.Add("c", "image/png", []byte("other")); !r.Rejected || r.Attachment != nil {
			t.Fatalf("write failure: %+v", r)
		}
		if r := s.Add("c", "image/png", []byte("pixels")); !r.Rejected {
			t.Fatal("failed dedup repair accepted")
		}
		if s.Count("c") != 1 {
			t.Fatal("write failure changed count")
		}
		if _, ok := s.Lookup("c", a.ID); ok {
			t.Fatal("unreadable file resolved")
		}
	})
}

func TestConcurrentDiskAccessAndCleanup(t *testing.T) {
	s := diskStore(t)
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			conv := fmt.Sprint(n)
			for i := 0; i < 20; i++ {
				a := s.Add(conv, "image/png", []byte(fmt.Sprintf("%d-%d", n, i)))
				if a.Rejected {
					continue
				} // Close may win the race.
				s.Lookup(conv, a.Attachment.ID)
				s.LookupAny(a.Attachment.ID)
				s.Count(conv)
				s.Clear(conv)
			}
		}(n)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
}

func TestRemovedDirectoryCanBeRecreated(t *testing.T) {
	s := diskStore(t)
	a := s.Add("c", "image/png", []byte("pixels")).Attachment
	if err := os.RemoveAll(s.dir); err != nil {
		t.Fatal(err)
	}
	b := s.Add("c", "image/png", []byte("pixels"))
	if b.Rejected || b.Attachment.ID != a.ID {
		t.Fatalf("reattach after directory removal: %+v", b)
	}
	if got, ok := s.Lookup("c", a.ID); !ok || string(got.Data) != "pixels" {
		t.Fatal("directory loss did not recover")
	}
}
