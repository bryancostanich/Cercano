package ollamacatalog

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_MissingFileReturnsNilNilNoError(t *testing.T) {
	// First-run behavior: nothing on disk yet. Callers should be able to
	// distinguish "no cache" (populate on first fetch) from "corrupt
	// cache" (error), so a missing file is nil/nil rather than an error.
	got, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("expected nil err for missing file, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil cache for missing file, got %+v", got)
	}
}

func TestLoad_CorruptFileReturnsError(t *testing.T) {
	// A file that exists but doesn't parse must return an error rather
	// than pretending it's empty — corruption is a real signal that
	// something went wrong, and the caller decides whether to overwrite.
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	if err := os.WriteFile(path, []byte("this is not json {"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error on corrupt cache; got nil")
	}
}

func TestSaveThenLoad_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "cache.json")
	orig := &Cache{
		FetchedAt: time.Now().UTC().Truncate(time.Second), // truncated so JSON round-trip is byte-exact
		Source:    "https://ollama.com/library",
		Models:    []Model{{Name: "qwen2.5-coder", Tags: []string{"7b", "32b"}}},
	}
	if err := orig.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.FetchedAt.Equal(orig.FetchedAt) {
		t.Errorf("FetchedAt: got %v, want %v", got.FetchedAt, orig.FetchedAt)
	}
	if got.Source != orig.Source {
		t.Errorf("Source: got %q, want %q", got.Source, orig.Source)
	}
	if len(got.Models) != 1 || got.Models[0].Name != "qwen2.5-coder" {
		t.Errorf("Models: got %+v", got.Models)
	}
}

func TestSave_CreatesParentDirectories(t *testing.T) {
	// Deep path — Save must mkdir -p before writing.
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "cache.json")
	if err := (&Cache{}).Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created at %s: %v", path, err)
	}
}

func TestSave_IsAtomic_NoStaleTempFileOnSuccess(t *testing.T) {
	// After a successful Save, only the target file should exist in the
	// directory — no leftover .tmp-* files. Guards against a subtle bug
	// where the atomic-write helper forgets to clean up.
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	if err := (&Cache{Source: "test"}).Save(path); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(path) {
			t.Errorf("unexpected leftover file after atomic save: %s", e.Name())
		}
	}
}

func TestIsStale_MissingCacheIsAlwaysStale(t *testing.T) {
	// nil cache and zero-FetchedAt cache both count as stale, so the
	// first-run path always fetches instead of serving empty results.
	if !(*Cache)(nil).IsStale(time.Now(), time.Hour) {
		t.Error("nil cache should be stale")
	}
	if !(&Cache{}).IsStale(time.Now(), time.Hour) {
		t.Error("cache with zero FetchedAt should be stale")
	}
}

func TestIsStale_RespectsTTL(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	c := &Cache{FetchedAt: now.Add(-2 * time.Hour)}
	if c.IsStale(now, time.Hour) != true {
		t.Error("2-hour-old cache should be stale under a 1-hour TTL")
	}
	if c.IsStale(now, 3*time.Hour) != false {
		t.Error("2-hour-old cache should not be stale under a 3-hour TTL")
	}
}
