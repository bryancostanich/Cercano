package mistralrs

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
	if model.DownloadState != localruntime.Downloaded || !model.SupportsChat || !model.SupportsTools {
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
		if m.Runtime != runtimeName || m.DownloadState != localruntime.DownloadNotStarted {
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

func TestArgsForUQFFAddsFromUQFFShard(t *testing.T) {
	provider := NewProvider(config.MistralRSConfig{Host: "127.0.0.1"})
	// A prebuilt UQFF model: -m points at the local dir, and --from-uqff names
	// the first shard's basename so mistral.rs actually loads the .uqff artifact
	// (without it, the load fails with "Missing required tensor ... q_proj.weight").
	model := provider.modelRecord("/models/qwen3-14b/residual.safetensors", fakeFileInfo{size: 42})
	model.LoadTarget = "/models/qwen3-14b"
	model.Format = "uqff"
	model.DownloadURLs = []string{
		"https://huggingface.co/mistralrs-community/Qwen3-14B-UQFF/resolve/main/residual.safetensors",
		"https://huggingface.co/mistralrs-community/Qwen3-14B-UQFF/resolve/main/afq4-0.uqff",
	}

	got := provider.argsFor(model, 8123)
	want := []string{
		"serve",
		"-m", "/models/qwen3-14b",
		"--port", "8123",
		"--no-ui",
		"--from-uqff", "afq4-0.uqff",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestArgsForSafetensorsOmitsFromUQFF(t *testing.T) {
	provider := NewProvider(config.MistralRSConfig{Host: "127.0.0.1"})
	// A plain safetensors model must NOT get --from-uqff; it loads via -m alone.
	model := provider.modelRecord("/models/qwen3-4b/config.json", fakeFileInfo{size: 42})
	model.LoadTarget = "/models/qwen3-4b"
	model.Format = "safetensors"
	model.DownloadURLs = []string{
		"https://huggingface.co/Qwen/Qwen3-4B/resolve/main/model.safetensors",
	}

	got := provider.argsFor(model, 8123)
	for _, a := range got {
		if a == "--from-uqff" {
			t.Fatalf("safetensors model should not include --from-uqff, got: %#v", got)
		}
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
		State:     localruntime.InstanceFailed,
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
		State: localruntime.InstanceStarting,
	}}

	p.finishReadiness("slow", server.URL, nil)

	state, lastErr := p.instanceStatus("slow")
	if state != localruntime.InstanceRunning {
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
		State: localruntime.InstanceStopped,
	}}

	p.finishReadiness("stopped", server.URL, nil)

	state, _ := p.instanceStatus("stopped")
	if state != localruntime.InstanceStopped {
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
	binary := filepath.Join(dir, "mistralrs")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	modelPath := filepath.Join(dir, "qwen", "config.json")
	if err := os.MkdirAll(filepath.Dir(modelPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte("{}"), 0o644); err != nil {
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

	provider := NewProvider(config.MistralRSConfig{Host: "127.0.0.1"})
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
	// Rename the file so it describes a live sibling owner instead of this
	// provider's own file; ownerAlive still validates against this test process.
	siblingFile := filepath.Join(provider.registry.dir, "sibling.json")
	if err := os.Rename(provider.registry.ownFile(), siblingFile); err != nil {
		t.Fatal(err)
	}

	model := localruntime.ModelRecord{ID: "mistralrs:catalog:qwen", Runtime: runtimeName, DisplayName: "Qwen", Path: modelPath}
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

func TestParseRuntimeProcesses(t *testing.T) {
	out := ` 67512     1 /Users/me/.cercano/bin/mistralrs serve -m /Users/me/.cercano/models/qwen/config.json --port 123
 69113 69111 /Users/me/.cercano/bin/mistralrs serve -m /Users/me/.cercano/models/qwen/config.json --port 456
 70000     1 grep mistralrs
`
	procs := parseRuntimeProcesses(out, "/mistralrs serve ")
	if len(procs) != 2 {
		t.Fatalf("process count = %d, want 2: %#v", len(procs), procs)
	}
	if procs[0].PID != 67512 || procs[0].PPID != 1 {
		t.Fatalf("unexpected first process: %#v", procs[0])
	}
	if procs[1].PID != 69113 || procs[1].PPID != 69111 {
		t.Fatalf("unexpected second process: %#v", procs[1])
	}
}
