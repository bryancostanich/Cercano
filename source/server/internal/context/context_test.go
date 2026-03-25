package context

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoader_LoadFromProject(t *testing.T) {
	dir := t.TempDir()
	cercanoDir := filepath.Join(dir, ".cercano")
	os.MkdirAll(cercanoDir, 0755)
	os.WriteFile(filepath.Join(cercanoDir, "context.md"), []byte("# Project Context\n\nThis is a test project."), 0644)

	loader := NewLoader()
	ctx, err := loader.Load(dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if ctx != "# Project Context\n\nThis is a test project." {
		t.Errorf("unexpected context: %q", ctx)
	}
}

func TestLoader_CachesContext(t *testing.T) {
	dir := t.TempDir()
	cercanoDir := filepath.Join(dir, ".cercano")
	os.MkdirAll(cercanoDir, 0755)
	os.WriteFile(filepath.Join(cercanoDir, "context.md"), []byte("original"), 0644)

	loader := NewLoader()
	ctx1, _ := loader.Load(dir)

	// Change the file — cached version should still be returned
	os.WriteFile(filepath.Join(cercanoDir, "context.md"), []byte("modified"), 0644)
	ctx2, _ := loader.Load(dir)

	if ctx1 != ctx2 {
		t.Errorf("expected cached result, got different: %q vs %q", ctx1, ctx2)
	}
}

func TestLoader_NoContextFile(t *testing.T) {
	dir := t.TempDir()

	loader := NewLoader()
	ctx, err := loader.Load(dir)
	if err != nil {
		t.Fatalf("Load should not error for missing file: %v", err)
	}
	if ctx != "" {
		t.Errorf("expected empty context, got %q", ctx)
	}
}

func TestLoader_IsInitialized(t *testing.T) {
	dir := t.TempDir()
	loader := NewLoader()

	if loader.IsInitialized(dir) {
		t.Error("expected not initialized for empty dir")
	}

	cercanoDir := filepath.Join(dir, ".cercano")
	os.MkdirAll(cercanoDir, 0755)
	os.WriteFile(filepath.Join(cercanoDir, "context.md"), []byte("context"), 0644)

	if !loader.IsInitialized(dir) {
		t.Error("expected initialized after writing context.md")
	}
}

func TestLoader_Invalidate(t *testing.T) {
	dir := t.TempDir()
	cercanoDir := filepath.Join(dir, ".cercano")
	os.MkdirAll(cercanoDir, 0755)
	os.WriteFile(filepath.Join(cercanoDir, "context.md"), []byte("original"), 0644)

	loader := NewLoader()
	loader.Load(dir)

	// Update file and invalidate cache
	os.WriteFile(filepath.Join(cercanoDir, "context.md"), []byte("updated"), 0644)
	loader.Invalidate(dir)

	ctx, _ := loader.Load(dir)
	if ctx != "updated" {
		t.Errorf("expected updated context after invalidate, got %q", ctx)
	}
}

func TestLoader_PrependContext(t *testing.T) {
	dir := t.TempDir()
	cercanoDir := filepath.Join(dir, ".cercano")
	os.MkdirAll(cercanoDir, 0755)
	os.WriteFile(filepath.Join(cercanoDir, "context.md"), []byte("Key struct: Foo has field bar at offset 0x10"), 0644)

	loader := NewLoader()
	result := loader.PrependContext(dir, "Explain this code")
	if result != "Project Context:\nKey struct: Foo has field bar at offset 0x10\n\n---\n\nExplain this code" {
		t.Errorf("unexpected prepend result: %q", result)
	}
}

func TestLoader_PrependContext_NoContext(t *testing.T) {
	dir := t.TempDir()
	loader := NewLoader()
	result := loader.PrependContext(dir, "Explain this code")
	if result != "Explain this code" {
		t.Errorf("expected unchanged prompt, got %q", result)
	}
}

func TestLoader_PrependContext_EmptyProjectDir(t *testing.T) {
	loader := NewLoader()
	result := loader.PrependContext("", "Explain this code")
	if result != "Explain this code" {
		t.Errorf("expected unchanged prompt, got %q", result)
	}
}

func TestLoader_NudgeNeeded(t *testing.T) {
	dir := t.TempDir()
	loader := NewLoader()

	// No context.md, first call — should nudge
	if !loader.NudgeNeeded(dir) {
		t.Error("expected nudge needed for uninitialized project")
	}

	// Second call — already nudged
	if loader.NudgeNeeded(dir) {
		t.Error("expected no nudge on second call")
	}
}

func TestLoader_NudgeNeeded_Initialized(t *testing.T) {
	dir := t.TempDir()
	cercanoDir := filepath.Join(dir, ".cercano")
	os.MkdirAll(cercanoDir, 0755)
	os.WriteFile(filepath.Join(cercanoDir, "context.md"), []byte("context"), 0644)

	loader := NewLoader()
	if loader.NudgeNeeded(dir) {
		t.Error("expected no nudge for initialized project")
	}
}

func TestLoader_NudgeNeeded_EmptyDir(t *testing.T) {
	loader := NewLoader()
	if loader.NudgeNeeded("") {
		t.Error("expected no nudge for empty project dir")
	}
}
