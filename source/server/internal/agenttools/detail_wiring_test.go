package agenttools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// These lock the per-tool Result.Detail wiring: each tool must emit a clean,
// content-free outcome token so clients can render "<detail> · <timing>".

func TestReadFile_Detail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := mustExec(t, ReadFile(), map[string]any{"path": p})
	if res.Detail != "4 lines" { // "a\nb\nc\n" spans 4 lines
		t.Errorf("Read Detail = %q, want %q", res.Detail, "4 lines")
	}
}

func TestGlob_Detail(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"x.go", "y.go"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res := mustExec(t, Glob(), map[string]any{"pattern": "*.go", "path": dir})
	if res.Detail != "2 matches" {
		t.Errorf("Glob Detail = %q, want %q", res.Detail, "2 matches")
	}
}

func TestEdit_Detail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := mustExec(t, EditFile(), map[string]any{
		"path": p, "old_string": "one", "new_string": "uno\ndos\ntres",
	})
	if res.Detail != "+3 −1" {
		t.Errorf("Edit Detail = %q, want %q", res.Detail, "+3 −1")
	}
}

func mustExec(t *testing.T, tool Tool, args map[string]any) *Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("%s: %v", tool.Name(), err)
	}
	return res
}
