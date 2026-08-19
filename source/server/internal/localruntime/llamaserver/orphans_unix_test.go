//go:build darwin || linux

package llamaserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
)

// deadOwnerPID returns a PID guaranteed not to be a live cercano process:
// a just-exited child. Combined with a nonsense OwnerExe, even an
// immediately recycled PID can't read as "alive" to the sweep.
func deadOwnerPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// spawnVictim starts a detached sleep process standing in for a leaked
// llama-server, and returns it with a cleanup kill in case the sweep
// under test fails to reap it.
func spawnVictim(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "60")
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Tests own this child directly, so kill-and-wait here instead
		// of killProcess. killProcess confirms disappearance via
		// processAlive, and a dead child remains "alive" as a zombie until
		// this Wait reaps it. Provider.Stop has a watch goroutine doing
		// that Wait concurrently; this cleanup path does not.
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

func writeRegistryFile(t *testing.T, dir string, state registryFile) {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%d.json", state.OwnerPID))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func spawnRecordedServer(t *testing.T, modelPath string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "while :; do sleep 1; done", "llama-test", modelPath)
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

func healthyServer(t *testing.T) (port int, close func()) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.Close)
	_, portText, err := net.SplitHostPort(s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err = strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return port, s.Close
}

// TestSweepOrphans_ReapsDeadOwnersServers: a registry file whose owner is
// gone means its recorded servers are orphans holding wired GPU memory —
// the sweep must kill them and remove the file.
func TestSweepOrphans_ReapsDeadOwnersServers(t *testing.T) {
	dir := t.TempDir()
	victim := spawnVictim(t)
	owner := deadOwnerPID(t)
	writeRegistryFile(t, dir, registryFile{
		OwnerPID: owner,
		OwnerExe: "cercano-test-dead-owner",
		Servers: []serverEntry{{
			PID:       victim.Process.Pid,
			Binary:    "/bin/sleep",
			StartedAt: time.Now(),
		}},
	})

	// terminateGroup polls processAlive until the PID disappears. Because
	// this test process owns the victim, disappearance requires Wait to
	// reap the zombie, so the waiter must run concurrently with the sweep.
	done := make(chan error, 1)
	go func() { _, err := victim.Process.Wait(); done <- err }()

	p := &Provider{running: map[string]*managedInstance{}, registry: &pidRegistry{dir: dir}}
	p.SweepOrphans(nil)

	select {
	case <-done:
		// reaped
	case <-time.After(5 * time.Second):
		t.Fatal("sweep did not kill the orphaned server")
	}
	if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("%d.json", owner))); !os.IsNotExist(err) {
		t.Fatal("sweep did not remove the dead owner's registry file")
	}
}

// TestSweepOrphans_LeavesLiveOwnersAlone: several cercano processes run
// concurrently (agent, MCP servers). A sweep in one must never touch the
// servers of a sibling that is still alive.
func TestSweepOrphans_LeavesLiveOwnersAlone(t *testing.T) {
	dir := t.TempDir()
	victim := spawnVictim(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The "sibling" is this test process itself — alive, command line
	// matching — but its file is named so the sweep doesn't skip it as
	// the sweeper's own.
	state := registryFile{
		OwnerPID: os.Getpid(),
		OwnerExe: filepath.Base(exe),
		Servers:  []serverEntry{{PID: victim.Process.Pid, Binary: "/bin/sleep"}},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "99999.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	p := &Provider{running: map[string]*managedInstance{}, registry: &pidRegistry{dir: dir}}
	p.SweepOrphans(nil)

	if !processAlive(victim.Process.Pid) {
		t.Fatal("sweep killed a live sibling's server")
	}
	if _, err := os.Stat(filepath.Join(dir, "99999.json")); err != nil {
		t.Fatal("sweep removed a live sibling's registry file")
	}
}

// TestSweepOrphans_NeverKillsARecycledPID: the recorded PID exists but no
// longer runs the recorded binary — reaping it would kill an innocent
// process, so the sweep must only drop the bookkeeping.
func TestSweepOrphans_NeverKillsARecycledPID(t *testing.T) {
	dir := t.TempDir()
	victim := spawnVictim(t)
	writeRegistryFile(t, dir, registryFile{
		OwnerPID: deadOwnerPID(t),
		OwnerExe: "cercano-test-dead-owner",
		Servers: []serverEntry{{
			PID:    victim.Process.Pid,
			Binary: "/opt/homebrew/bin/llama-server", // victim actually runs sleep
		}},
	})

	p := &Provider{running: map[string]*managedInstance{}, registry: &pidRegistry{dir: dir}}
	p.SweepOrphans(nil)

	if !processAlive(victim.Process.Pid) {
		t.Fatal("sweep killed a process whose command line no longer matches the registry entry")
	}
}

func TestReapBeforeSpawn_ReapsDeadOwnerForRequestedModel(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(modelPath, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	victim := spawnRecordedServer(t, modelPath)
	wait := waitOwnedProcess(victim)
	owner := deadOwnerPID(t)
	writeRegistryFile(t, dir, registryFile{
		OwnerPID: owner,
		OwnerExe: "cercano-test-dead-owner",
		Servers:  []serverEntry{{PID: victim.Process.Pid, Binary: "/bin/sh", ModelPath: modelPath, Port: 65001}},
	})

	p := NewProvider(config.LlamaServerConfig{})
	p.registry = &pidRegistry{dir: dir}
	p.reapBeforeSpawn(context.Background(), localruntime.ModelRecord{Path: modelPath}, "/bin/sh", nil)

	select {
	case <-wait:
		// reaped and waited
	case <-time.After(time.Second):
		t.Fatal("pre-spawn barrier did not reap the dead owner's server")
	}
	if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("%d.json", owner))); !os.IsNotExist(err) {
		t.Fatal("dead-owner registry file was not removed")
	}
}

func TestReapBeforeSpawn_ReapsUnhealthyLiveOwnerForRequestedModel(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(modelPath, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	victim := spawnRecordedServer(t, modelPath)
	wait := waitOwnedProcess(victim)
	state := registryFile{
		OwnerPID: os.Getpid(),
		OwnerExe: currentExecutableBase(),
		Servers:  []serverEntry{{PID: victim.Process.Pid, Binary: "/bin/sh", ModelPath: modelPath, Port: 1}}, // port 1 is not healthy/adoptable
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "99999.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewProvider(config.LlamaServerConfig{Host: "127.0.0.1"})
	p.registry = &pidRegistry{dir: dir}
	p.reapBeforeSpawn(context.Background(), localruntime.ModelRecord{Path: modelPath}, "/bin/sh", nil)

	select {
	case <-wait:
		// reaped and waited
	case <-time.After(time.Second):
		t.Fatal("pre-spawn barrier did not reap unhealthy live-owner server")
	}
}

func TestReapBeforeSpawn_LeavesHealthyLiveOwnerAlone(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(modelPath, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	victim := spawnRecordedServer(t, modelPath)
	port, closeHealth := healthyServer(t)
	defer closeHealth()
	state := registryFile{
		OwnerPID: os.Getpid(),
		OwnerExe: currentExecutableBase(),
		Servers:  []serverEntry{{PID: victim.Process.Pid, Binary: "/bin/sh", ModelPath: modelPath, Port: port}},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "99999.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewProvider(config.LlamaServerConfig{Host: "127.0.0.1"})
	p.registry = &pidRegistry{dir: dir}
	p.reapBeforeSpawn(context.Background(), localruntime.ModelRecord{Path: modelPath}, "/bin/sh", nil)

	if !processAlive(victim.Process.Pid) {
		t.Fatal("pre-spawn barrier killed a healthy live owner's server")
	}
}

// TestRegistry_RegisterUnregisterRoundTrip: the on-disk file must carry
// the owner identity plus every live entry — it is the only thing a
// future sweep has to go on.
func TestRegistry_RegisterUnregisterRoundTrip(t *testing.T) {
	dir := t.TempDir()
	r := newPidRegistry(dir)
	r.register(serverEntry{PID: 111, Binary: "/opt/llama-server", ModelPath: "/models/a.gguf", Port: 50001})
	r.register(serverEntry{PID: 222, Binary: "/opt/llama-server", ModelPath: "/models/b.gguf", Port: 50002})
	r.unregister(111)

	data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%d.json", os.Getpid())))
	if err != nil {
		t.Fatal(err)
	}
	var state registryFile
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.OwnerPID != os.Getpid() || state.OwnerExe == "" {
		t.Fatalf("owner identity missing: %+v", state)
	}
	if len(state.Servers) != 1 || state.Servers[0].PID != 222 {
		t.Fatalf("servers = %+v, want only pid 222", state.Servers)
	}
}

// TestStartProcess_RegistersAndUnregistersPID: the registry is only as
// good as the spawn path's bookkeeping — a PID that never gets recorded
// can never be swept, and one that never gets removed would be re-checked
// forever. Exercises the real startProcess → watch lifecycle with a
// short-lived process.
func TestStartProcess_RegistersAndUnregistersPID(t *testing.T) {
	dir := t.TempDir()
	p := &Provider{
		running:  map[string]*managedInstance{},
		registry: &pidRegistry{dir: dir},
	}
	p.running["inst"] = &managedInstance{
		model:  localruntime.ModelRecord{Path: "/models/fake.gguf"},
		record: localruntime.InstanceRecord{ID: "inst", State: localruntime.InstanceStarting},
	}

	// /bin/sleep rejects the llama-server flags and exits immediately —
	// exactly what's needed: a real spawn, then a fast real exit.
	if err := p.startProcess("inst", "/bin/sleep", nil); err != nil {
		t.Fatal(err)
	}
	pid := p.running["inst"].record.PID

	readState := func() registryFile {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%d.json", os.Getpid())))
		if err != nil {
			t.Fatal(err)
		}
		var state registryFile
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
		return state
	}

	state := readState()
	if len(state.Servers) != 1 || state.Servers[0].PID != pid || state.Servers[0].ModelPath != "/models/fake.gguf" {
		t.Fatalf("after spawn, registry = %+v, want the spawned pid %d on the books", state.Servers, pid)
	}

	// watch observes the exit and must take the PID off the books.
	p.watch("inst", "/bin/sleep", nil)
	if state := readState(); len(state.Servers) != 0 {
		t.Fatalf("after exit, registry = %+v, want empty", state.Servers)
	}
}
