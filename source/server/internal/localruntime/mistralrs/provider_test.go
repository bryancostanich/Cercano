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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
)

// TestReloadConfigRaceSafe hammers ReloadConfig concurrently with the readers
// (Discover, argsFor, resolveBinary) under -race. Before the snapshot refactor,
// p.cfg was written by ReloadConfig while these readers accessed it lock-free —
// a data race the race detector flags. It also confirms a reload actually
// changes the args a subsequent launch would use.
func TestReloadConfigRaceSafe(t *testing.T) {
	dir := t.TempDir()
	provider := NewProvider(config.MistralRSConfig{Host: "127.0.0.1", ModelDirs: []string{dir}})
	model := localruntime.ModelRecord{Path: filepath.Join(dir, "m.gguf")}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	// Writer: flip MaxSeqLen back and forth.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			seq := 8192
			if i%2 == 0 {
				seq = 32768
			}
			provider.ReloadConfig(config.Config{MistralRS: config.MistralRSConfig{
				Host: "127.0.0.1", ModelDirs: []string{dir}, MaxSeqLen: seq,
			}})
		}
	}()
	// Readers: exercise the snapshot paths.
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = provider.Discover(context.Background())
				_ = provider.argsFor(provider.snapshot(), model, 8123)
				_, _ = provider.resolveBinary()
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	// After reloads, a snapshot reflects one of the written values (proving the
	// swap took effect), and argsFor emits the corresponding --max-seq-len.
	provider.ReloadConfig(config.Config{MistralRS: config.MistralRSConfig{
		Host: "127.0.0.1", ModelDirs: []string{dir}, MaxSeqLen: 32768,
	}})
	args := provider.argsFor(provider.snapshot(), model, 8123)
	if !containsPair(args, "--max-seq-len", "32768") {
		t.Fatalf("expected reloaded --max-seq-len 32768 in args, got %v", args)
	}
}

func containsPair(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}

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

	got := withoutPagedAttn(provider.argsFor(provider.snapshot(), model, 8123))
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

	got := withoutPagedAttn(provider.argsFor(provider.snapshot(), model, 8123))
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

	got := withoutPagedAttn(provider.argsFor(provider.snapshot(), model, 8123))
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

	got := withoutPagedAttn(provider.argsFor(provider.snapshot(), model, 8123))
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

func TestArgsForAddsMemorySafetyCaps(t *testing.T) {
	provider := NewProvider(config.MistralRSConfig{
		Host:             "127.0.0.1",
		PagedAttn:        "auto",
		PAMemoryFraction: "0.35",
		MaxSeqLen:        32768,
		MaxSeqs:          1,
		MaxBatchSize:     1,
	})
	model := provider.modelRecord("/models/qwen3-30b/config.json", fakeFileInfo{size: 42})
	model.LoadTarget = "/models/qwen3-30b"

	got := provider.argsFor(provider.snapshot(), model, 8123)
	// "auto" is translated to an explicit "on" on platforms where mistral.rs
	// supports PagedAttention (Metal/CUDA), because its native "auto" DISABLES
	// the KV governor on Metal. On other platforms it stays "auto".
	wantPagedAttn := "auto"
	if pagedAttnAvailable() {
		wantPagedAttn = "on"
	}
	for _, want := range [][]string{
		{"--paged-attn", wantPagedAttn},
		{"--pa-memory-fraction", "0.35"},
		{"--max-seq-len", "32768"},
		{"--max-seqs", "1"},
		{"--max-batch-size", "1"},
	} {
		if !containsAdjacent(got, want[0], want[1]) {
			t.Fatalf("args missing %s %s: %#v", want[0], want[1], got)
		}
	}
}

func TestArgsForForcesPagedAttnOnMetal(t *testing.T) {
	if !pagedAttnAvailable() {
		t.Skip("PagedAttention not forced on this platform")
	}
	// Empty PagedAttn (the default) must resolve to "on" where supported, so the
	// KV-cache memory governor is active without explicit config.
	provider := NewProvider(config.MistralRSConfig{Host: "127.0.0.1"})
	model := provider.modelRecord("/models/qwen3-30b/config.json", fakeFileInfo{size: 42})
	model.LoadTarget = "/models/qwen3-30b"
	got := provider.argsFor(provider.snapshot(), model, 8123)
	if !containsAdjacent(got, "--paged-attn", "on") {
		t.Fatalf("empty PagedAttn must force --paged-attn on where supported: %#v", got)
	}
}

func TestArgsForPAMemoryMBTakesPrecedenceOverFraction(t *testing.T) {
	provider := NewProvider(config.MistralRSConfig{
		Host:             "127.0.0.1",
		PAMemoryFraction: "0.35",
		PAMemoryMB:       8192,
	})
	model := provider.modelRecord("/models/qwen3-30b/config.json", fakeFileInfo{size: 42})
	model.LoadTarget = "/models/qwen3-30b"
	got := provider.argsFor(provider.snapshot(), model, 8123)
	if !containsAdjacent(got, "--pa-memory-mb", "8192") {
		t.Fatalf("expected --pa-memory-mb 8192 when PAMemoryMB set: %#v", got)
	}
	for _, a := range got {
		if a == "--pa-memory-fraction" {
			t.Fatalf("fraction must be suppressed when absolute MB cap is set: %#v", got)
		}
	}
}

func TestArgsForPAMemoryFractionUsedWhenNoMB(t *testing.T) {
	provider := NewProvider(config.MistralRSConfig{
		Host:             "127.0.0.1",
		PAMemoryFraction: "0.35",
		// PAMemoryMB unset (0)
	})
	model := provider.modelRecord("/models/qwen3-30b/config.json", fakeFileInfo{size: 42})
	model.LoadTarget = "/models/qwen3-30b"
	got := provider.argsFor(provider.snapshot(), model, 8123)
	if !containsAdjacent(got, "--pa-memory-fraction", "0.35") {
		t.Fatalf("expected --pa-memory-fraction 0.35 when no MB cap: %#v", got)
	}
	for _, a := range got {
		if a == "--pa-memory-mb" {
			t.Fatalf("no --pa-memory-mb expected when PAMemoryMB unset: %#v", got)
		}
	}
}

func TestArgsForExtraArgsOverrideManagedMemoryCaps(t *testing.T) {
	provider := NewProvider(config.MistralRSConfig{
		Host:             "127.0.0.1",
		PagedAttn:        "auto",
		PAMemoryFraction: "0.35",
		MaxSeqLen:        32768,
		MaxSeqs:          1,
		MaxBatchSize:     1,
		ExtraArgs: []string{
			"--paged-attn", "off",
			"--pa-memory-mb=4096",
			"--max-seq-len", "8192",
			"--max-seqs", "2",
			"--max-batch-size", "4",
		},
	})
	model := provider.modelRecord("/models/qwen3-30b/config.json", fakeFileInfo{size: 42})
	model.LoadTarget = "/models/qwen3-30b"

	got := provider.argsFor(provider.snapshot(), model, 8123)
	if countFlag(got, "--paged-attn") != 1 || countFlag(got, "--max-seq-len") != 1 || countFlag(got, "--max-seqs") != 1 || countFlag(got, "--max-batch-size") != 1 {
		t.Fatalf("managed caps should not duplicate explicit extra_args: %#v", got)
	}
	if containsAdjacent(got, "--pa-memory-fraction", "0.35") {
		t.Fatalf("--pa-memory-mb extra arg should suppress managed pa_memory_fraction: %#v", got)
	}
}

// withoutPagedAttn strips a managed "--paged-attn <mode>" pair so exact-match
// arg tests can assert the rest of the command line independent of the
// platform-dependent PagedAttention default (forced "on" on Metal/CUDA).
func withoutPagedAttn(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--paged-attn" && i+1 < len(args) {
			i++ // skip flag and its value
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func containsAdjacent(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func countFlag(args []string, flag string) int {
	count := 0
	for _, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			count++
		}
	}
	return count
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

	got := provider.argsFor(provider.snapshot(), model, 8123)
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

	// A protecting sibling must be a DIFFERENT live process, not this test
	// process: Stop() reclaims a sidecar whose owner is our own PID (the
	// last-owner case). Spawn a real long-lived helper to stand in as the
	// live sibling owner.
	sibling := exec.Command("sleep", "30")
	if err := sibling.Start(); err != nil {
		t.Fatalf("start sibling owner: %v", err)
	}
	defer func() { _ = sibling.Process.Kill(); _ = sibling.Wait() }()

	provider := NewProvider(config.MistralRSConfig{Host: "127.0.0.1"})
	provider.registry = newPidRegistry(filepath.Join(dir, "registry"))
	provider.registry.writeOwnLocked(registryFile{
		OwnerPID: sibling.Process.Pid,
		OwnerExe: "sleep", // must match the sibling's real command (ownerAlive check)
		Servers: []serverEntry{{
			PID:       cmd.Process.Pid,
			Binary:    binary,
			ModelPath: modelPath,
			Port:      port,
			StartedAt: time.Now().Add(-time.Minute),
		}},
	})
	// Rename the file so it describes a live sibling owner instead of this
	// provider's own file; siblingOwnerAlive validates the sibling PID is live.
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

// TestSiblingOwnerAlive guards the Bug B fix: an adopted sidecar is protected
// only by a DIFFERENT, live owner. Absent, dead, or self owners are not
// protecting siblings, so the last owner may reclaim the sidecar on shutdown.
func TestSiblingOwnerAlive(t *testing.T) {
	if siblingOwnerAlive(0) {
		t.Error("owner pid 0 (absent) must not be a protecting sibling")
	}
	if siblingOwnerAlive(-1) {
		t.Error("negative owner pid must not be a protecting sibling")
	}
	if siblingOwnerAlive(os.Getpid()) {
		t.Error("self as owner must not be a protecting sibling (last-owner case)")
	}

	// A live, different process IS a protecting sibling.
	sib := exec.Command("sleep", "30")
	if err := sib.Start(); err != nil {
		t.Fatalf("start sibling: %v", err)
	}
	defer func() { _ = sib.Process.Kill(); _ = sib.Wait() }()
	if !siblingOwnerAlive(sib.Process.Pid) {
		t.Error("a live, different owner must count as a protecting sibling")
	}

	// A dead process is NOT a protecting sibling.
	dead := exec.Command("sleep", "0.01")
	if err := dead.Start(); err != nil {
		t.Fatalf("start short-lived: %v", err)
	}
	deadPID := dead.Process.Pid
	_ = dead.Wait()
	if siblingOwnerAlive(deadPID) {
		t.Error("a dead owner must not be a protecting sibling")
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
