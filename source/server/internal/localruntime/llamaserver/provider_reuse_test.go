package llamaserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
)

// TestStart_ReusesLiveInstanceForSameModel: Start is the last line of
// defense against duplicate spawns — whatever a caller requests, a model
// that already has a live instance must get that instance back, never a
// second process wiring another full copy of the weights into GPU memory.
func TestStart_ReusesLiveInstanceForSameModel(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "phi4-mini-latest.gguf")
	if err := os.WriteFile(modelPath, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Binary points at the stub file: it exists, so resolveBinary passes,
	// and the reuse path must return before anything tries to exec it.
	p := NewProvider(config.LlamaServerConfig{
		ModelDirs: []string{dir},
		Binary:    modelPath,
	})
	p.running["llama_server:seeded:1234"] = &managedInstance{
		model: localruntime.ModelRecord{Path: modelPath},
		record: localruntime.InstanceRecord{
			ID:       "llama_server:seeded:1234",
			Runtime:  runtimeName,
			State:    localruntime.StateRunning,
			Endpoint: "http://127.0.0.1:1234",
		},
	}

	// The Ollama-style alias resolves to the same file the seeded instance
	// serves — the incident's exact request shape.
	record, err := p.Start(context.Background(), localruntime.StartRequest{ModelID: "phi4-mini:latest"}, nil)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if record.ID != "llama_server:seeded:1234" {
		t.Fatalf("Start returned instance %q, want the seeded live instance", record.ID)
	}
	if len(p.running) != 1 {
		t.Fatalf("Start left %d instances for one model, want 1 (no duplicate spawn)", len(p.running))
	}
}

// TestStart_StoppedInstanceIsNotReused: a user-stopped instance is dead
// weight, not a warm instance — Restart's Stop→Start sequence relies on
// Start actually starting something new in that case. (It will fail at
// process exec here because the binary is a stub; reaching the spawn path
// at all is what's being asserted.)
func TestStart_StoppedInstanceIsNotReused(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "phi4-mini-latest.gguf")
	if err := os.WriteFile(modelPath, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewProvider(config.LlamaServerConfig{
		ModelDirs: []string{dir},
		Binary:    modelPath,
	})
	p.running["llama_server:seeded:1234"] = &managedInstance{
		stopping: true,
		model:    localruntime.ModelRecord{Path: modelPath},
		record: localruntime.InstanceRecord{
			ID:    "llama_server:seeded:1234",
			State: localruntime.StateStopped,
		},
	}

	record, _ := p.Start(context.Background(), localruntime.StartRequest{ModelID: "phi4-mini:latest"}, nil)
	if record != nil && record.ID == "llama_server:seeded:1234" {
		t.Fatal("Start reused a stopped instance instead of spawning a new one")
	}
}
