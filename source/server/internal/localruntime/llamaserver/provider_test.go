package llamaserver

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"cercano/source/server/internal/config"
)

func TestDiscoverFindsGGUFModels(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "Qwen3-Coder-Q4_K_M.gguf")
	if err := os.WriteFile(modelPath, []byte("gguf"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore"), 0644); err != nil {
		t.Fatal(err)
	}

	provider := NewProvider(config.LlamaServerConfig{
		ModelDirs:    []string{dir},
		DefaultModel: "Qwen3-Coder-Q4_K_M",
	})

	models, err := provider.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %#v", models)
	}
	model := models[0]
	if model.Path != modelPath {
		t.Fatalf("expected path %q, got %q", modelPath, model.Path)
	}
	if !model.Active {
		t.Fatal("expected default model to be marked active")
	}
	if model.Format != "gguf" || model.Family != "qwen" || model.Quantization != "Q4_K_M" {
		t.Fatalf("unexpected model metadata: %#v", model)
	}
}

func TestArgsForBuildsLlamaServerCommand(t *testing.T) {
	provider := NewProvider(config.LlamaServerConfig{
		Host:        "127.0.0.1",
		ContextSize: 4096,
		GPULayers:   "auto",
		Threads:     6,
		ExtraArgs:   []string{"--no-webui"},
	})
	model := modelRecord("/models/test.gguf", fakeFileInfo{size: 42})

	got := provider.argsFor(model, 8123)
	want := []string{
		"--model", "/models/test.gguf",
		"--host", "127.0.0.1",
		"--port", "8123",
		"--ctx-size", "4096",
		"--threads", "6",
		"--gpu-layers", "auto",
		"--no-webui",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

type fakeFileInfo struct {
	size int64
}

func (f fakeFileInfo) Name() string       { return "test.gguf" }
func (f fakeFileInfo) Size() int64        { return f.size }
func (f fakeFileInfo) Mode() os.FileMode  { return 0644 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }
