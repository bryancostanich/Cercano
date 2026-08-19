package llamaserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cercano/source/server/internal/crashlog"
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
)

const gib = int64(1 << 30)

func sparseModel(t *testing.T, dir, name string, size int64) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStart_MemoryGuardRefusesBeforeSpawning(t *testing.T) {
	dir := t.TempDir()
	sparseModel(t, dir, "huge-model.gguf", 20*gib)
	p := NewProvider(config.LlamaServerConfig{ModelDirs: []string{dir}, Binary: "/usr/bin/false"})
	p.totalRAM = func() int64 { return 128 * gib }
	p.nonEvictable = func() (int64, bool) { return 110 * gib, true }
	log, read := newTestEventLog(t)
	p.SetEventLog(log)

	record, err := p.Start(context.Background(), localruntime.StartRequest{ModelID: "huge-model"}, nil)
	if err == nil {
		t.Fatal("Start succeeded; memory guard should have refused")
	}
	if record != nil {
		t.Fatalf("Start returned record %+v; refused spawn should create no synthetic instance", record)
	}
	if len(p.running) != 0 {
		t.Fatalf("running has %d entries; guard must block before inserting/spawning", len(p.running))
	}
	msg := err.Error()
	for _, want := range []string{"projected non-evictable memory", "current 110.00 GiB", "model 20.00 GiB", "headroom 10.00 GiB", "total 128.00 GiB"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	if _, ok := findEvent(read(), crashlog.EventRefused); !ok {
		t.Fatal("memory refusal did not write durable refused event")
	}
}

func TestStart_MemoryGuardPermitsWhenProjectionFits(t *testing.T) {
	dir := t.TempDir()
	sparseModel(t, dir, "small-model.gguf", 1*gib)
	p := NewProvider(config.LlamaServerConfig{ModelDirs: []string{dir}, Binary: "/usr/bin/false"})
	p.totalRAM = func() int64 { return 128 * gib }
	p.nonEvictable = func() (int64, bool) { return 10 * gib, true }

	// /usr/bin/false is a real executable, so if the guard permits the
	// path reaches cmd.Start and then readiness/exit handling fails later.
	_, err := p.Start(context.Background(), localruntime.StartRequest{ModelID: "small-model"}, nil)
	if err == nil {
		t.Fatal("expected /usr/bin/false launch to fail after the guard permitted it")
	}
	if strings.Contains(err.Error(), "memory guard refused") {
		t.Fatalf("guard refused a fit projection: %v", err)
	}
}

func TestMemoryGuardUsesRegistryAsFloorWhenProbeLags(t *testing.T) {
	p := NewProvider(config.LlamaServerConfig{})
	p.totalRAM = func() int64 { return 128 * gib }
	// Simulate the check-to-load window: the OS has not yet reflected a
	// just-started 100 GiB model as wired, but the provider has already
	// registered it as starting/running.
	p.nonEvictable = func() (int64, bool) { return 10 * gib, true }
	p.running["first"] = &managedInstance{
		model:  localruntime.ModelRecord{SizeBytes: 100 * gib},
		record: localruntime.InstanceRecord{ID: "first", PID: 4242, Port: 1234, State: localruntime.InstanceStarting},
	}

	projection, err := p.checkMemoryBudget(localruntime.ModelRecord{ID: "second", DisplayName: "second", SizeBytes: 20 * gib})
	if err == nil {
		t.Fatal("expected registry floor to refuse second large start while first is still loading")
	}
	if !projection.CurrentProbeOK || projection.FallbackToRegistry {
		t.Fatalf("probe should be marked successful without fallback: %+v", projection)
	}
	if projection.CurrentBytes != 100*gib {
		t.Fatalf("CurrentBytes = %d, want registry floor 100 GiB over lagging OS probe", projection.CurrentBytes)
	}
}

func TestMemoryGuardFallsBackToRegistryWhenProbeUnknown(t *testing.T) {
	p := NewProvider(config.LlamaServerConfig{})
	p.totalRAM = func() int64 { return 128 * gib }
	p.nonEvictable = func() (int64, bool) { return 0, false }
	p.running["seed"] = &managedInstance{
		model:  localruntime.ModelRecord{SizeBytes: 100 * gib},
		record: localruntime.InstanceRecord{ID: "seed", PID: 4242, Port: 1234, State: localruntime.InstanceRunning},
	}

	projection, err := p.checkMemoryBudget(localruntime.ModelRecord{ID: "next", DisplayName: "next", SizeBytes: 20 * gib})
	if err == nil {
		t.Fatal("expected registry fallback to refuse 100 GiB + 20 GiB with 10 GiB headroom on 128 GiB machine")
	}
	if !projection.FallbackToRegistry {
		t.Fatal("projection did not record fallback_to_registry")
	}
	if projection.CurrentBytes != 100*gib {
		t.Fatalf("CurrentBytes = %d, want registry estimate 100 GiB", projection.CurrentBytes)
	}
	if !strings.Contains(err.Error(), "largest live instance seed pid 4242 port 1234") {
		t.Fatalf("error did not name blocking instance: %v", err)
	}
}

func TestMemoryGuardPermitsWhenTotalUnknown(t *testing.T) {
	p := NewProvider(config.LlamaServerConfig{})
	p.totalRAM = func() int64 { return 0 }
	p.nonEvictable = func() (int64, bool) { return 120 * gib, true }
	_, err := p.checkMemoryBudget(localruntime.ModelRecord{ID: "next", DisplayName: "next", SizeBytes: 20 * gib})
	if err != nil {
		t.Fatalf("unknown total RAM should soft-fail rather than refuse: %v", err)
	}
}
