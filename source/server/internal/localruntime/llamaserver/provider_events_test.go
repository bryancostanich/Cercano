package llamaserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/crashlog"
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
)

// newTestEventLog returns a provider-attachable durable log plus a reader
// for what landed in it.
func newTestEventLog(t *testing.T) (*crashlog.Writer, func() []crashlog.Entry) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crash.log")
	w, err := crashlog.NewWriter(path, "test")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w, func() []crashlog.Entry {
		entries, err := crashlog.TailEntries(path, 100)
		if err != nil {
			t.Fatalf("TailEntries: %v", err)
		}
		return entries
	}
}

func findEvent(entries []crashlog.Entry, verb string) (crashlog.Entry, bool) {
	for _, e := range entries {
		if e.Kind == crashlog.KindRuntimeEvent && e.Event == verb {
			return e, true
		}
	}
	return crashlog.Entry{}, false
}

// TestStart_ReuseWritesDurableEvent: the whole point of the durable log is
// that lifecycle decisions survive the process. A reuse must be recorded
// with the instance actually handed back, so a post-mortem can tell reuse
// from a fresh spawn.
func TestStart_ReuseWritesDurableEvent(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "phi4-mini-latest.gguf")
	if err := os.WriteFile(modelPath, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := NewProvider(config.LlamaServerConfig{ModelDirs: []string{dir}, Binary: modelPath})
	log, read := newTestEventLog(t)
	p.SetEventLog(log)

	p.running["llama_server:seeded:1234"] = &managedInstance{
		model: localruntime.ModelRecord{Path: modelPath},
		record: localruntime.InstanceRecord{
			ID:       "llama_server:seeded:1234",
			Runtime:  runtimeName,
			State:    localruntime.InstanceRunning,
			Endpoint: "http://127.0.0.1:1234",
			PID:      4242,
			Port:     1234,
		},
	}

	if _, err := p.Start(context.Background(), localruntime.StartRequest{ModelID: "phi4-mini:latest"}, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}

	entries := read()
	e, ok := findEvent(entries, crashlog.EventReuse)
	if !ok {
		t.Fatalf("no reuse event recorded; got %d entries", len(entries))
	}
	if e.Runtime == nil {
		t.Fatal("reuse event carried no runtime payload")
	}
	if e.Runtime.InstanceID != "llama_server:seeded:1234" {
		t.Errorf("InstanceID = %q, want the seeded instance", e.Runtime.InstanceID)
	}
	if e.Runtime.PID != 4242 || e.Runtime.Port != 1234 {
		t.Errorf("PID/Port = %d/%d, want 4242/1234 — the fields emit() could never supply",
			e.Runtime.PID, e.Runtime.Port)
	}
	if e.Runtime.Runtime != runtimeName {
		t.Errorf("Runtime = %q, want %q", e.Runtime.Runtime, runtimeName)
	}
	if _, spawned := findEvent(entries, crashlog.EventSpawn); spawned {
		t.Error("reuse path recorded a spawn event; the two must be distinguishable")
	}
}

// TestStart_SpawnWritesDurableEventWithModelSize: the spawn record carries
// the model size, which is the input to the memory projection. Without it
// a post-mortem cannot reconstruct why a spawn was permitted.
func TestStart_SpawnWritesDurableEventWithModelSize(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "phi4-mini-latest.gguf")
	if err := os.WriteFile(modelPath, []byte("stub-weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Binary is the stub model file: resolveBinary passes, exec fails.
	// Reaching the spawn path is what matters here, not a live server.
	p := NewProvider(config.LlamaServerConfig{ModelDirs: []string{dir}, Binary: modelPath})
	log, read := newTestEventLog(t)
	p.SetEventLog(log)

	_, _ = p.Start(context.Background(), localruntime.StartRequest{ModelID: "phi4-mini:latest"}, nil)

	entries := read()
	e, ok := findEvent(entries, crashlog.EventSpawn)
	if !ok {
		t.Fatalf("no spawn event recorded; got %d entries", len(entries))
	}
	if e.Runtime == nil || e.Runtime.Port == 0 {
		t.Errorf("spawn event missing port: %+v", e.Runtime)
	}
	size, ok := e.Extra["model_size_bytes"].(float64)
	if !ok || size <= 0 {
		t.Errorf("model_size_bytes = %v, want the on-disk weight size", e.Extra["model_size_bytes"])
	}
}

// TestProviderEvent_NilLogIsNoOp: providers are constructed without a log
// in tests and in MCP embedded mode. That must stay silent, not panic.
func TestProviderEvent_NilLogIsNoOp(t *testing.T) {
	p := NewProvider(config.LlamaServerConfig{})
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("event with nil log panicked: %v", r)
		}
	}()
	p.event(crashlog.EventSpawn, "no log attached", crashlog.RuntimeInfo{PID: 1}, nil)
}

// TestProviderEvent_DoesNotCaptureSubprocessOutput: emit carries piped
// llama-server stdout (see pipeLogs). If durable logging were hooked
// there, routine subprocess chatter would bury the lifecycle records the
// log exists for. This pins the separation.
func TestProviderEvent_DoesNotCaptureSubprocessOutput(t *testing.T) {
	p := NewProvider(config.LlamaServerConfig{})
	log, read := newTestEventLog(t)
	p.SetEventLog(log)

	sink := &captureSink{}
	for i := 0; i < 50; i++ {
		p.emit(sink, "info", "inst", "model", "llama_server log line", "llama_server.inst.stdout")
	}

	if got := len(read()); got != 0 {
		t.Errorf("%d durable entries written from emit; want 0 (subprocess output must not reach the durable log)", got)
	}
	if len(sink.entries) != 50 {
		t.Errorf("UI sink got %d entries, want 50 — emit's existing behavior must be unchanged", len(sink.entries))
	}
}

type captureSink struct{ entries []localruntime.LogEntry }

func (c *captureSink) WriteLog(e localruntime.LogEntry) { c.entries = append(c.entries, e) }
