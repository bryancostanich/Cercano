package mistralrs

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
)

const runtimeName = "mistralrs"

var quantRE = regexp.MustCompile(`(?i)(?:^|[-_. ])(Q[0-9][A-Z0-9]*(?:[_-][A-Z0-9]+){0,3})(?:$|[-_. ])`)

type instanceUpdater interface {
	UpdateInstance(localruntime.InstanceRecord)
}

type Provider struct {
	cfg     config.MistralRSConfig
	client  *http.Client
	mu      sync.RWMutex
	running map[string]*managedInstance

	// registry tracks spawned mistral.rs PIDs on disk so the next cercano
	// process can reap them if this one dies without cleanup (see
	// SweepOrphans). Nil disables tracking.
	registry *pidRegistry
}

type managedInstance struct {
	record   localruntime.InstanceRecord
	model    localruntime.ModelRecord
	cmd      *exec.Cmd
	stopping bool
	adopted  bool
	ownerPID int
}

func NewProvider(cfg config.MistralRSConfig) *Provider {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.ReadinessTimeout == "" {
		cfg.ReadinessTimeout = "60s"
	}
	if cfg.Restart.Backoff == "" {
		cfg.Restart.Backoff = "2s"
	}
	if cfg.Restart.MaxAttempts == 0 {
		cfg.Restart.MaxAttempts = 3
	}
	return &Provider{
		cfg:      cfg,
		client:   &http.Client{Timeout: 2 * time.Second},
		running:  make(map[string]*managedInstance),
		registry: newPidRegistry(defaultRegistryDir()),
	}
}

func (p *Provider) Name() string { return runtimeName }

func (p *Provider) Capabilities() localruntime.RuntimeCapabilities {
	return localruntime.RuntimeCapabilities{
		ManagedProcesses: true,
		CanStart:         true,
		CanStop:          true,
		CanRestart:       true,
		CanListModels:    true,
		CanStreamLogs:    true,
		SupportsChat:     true,
		// mistral.rs speaks the OpenAI tool-call protocol natively, so the
		// runtime advertises tool support (llama-server's provider does not).
		SupportsTools: true,
		// mistral.rs browses safetensors first (ISQ-quantized at load), then
		// pre-quantized UQFF, then GGUF — all formats its loaders accept.
		CatalogFormats: []string{"safetensors", "uqff", "gguf"},
	}
}

func (p *Provider) Discover(ctx context.Context) ([]localruntime.ModelRecord, error) {
	var models []localruntime.ModelRecord
	var errs []error
	for _, dir := range p.cfg.ModelDirs {
		select {
		case <-ctx.Done():
			return models, ctx.Err()
		default:
		}
		expanded, err := expandPath(dir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := os.Stat(expanded); err != nil {
			if !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("stat model dir %s: %w", expanded, err))
			}
			continue
		}
		err = filepath.WalkDir(expanded, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() || strings.ToLower(filepath.Ext(path)) != ".gguf" {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			model := p.modelRecord(path, info)
			model.Active = matchesModel(p.cfg.DefaultModel, model)
			models = append(models, model)
			return nil
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("scan model dir %s: %w", expanded, err))
		}
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].DisplayName < models[j].DisplayName
	})
	// Append the embedded curated catalog (safetensors/UQFF/GGUF builds
	// verified on the pinned mistral.rs) as downloadable records.
	models = append(models, p.catalogModels()...)
	return models, errors.Join(errs...)
}

func (p *Provider) Start(ctx context.Context, req localruntime.StartRequest, sink localruntime.LogSink) (*localruntime.InstanceRecord, error) {
	// Sweep again at the point of demand, not only during provider registration.
	// A launchd-adopted orphan has no registry entry for sibling adoption to see,
	// so duplicate prevention must be part of the start lifecycle itself.
	p.sweepUnregisteredOrphans(sink)

	model, err := p.resolveModel(ctx, req.ModelID)
	if err != nil {
		return nil, err
	}
	binary, err := p.resolveBinary()
	if err != nil {
		return nil, err
	}

	// One process per model: if a sibling Cercano process already owns a
	// healthy mistral.rs sidecar for this model, adopt a local record that
	// proxies to it instead of spawning another full model load on a random
	// port. This is intentionally before choosePort: the best port is the one
	// already serving the model.
	if adopted, ok := p.adoptLiveSibling(ctx, model, binary, sink); ok {
		return adopted, nil
	}

	port, err := p.choosePort()
	if err != nil {
		return nil, err
	}

	host := p.cfg.Host
	endpoint := fmt.Sprintf("http://%s:%d", healthHost(host), port)
	id := runtimeName + ":" + shortID(model.Path) + ":" + strconv.Itoa(port)
	now := time.Now()
	instance := &managedInstance{
		model: model,
		record: localruntime.InstanceRecord{
			ID:        id,
			Runtime:   runtimeName,
			ModelID:   model.ID,
			State:     localruntime.InstanceStarting,
			Address:   host,
			Port:      port,
			Endpoint:  endpoint,
			StartedAt: now,
		},
	}

	// One process per model: if a live instance already serves this model,
	// hand it back instead of spawning another. Every spawn wires the full
	// model into GPU memory, so a caller that misses the warm-instance
	// lookup (or several racing at once) must degrade to reuse here — not
	// to a fleet of duplicate servers that wires out physical RAM. The
	// check shares the insert's critical section so two concurrent Starts
	// can't both miss it.
	p.mu.Lock()
	if existing := p.liveInstanceForLocked(model.Path); existing != nil {
		record := existing.record
		p.mu.Unlock()
		p.emit(sink, "info", record.ID, model.ID, "reusing running mistral.rs for "+model.DisplayName)
		return &record, nil
	}
	p.running[id] = instance
	p.mu.Unlock()
	p.emit(sink, "info", id, model.ID, "starting mistral.rs for "+model.DisplayName)

	if err := p.startProcess(id, binary, sink); err != nil {
		p.updateRecord(id, sink, func(record *localruntime.InstanceRecord) {
			record.State = localruntime.InstanceFailed
			record.LastError = err.Error()
		})
		return &instance.record, err
	}
	// The watch goroutine owns the process from birth, so a crash during the
	// (possibly long) initial model load is observed — and restarted per
	// config — instead of leaving a stale record behind.
	go p.watch(id, binary, sink)

	readyCtx, cancel := context.WithTimeout(ctx, p.readinessTimeout())
	defer cancel()
	if err := p.waitReady(readyCtx, id, endpoint); err != nil {
		if p.instanceState(id) == localruntime.InstanceStarting {
			// The process is alive and still loading — a large model (or an
			// in-situ quantization pass) legitimately needs longer than the
			// readiness window, and killing it here would throw the whole
			// load away and pay it again on the next request. Leave it
			// loading: finishReadiness flips the record to running when the
			// port comes up, and callers retry against the warm instance.
			go p.finishReadiness(id, endpoint, sink)
			p.emit(sink, "warn", id, model.ID, "mistral.rs still loading "+model.DisplayName+" after "+p.readinessTimeout().String()+" — leaving it to finish in the background")
			return &instance.record, fmt.Errorf("mistral.rs is still loading %s (instance left running — retry shortly): %w", model.DisplayName, err)
		}
		// Not starting anymore: the process died. watch has already recorded
		// the exit (and handles any configured restarts); surface the error.
		p.updateRecord(id, sink, func(record *localruntime.InstanceRecord) {
			record.LastError = err.Error()
		})
		return &instance.record, err
	}

	p.updateRecord(id, sink, func(record *localruntime.InstanceRecord) {
		record.State = localruntime.InstanceRunning
		record.ReadyAt = time.Now()
	})

	p.mu.RLock()
	out := p.running[id].record
	p.mu.RUnlock()
	return &out, nil
}

func (p *Provider) adoptLiveSibling(ctx context.Context, model localruntime.ModelRecord, binary string, sink localruntime.LogSink) (*localruntime.InstanceRecord, bool) {
	sibling, ok := p.liveSiblingFor(ctx, model.Path, binary, sink)
	if !ok {
		return nil, false
	}
	host := p.cfg.Host
	endpoint := fmt.Sprintf("http://%s:%d", healthHost(host), sibling.server.Port)
	id := runtimeName + ":" + shortID(model.Path) + ":" + strconv.Itoa(sibling.server.Port)
	inst := &managedInstance{
		model:    model,
		adopted:  true,
		ownerPID: sibling.owner.OwnerPID,
		record: localruntime.InstanceRecord{
			ID:        id,
			Runtime:   runtimeName,
			ModelID:   model.ID,
			State:     localruntime.InstanceRunning,
			PID:       sibling.server.PID,
			Address:   host,
			Port:      sibling.server.Port,
			Endpoint:  endpoint,
			StartedAt: sibling.server.StartedAt,
			ReadyAt:   time.Now(),
		},
	}
	p.mu.Lock()
	if existing := p.liveInstanceForLocked(model.Path); existing != nil {
		record := existing.record
		p.mu.Unlock()
		p.emit(sink, "info", record.ID, model.ID, "reusing running mistral.rs for "+model.DisplayName)
		return &record, true
	}
	p.running[id] = inst
	p.mu.Unlock()
	p.emit(sink, "info", id, model.ID, fmt.Sprintf("adopted mistral.rs sidecar pid %d from owner pid %d for %s", sibling.server.PID, sibling.owner.OwnerPID, model.DisplayName))
	out := inst.record
	return &out, true
}

func (p *Provider) Stop(_ context.Context, instanceID string) error {
	p.mu.Lock()
	instance, ok := p.running[instanceID]
	if !ok {
		p.mu.Unlock()
		return fmt.Errorf("mistral.rs instance %q not found", instanceID)
	}
	instance.stopping = true
	cmd := instance.cmd
	adopted := instance.adopted
	ownerPID := instance.ownerPID
	pid := instance.record.PID
	instance.record.State = localruntime.InstanceStopped
	if adopted {
		// An adopted sidecar is normally owned by a *sibling* agent, so we must
		// not kill it out from under that owner. But if the recorded owner is no
		// longer alive — or is this very process (the last owner, shutting down)
		// — there is no sibling to protect, and leaving it running leaks the
		// model's (potentially huge) wired GPU memory. In that case, reclaim it.
		delete(p.running, instanceID)
		p.mu.Unlock()
		if pid > 0 && !siblingOwnerAlive(ownerPID) {
			terminateGroup(pid)
		}
		return nil
	}
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// Use the escalating group terminator (SIGTERM group -> grace -> SIGKILL):
	// a mistral.rs wedged mid-Metal-allocation can ignore a bare SIGTERM, so a
	// single non-escalating signal is unreliable ("hard to kill").
	terminateGroup(cmd.Process.Pid)
	return nil
}

// siblingOwnerAlive reports whether an adopted instance is still protected by a
// live sibling owner. The owner is NOT a protecting sibling when it is absent
// (pid <= 0), dead, or this very process — in those cases the caller is the last
// owner and may reclaim the sidecar.
func siblingOwnerAlive(ownerPID int) bool {
	if ownerPID <= 0 || ownerPID == os.Getpid() {
		return false
	}
	return processAlive(ownerPID)
}

func (p *Provider) Probe(ctx context.Context, instanceID string) (*localruntime.InstanceHealth, error) {
	p.mu.RLock()
	instance, ok := p.running[instanceID]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("mistral.rs instance %q not found", instanceID)
	}
	start := time.Now()
	// GET /health is a real route in v0.9.0 (verified in
	// mistralrs-server-core/src/route_registry.rs: HEALTH_ROUTE = "/health");
	// a 2xx on /v1/models is accepted as a fallback readiness signal.
	err := p.probeReady(ctx, instance.record.Endpoint)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return &localruntime.InstanceHealth{
			InstanceID: instanceID,
			State:      localruntime.InstanceUnreachable,
			LatencyMS:  latency,
			Error:      err.Error(),
			CheckedAt:  time.Now(),
		}, nil
	}
	return &localruntime.InstanceHealth{
		InstanceID: instanceID,
		State:      localruntime.InstanceHealthy,
		LatencyMS:  latency,
		CheckedAt:  time.Now(),
	}, nil
}

func (p *Provider) resolveModel(ctx context.Context, requested string) (localruntime.ModelRecord, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = strings.TrimSpace(p.cfg.DefaultModel)
	}
	models, err := p.Discover(ctx)
	if err != nil && len(models) == 0 {
		return localruntime.ModelRecord{}, err
	}
	if requested == "" && len(models) == 1 {
		return models[0], nil
	}
	for _, model := range models {
		if matchesModel(requested, model) {
			if model.DownloadState != localruntime.Downloaded {
				return localruntime.ModelRecord{}, fmt.Errorf("mistral.rs model %q is not downloaded", requested)
			}
			return model, nil
		}
	}
	if requested != "" {
		path, err := expandPath(requested)
		if err == nil {
			if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() && strings.ToLower(filepath.Ext(path)) == ".gguf" {
				return p.modelRecord(path, info), nil
			}
		}
		return localruntime.ModelRecord{}, fmt.Errorf("mistral.rs model %q not found in configured model_dirs", requested)
	}
	return localruntime.ModelRecord{}, errors.New("mistral.rs default_model is not configured")
}

func (p *Provider) resolveBinary() (string, error) {
	if p.cfg.Binary != "" {
		path, err := expandPath(p.cfg.Binary)
		if err != nil {
			return "", err
		}
		if info, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("mistral.rs binary %s: %w", path, err)
		} else if info.IsDir() {
			return "", fmt.Errorf("mistral.rs binary %s is a directory", path)
		}
		return path, nil
	}
	if path, err := exec.LookPath("mistralrs"); err == nil {
		return path, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".cercano", "bin", "mistralrs")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	// TODO(phase1): auto-fetch the pinned v0.9.0 Metal tarball. Until then a
	// missing binary is a clear, actionable configuration error.
	return "", errors.New("mistralrs binary not found; install via the mistral.rs installer or set mistralrs.binary")
}

func (p *Provider) choosePort() (int, error) {
	if p.cfg.Port > 0 {
		return p.cfg.Port, nil
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(healthHost(p.cfg.Host), "0"))
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func (p *Provider) startProcess(instanceID, binary string, sink localruntime.LogSink) error {
	p.mu.RLock()
	instance, ok := p.running[instanceID]
	p.mu.RUnlock()
	if !ok {
		return fmt.Errorf("mistral.rs instance %q not found", instanceID)
	}
	args := p.argsFor(instance.model, instance.record.Port)
	cmd := exec.Command(binary, args...)
	cmd.Stdin = nil
	// Turn up mistral.rs's own tracing so its model-load and
	// paged-attention/KV allocation lines are captured. RUST_LOG="off" in
	// config means "don't touch it" — inherit the parent environment as-is.
	if level := strings.TrimSpace(p.cfg.LogLevel); !strings.EqualFold(level, "off") {
		if level == "" {
			level = "info"
		}
		cmd.Env = append(os.Environ(), "RUST_LOG="+level)
	}
	setProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// On the books before anything else: if this process dies without
	// cleanup, the registry entry is how the next cercano finds and reaps
	// the detached server (SweepOrphans).
	p.registry.register(serverEntry{
		PID:       cmd.Process.Pid,
		Binary:    binary,
		ModelPath: instance.model.Path,
		Port:      instance.record.Port,
		StartedAt: time.Now(),
	})
	p.mu.Lock()
	instance.cmd = cmd
	instance.record.PID = cmd.Process.Pid
	instance.record.State = localruntime.InstanceStarting
	instance.record.StartedAt = time.Now()
	instance.record.LastError = ""
	p.mu.Unlock()
	p.updateSink(sink, instance.record)

	// Tee the child's stdout/stderr to a per-PID file that is fsync'd per
	// line, so the last lines before a hard crash/power-off survive on disk.
	// This is the durable record the in-memory sink cannot provide. A nil
	// writer (open failure) degrades to sink-only logging.
	lf := p.openInstanceLog(cmd.Process.Pid, sink, instanceID, instance.model.ID)
	var logsWG sync.WaitGroup
	logsWG.Add(2)
	go func() { defer logsWG.Done(); p.pipeLogs(stdout, sink, lf, instanceID, instance.model.ID, "stdout") }()
	go func() { defer logsWG.Done(); p.pipeLogs(stderr, sink, lf, instanceID, instance.model.ID, "stderr") }()
	if lf != nil {
		go func() { logsWG.Wait(); lf.Close() }()
	}
	return nil
}

// syncedLog is a mutex-guarded, fsync-per-line file writer shared by the
// stdout and stderr pipe goroutines. Per-line Sync is deliberate: mistral.rs
// at info level is low-frequency (load + per-request lines, not per-token), so
// the fsync cost is negligible and buys crash-survivability of the last lines.
type syncedLog struct {
	mu sync.Mutex
	f  *os.File
}

func (s *syncedLog) writeLine(stream, line string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.f, "%s [%s] %s\n", time.Now().Format(time.RFC3339Nano), stream, line)
	_ = s.f.Sync()
}

func (s *syncedLog) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.f.Close()
}

// openInstanceLog creates <registry.dir>/<pid>.mistralrs.log for the durable
// tee. Returns nil (and emits a warning) if the file can't be opened; callers
// treat nil as "sink-only".
func (p *Provider) openInstanceLog(pid int, sink localruntime.LogSink, runtimeID, modelID string) *syncedLog {
	if p.registry == nil || p.registry.dir == "" {
		return nil
	}
	if err := os.MkdirAll(p.registry.dir, 0o755); err != nil {
		p.emit(sink, "warn", runtimeID, modelID, "could not create mistral.rs log dir: "+err.Error())
		return nil
	}
	path := filepath.Join(p.registry.dir, fmt.Sprintf("%d.mistralrs.log", pid))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		p.emit(sink, "warn", runtimeID, modelID, "could not open mistral.rs log file: "+err.Error())
		return nil
	}
	p.emit(sink, "info", runtimeID, modelID, "mistral.rs logs -> "+path)
	return &syncedLog{f: f}
}

// argsFor builds the mistral.rs command line. The v0.9.0 unified CLI serves a
// model with `mistralrs serve -m <path> --port <port> --no-ui` (NOT the old
// plain/gguf/uqff subcommands). --isq applies in-situ quantization when
// configured. Every flag below is verified against the v0.9.0 source tree
// (mistralrs-cli/src/args): `-m`/`--port`/`--no-ui` and `--isq` sit at the
// `serve` level via DefaultModelOptions+ServerOptions; model format is
// auto-detected (the default `Auto` ModelType), so a UQFF/safetensors dir or a
// single GGUF file all load through the same `-m` target.
func (p *Provider) argsFor(model localruntime.ModelRecord, port int) []string {
	// mistral.rs is pointed at the model directory for a multi-file
	// safetensors/UQFF model (LoadTarget), or the file itself for a single-file
	// GGUF (Path when LoadTarget is empty).
	target := model.Path
	if model.LoadTarget != "" {
		target = model.LoadTarget
	}
	args := []string{
		"serve",
		"-m", target,
		"--port", strconv.Itoa(port),
		"--no-ui",
	}
	// A prebuilt UQFF repo ships a `residual.safetensors` plus one or more
	// `<quant>-N.uqff` shards. Pointing `-m` at the directory alone is NOT
	// enough: without --from-uqff, mistral.rs treats it as a plain safetensors
	// model, never loads the .uqff artifact, and dies with "Missing required
	// tensor(s) ... q_proj.weight". Verified live against the v0.9.0 binary on
	// Apple Silicon (2026-07). We pass the first `.uqff` shard's basename; the
	// binary auto-discovers sibling shards (afq4-0.uqff -> also afq4-1.uqff),
	// and the basename resolves inside the local `-m` directory.
	if strings.EqualFold(model.Format, "uqff") {
		if shard := firstUQFFShard(model.DownloadURLs); shard != "" {
			args = append(args, "--from-uqff", shard)
		}
	}
	if p.cfg.ISQ != "" {
		args = append(args, "--isq", p.cfg.ISQ)
	}
	// Memory/context caps are safety-critical on Apple Silicon: mistral.rs Metal
	// allocations live in IOAccelerator unified memory, not ordinary RSS, so an
	// uncapped 30B/262k-context model can allocate well beyond physical RAM while
	// ps still reports tiny RSS. Config defaults compute conservative RAM-aware
	// caps; explicit extra_args win and suppress duplicate managed flags.
	extra := p.cfg.ExtraArgs
	// mistral.rs's own "auto" mode ENABLES PagedAttention on CUDA but DISABLES it
	// on Metal/CPU — which silently turns off the KV-cache memory governor on
	// Apple Silicon, exactly where the unified-memory over-allocation above bites
	// hardest. Since Cercano wants the governor on, translate "auto" (and the
	// empty default) to an explicit "on" when PagedAttention is available on this
	// platform (Metal). Explicit "on"/"off" from config always wins.
	mode := strings.ToLower(strings.TrimSpace(p.cfg.PagedAttn))
	if mode == "" || mode == "auto" {
		if pagedAttnAvailable() {
			mode = "on"
		} else {
			mode = "auto"
		}
	}
	switch mode {
	case "auto":
		if !hasFlag(extra, "--paged-attn") {
			args = append(args, "--paged-attn", "auto")
		}
	case "on":
		if !hasFlag(extra, "--paged-attn") {
			args = append(args, "--paged-attn", "on")
		}
	case "off":
		if !hasFlag(extra, "--paged-attn") {
			args = append(args, "--paged-attn", "off")
		}
	}
	if f := strings.TrimSpace(p.cfg.PAMemoryFraction); f != "" && !hasAnyFlag(extra, "--pa-memory-fraction", "--pa-memory-mb", "--pa-context-len") {
		args = append(args, "--pa-memory-fraction", f)
	}
	if p.cfg.MaxSeqLen > 0 && !hasFlag(extra, "--max-seq-len") {
		args = append(args, "--max-seq-len", strconv.Itoa(p.cfg.MaxSeqLen))
	}
	if p.cfg.MaxSeqs > 0 && !hasFlag(extra, "--max-seqs") {
		args = append(args, "--max-seqs", strconv.Itoa(p.cfg.MaxSeqs))
	}
	if p.cfg.MaxBatchSize > 0 && !hasFlag(extra, "--max-batch-size") {
		args = append(args, "--max-batch-size", strconv.Itoa(p.cfg.MaxBatchSize))
	}
	args = append(args, extra...)
	return args
}

// pagedAttnAvailable reports whether this platform supports forcing
// PagedAttention on. mistral.rs supports it on CUDA (Linux/Windows w/ NVIDIA)
// and, as verified on 0.9.0, on Apple Silicon Metal (darwin/arm64). We force it
// on there so the KV-cache memory governor is active. On other platforms
// (e.g. darwin/amd64, plain CPU) we leave "auto" alone rather than risk
// `--paged-attn on` erroring with "device doesn't support it".
func pagedAttnAvailable() bool {
	if runtime.GOOS == "darwin" {
		return runtime.GOARCH == "arm64" // Metal
	}
	// CUDA builds on Linux/Windows; auto already enables PA there, and forcing
	// "on" is harmless when a supported device is present.
	return runtime.GOOS == "linux" || runtime.GOOS == "windows"
}

// firstUQFFShard returns the basename of the first `.uqff` file among the
// model's download URLs (e.g. "afq4-0.uqff"), or "" if none is present. The
// basename is what --from-uqff wants when -m points at a local directory; the
// binary resolves it relative to that directory and auto-finds sibling shards.
func hasAnyFlag(args []string, flags ...string) bool {
	for _, flag := range flags {
		if hasFlag(args, flag) {
			return true
		}
	}
	return false
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
	}
	return false
}

func firstUQFFShard(urls []string) string {
	for _, u := range urls {
		name := u
		if i := strings.LastIndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		if strings.HasSuffix(strings.ToLower(name), ".uqff") {
			return name
		}
	}
	return ""
}

func (p *Provider) waitReady(ctx context.Context, instanceID, endpoint string) error {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		// A dead process never becomes ready — fail immediately instead of
		// polling a closed port for the rest of the window. (watch owns the
		// exit and records state/LastError.)
		if state, procErr := p.instanceStatus(instanceID); state == localruntime.InstanceFailed || state == localruntime.InstanceStopped || state == localruntime.InstanceUnknown {
			if procErr != "" {
				return fmt.Errorf("mistral.rs exited during startup: %s", procErr)
			}
			return errors.New("mistral.rs exited during startup")
		}
		if err := p.probeReady(ctx, endpoint); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("mistral.rs readiness timed out: %w", lastErr)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// probeReady reports readiness by polling GET /health and treating any 2xx as
// ready; a 2xx on GET /v1/models is accepted as a fallback. The health path is
// pinned to the mistral.rs v0.9.0 README and should be reconfirmed against the
// binary during the Phase 1 integration test.
func (p *Provider) probeReady(ctx context.Context, endpoint string) error {
	if err := p.get2xx(ctx, endpoint+"/health"); err == nil {
		return nil
	} else {
		lastErr := err
		if err := p.get2xx(ctx, endpoint+"/v1/models"); err == nil {
			return nil
		}
		return lastErr
	}
}

// get2xx performs a GET and returns nil only for a 2xx status.
func (p *Provider) get2xx(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("%s returned status %d", url, resp.StatusCode)
}

// liveInstanceForLocked returns an instance already serving the given model
// file — starting, running, or healthy — or nil. Callers must hold p.mu.
func (p *Provider) liveInstanceForLocked(modelPath string) *managedInstance {
	for _, inst := range p.running {
		if inst.stopping || inst.model.Path != modelPath {
			continue
		}
		switch inst.record.State {
		case localruntime.InstanceStarting, localruntime.InstanceRunning, localruntime.InstanceHealthy:
			return inst
		}
	}
	return nil
}

// instanceState returns the instance's current lifecycle state, or
// InstanceUnknown when the record no longer exists.
func (p *Provider) instanceState(instanceID string) localruntime.InstanceState {
	state, _ := p.instanceStatus(instanceID)
	return state
}

// instanceStatus returns the instance's state and last recorded error. A
// missing record reports InstanceUnknown.
func (p *Provider) instanceStatus(instanceID string) (localruntime.InstanceState, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if inst, ok := p.running[instanceID]; ok {
		return inst.record.State, inst.record.LastError
	}
	return localruntime.InstanceUnknown, ""
}

// finishReadinessCap bounds the background wait for a slow initial model
// load. Generous on purpose — a large model plus an in-situ quantization
// pass on a cold page cache takes minutes — it exists only so an instance
// wedged before ever binding its port doesn't leak a poller forever.
// Crash/restart remains watch's job.
const finishReadinessCap = 30 * time.Minute

// finishReadiness keeps polling a still-loading instance after the caller's
// readiness window expired and flips it to running when the port comes up.
// This is what makes a slow-loading model eventually become a WARM instance
// that later requests reuse, instead of every request re-paying the load.
func (p *Provider) finishReadiness(instanceID, endpoint string, sink localruntime.LogSink) {
	ctx, cancel := context.WithTimeout(context.Background(), finishReadinessCap)
	defer cancel()
	if err := p.waitReady(ctx, instanceID, endpoint); err != nil {
		p.updateRecord(instanceID, sink, func(record *localruntime.InstanceRecord) {
			record.LastError = err.Error()
		})
		return
	}
	p.updateRecord(instanceID, sink, func(record *localruntime.InstanceRecord) {
		if record.State == localruntime.InstanceStarting {
			record.State = localruntime.InstanceRunning
			record.ReadyAt = time.Now()
			record.LastError = ""
		}
	})
	p.emit(sink, "info", instanceID, "", "mistral.rs finished loading and is ready")
}

func (p *Provider) watch(instanceID, binary string, sink localruntime.LogSink) {
	for {
		p.mu.RLock()
		instance, ok := p.running[instanceID]
		if !ok || instance.cmd == nil {
			p.mu.RUnlock()
			return
		}
		cmd := instance.cmd
		p.mu.RUnlock()

		err := cmd.Wait()
		exitCode := exitCode(err)
		// The process is gone — it can't be orphaned anymore, so take it
		// off the books before deciding whether to restart (a restart
		// registers its new PID in startProcess).
		p.registry.unregister(cmd.Process.Pid)

		p.mu.Lock()
		instance, ok = p.running[instanceID]
		if !ok {
			p.mu.Unlock()
			return
		}
		if instance.stopping {
			instance.record.State = localruntime.InstanceStopped
			instance.record.LastExitCode = exitCode
			record := instance.record
			p.mu.Unlock()
			p.updateSink(sink, record)
			p.emit(sink, "info", instanceID, record.ModelID, "mistral.rs stopped")
			return
		}

		shouldRestart := p.cfg.Restart.Enabled && instance.record.RestartCount < p.cfg.Restart.MaxAttempts
		instance.record.LastExitCode = exitCode
		instance.record.LastError = errorString(err)
		if shouldRestart {
			instance.record.State = localruntime.InstanceStarting
			instance.record.RestartCount++
		} else {
			instance.record.State = localruntime.InstanceFailed
		}
		record := instance.record
		p.mu.Unlock()

		p.updateSink(sink, record)
		if !shouldRestart {
			p.emit(sink, "error", instanceID, record.ModelID, "mistral.rs exited: "+record.LastError)
			return
		}

		p.emit(sink, "warn", instanceID, record.ModelID, "mistral.rs exited; restarting")
		time.Sleep(p.restartBackoff())
		if err := p.startProcess(instanceID, binary, sink); err != nil {
			p.updateRecord(instanceID, sink, func(record *localruntime.InstanceRecord) {
				record.State = localruntime.InstanceFailed
				record.LastError = err.Error()
			})
			p.emit(sink, "error", instanceID, record.ModelID, "restart failed: "+err.Error())
			return
		}
		p.mu.RLock()
		endpoint := p.running[instanceID].record.Endpoint
		p.mu.RUnlock()
		// Same leave-it-loading rule as Start: finishReadiness flips the
		// record to running when the reloaded model binds; if the process
		// crashes again the Wait at the top of this loop observes it.
		go p.finishReadiness(instanceID, endpoint, sink)
	}
}

func (p *Provider) kill(instanceID string) error {
	p.mu.RLock()
	instance, ok := p.running[instanceID]
	p.mu.RUnlock()
	if !ok || instance.cmd == nil || instance.cmd.Process == nil {
		return nil
	}
	return killProcess(instance.cmd.Process)
}

func (p *Provider) updateRecord(instanceID string, sink localruntime.LogSink, fn func(*localruntime.InstanceRecord)) {
	p.mu.Lock()
	instance, ok := p.running[instanceID]
	if !ok {
		p.mu.Unlock()
		return
	}
	fn(&instance.record)
	record := instance.record
	p.mu.Unlock()
	p.updateSink(sink, record)
}

func (p *Provider) updateSink(sink localruntime.LogSink, record localruntime.InstanceRecord) {
	if updater, ok := sink.(instanceUpdater); ok {
		updater.UpdateInstance(record)
	}
}

func (p *Provider) pipeLogs(r io.Reader, sink localruntime.LogSink, lf *syncedLog, runtimeID, modelID, stream string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	source := runtimeName + "." + runtimeID + "." + stream
	for scanner.Scan() {
		line := scanner.Text()
		lf.writeLine(stream, line)
		p.emit(sink, "info", runtimeID, modelID, line, source)
	}
	if err := scanner.Err(); err != nil {
		lf.writeLine(stream, "log stream error: "+err.Error())
		p.emit(sink, "warn", runtimeID, modelID, "log stream error: "+err.Error(), source)
	}
}

func (p *Provider) emit(sink localruntime.LogSink, level, runtimeID, modelID, message string, sourceOverride ...string) {
	if sink == nil {
		return
	}
	source := "cercano.runtime.mistralrs"
	if len(sourceOverride) > 0 && sourceOverride[0] != "" {
		source = sourceOverride[0]
	}
	sink.WriteLog(localruntime.LogEntry{
		Timestamp: time.Now(),
		Source:    source,
		Level:     level,
		RuntimeID: runtimeID,
		ModelID:   modelID,
		Message:   message,
	})
}

func (p *Provider) readinessTimeout() time.Duration {
	d, err := time.ParseDuration(p.cfg.ReadinessTimeout)
	if err != nil || d <= 0 {
		return 60 * time.Second
	}
	return d
}

func (p *Provider) restartBackoff() time.Duration {
	d, err := time.ParseDuration(p.cfg.Restart.Backoff)
	if err != nil || d <= 0 {
		return 2 * time.Second
	}
	return d
}

// modelRecord builds a model record from an on-disk GGUF. Phase 1 uses
// filename-based identity (DisplayName/Family/Quantization inferred from the
// name); reading the GGUF header for authoritative identity is deferred.
func (p *Provider) modelRecord(path string, info os.FileInfo) localruntime.ModelRecord {
	display := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return localruntime.ModelRecord{
		ID:            runtimeName + ":" + shortID(path),
		DisplayName:   display,
		Runtime:       runtimeName,
		Source:        "configured_path",
		Path:          path,
		Format:        "gguf",
		Family:        inferFamily(display),
		Quantization:  inferQuantization(display),
		SizeBytes:     info.Size(),
		ModifiedAt:    info.ModTime(),
		DownloadState: localruntime.Downloaded,
		RuntimeState:  localruntime.InstanceStopped,
		SupportsChat:  true,
		SupportsTools: true,
	}
}

// matchesModel delegates to the shared matcher so the provider and the
// inference engine can never drift apart on what a model name means —
// drift is how the engine misses a warm instance and Start gets called
// once per request.
func matchesModel(requested string, model localruntime.ModelRecord) bool {
	return localruntime.MatchesModel(requested, model)
}

func expandPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Abs(path)
}

func healthHost(host string) string {
	switch host {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	default:
		return host
	}
}

func inferFamily(name string) string {
	lower := strings.ToLower(name)
	for _, family := range []string{"qwen", "llama", "gemma", "mistral", "phi", "deepseek", "glm"} {
		if strings.Contains(lower, family) {
			return family
		}
	}
	return "unknown"
}

func inferQuantization(name string) string {
	upper := strings.ToUpper(name)
	if match := quantRE.FindStringSubmatch(upper); len(match) == 2 {
		return strings.ReplaceAll(match[1], "-", "_")
	}
	return "unknown"
}

func shortID(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func errorString(err error) string {
	if err == nil {
		return "process exited"
	}
	return err.Error()
}
