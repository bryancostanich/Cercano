package builtins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
)

// -- write_file tests --

func TestWriteFileCapability_Basic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.txt")

	cap := WriteFile()
	if cap.Name() != "write_file" {
		t.Fatalf("name = %q, want write_file", cap.Name())
	}
	if cap.Tier() != capabilities.TierW {
		t.Fatalf("tier = %q, want W", cap.Tier())
	}
	want := capabilities.SurfaceAgent | capabilities.SurfaceMCP
	if cap.Surfaces() != want {
		t.Fatalf("surfaces = %v, want Agent|MCP", cap.Surfaces())
	}

	args, _ := json.Marshal(map[string]any{"path": p, "content": "hello\nworld\n"})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Type != capabilities.ResultText {
		t.Fatalf("type = %q, want text", res.Type)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "hello\nworld\n" {
		t.Fatalf("content = %q", string(data))
	}
}

func TestWriteFileCapability_MkdirDefault(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "deep", "file.txt")

	args, _ := json.Marshal(map[string]any{"path": p, "content": "x"})
	res, err := WriteFile().Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestWriteFileCapability_Overwrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	os.WriteFile(p, []byte("old"), 0o644)

	args, _ := json.Marshal(map[string]any{"path": p, "content": "new"})
	_, err := WriteFile().Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "new" {
		t.Fatalf("overwrite failed: content = %q", string(data))
	}
}

func TestWriteFileCapability_OverwritePreservesMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "launcher.sh")
	os.WriteFile(p, []byte("#!/bin/sh\necho old\n"), 0o755)

	args, _ := json.Marshal(map[string]any{"path": p, "content": "#!/bin/sh\necho new\n"})
	if _, err := WriteFile().Execute(context.Background(), &capabilities.Call{Args: args}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("mode = %o after overwrite, want 755 preserved", got)
	}
}

func TestWriteFileCapability_NewFileDefaultMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "fresh.txt")

	args, _ := json.Marshal(map[string]any{"path": p, "content": "x"})
	if _, err := WriteFile().Execute(context.Background(), &capabilities.Call{Args: args}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("new-file mode = %o, want 644 (not CreateTemp's 0600)", got)
	}
}

func TestEditFileCapability_PreservesMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "launcher.sh")
	os.WriteFile(p, []byte("#!/bin/sh\necho old\n"), 0o755)

	args, _ := json.Marshal(map[string]any{"path": p, "old_string": "old", "new_string": "new"})
	if _, err := EditFile().Execute(context.Background(), &capabilities.Call{Args: args}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("mode = %o after edit, want 755 preserved", got)
	}
}

func TestWriteFileCapability_MissingPath(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"path": "", "content": "x"})
	_, err := WriteFile().Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil || !strings.Contains(err.Error(), "write_file") {
		t.Fatalf("expected write_file error, got %v", err)
	}
}

// -- edit_file tests --

func TestEditFileCapability_Basic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "edit.txt")
	os.WriteFile(p, []byte("foo bar baz"), 0o644)

	cap := EditFile()
	if cap.Name() != "edit_file" {
		t.Fatalf("name = %q, want edit_file", cap.Name())
	}
	if cap.Tier() != capabilities.TierW {
		t.Fatalf("tier = %q, want W", cap.Tier())
	}
	want := capabilities.SurfaceAgent | capabilities.SurfaceMCP
	if cap.Surfaces() != want {
		t.Fatalf("surfaces = %v, want Agent|MCP", cap.Surfaces())
	}

	args, _ := json.Marshal(map[string]any{"path": p, "old_string": "bar", "new_string": "BAR"})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "foo BAR baz" {
		t.Fatalf("content after edit = %q", string(data))
	}
	if res.Type != capabilities.ResultText {
		t.Fatalf("type = %q, want text", res.Type)
	}
}

func TestEditFileCapability_RefusesAmbiguous(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "dup.txt")
	os.WriteFile(p, []byte("abc abc"), 0o644)

	args, _ := json.Marshal(map[string]any{"path": p, "old_string": "abc", "new_string": "xyz"})
	_, err := EditFile().Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error for ambiguous match")
	}
	if !strings.Contains(err.Error(), "edit_file") || !strings.Contains(err.Error(), "2") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestEditFileCapability_RefusesZeroMatch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nomatch.txt")
	os.WriteFile(p, []byte("hello world"), 0o644)

	args, _ := json.Marshal(map[string]any{"path": p, "old_string": "MISSING", "new_string": "x"})
	_, err := EditFile().Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error for zero match")
	}
	if !strings.Contains(err.Error(), "edit_file") || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestEditFileCapability_RefusesNoOp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "noop.txt")
	os.WriteFile(p, []byte("same"), 0o644)

	args, _ := json.Marshal(map[string]any{"path": p, "old_string": "same", "new_string": "same"})
	_, err := EditFile().Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error for no-op")
	}
	if !strings.Contains(err.Error(), "no-op") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestEditFileCapability_Detail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "detail.txt")
	os.WriteFile(p, []byte("a\nb\nc"), 0o644)

	// replace "a" (1 line) with "x\ny\nz" (3 lines) → detail "+3 −1"
	args, _ := json.Marshal(map[string]any{"path": p, "old_string": "a", "new_string": "x\ny\nz"})
	res, err := EditFile().Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Detail != "+3 −1" {
		t.Fatalf("detail = %q, want \"+3 −1\"", res.Detail)
	}
}

// -- start-line metadata tests --

func TestEditFileCapability_RecordsStartLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(p, []byte("l1\nl2\nl3 target\nl4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": p, "old_string": "l3 target", "new_string": "l3 changed"})
	res, err := EditFile().Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.StartLine != 3 {
		t.Errorf("StartLine = %d, want 3 (match begins on line 3 of the pre-edit file)", res.StartLine)
	}
}

func TestWriteFileCapability_RecordsStartLineOne(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.txt")
	args, _ := json.Marshal(map[string]any{"path": p, "content": "a\nb\n"})
	res, err := WriteFile().Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.StartLine != 1 {
		t.Errorf("StartLine = %d, want 1 (a write always begins at line 1)", res.StartLine)
	}
}

// -- WorkDir resolution tests --

func TestWriteFile_RelativePathResolvesAgainstWorkDir(t *testing.T) {
	workDir := t.TempDir()
	call := &capabilities.Call{
		WorkDir: workDir,
		Args:    []byte(`{"path":"out.txt","content":"data"}`),
		Emit:    func(string) {},
	}
	if _, err := WriteFile().Execute(context.Background(), call); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(workDir, "out.txt"))
	if err != nil || string(b) != "data" {
		t.Errorf("file not written under WorkDir: b=%q err=%v", b, err)
	}
}

func TestEditFile_RelativePathResolvesAgainstWorkDir(t *testing.T) {
	workDir := t.TempDir()
	relPath := "edit.txt"
	fullPath := filepath.Join(workDir, relPath)
	os.WriteFile(fullPath, []byte("foo bar"), 0o644)

	call := &capabilities.Call{
		WorkDir: workDir,
		Args:    []byte(`{"path":"edit.txt","old_string":"bar","new_string":"BAR"}`),
		Emit:    func(string) {},
	}
	if _, err := EditFile().Execute(context.Background(), call); err != nil {
		t.Fatalf("edit: %v", err)
	}
	b, err := os.ReadFile(fullPath)
	if err != nil || string(b) != "foo BAR" {
		t.Errorf("file not edited at WorkDir path: b=%q err=%v", b, err)
	}
}
