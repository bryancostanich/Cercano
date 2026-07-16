package slash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterContext_ReadsFromWorkDir(t *testing.T) {
	// Create a temp dir with a .cercano/context.md file.
	dir := t.TempDir()
	ctxDir := filepath.Join(dir, ".cercano")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const content = "# My Project Context\nThis is the context."
	if err := os.WriteFile(filepath.Join(ctxDir, "context.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := New()
	RegisterContext(r, func() string { return dir })

	res, ok := r.Dispatch("/context")
	if !ok {
		t.Fatalf("dispatch returned ok=false")
	}
	if res.Kind != ResultText {
		t.Fatalf("kind = %v, want ResultText", res.Kind)
	}
	if !strings.Contains(res.Text, content) {
		t.Fatalf("result text missing file content: %q", res.Text)
	}
}

func TestRegisterContext_NilWorkDirFallsBackToGetwd(t *testing.T) {
	// When getter is nil, the handler must not panic and must fall back to
	// os.Getwd() (the file likely won't exist there, so we get the "no
	// project context" message — the important thing is no panic).
	r := New()
	RegisterContext(r, nil)

	res, ok := r.Dispatch("/context")
	// ok=true means the command dispatched without panic.
	if !ok {
		t.Fatalf("dispatch returned ok=false")
	}
	if res.Kind != ResultText {
		t.Fatalf("kind = %v, want ResultText", res.Kind)
	}
	// Either "no project context" or actual file content — both are valid.
	// The key contract is no panic.
}

func TestContextRegen_ReturnsRegenResult(t *testing.T) {
	r := New()
	RegisterContextRegen(r)
	res, ok := r.Dispatch("/context-regen")
	if !ok {
		t.Fatal("expected /context-regen to dispatch")
	}
	if res.Kind != ResultRegenContext {
		t.Fatalf("kind = %v, want ResultRegenContext", res.Kind)
	}
}

func TestCompact_ReturnsCompactResult(t *testing.T) {
	r := New()
	RegisterCompact(r)
	res, ok := r.Dispatch("/compact")
	if !ok {
		t.Fatal("expected /compact to dispatch")
	}
	if res.Kind != ResultCompactContext {
		t.Fatalf("kind = %v, want ResultCompactContext", res.Kind)
	}
}

func TestClearCompactedContext_ReturnsClearResult(t *testing.T) {
	r := New()
	RegisterClearCompactedContext(r)
	res, ok := r.Dispatch("/clear-compacted-context")
	if !ok {
		t.Fatal("expected /clear-compacted-context to dispatch")
	}
	if res.Kind != ResultClearCompactedContext {
		t.Fatalf("kind = %v, want ResultClearCompactedContext", res.Kind)
	}
}
