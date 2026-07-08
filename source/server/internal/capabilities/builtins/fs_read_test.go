package builtins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestReadFileCapability(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cap := ReadFile()
	if cap.Name() != "read_file" || cap.Tier() != capabilities.TierR {
		t.Fatalf("name/tier wrong: %q %q", cap.Name(), cap.Tier())
	}
	args, _ := json.Marshal(map[string]any{"path": p})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "line1\nline2\n" {
		t.Fatalf("content = %q", res.Text)
	}
}

func TestListDirCapability(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	cap := ListDir()
	if cap.Name() != "list_dir" || cap.Tier() != capabilities.TierR {
		t.Fatalf("name/tier wrong: %q %q", cap.Name(), cap.Tier())
	}
	args, _ := json.Marshal(map[string]any{"path": dir})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	// dirs first
	if res.Rows[0]["type"] != "dir" {
		t.Fatalf("expected dir first, got %q", res.Rows[0]["type"])
	}
}

func TestStatFileCapability(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	cap := StatFile()
	if cap.Name() != "stat_file" || cap.Tier() != capabilities.TierR {
		t.Fatalf("name/tier wrong: %q %q", cap.Name(), cap.Tier())
	}
	args, _ := json.Marshal(map[string]any{"path": p})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0]["exists"] != true {
		t.Fatalf("expected exists=true")
	}
	// missing path
	args2, _ := json.Marshal(map[string]any{"path": filepath.Join(dir, "nope.txt")})
	res2, err := cap.Execute(context.Background(), &capabilities.Call{Args: args2})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Rows[0]["exists"] != false {
		t.Fatalf("expected exists=false for missing path")
	}
}

func TestGlobCapability(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cap := Glob()
	if cap.Name() != "glob" || cap.Tier() != capabilities.TierR {
		t.Fatalf("name/tier wrong: %q %q", cap.Name(), cap.Tier())
	}
	args, _ := json.Marshal(map[string]any{"pattern": "*.go", "path": dir})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.Type != capabilities.ResultText {
		t.Fatalf("expected text result")
	}
	if res.Detail != "2 matches" {
		t.Fatalf("expected '2 matches', got %q", res.Detail)
	}
}

func TestReadFileCapability_Surfaces(t *testing.T) {
	cap := ReadFile()
	want := capabilities.SurfaceAgent | capabilities.SurfaceMCP
	if cap.Surfaces() != want {
		t.Fatalf("surfaces wrong: %v", cap.Surfaces())
	}
}

func TestReadFile_RelativePathResolvesAgainstWorkDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": "hello.txt"})
	call := &capabilities.Call{WorkDir: dir, Args: args, Emit: func(string) {}}
	res, err := ReadFile().Execute(context.Background(), call)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if res.Text != "hi" {
		t.Errorf("content = %q, want hi", res.Text)
	}
}
