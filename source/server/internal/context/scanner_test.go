package context

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanner_DiscoverFiles(t *testing.T) {
	dir := t.TempDir()

	// Create key files
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# My Project"), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test"), 0644)
	os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:"), 0644)
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0644)

	// Create files that should be skipped
	os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0755)
	os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "index.js"), []byte("module.exports = {}"), 0644)
	os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0755)
	os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[core]"), 0644)

	scanner := NewScanner()
	files, err := scanner.DiscoverFiles(dir)
	if err != nil {
		t.Fatalf("DiscoverFiles failed: %v", err)
	}

	if len(files) == 0 {
		t.Fatal("expected discovered files")
	}

	// Should include README.md and go.mod
	found := map[string]bool{}
	for _, f := range files {
		found[filepath.Base(f.Path)] = true
	}
	if !found["README.md"] {
		t.Error("expected README.md in discovered files")
	}
	if !found["go.mod"] {
		t.Error("expected go.mod in discovered files")
	}

	// Should NOT include node_modules or .git contents
	for _, f := range files {
		if filepath.Base(filepath.Dir(f.Path)) == "node_modules" {
			t.Errorf("should not include node_modules file: %s", f.Path)
		}
		if filepath.Base(filepath.Dir(f.Path)) == ".git" {
			t.Errorf("should not include .git file: %s", f.Path)
		}
	}
}

func TestScanner_DiscoverFiles_ClaudeMemory(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".claude", "memory")
	os.MkdirAll(memDir, 0755)
	os.WriteFile(filepath.Join(memDir, "user_prefs.md"), []byte("# User Prefs"), 0644)
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Claude Instructions"), 0644)

	scanner := NewScanner()
	files, err := scanner.DiscoverFiles(dir)
	if err != nil {
		t.Fatalf("DiscoverFiles failed: %v", err)
	}

	found := map[string]bool{}
	for _, f := range files {
		found[filepath.Base(f.Path)] = true
	}
	if !found["CLAUDE.md"] {
		t.Error("expected CLAUDE.md")
	}
	if !found["user_prefs.md"] {
		t.Error("expected memory files")
	}
}

func TestScanner_DiscoverFiles_ProtoAndHeaders(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "proto"), 0755)
	os.WriteFile(filepath.Join(dir, "proto", "api.proto"), []byte("syntax = \"proto3\";"), 0644)
	os.MkdirAll(filepath.Join(dir, "include"), 0755)
	os.WriteFile(filepath.Join(dir, "include", "types.h"), []byte("typedef struct {}"), 0644)

	scanner := NewScanner()
	files, err := scanner.DiscoverFiles(dir)
	if err != nil {
		t.Fatalf("DiscoverFiles failed: %v", err)
	}

	found := map[string]bool{}
	for _, f := range files {
		found[filepath.Base(f.Path)] = true
	}
	if !found["api.proto"] {
		t.Error("expected .proto files")
	}
	if !found["types.h"] {
		t.Error("expected .h files")
	}
}

func TestScanner_DiscoverFiles_SizeLimit(t *testing.T) {
	dir := t.TempDir()
	// Create a file larger than the size limit
	bigContent := make([]byte, MaxFileSize+1)
	os.WriteFile(filepath.Join(dir, "README.md"), bigContent, 0644)
	// Create a small file
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)

	scanner := NewScanner()
	files, err := scanner.DiscoverFiles(dir)
	if err != nil {
		t.Fatalf("DiscoverFiles failed: %v", err)
	}

	for _, f := range files {
		if filepath.Base(f.Path) == "README.md" {
			t.Error("should skip files over size limit")
		}
	}
}

func TestScanner_DiscoverFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	scanner := NewScanner()
	files, err := scanner.DiscoverFiles(dir)
	if err != nil {
		t.Fatalf("DiscoverFiles failed: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestScanner_PriorityOrder(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("readme"), 0644)
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("claude"), 0644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "api.proto"), []byte("syntax = \"proto3\";"), 0644)

	scanner := NewScanner()
	files, err := scanner.DiscoverFiles(dir)
	if err != nil {
		t.Fatalf("DiscoverFiles failed: %v", err)
	}

	// High-priority files should come first
	if len(files) < 2 {
		t.Fatalf("expected at least 2 files, got %d", len(files))
	}
	firstName := filepath.Base(files[0].Path)
	if firstName != "CLAUDE.md" && firstName != "README.md" {
		t.Errorf("expected high-priority file first, got %q", firstName)
	}
}
