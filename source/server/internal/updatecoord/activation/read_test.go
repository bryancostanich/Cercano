package activation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writeSelectionFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadSelectionValid(t *testing.T) {
	path := writeSelectionFile(t, t.TempDir(), "selection.json", validSelectionJSON)
	s, err := ReadSelection(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.InstallID != "test-install" || s.SelectedVersion != "9.9.9" || s.Generation != 4 {
		t.Fatalf("read selection mismatch: %+v", s)
	}
}

func TestReadSelectionAbsentDistinctFromFailures(t *testing.T) {
	dir := t.TempDir()

	// Nothing exists: ABSENT, distinct from unreadable/malformed.
	sel, err := ReadSelection(filepath.Join(dir, "selection.json"))
	if !errors.Is(err, ErrSelectionAbsent) {
		t.Fatalf("want absent, got %v", err)
	}
	if sel != (Selection{}) {
		t.Fatalf("absent returned a selection: %+v", sel)
	}

	// Present but empty: MALFORMED (a torn write is not an absent state).
	empty := writeSelectionFile(t, dir, "empty.json", "")
	if _, err = ReadSelection(empty); !errors.Is(err, ErrSelectionMalformed) {
		t.Fatalf("empty file: want malformed, got %v", err)
	}

	// Present but invalid content: MALFORMED, and distinct from absent.
	bad := writeSelectionFile(t, dir, "bad.json", `{"schema_version":99}`)
	if _, err = ReadSelection(bad); !errors.Is(err, ErrSelectionMalformed) {
		t.Fatalf("bad content: want malformed, got %v", err)
	}

	// A directory where the selection should be: UNREADABLE.
	somedir := filepath.Join(dir, "somedir")
	if err := os.Mkdir(somedir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadSelection(somedir); !errors.Is(err, ErrSelectionUnreadable) {
		t.Fatalf("directory: want unreadable, got %v", err)
	}
}

func TestReadSelectionRefusesNonRegularAndLinks(t *testing.T) {
	dir := t.TempDir()
	real := writeSelectionFile(t, dir, "real.json", validSelectionJSON)

	// A symlink to a perfectly valid selection file is still refused: the
	// entry itself must be a regular non-link file.
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ReadSelection(link); !errors.Is(err, ErrSelectionUnreadable) {
		t.Fatalf("symlink accepted: %v", err)
	}

	// The real file the link points at still reads fine.
	if _, err := ReadSelection(real); err != nil {
		t.Fatalf("real file refused after link test: %v", err)
	}
}

func TestReadSelectionBoundedAndOversize(t *testing.T) {
	dir := t.TempDir()
	// Oversize content: present but malformed (never a legitimate
	// selection; every field is individually bounded far below this).
	oversize := writeSelectionFile(t, dir, "big.json", `{"pad":"`+strings.Repeat("x", maxSelectionJSONBytes)+`"}`)
	if _, err := ReadSelection(oversize); !errors.Is(err, ErrSelectionMalformed) {
		t.Fatalf("oversize: want malformed, got %v", err)
	}
}

func TestReadSelectionRequiresExplicitCleanedAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	path := writeSelectionFile(t, dir, "selection.json", validSelectionJSON)
	// Positive control: the cleaned explicit absolute path reads fine.
	if _, err := ReadSelection(path); err != nil {
		t.Fatalf("clean absolute path refused: %v", err)
	}

	if _, err := ReadSelection("selection.json"); !errors.Is(err, ErrSelectionUnreadable) {
		t.Fatalf("relative path accepted: %v", err)
	}
	unclean := dir + string(filepath.Separator) + "sub" + string(filepath.Separator) + ".." + string(filepath.Separator) + "selection.json"
	if _, err := ReadSelection(unclean); !errors.Is(err, ErrSelectionUnreadable) {
		t.Fatalf("unclean path accepted: %v", err)
	}
	if _, err := ReadSelection("nul\x00byte"); !errors.Is(err, ErrSelectionUnreadable) {
		t.Fatalf("NUL path accepted: %v", err)
	}
}

func TestObserveClassification(t *testing.T) {
	dir := t.TempDir()
	absentPath := filepath.Join(dir, "none.json")

	if got := Observe(Selection{}, errorString("some other error")); got.State != ObservedUnreadable {
		t.Fatalf("unexpected error misclassified: %+v", got)
	}
	sel, err := ReadSelection(absentPath)
	if got := Observe(sel, err); got.State != ObservedAbsent || got.Selection != nil {
		t.Fatalf("absent misclassified: %+v", got)
	}

	path := writeSelectionFile(t, dir, "selection.json", validSelectionJSON)
	sel, err = ReadSelection(path)
	got := Observe(sel, err)
	if got.State != ObservedPresent || got.Selection == nil || got.Selection.SelectedVersion != "9.9.9" {
		t.Fatalf("present misclassified: %+v", got)
	}

	malformed := writeSelectionFile(t, dir, "bad.json", "{")
	sel, err = ReadSelection(malformed)
	if got = Observe(sel, err); got.State != ObservedMalformed {
		t.Fatalf("malformed misclassified: %+v", got)
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

// TestReadSelectionConcurrentReadsRace hammers the read path from many
// goroutines against a shared valid file: the reader is read-only and every
// read must be independent (run under -race).
func TestReadSelectionConcurrentReadsRace(t *testing.T) {
	dir := t.TempDir()
	path := writeSelectionFile(t, dir, "selection.json", validSelectionJSON)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 32; j++ {
				s, err := ReadSelection(path)
				if err != nil || s.SelectedVersion != "9.9.9" {
					t.Errorf("concurrent read: %+v %v", s, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
