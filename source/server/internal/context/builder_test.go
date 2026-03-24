package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuilder_BuildPrompt_Basic(t *testing.T) {
	files := []DiscoveredFile{
		{RelPath: "README.md", Content: "# My Project\nA cool project."},
		{RelPath: "go.mod", Content: "module example.com/test"},
	}

	builder := NewBuilder()
	prompt, summary := builder.BuildPrompt(files, "")

	if !strings.Contains(prompt, "README.md") {
		t.Error("expected README.md in prompt")
	}
	if !strings.Contains(prompt, "go.mod") {
		t.Error("expected go.mod in prompt")
	}
	if !strings.Contains(summary, "2 files") {
		t.Errorf("expected '2 files' in summary, got %q", summary)
	}
}

func TestBuilder_BuildPrompt_WithHostContext(t *testing.T) {
	files := []DiscoveredFile{
		{RelPath: "README.md", Content: "# Project"},
	}

	builder := NewBuilder()
	prompt, _ := builder.BuildPrompt(files, "This project uses a custom binary protocol over SPI.")

	if !strings.Contains(prompt, "custom binary protocol over SPI") {
		t.Error("expected host context in prompt")
	}
	if !strings.Contains(prompt, "Additional context provided by the host AI") {
		t.Error("expected host context header")
	}
}

func TestBuilder_BuildPrompt_SizeLimit(t *testing.T) {
	// Create files that together exceed the max prompt size (48KB)
	bigContent := strings.Repeat("x", 20*1024)
	files := []DiscoveredFile{
		{RelPath: "file1.md", Content: bigContent},
		{RelPath: "file2.md", Content: bigContent},
		{RelPath: "file3.md", Content: bigContent},
	}

	builder := NewBuilder()
	prompt, summary := builder.BuildPrompt(files, "")

	if strings.Contains(prompt, "file3.md") {
		t.Error("file3.md should be excluded due to size limit")
	}
	// Should include file1 and file2 but not file3
	if !strings.Contains(summary, "2 files") {
		t.Errorf("expected 2 files in summary, got %q", summary)
	}
}

func TestBuilder_WriteContext(t *testing.T) {
	dir := t.TempDir()
	builder := NewBuilder()

	err := builder.WriteContext(dir, "# Project Context\n\nTest content.")
	if err != nil {
		t.Fatalf("WriteContext failed: %v", err)
	}

	// Verify file was created
	content, err := os.ReadFile(filepath.Join(dir, ".cercano", "context.md"))
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if string(content) != "# Project Context\n\nTest content." {
		t.Errorf("unexpected content: %q", string(content))
	}
}

func TestBuilder_WriteContext_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	builder := NewBuilder()

	err := builder.WriteContext(dir, "content")
	if err != nil {
		t.Fatalf("WriteContext failed: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, ".cercano"))
	if err != nil {
		t.Fatal("expected .cercano directory to be created")
	}
	if !info.IsDir() {
		t.Error("expected .cercano to be a directory")
	}
}
