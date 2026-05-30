package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFile_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	tool := NewWriteFile()
	args, _ := json.Marshal(map[string]any{"path": path, "content": "hello"})
	got, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "wrote 5 bytes") {
		t.Errorf("got %q", got)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "hello" {
		t.Errorf("file content = %q, want %q", string(b), "hello")
	}
}

func TestWriteFile_Overwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewWriteFile()
	args, _ := json.Marshal(map[string]any{"path": path, "content": "new"})
	if _, err := tool.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "new" {
		t.Errorf("file content = %q, want %q", string(b), "new")
	}
}

func TestWriteFile_CreateDirsTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested/deeper/out.txt")
	tool := NewWriteFile()
	args, _ := json.Marshal(map[string]any{"path": path, "content": "x", "create_dirs": true})
	if _, err := tool.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file at %s: %v", path, err)
	}
}

func TestWriteFile_CreateDirsFalseErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing/out.txt")
	tool := NewWriteFile()
	args, _ := json.Marshal(map[string]any{"path": path, "content": "x"})
	_, err := tool.Run(context.Background(), args)
	if err == nil {
		t.Fatal("expected error when parent dir missing")
	}
}

func TestWriteFile_BadArgs(t *testing.T) {
	tool := NewWriteFile()
	_, err := tool.Run(context.Background(), json.RawMessage(`{`))
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
}
