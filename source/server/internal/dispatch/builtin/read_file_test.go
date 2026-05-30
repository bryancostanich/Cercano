package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadFile_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFile()
	args, _ := json.Marshal(map[string]string{"path": path})
	got, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestReadFile_MissingFile(t *testing.T) {
	tool := NewReadFile()
	args, _ := json.Marshal(map[string]string{"path": "/nonexistent/file/here"})
	_, err := tool.Run(context.Background(), args)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReadFile_BinaryDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin.dat")
	if err := os.WriteFile(path, []byte{0x01, 0x00, 0x02, 0x03}, 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFile()
	args, _ := json.Marshal(map[string]string{"path": path})
	_, err := tool.Run(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for binary file")
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("err = %q, want it to mention 'binary'", err.Error())
	}
}

func TestReadFile_PermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission test would fail")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "denied.txt")
	if err := os.WriteFile(path, []byte("nope"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0644) })
	tool := NewReadFile()
	args, _ := json.Marshal(map[string]string{"path": path})
	_, err := tool.Run(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for permission denied")
	}
}

func TestReadFile_BadArgs(t *testing.T) {
	tool := NewReadFile()
	_, err := tool.Run(context.Background(), json.RawMessage(`{"path":`))
	if err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}
