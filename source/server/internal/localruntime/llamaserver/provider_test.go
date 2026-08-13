package llamaserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
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
	model, ok := findModelByPath(models, modelPath)
	if !ok {
		t.Fatalf("expected local model %q in %#v", modelPath, models)
	}
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

func TestDiscoverIncludesQwenCatalogModels(t *testing.T) {
	dir := t.TempDir()
	provider := NewProvider(config.LlamaServerConfig{ModelDirs: []string{dir}})

	models, err := provider.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	model, ok := findModelByID(models, runtimeName+":catalog:qwen3-14b-q4_k_m")
	if !ok {
		t.Fatalf("expected Qwen3 catalog model in %#v", models)
	}
	if model.Source != "catalog" || model.DownloadState != localruntime.DownloadNotStarted {
		t.Fatalf("unexpected catalog state: %#v", model)
	}
	if len(model.DownloadURLs) == 0 || model.DownloadTotalBytes == 0 {
		t.Fatalf("expected download metadata: %#v", model)
	}
	if model.Path != filepath.Join(dir, "qwen3-14b-q4_k_m", "Qwen3-14B-Q4_K_M.gguf") {
		t.Fatalf("unexpected target path: %q", model.Path)
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
	model := provider.modelRecord("/models/test.gguf", fakeFileInfo{size: 42})

	got := provider.argsFor(provider.snapshot(), model, 8123)
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

// TestArgsForAppendsPerModelExtraArgs verifies a catalog model's per-model
// ExtraArgs (e.g. GLM's required "--jinja") are appended after the global
// config ExtraArgs, and that a model with no ExtraArgs adds nothing extra.
func TestArgsForAppendsPerModelExtraArgs(t *testing.T) {
	provider := NewProvider(config.LlamaServerConfig{
		Host:      "127.0.0.1",
		ExtraArgs: []string{"--no-webui"},
	})

	glm := localruntime.ModelRecord{Path: "/models/glm.gguf", ExtraArgs: []string{"--jinja"}}
	got := provider.argsFor(provider.snapshot(), glm, 8123)
	want := []string{
		"--model", "/models/glm.gguf",
		"--host", "127.0.0.1",
		"--port", "8123",
		"--no-webui",
		"--jinja",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GLM args mismatch:\n got: %#v\nwant: %#v", got, want)
	}

	plain := localruntime.ModelRecord{Path: "/models/qwen.gguf"}
	gotPlain := provider.argsFor(provider.snapshot(), plain, 8123)
	for _, a := range gotPlain {
		if a == "--jinja" {
			t.Fatalf("non-GLM model must not carry --jinja: %#v", gotPlain)
		}
	}
}

// TestArgsForPassesMmproj verifies a vision model's projector is passed as
// --mmproj (right after --model) when the file is present, and is omitted with
// a text-only launch when the declared projector is missing at spawn time.
func TestLiveInstanceReuseRequiresSameMmproj(t *testing.T) {
	modelPath := "/models/qwen-vl.gguf"
	textOnly := localruntime.ModelRecord{ID: "llama_server:path", Path: modelPath}
	vision := localruntime.ModelRecord{ID: "llama_server:catalog:qwen-vl", Path: modelPath, MmprojPath: "/models/mmproj.gguf"}

	if canReuseInstanceForModel(textOnly, vision) {
		t.Fatal("text-only same-path instance must not satisfy a vision request")
	}
	if !canReuseInstanceForModel(vision, textOnly) {
		t.Fatal("text-only request can reuse a projector-backed server")
	}
	if !canReuseInstanceForModel(vision, vision) {
		t.Fatal("same path and same projector should be reusable")
	}
}

func TestArgsForPassesMmproj(t *testing.T) {
	provider := NewProvider(config.LlamaServerConfig{Host: "127.0.0.1"})

	dir := t.TempDir()
	projector := filepath.Join(dir, "mmproj-f16.gguf")
	if err := os.WriteFile(projector, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}

	vision := localruntime.ModelRecord{Path: "/models/vl.gguf", MmprojPath: projector}
	got := provider.argsFor(provider.snapshot(), vision, 8123)
	// Flag order among these is immaterial to llama-server; --mmproj is appended
	// after the base model/host/port trio.
	want := []string{
		"--model", "/models/vl.gguf",
		"--host", "127.0.0.1",
		"--port", "8123",
		"--mmproj", projector,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("vision args mismatch:\n got: %#v\nwant: %#v", got, want)
	}

	// Declared but missing at launch → text-only, no --mmproj.
	missing := localruntime.ModelRecord{Path: "/models/vl.gguf", MmprojPath: filepath.Join(dir, "gone.gguf")}
	gotMissing := provider.argsFor(provider.snapshot(), missing, 8123)
	for _, a := range gotMissing {
		if a == "--mmproj" {
			t.Fatalf("missing projector must not add --mmproj: %#v", gotMissing)
		}
	}

	// Non-vision model: no --mmproj at all.
	plain := localruntime.ModelRecord{Path: "/models/text.gguf"}
	for _, a := range provider.argsFor(provider.snapshot(), plain, 8123) {
		if a == "--mmproj" {
			t.Fatalf("text-only model must not add --mmproj")
		}
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

func findModelByPath(models []localruntime.ModelRecord, path string) (localruntime.ModelRecord, bool) {
	for _, model := range models {
		if model.Path == path {
			return model, true
		}
	}
	return localruntime.ModelRecord{}, false
}

func findModelByID(models []localruntime.ModelRecord, id string) (localruntime.ModelRecord, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return localruntime.ModelRecord{}, false
}

func TestAdoptLiveSiblingReusesHealthyRegisteredServer(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("unexpected health path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("OK"))
	}))
	defer health.Close()
	port := mustURLPort(t, health.URL)

	dir := t.TempDir()
	binary := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(modelPath, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binary, modelPath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	provider := NewProvider(config.LlamaServerConfig{Host: "127.0.0.1"})
	provider.registry = newPidRegistry(filepath.Join(dir, "registry"))
	provider.registry.writeOwnLocked(registryFile{
		OwnerPID: os.Getpid(),
		OwnerExe: filepath.Base(os.Args[0]),
		Servers: []serverEntry{{
			PID:       cmd.Process.Pid,
			Binary:    binary,
			ModelPath: modelPath,
			Port:      port,
			StartedAt: time.Now().Add(-time.Minute),
		}},
	})
	if err := os.Rename(provider.registry.ownFile(), filepath.Join(provider.registry.dir, "sibling.json")); err != nil {
		t.Fatal(err)
	}

	model := localruntime.ModelRecord{ID: "llama_server:model", Runtime: runtimeName, DisplayName: "Model", Path: modelPath}
	record, ok := provider.adoptLiveSibling(context.Background(), model, binary, localruntime.NewManager())
	if !ok {
		t.Fatal("expected healthy sibling to be adopted")
	}
	if record.Port != port || record.PID != cmd.Process.Pid || record.State != localruntime.InstanceRunning {
		t.Fatalf("unexpected adopted record: %#v", record)
	}
	if err := provider.Stop(context.Background(), record.ID); err != nil {
		t.Fatalf("Stop(adopted): %v", err)
	}
	if !processAlive(cmd.Process.Pid) {
		t.Fatal("Stop on adopted instance killed sibling-owned process")
	}
}

func mustURLPort(t *testing.T, raw string) int {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}
