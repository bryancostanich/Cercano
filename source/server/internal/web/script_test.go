package web

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureSearchScriptMaterializes checks the embedded script is written
// out with the embedded content on first call.
func TestEnsureSearchScriptMaterializes(t *testing.T) {
	dir := t.TempDir()
	path, err := EnsureSearchScript(dir)
	if err != nil {
		t.Fatalf("EnsureSearchScript: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("script written to %q, want inside %q", path, dir)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read materialized script: %v", err)
	}
	if !bytes.Equal(got, ddgScriptSource) {
		t.Fatal("materialized script differs from embedded source")
	}
	if len(got) == 0 {
		t.Fatal("embedded script is empty")
	}
}

// TestEnsureSearchScriptRefreshesStale checks a stale or corrupted on-disk
// copy (e.g. from an older binary) is overwritten with this binary's copy.
func TestEnsureSearchScriptRefreshesStale(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "ddg_search.py")
	if err := os.WriteFile(stale, []byte("# old version"), 0o644); err != nil {
		t.Fatalf("seed stale script: %v", err)
	}
	path, err := EnsureSearchScript(dir)
	if err != nil {
		t.Fatalf("EnsureSearchScript: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read refreshed script: %v", err)
	}
	if !bytes.Equal(got, ddgScriptSource) {
		t.Fatal("stale script was not refreshed to the embedded source")
	}
}

// TestEnsureSearchScriptCreatesDir checks the target directory is created
// when missing.
func TestEnsureSearchScriptCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "scripts")
	if _, err := EnsureSearchScript(dir); err != nil {
		t.Fatalf("EnsureSearchScript with missing dir: %v", err)
	}
}
