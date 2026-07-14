package llamaserver

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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cercano/source/server/internal/gguf"
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
)

const runtimeName = "llama_server"

var quantRE = regexp.MustCompile(`(?i)(?:^|[-_. ])(Q[0-9][A-Z0-9]*(?:[_-][A-Z0-9]+){0,3})(?:$|[-_. ])`)

type instanceUpdater interface {
	UpdateInstance(localruntime.InstanceRecord)
}

type Provider struct {
	cfg     config.LlamaServerConfig
	client  *http.Client
	mu      sync.RWMutex
	running map[string]*managedInstance

	// registry tracks spawned llama-server PIDs on disk so the next
	// cercano process can reap them if this one dies without cleanup
	// (see SweepOrphans). Nil disables tracking.
	registry *pidRegistry

	// headerCache memoizes per-file GGUF identity metadata (name,
	// architecture, quantization) so Discover — which runs on every
	// dashboard tick — reads each file's header once, not repeatedly.
	// Invalidated by mtime+size. Failed parses are cached too, so a
	// corrupt file doesn't get re-read every tick.
	headerMu    sync.Mutex
	headerCache map[string]headerIdentity
}

// headerIdentity is the cached result of reading a GGUF's identity
// metadata off disk.
type headerIdentity struct {
	modTime time.Time
	size    int64
	name    string
	family  string
	quant   string
	encoder bool
	ok      bool
}

// identityHeaderWindow bounds the header read — same window the RAM
// estimator uses; identity keys sit even earlier in the file.
const identityHeaderWindow = 256 * 1024

// headerIdentity returns the file's parsed identity, from cache when
// the file hasn't changed.
func (p *Provider) headerIdentity(path string, info os.FileInfo) headerIdentity {
	p.headerMu.Lock()
	cached, hit := p.headerCache[path]
	p.headerMu.Unlock()
	if hit && cached.modTime.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached
	}
	id := headerIdentity{modTime: info.ModTime(), size: info.Size()}
	if f, err := os.Open(path); err == nil {
		if meta, perr := gguf.ParseMeta(io.LimitReader(f, identityHeaderWindow)); perr == nil {
			id.name = meta.Name
			id.family = meta.Architecture
			id.quant = meta.QuantLabel()
			id.encoder = meta.IsEncoder()
			id.ok = true
		}
		_ = f.Close()
	}
	p.headerMu.Lock()
	if p.headerCache == nil {
		p.headerCache = make(map[string]headerIdentity)
	}
	p.headerCache[path] = id
	p.headerMu.Unlock()
	return id
}

type managedInstance struct {
	record   localruntime.InstanceRecord
	model    localruntime.ModelRecord
	cmd      *exec.Cmd
	stopping bool
}

func NewProvider(cfg config.LlamaServerConfig) *Provider {
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
	models = append(models, p.catalogModels()...)
	return models, errors.Join(errs...)
}

func (p *Provider) Start(ctx context.Context, req localruntime.StartRequest, sink localruntime.LogSink) (*localruntime.InstanceRecord, error) {
	model, err := p.resolveModel(ctx, req.ModelID)
	if err != nil {
		return nil, err
	}
	binary, err := p.resolveBinary()
	if err != nil {
		return nil, err
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
			State:     localruntime.StateStarting,
			Address:   host,
			Port:      port,
			Endpoint:  endpoint,
			StartedAt: now,
		},
	}

	// One process per model: if a live instance already serves this GGUF,
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
		p.emit(sink, "info", record.ID, model.ID, "reusing running llama-server for "+model.DisplayName)
		return &record, nil
	}
	p.running[id] = instance
	p.mu.Unlock()
	p.emit(sink, "info", id, model.ID, "starting llama-server for "+model.DisplayName)

	if err := p.startProcess(id, binary, sink); err != nil {
		p.updateRecord(id, sink, func(record *localruntime.InstanceRecord) {
			record.State = localruntime.StateFailed
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
		if p.instanceState(id) == localruntime.StateStarting {
			// The process is alive and still loading — a large GGUF
			// legitimately needs longer than the readiness window, and
			// killing it here would throw the whole load away and pay it
			// again on the next request. Leave it loading: finishReadiness
			// flips the record to running when the port comes up, and
			// callers retry against the warm instance.
			go p.finishReadiness(id, endpoint, sink)
			p.emit(sink, "warn", id, model.ID, "llama-server still loading "+model.DisplayName+" after "+p.readinessTimeout().String()+" — leaving it to finish in the background")
			return &instance.record, fmt.Errorf("llama-server is still loading %s (instance left running — retry shortly): %w", model.DisplayName, err)
		}
		// Not starting anymore: the process died. watch has already recorded
		// the exit (and handles any configured restarts); surface the error.
		p.updateRecord(id, sink, func(record *localruntime.InstanceRecord) {
			record.LastError = err.Error()
		})
		return &instance.record, err
	}

	p.updateRecord(id, sink, func(record *localruntime.InstanceRecord) {
		record.State = localruntime.StateRunning
		record.ReadyAt = time.Now()
	})

	p.mu.RLock()
	out := p.running[id].record
	p.mu.RUnlock()
	return &out, nil
}

func (p *Provider) Stop(_ context.Context, instanceID string) error {
	p.mu.Lock()
	instance, ok := p.running[instanceID]
	if !ok {
		p.mu.Unlock()
		return fmt.Errorf("llama-server instance %q not found", instanceID)
	}
	instance.stopping = true
	cmd := instance.cmd
	instance.record.State = localruntime.StateStopped
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return killProcess(cmd.Process)
}

func (p *Provider) Probe(ctx context.Context, instanceID string) (*localruntime.InstanceHealth, error) {
	p.mu.RLock()
	instance, ok := p.running[instanceID]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("llama-server instance %q not found", instanceID)
	}
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", instance.record.Endpoint+"/health", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return &localruntime.InstanceHealth{
			InstanceID: instanceID,
			State:      localruntime.StateUnreachable,
			LatencyMS:  latency,
			Error:      err.Error(),
			CheckedAt:  time.Now(),
		}, nil
	}
	defer resp.Body.Close()
	state := localruntime.StateHealthy
	if resp.StatusCode != http.StatusOK {
		state = localruntime.StateUnhealthy
	}
	return &localruntime.InstanceHealth{
		InstanceID: instanceID,
		State:      state,
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
			if model.DownloadState != "" && model.DownloadState != "downloaded" {
				return localruntime.ModelRecord{}, fmt.Errorf("llama-server model %q is not downloaded", requested)
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
		return localruntime.ModelRecord{}, fmt.Errorf("llama-server model %q not found in configured model_dirs", requested)
	}
	return localruntime.ModelRecord{}, errors.New("llama-server default_model is not configured")
}

func (p *Provider) resolveBinary() (string, error) {
	if p.cfg.Binary != "" {
		path, err := expandPath(p.cfg.Binary)
		if err != nil {
			return "", err
		}
		if info, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("llama-server binary %s: %w", path, err)
		} else if info.IsDir() {
			return "", fmt.Errorf("llama-server binary %s is a directory", path)
		}
		return path, nil
	}
	path, err := exec.LookPath("llama-server")
	if err != nil {
		return "", errors.New("llama-server binary not found; set llama_server.binary or install llama-server on PATH")
	}
	return path, nil
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
		return fmt.Errorf("llama-server instance %q not found", instanceID)
	}
	args := p.argsFor(instance.model, instance.record.Port)
	cmd := exec.Command(binary, args...)
	cmd.Stdin = nil
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
	instance.record.State = localruntime.StateStarting
	instance.record.StartedAt = time.Now()
	instance.record.LastError = ""
	p.mu.Unlock()
	p.updateSink(sink, instance.record)
	go p.pipeLogs(stdout, sink, instanceID, instance.model.ID, "stdout")
	go p.pipeLogs(stderr, sink, instanceID, instance.model.ID, "stderr")
	return nil
}

func (p *Provider) argsFor(model localruntime.ModelRecord, port int) []string {
	args := []string{
		"--model", model.Path,
		"--host", p.cfg.Host,
		"--port", strconv.Itoa(port),
	}
	if model.SupportsEmbed && !model.SupportsChat {
		// Encoder models (bert family — nomic etc.) serve /v1/embeddings;
		// llama-server only enables that endpoint when spawned with the
		// flag. Chat models never get it — embedding mode disables the
		// completions endpoints.
		args = append(args, "--embedding")
	}
	if p.cfg.ContextSize > 0 {
		args = append(args, "--ctx-size", strconv.Itoa(p.cfg.ContextSize))
	}
	if p.cfg.Threads > 0 {
		args = append(args, "--threads", strconv.Itoa(p.cfg.Threads))
	}
	if p.cfg.GPULayers != "" {
		args = append(args, "--gpu-layers", p.cfg.GPULayers)
	}
	args = append(args, p.cfg.ExtraArgs...)
	return args
}

func (p *Provider) waitReady(ctx context.Context, instanceID, endpoint string) error {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		// A dead process never becomes ready — fail immediately instead of
		// polling a closed port for the rest of the window. (watch owns the
		// exit and records state/LastError.)
		if state, procErr := p.instanceStatus(instanceID); state == localruntime.StateFailed || state == localruntime.StateStopped || state == "" {
			if procErr != "" {
				return fmt.Errorf("llama-server exited during startup: %s", procErr)
			}
			return errors.New("llama-server exited during startup")
		}
		req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/health", nil)
		if err != nil {
			return err
		}
		resp, err := p.client.Do(req)
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				return nil
			}
			lastErr = fmt.Errorf("health returned status %d", resp.StatusCode)
			resp.Body.Close()
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("llama-server readiness timed out: %w", lastErr)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// liveInstanceForLocked returns an instance already serving the given model
// file — starting, running, or healthy — or nil. Callers must hold p.mu.
func (p *Provider) liveInstanceForLocked(modelPath string) *managedInstance {
	for _, inst := range p.running {
		if inst.stopping || inst.model.Path != modelPath {
			continue
		}
		switch inst.record.State {
		case localruntime.StateStarting, localruntime.StateRunning, localruntime.StateHealthy:
			return inst
		}
	}
	return nil
}

// instanceState returns the instance's current lifecycle state, or "" when
// the record no longer exists.
func (p *Provider) instanceState(instanceID string) string {
	state, _ := p.instanceStatus(instanceID)
	return state
}

// instanceStatus returns the instance's state and last recorded error.
func (p *Provider) instanceStatus(instanceID string) (string, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if inst, ok := p.running[instanceID]; ok {
		return inst.record.State, inst.record.LastError
	}
	return "", ""
}

// finishReadinessCap bounds the background wait for a slow initial model
// load. Generous on purpose — a 50+ GB GGUF on a cold page cache takes
// minutes — it exists only so an instance wedged before ever binding its
// port doesn't leak a poller forever. Crash/restart remains watch's job.
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
		if record.State == localruntime.StateStarting {
			record.State = localruntime.StateRunning
			record.ReadyAt = time.Now()
			record.LastError = ""
		}
	})
	p.emit(sink, "info", instanceID, "", "llama-server finished loading and is ready")
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
			instance.record.State = localruntime.StateStopped
			instance.record.LastExitCode = exitCode
			record := instance.record
			p.mu.Unlock()
			p.updateSink(sink, record)
			p.emit(sink, "info", instanceID, record.ModelID, "llama-server stopped")
			return
		}

		shouldRestart := p.cfg.Restart.Enabled && instance.record.RestartCount < p.cfg.Restart.MaxAttempts
		instance.record.LastExitCode = exitCode
		instance.record.LastError = errorString(err)
		if shouldRestart {
			instance.record.State = localruntime.StateStarting
			instance.record.RestartCount++
		} else {
			instance.record.State = localruntime.StateFailed
		}
		record := instance.record
		p.mu.Unlock()

		p.updateSink(sink, record)
		if !shouldRestart {
			p.emit(sink, "error", instanceID, record.ModelID, "llama-server exited: "+record.LastError)
			return
		}

		p.emit(sink, "warn", instanceID, record.ModelID, "llama-server exited; restarting")
		time.Sleep(p.restartBackoff())
		if err := p.startProcess(instanceID, binary, sink); err != nil {
			p.updateRecord(instanceID, sink, func(record *localruntime.InstanceRecord) {
				record.State = localruntime.StateFailed
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

func (p *Provider) pipeLogs(r io.Reader, sink localruntime.LogSink, runtimeID, modelID, stream string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	source := runtimeName + "." + runtimeID + "." + stream
	for scanner.Scan() {
		p.emit(sink, "info", runtimeID, modelID, scanner.Text(), source)
	}
	if err := scanner.Err(); err != nil {
		p.emit(sink, "warn", runtimeID, modelID, "log stream error: "+err.Error(), source)
	}
}

func (p *Provider) emit(sink localruntime.LogSink, level, runtimeID, modelID, message string, sourceOverride ...string) {
	if sink == nil {
		return
	}
	source := "cercano.runtime.llama_server"
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

func (p *Provider) modelRecord(path string, info os.FileInfo) localruntime.ModelRecord {
	display := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	rec := localruntime.ModelRecord{
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
		DownloadState: "downloaded",
		RuntimeState:  localruntime.StateStopped,
		SupportsChat:  true,
	}
	// The GGUF header is the authority on identity — the filename
	// inference above is only the fallback for unparseable files.
	if id := p.headerIdentity(path, info); id.ok {
		if id.name != "" {
			rec.DisplayName = id.name
		}
		if id.family != "" {
			rec.Family = id.family
		}
		if id.quant != "" {
			rec.Quantization = id.quant
		}
		if id.encoder {
			rec.SupportsChat = false
			rec.SupportsEmbed = true
		}
	}
	return rec
}

// catalogModels surfaces the curated compatibility catalog (the embedded,
// gate-verified RAM-tier set) as downloadable model records. Each model's
// files land in the target dir under their URL filenames; a multi-shard model
// (e.g. GLM-4.5-Air's two-part Q4_K_M) counts as downloaded only when every
// shard is present. Ordered by id for a stable dashboard.
func (p *Provider) catalogModels() []localruntime.ModelRecord {
	cat, err := loadCatalog()
	if err != nil {
		// A malformed embedded catalog is a build-time bug (the validity test
		// guards it); at runtime, surface nothing rather than crash Discover.
		return nil
	}
	targetDir := p.catalogTargetDir()
	ids := make([]string, 0, len(cat.Models))
	for id := range cat.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]localruntime.ModelRecord, 0, len(ids))
	for _, id := range ids {
		m := cat.Models[id]
		urls := m.DownloadURLs()
		if len(urls) == 0 {
			continue
		}
		// Path names the first shard — what llama-server is pointed at.
		primary := filepath.Join(targetDir, urlFilename(urls[0]))
		state := "not_downloaded"
		var modified time.Time
		if allShardsPresent(targetDir, urls) {
			state = "downloaded"
			if info, statErr := os.Stat(primary); statErr == nil {
				modified = info.ModTime()
			}
		}
		out = append(out, localruntime.ModelRecord{
			ID:                 runtimeName + ":catalog:" + m.ID,
			DisplayName:        m.DisplayName,
			Runtime:            runtimeName,
			Source:             "catalog",
			Path:               primary,
			Format:             "gguf",
			Family:             m.Family,
			Quantization:       m.Quantization,
			SizeBytes:          m.SizeBytes,
			ModifiedAt:         modified,
			DownloadState:      state,
			DownloadURLs:       urls,
			DownloadTotalBytes: m.SizeBytes,
			RuntimeState:       localruntime.StateStopped,
			SupportsChat:       !m.SupportsEmbed,
			SupportsEmbed:      m.SupportsEmbed,
			SupportsTools:      m.SupportsTools,
		})
	}
	return out
}

// urlFilename returns the filename portion of a download URL (after the last
// slash), used to place each shard on disk under its own name.
func urlFilename(u string) string {
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	return u
}

// allShardsPresent reports whether every shard file of a (possibly multi-part)
// model is present in dir — the "downloaded" test for the curated catalog.
func allShardsPresent(dir string, urls []string) bool {
	if len(urls) == 0 {
		return false
	}
	for _, u := range urls {
		info, err := os.Stat(filepath.Join(dir, urlFilename(u)))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func (p *Provider) catalogTargetDir() string {
	if len(p.cfg.ModelDirs) > 0 {
		if expanded, err := expandPath(p.cfg.ModelDirs[0]); err == nil && expanded != "" {
			return expanded
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".cercano", "models")
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

// CatalogModels returns the embedded curated catalog as downloadable
// ModelRecords (ID + DownloadURLs). Exported for startup download-resume:
// callers map each shard filename to its model ID to match stranded .part
// files back to the model that owns them.
func (p *Provider) CatalogModels() []localruntime.ModelRecord { return p.catalogModels() }
