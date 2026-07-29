package llamaserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cercano/source/server/pkg/config"
)

// writeGGUF creates an empty file with the given name in dir. The Discover
// scanner only looks at extension + size > 0, so a single byte is enough to
// simulate a "real" model without shipping a multi-GB fixture.
func writeGGUF(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte{0x00}, 0o644); err != nil {
		t.Fatalf("write gguf: %v", err)
	}
	return p
}

// writeFakeBinary drops an executable stub at dir/llama-server so exec.LookPath
// (rooted at dir when PATH is set) can find it. The file is never invoked by
// Detect — only inspected for existence + not-a-dir. On Windows, LookPath only
// resolves a bare name against PATHEXT-suffixed files (a plain extensionless
// stub is invisible to it), so the fake binary needs the .exe suffix there.
func writeFakeBinary(t *testing.T, dir string) string {
	t.Helper()
	name := "llama-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return p
}

// withPATH temporarily sets $PATH for the test's duration so LookPath finds
// only the fake binary we drop in dir (no interference from a real host-
// installed llama-server or unrelated PATH entries).
func withPATH(t *testing.T, dir string) {
	t.Helper()
	orig := os.Getenv("PATH")
	if err := os.Setenv("PATH", dir); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	t.Cleanup(func() { os.Setenv("PATH", orig) })
}

// baseCfg builds a LlamaServerConfig with a per-test model dir so tests don't
// contaminate each other's Discover scans.
func baseCfg(modelDir string) config.LlamaServerConfig {
	return config.LlamaServerConfig{ModelDirs: []string{modelDir}}
}

func TestDetect_SucceedsWithBinaryAndSingleModel(t *testing.T) {
	binDir := t.TempDir()
	modelDir := t.TempDir()
	writeFakeBinary(t, binDir)
	gguf := writeGGUF(t, modelDir, "qwen3-coder.gguf")
	withPATH(t, binDir)

	cfg := baseCfg(modelDir)
	if err := Detect(context.Background(), &cfg); err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if cfg.Binary == "" {
		t.Errorf("Binary not populated")
	}
	if cfg.DefaultModel != gguf {
		t.Errorf("DefaultModel = %q, want %q", cfg.DefaultModel, gguf)
	}
	if !cfg.Enabled {
		t.Errorf("Enabled should be true after successful detection")
	}
}

func TestDetect_ReturnsMissingBinaryWhenPATHEmpty(t *testing.T) {
	modelDir := t.TempDir()
	writeGGUF(t, modelDir, "qwen3-coder.gguf")
	withPATH(t, t.TempDir()) // empty dir on PATH — no llama-server

	cfg := baseCfg(modelDir)
	err := Detect(context.Background(), &cfg)
	if err == nil {
		t.Fatal("Detect should error when no llama-server on PATH")
	}
	var de *DetectError
	if !errors.As(err, &de) {
		t.Fatalf("expected *DetectError, got %T: %v", err, err)
	}
	if de.Missing != "binary" {
		t.Errorf("Missing = %q, want %q", de.Missing, "binary")
	}
	// SuggestedCommand names the exact command "Install now" would run
	// (defaultInstallCommand in install.go), which only exists on darwin —
	// everywhere else there's no managed install path, so it must be empty
	// rather than suggesting a brew command that doesn't exist there (e.g.
	// Windows, where brew isn't a thing).
	wantCmd := ""
	if runtime.GOOS == "darwin" {
		wantCmd = "brew install llama.cpp"
	}
	if got := de.SuggestedCommand(); got != wantCmd {
		t.Errorf("SuggestedCommand = %q, want %q", got, wantCmd)
	}
	if cfg.Enabled {
		t.Errorf("Enabled should stay false when binary is missing")
	}
}

func TestDetect_ReturnsMissingModelWhenBinaryFoundButNoGGUFs(t *testing.T) {
	binDir := t.TempDir()
	modelDir := t.TempDir()
	writeFakeBinary(t, binDir)
	// no GGUFs in modelDir
	withPATH(t, binDir)

	cfg := baseCfg(modelDir)
	err := Detect(context.Background(), &cfg)
	if err == nil {
		t.Fatal("Detect should error when no GGUFs present")
	}
	var de *DetectError
	if !errors.As(err, &de) {
		t.Fatalf("expected *DetectError, got %T: %v", err, err)
	}
	if de.Missing != "model" {
		t.Errorf("Missing = %q, want %q", de.Missing, "model")
	}
	// Binary was found so it should still be populated even on model failure —
	// the CLI needs the binary path to render "found llama-server at X but no
	// models" diagnostics.
	if cfg.Binary == "" {
		t.Errorf("Binary should be populated even when model detection fails")
	}
}

func TestDetect_PreservesExplicitDefaultModel(t *testing.T) {
	binDir := t.TempDir()
	modelDir := t.TempDir()
	writeFakeBinary(t, binDir)
	writeGGUF(t, modelDir, "a.gguf")
	writeGGUF(t, modelDir, "b.gguf")
	withPATH(t, binDir)

	cfg := baseCfg(modelDir)
	cfg.DefaultModel = "user-chosen-model.gguf" // explicit choice — no scan
	if err := Detect(context.Background(), &cfg); err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if cfg.DefaultModel != "user-chosen-model.gguf" {
		t.Errorf("DefaultModel = %q, want the explicit value preserved", cfg.DefaultModel)
	}
}

func TestDetect_AmbiguousModelReturnsError(t *testing.T) {
	binDir := t.TempDir()
	modelDir := t.TempDir()
	writeFakeBinary(t, binDir)
	writeGGUF(t, modelDir, "a.gguf")
	writeGGUF(t, modelDir, "b.gguf")
	withPATH(t, binDir)

	cfg := baseCfg(modelDir)
	err := Detect(context.Background(), &cfg)
	if err == nil {
		t.Fatal("Detect should error when multiple GGUFs are present and no default is set")
	}
	var de *DetectError
	if !errors.As(err, &de) {
		t.Fatalf("expected *DetectError, got %T: %v", err, err)
	}
	if de.Missing != "model" {
		t.Errorf("Missing = %q, want %q", de.Missing, "model")
	}
	if !strings.Contains(de.Error(), "disambiguate") {
		t.Errorf("error message should mention disambiguation, got: %v", de)
	}
}

func TestDetect_PrefersQwenOverGLMWhenAmbiguous(t *testing.T) {
	binDir := t.TempDir()
	modelDir := t.TempDir()
	writeFakeBinary(t, binDir)
	// GLM loads but has known plain-chat problems under llama-server, so a
	// qwen instruct GGUF must win auto-selection when both are present.
	writeGGUF(t, modelDir, "GLM-4.5-Air-Q4_K_M-00001-of-00002.gguf")
	writeGGUF(t, modelDir, "Qwen3-30B-A3B-Instruct-2507-Q4_K_M.gguf")
	withPATH(t, binDir)

	cfg := baseCfg(modelDir)
	if err := Detect(context.Background(), &cfg); err != nil {
		t.Fatalf("Detect should auto-select the preferred model, got error: %v", err)
	}
	if !strings.Contains(strings.ToLower(cfg.DefaultModel), "qwen3-30b-a3b-instruct") {
		t.Fatalf("DefaultModel = %q, want the qwen instruct GGUF", cfg.DefaultModel)
	}
}

func TestDetect_AppliesDefaultsWhenFieldsEmpty(t *testing.T) {
	// Detect should populate ModelDirs (etc.) from config.Defaults() when
	// callers pass an entirely zero-valued cfg. Without this the model_dir
	// mkdir would run on an empty slice and Discover would find nothing.
	binDir := t.TempDir()
	writeFakeBinary(t, binDir)
	withPATH(t, binDir)

	cfg := config.LlamaServerConfig{}
	// We expect the "model" failure (no GGUFs in the default dir) but the
	// point of this test is that ModelDirs gets populated as a side effect.
	_ = Detect(context.Background(), &cfg)
	if len(cfg.ModelDirs) == 0 {
		t.Errorf("ModelDirs should be populated from defaults")
	}
	if cfg.Host == "" {
		t.Errorf("Host should be populated from defaults")
	}
	if cfg.ContextSize == 0 {
		t.Errorf("ContextSize should be populated from defaults")
	}
}
