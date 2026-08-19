package llamaserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cercano/source/server/internal/crashlog"
	"cercano/source/server/internal/localruntime"
)

// pidRegistry records the llama-server processes this cercano process has
// spawned, in a per-owner file under the registry dir. Spawned servers are
// detached (their own process group), so when the owning process dies
// without cleanup — SIGKILL, crash, binary swapped under it — the servers
// are reparented to init and keep the full model wired in GPU memory
// indefinitely. The registry is what lets the next cercano process find and
// reap them: SweepOrphans kills the recorded servers of any owner that is
// no longer running.
//
// One file per owner PID. Several cercano processes run concurrently (the
// agent, MCP servers), and each must only ever write its own file — a
// shared file would race, and a name-based hunt (pgrep llama-server) could
// kill a live sibling's instances or a server the user started by hand.
type pidRegistry struct {
	mu  sync.Mutex
	dir string
}

type registryFile struct {
	OwnerPID int `json:"owner_pid"`
	// OwnerExe (basename) guards against owner-PID reuse: a recycled PID
	// only counts as "still alive" if it also still runs this executable.
	OwnerExe string        `json:"owner_exe"`
	Servers  []serverEntry `json:"servers"`
}

type serverEntry struct {
	PID       int       `json:"pid"`
	Binary    string    `json:"binary"`
	ModelPath string    `json:"model_path"`
	Port      int       `json:"port"`
	StartedAt time.Time `json:"started_at"`
}

// defaultRegistryDir returns the shared registry location, or "" (registry
// disabled) when the home dir cannot be resolved.
func defaultRegistryDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cercano", "run", "llamaserver")
}

func newPidRegistry(dir string) *pidRegistry {
	if dir == "" {
		return nil
	}
	return &pidRegistry{dir: dir}
}

func (r *pidRegistry) ownFile() string {
	return filepath.Join(r.dir, fmt.Sprintf("%d.json", os.Getpid()))
}

func (r *pidRegistry) register(entry serverEntry) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.loadOwnLocked()
	state.Servers = append(state.Servers, entry)
	r.writeOwnLocked(state)
}

func (r *pidRegistry) unregister(pid int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.loadOwnLocked()
	kept := state.Servers[:0]
	for _, entry := range state.Servers {
		if entry.PID != pid {
			kept = append(kept, entry)
		}
	}
	state.Servers = kept
	r.writeOwnLocked(state)
}

func (r *pidRegistry) loadOwnLocked() registryFile {
	state := registryFile{OwnerPID: os.Getpid()}
	if exe, err := os.Executable(); err == nil {
		state.OwnerExe = filepath.Base(exe)
	}
	data, err := os.ReadFile(r.ownFile())
	if err != nil {
		return state
	}
	var parsed registryFile
	if json.Unmarshal(data, &parsed) == nil && parsed.OwnerPID == os.Getpid() {
		state.Servers = parsed.Servers
	}
	return state
}

// writeOwnLocked persists this owner's state atomically — a sweeping
// sibling must never read a torn file, because a parse failure would make
// it skip entries that then leak.
func (r *pidRegistry) writeOwnLocked(state registryFile) {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	tmp := r.ownFile() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, r.ownFile())
}

// SweepOrphans reaps llama-server processes left behind by cercano
// processes that died without cleanup. Call it once at startup, before
// spawning anything. Live owners' files are left strictly alone; a server
// is only killed when its recorded PID still runs the recorded binary (and
// model, when recorded), so a recycled PID can never take down an
// unrelated process.
func (p *Provider) SweepOrphans(sink localruntime.LogSink) {
	r := p.registry
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return
	}
	for _, dirEntry := range entries {
		name := dirEntry.Name()
		if dirEntry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(r.dir, name)
		if path == r.ownFile() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var state registryFile
		if err := json.Unmarshal(data, &state); err != nil || state.OwnerPID <= 0 {
			// Unparseable or damaged file: nothing can be safely reaped
			// from it, and keeping it would re-attempt forever.
			_ = os.Remove(path)
			continue
		}
		if ownerAlive(state) {
			continue
		}
		for _, server := range state.Servers {
			if !serverStillOurs(server) {
				continue
			}
			res, err := terminateGroup(server.PID)
			msg := fmt.Sprintf("reaped orphaned llama-server pid %d (model %s, owner pid %d died without cleanup)",
				server.PID, filepath.Base(server.ModelPath), state.OwnerPID)
			if err != nil {
				msg = fmt.Sprintf("failed to confirm orphaned llama-server pid %d died (model %s, owner pid %d): %v",
					server.PID, filepath.Base(server.ModelPath), state.OwnerPID, err)
			}
			p.emit(sink, "warn", "", "", msg)
			extra := map[string]any{
				"owner_pid":      state.OwnerPID,
				"model_path":     server.ModelPath,
				"trigger":        "startup_sweep",
				"wait_ms":        res.Wait.Milliseconds(),
				"escalated":      res.Escalated,
				"already_gone":   res.AlreadyGone,
				"confirmed_dead": err == nil,
			}
			if err != nil {
				extra["error"] = err.Error()
			}
			p.event(crashlog.EventReap, msg, crashlog.RuntimeInfo{
				PID:  server.PID,
				Port: server.Port,
			}, extra)
		}
		_ = os.Remove(path)
	}
}

type reapCandidate struct {
	owner       registryFile
	server      serverEntry
	registry    string
	removeFile  bool
	trigger     string
	description string
}

// reapBeforeSpawn is the load-bearing startup barrier for the hard-lock
// class described in efforts/llama-server-memory-guard. It runs after a
// healthy sibling adoption attempt has failed and before cmd.Start. Dead
// owners are ordinary orphans. Live owners are considered only for the
// exact requested model and only when they look like the same executable
// family but their server is not healthy/adoptable — the observed restart
// shape where the old owner is draining while the new one is about to
// wire a second full model into memory.
func (p *Provider) reapBeforeSpawn(ctx context.Context, model localruntime.ModelRecord, binary string, sink localruntime.LogSink) {
	r := p.registry
	if r == nil || model.Path == "" {
		return
	}
	candidates := p.spawnReapCandidates(ctx, model, binary)
	for _, c := range candidates {
		res, err := terminateGroup(c.server.PID)
		msg := fmt.Sprintf("pre-spawn reap of llama-server pid %d for %s (%s)", c.server.PID, filepath.Base(c.server.ModelPath), c.description)
		if err != nil {
			msg = fmt.Sprintf("failed to confirm pre-spawn reaped llama-server pid %d died for %s (%s): %v", c.server.PID, filepath.Base(c.server.ModelPath), c.description, err)
		}
		p.emit(sink, "warn", "", "", msg)
		extra := map[string]any{
			"owner_pid":      c.owner.OwnerPID,
			"model_path":     c.server.ModelPath,
			"trigger":        c.trigger,
			"wait_ms":        res.Wait.Milliseconds(),
			"escalated":      res.Escalated,
			"already_gone":   res.AlreadyGone,
			"confirmed_dead": err == nil,
		}
		if err != nil {
			extra["error"] = err.Error()
		}
		p.event(crashlog.EventReap, msg, crashlog.RuntimeInfo{PID: c.server.PID, Port: c.server.Port}, extra)
		if c.removeFile {
			_ = os.Remove(c.registry)
		}
	}
}

func (p *Provider) spawnReapCandidates(ctx context.Context, model localruntime.ModelRecord, binary string) []reapCandidate {
	r := p.registry
	if r == nil {
		return nil
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil
	}
	currentExe := currentExecutableBase()
	var out []reapCandidate
	for _, dirEntry := range entries {
		name := dirEntry.Name()
		if dirEntry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(r.dir, name)
		if path == r.ownFile() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var state registryFile
		if err := json.Unmarshal(data, &state); err != nil || state.OwnerPID <= 0 {
			continue
		}
		ownerLive := ownerAlive(state)
		for _, server := range state.Servers {
			if !serverStillOurs(server) || server.ModelPath != model.Path || filepath.Base(server.Binary) != filepath.Base(binary) {
				continue
			}
			if !ownerLive {
				out = append(out, reapCandidate{owner: state, server: server, registry: path, removeFile: true, trigger: "pre_spawn_dead_owner", description: fmt.Sprintf("owner pid %d is gone", state.OwnerPID)})
				continue
			}
			if currentExe == "" || state.OwnerExe == "" || state.OwnerExe != currentExe {
				continue
			}
			if p.serverHealthy(ctx, server) {
				continue
			}
			out = append(out, reapCandidate{owner: state, server: server, registry: path, trigger: "pre_spawn_unhealthy_live_owner", description: fmt.Sprintf("owner pid %d is live but server is not healthy/adoptable", state.OwnerPID)})
		}
	}
	return out
}

func (p *Provider) serverHealthy(ctx context.Context, server serverEntry) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	host := p.snapshot().Host
	endpoint := fmt.Sprintf("http://%s:%d", healthHost(host), server.Port)
	return p.probeEndpoint(probeCtx, endpoint) == nil
}

func currentExecutableBase() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Base(exe)
}

// ownerAlive reports whether the recorded owner process still exists AND
// still runs the recorded executable — both, so a recycled PID doesn't
// keep a dead owner's file (and its leaked servers) alive forever.
func ownerAlive(state registryFile) bool {
	if !processAlive(state.OwnerPID) {
		return false
	}
	if state.OwnerExe == "" {
		// No executable recorded: err on the side of "alive" — never
		// reap when ownership can't be established.
		return true
	}
	return strings.Contains(processCommand(state.OwnerPID), state.OwnerExe)
}

// serverStillOurs verifies the recorded PID is alive and its command line
// still matches what the registry says was spawned there.
func serverStillOurs(server serverEntry) bool {
	if server.PID <= 0 || !processAlive(server.PID) {
		return false
	}
	command := processCommand(server.PID)
	if !strings.Contains(command, filepath.Base(server.Binary)) {
		return false
	}
	return server.ModelPath == "" || strings.Contains(command, server.ModelPath)
}

type liveSibling struct {
	owner  registryFile
	server serverEntry
}

// liveSiblingFor returns a healthy llama-server process for modelPath that is
// owned by another currently-live Cercano process. It never kills anything;
// unhealthy or ambiguous entries are ignored so callers can spawn their own
// managed process if no safe sibling exists.
func (p *Provider) liveSiblingFor(ctx context.Context, modelPath, binary string, sink localruntime.LogSink) (*liveSibling, bool) {
	r := p.registry
	if r == nil {
		return nil, false
	}
	modelPath = filepath.Clean(modelPath)
	r.mu.Lock()
	defer r.mu.Unlock()
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil, false
	}
	for _, dirEntry := range entries {
		name := dirEntry.Name()
		if dirEntry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(r.dir, name)
		if path == r.ownFile() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var state registryFile
		if err := json.Unmarshal(data, &state); err != nil || state.OwnerPID <= 0 || !ownerAlive(state) {
			continue
		}
		for _, server := range state.Servers {
			if filepath.Clean(server.ModelPath) != modelPath || !serverStillOurs(server) {
				continue
			}
			if binary != "" && filepath.Base(server.Binary) != filepath.Base(binary) {
				continue
			}
			if server.Port <= 0 {
				continue
			}
			endpoint := fmt.Sprintf("http://%s:%d", healthHost(p.snapshot().Host), server.Port)
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := p.probeEndpoint(probeCtx, endpoint)
			cancel()
			if err != nil {
				p.emit(sink, "warn", "", "", fmt.Sprintf("ignored llama-server sibling pid %d from owner pid %d: health failed: %v", server.PID, state.OwnerPID, err))
				continue
			}
			return &liveSibling{owner: state, server: server}, true
		}
	}
	return nil, false
}

func (p *Provider) probeEndpoint(ctx context.Context, endpoint string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned status %d", resp.StatusCode)
	}
	return nil
}
