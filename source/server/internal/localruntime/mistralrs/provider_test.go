package mistralrs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
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

	provider := NewProvider(config.MistralRSConfig{
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
	if model.Runtime != runtimeName || model.Format != "gguf" || model.Family != "qwen" || model.Quantization != "Q4_K_M" {
		t.Fatalf("unexpected model metadata: %#v", model)
	}
	if model.DownloadState != "downloaded" || !model.SupportsChat || !model.SupportsTools {
		t.Fatalf("unexpected model flags: %#v", model)
	}
}

// TestDiscoverIncludesCuratedCatalog: on an empty model dir, Discover surfaces
// exactly the embedded curated catalog entries (as not-yet-downloaded records)
// and no on-disk models.
func TestDiscoverIncludesCuratedCatalog(t *testing.T) {
	dir := t.TempDir()
	provider := NewProvider(config.MistralRSConfig{ModelDirs: []string{dir}})

	models, err := provider.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("expected curated catalog models from an empty dir, got none")
	}
	for _, m := range models {
		if m.Source != "catalog" {
			t.Fatalf("empty dir should yield only catalog models, got source %q for %s", m.Source, m.ID)
		}
		if m.Runtime != runtimeName || m.DownloadState != "not_downloaded" {
			t.Fatalf("unexpected catalog record: %#v", m)
		}
	}
}

func TestArgsForBuildsServeCommand(t *testing.T) {
	provider := NewProvider(config.MistralRSConfig{
		Host:      "127.0.0.1",
		ExtraArgs: []string{"--token-source", "none"},
	})
	model := provider.modelRecord("/models/test.gguf", fakeFileInfo{size: 42})

	got := provider.argsFor(model, 8123)
	want := []string{
		"serve",
		"-m", "/models/test.gguf",
		"--port", "8123",
		"--no-ui",
		"--token-source", "none",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestArgsForIncludesISQ(t *testing.T) {
	provider := NewProvider(config.MistralRSConfig{
		Host: "127.0.0.1",
		ISQ:  "Q4K",
	})
	model := provider.modelRecord("/models/test.gguf", fakeFileInfo{size: 42})

	got := provider.argsFor(model, 8123)
	want := []string{
		"serve",
		"-m", "/models/test.gguf",
		"--port", "8123",
		"--no-ui",
		"--isq", "Q4K",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestArgsForUsesLoadTargetDirectory(t *testing.T) {
	provider := NewProvider(config.MistralRSConfig{Host: "127.0.0.1"})
	// A multi-file safetensors/UQFF model: Path anchors the download inside the
	// model's directory, but mistral.rs is pointed at the directory itself.
	model := provider.modelRecord("/models/qwen3-4b/config.json", fakeFileInfo{size: 42})
	model.LoadTarget = "/models/qwen3-4b"

	got := provider.argsFor(model, 8123)
	want := []string{
		"serve",
		"-m", "/models/qwen3-4b",
		"--port", "8123",
		"--no-ui",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestCapabilitiesAdvertiseTools(t *testing.T) {
	caps := NewProvider(config.MistralRSConfig{}).Capabilities()
	if !caps.ManagedProcesses || !caps.CanStart || !caps.CanStop || !caps.CanRestart {
		t.Fatalf("lifecycle capabilities missing: %#v", caps)
	}
	if !caps.CanListModels || !caps.CanStreamLogs || !caps.SupportsChat {
		t.Fatalf("core capabilities missing: %#v", caps)
	}
	if !caps.SupportsTools {
		t.Fatal("mistral.rs provider must advertise tool support")
	}
}

// TestResolveBinaryNotFound: with no configured binary, none on PATH, and none
// under ~/.cercano/bin, resolveBinary returns the actionable install error.
func TestResolveBinaryNotFound(t *testing.T) {
	// Force an empty PATH and a HOME with no mistralrs so all three lookups
	// miss deterministically.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	_, err := NewProvider(config.MistralRSConfig{}).resolveBinary()
	if err == nil {
		t.Fatal("expected an error when the binary cannot be found")
	}
	if !strings.Contains(err.Error(), "mistralrs binary not found") || !strings.Contains(err.Error(), "mistralrs.binary") {
		t.Fatalf("error is not actionable: %v", err)
	}
}

// TestResolveBinaryConfigured: an explicit binary path that exists resolves to
// that path.
func TestResolveBinaryConfigured(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "mistralrs")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := NewProvider(config.MistralRSConfig{Binary: bin}).resolveBinary()
	if err != nil {
		t.Fatalf("resolveBinary returned error: %v", err)
	}
	if got != bin {
		t.Fatalf("resolveBinary = %q, want %q", got, bin)
	}
}

// TestWaitReadyFailsFastWhenInstanceDied: a dead process never becomes ready,
// so the readiness poll must return immediately with the recorded exit error
// instead of watching a closed port until the deadline.
func TestWaitReadyFailsFastWhenInstanceDied(t *testing.T) {
	p := &Provider{
		client:  http.DefaultClient,
		running: map[string]*managedInstance{},
	}
	p.running["dead"] = &managedInstance{record: localruntime.InstanceRecord{
		ID:        "dead",
		State:     localruntime.StateFailed,
		LastError: "exit status 1",
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	err := p.waitReady(ctx, "dead", "http://127.0.0.1:1")
	if err == nil || !strings.Contains(err.Error(), "exited during startup") {
		t.Fatalf("err = %v, want exited-during-startup", err)
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("err = %v, want the instance's LastError included", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waitReady took %s — death must fail fast, not poll out the window", elapsed)
	}
}

// TestFinishReadinessFlipsStartingToRunning: the background poller keeps
// waiting past the caller's readiness window and marks the instance running
// once /health finally answers — the mechanism that turns a slow-loading
// model into a warm, reusable instance.
func TestFinishReadinessFlipsStartingToRunning(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable) // still loading
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := &Provider{
		client:  server.Client(),
		running: map[string]*managedInstance{},
	}
	p.running["slow"] = &managedInstance{record: localruntime.InstanceRecord{
		ID:    "slow",
		State: localruntime.StateStarting,
	}}

	p.finishReadiness("slow", server.URL, nil)

	state, lastErr := p.instanceStatus("slow")
	if state != localruntime.StateRunning {
		t.Fatalf("state = %q, want running", state)
	}
	if lastErr != "" {
		t.Fatalf("lastError = %q, want cleared", lastErr)
	}
	p.mu.RLock()
	readyAt := p.running["slow"].record.ReadyAt
	p.mu.RUnlock()
	if readyAt.IsZero() {
		t.Fatal("ReadyAt not set")
	}
}

// TestFinishReadinessLeavesStoppedInstanceAlone: a user-stopped instance must
// not be resurrected to running by a late health response.
func TestFinishReadinessLeavesStoppedInstanceAlone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := &Provider{
		client:  server.Client(),
		running: map[string]*managedInstance{},
	}
	p.running["stopped"] = &managedInstance{record: localruntime.InstanceRecord{
		ID:    "stopped",
		State: localruntime.StateStopped,
	}}

	p.finishReadiness("stopped", server.URL, nil)

	state, _ := p.instanceStatus("stopped")
	if state != localruntime.StateStopped {
		t.Fatalf("state = %q — finishReadiness must not resurrect a stopped instance", state)
	}
}

// TestProbeReadyFallsBackToModels: when /health is not 2xx, a 2xx on
// /v1/models is accepted as the readiness signal.
func TestProbeReadyFallsBackToModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	p := &Provider{client: server.Client()}
	if err := p.probeReady(context.Background(), server.URL); err != nil {
		t.Fatalf("probeReady = %v, want nil via /v1/models fallback", err)
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
