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

	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
)

const runtimeName = "llama_server"

var quantRE = regexp.MustCompile(`(?i)(?:^|[-_. ])(Q[0-9][A-Z0-9]*(?:[_-][A-Z0-9]+){0,3})(?:$|[-_. ])`)

type catalogModel struct {
	ID            string
	DisplayName   string
	Filename      string
	DownloadURL   string
	Family        string
	Quantization  string
	SizeBytes     int64
	SupportsTools bool
}

var defaultCatalog = []catalogModel{
	{
		ID:            runtimeName + ":catalog:qwen2.5-coder-1.5b-q4_k_m",
		DisplayName:   "Qwen2.5 Coder 1.5B Instruct Q4_K_M",
		Filename:      "qwen2.5-coder-1.5b-instruct-q4_k_m.gguf",
		DownloadURL:   "https://huggingface.co/Qwen/Qwen2.5-Coder-1.5B-Instruct-GGUF/resolve/main/qwen2.5-coder-1.5b-instruct-q4_k_m.gguf",
		Family:        "qwen",
		Quantization:  "Q4_K_M",
		SizeBytes:     1117320768,
		SupportsTools: true,
	},
	{
		ID:            runtimeName + ":catalog:qwen2.5-coder-7b-q4_k_m",
		DisplayName:   "Qwen2.5 Coder 7B Instruct Q4_K_M",
		Filename:      "qwen2.5-coder-7b-instruct-q4_k_m.gguf",
		DownloadURL:   "https://huggingface.co/Qwen/Qwen2.5-Coder-7B-Instruct-GGUF/resolve/main/qwen2.5-coder-7b-instruct-q4_k_m.gguf",
		Family:        "qwen",
		Quantization:  "Q4_K_M",
		SizeBytes:     4683073536,
		SupportsTools: true,
	},
}

type instanceUpdater interface {
	UpdateInstance(localruntime.InstanceRecord)
}

type Provider struct {
	cfg     config.LlamaServerConfig
	client  *http.Client
	mu      sync.RWMutex
	running map[string]*managedInstance
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
		cfg:     cfg,
		client:  &http.Client{Timeout: 2 * time.Second},
		running: make(map[string]*managedInstance),
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
			model := modelRecord(path, info)
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

	p.mu.Lock()
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

	readyCtx, cancel := context.WithTimeout(ctx, p.readinessTimeout())
	defer cancel()
	if err := p.waitReady(readyCtx, endpoint); err != nil {
		_ = p.kill(id)
		p.updateRecord(id, sink, func(record *localruntime.InstanceRecord) {
			record.State = localruntime.StateFailed
			record.LastError = err.Error()
		})
		return &instance.record, err
	}

	p.updateRecord(id, sink, func(record *localruntime.InstanceRecord) {
		record.State = localruntime.StateRunning
		record.ReadyAt = time.Now()
	})
	go p.watch(id, binary, sink)

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
				return modelRecord(path, info), nil
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

func (p *Provider) waitReady(ctx context.Context, endpoint string) error {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
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
		readyCtx, cancel := context.WithTimeout(context.Background(), p.readinessTimeout())
		err = p.waitReady(readyCtx, endpoint)
		cancel()
		if err != nil {
			_ = p.kill(instanceID)
			p.updateRecord(instanceID, sink, func(record *localruntime.InstanceRecord) {
				record.LastError = err.Error()
			})
			continue
		}
		p.updateRecord(instanceID, sink, func(record *localruntime.InstanceRecord) {
			record.State = localruntime.StateRunning
			record.ReadyAt = time.Now()
			record.LastError = ""
		})
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

func modelRecord(path string, info os.FileInfo) localruntime.ModelRecord {
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
		DownloadState: "downloaded",
		RuntimeState:  localruntime.StateStopped,
		SupportsChat:  true,
	}
}

func (p *Provider) catalogModels() []localruntime.ModelRecord {
	targetDir := p.catalogTargetDir()
	out := make([]localruntime.ModelRecord, 0, len(defaultCatalog))
	for _, item := range defaultCatalog {
		path := filepath.Join(targetDir, item.Filename)
		state := "not_downloaded"
		var modified time.Time
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			state = "downloaded"
			modified = info.ModTime()
		}
		out = append(out, localruntime.ModelRecord{
			ID:                 item.ID,
			DisplayName:        item.DisplayName,
			Runtime:            runtimeName,
			Source:             "catalog",
			Path:               path,
			Format:             "gguf",
			Family:             item.Family,
			Quantization:       item.Quantization,
			SizeBytes:          item.SizeBytes,
			ModifiedAt:         modified,
			DownloadState:      state,
			DownloadURL:        item.DownloadURL,
			DownloadTotalBytes: item.SizeBytes,
			RuntimeState:       localruntime.StateStopped,
			SupportsChat:       true,
			SupportsTools:      item.SupportsTools,
		})
	}
	return out
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

func matchesModel(requested string, model localruntime.ModelRecord) bool {
	if requested == "" {
		return false
	}
	expanded, _ := expandPath(requested)
	return requested == model.ID ||
		requested == model.DisplayName ||
		requested == filepath.Base(model.Path) ||
		expanded == model.Path
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
