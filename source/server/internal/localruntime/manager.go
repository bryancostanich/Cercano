package localruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultLogLimit = 300

type Option func(*InMemoryManager)

func WithEndpoints(endpoints []EndpointRecord) Option {
	return func(m *InMemoryManager) {
		m.endpoints = cloneEndpoints(endpoints)
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(m *InMemoryManager) {
		if client != nil {
			m.httpClient = client
		}
	}
}

// InMemoryManager is the first runtime manager implementation. It keeps
// dashboard state in memory and delegates real runtime behavior to providers.
type InMemoryManager struct {
	mu           sync.RWMutex
	providers    map[string]Provider
	instances    map[string]InstanceRecord
	endpoints    []EndpointRecord
	downloads    map[string]ModelRecord
	downloadJobs map[string]*downloadJob
	httpClient   *http.Client
	logs         []LogEntry
	logLimit     int

	// ociResolver resolves an OllamaRef ("name:tag") into a concrete
	// blob URL by fetching the manifest from registry.ollama.ai.
	// Optional — nil means DownloadModel will refuse to fetch ollama-
	// library entries (they'll still list, but Download errors clearly
	// rather than trying an empty URL). Set via SetOCIResolver.
	ociResolver OCIResolver
}

// OCIResolver produces a concrete download URL (and total size) from
// an Ollama library reference. Implementation lives in the
// ollamacatalog package; this interface keeps localruntime from
// importing it directly (avoids a package cycle if ollamacatalog ever
// needs to import localruntime types for testing).
type OCIResolver interface {
	// Resolve returns the blob download URL for an Ollama library
	// reference of the form "name:tag" (e.g. "qwen2.5-coder:7b").
	// Size is the manifest's model-layer size in bytes.
	Resolve(ctx context.Context, ref string) (downloadURL string, sizeBytes int64, err error)
}

type downloadJob struct {
	cancel context.CancelFunc
}

func NewManager(opts ...Option) *InMemoryManager {
	m := &InMemoryManager{
		providers:    make(map[string]Provider),
		instances:    make(map[string]InstanceRecord),
		downloads:    make(map[string]ModelRecord),
		downloadJobs: make(map[string]*downloadJob),
		httpClient:   http.DefaultClient,
		logLimit:     defaultLogLimit,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func (m *InMemoryManager) RegisterProvider(provider Provider) {
	if provider == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers[provider.Name()] = provider
}

// SetOCIResolver attaches the resolver used to translate an
// ollama-library ref into a concrete blob URL. Nil (the default) means
// online-library entries will error on Download with a clear message —
// they'll still list, they just can't be fetched. Called from main.go
// wire-up with the ollamacatalog.Manager, which implements OCIResolver.
func (m *InMemoryManager) SetOCIResolver(r OCIResolver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ociResolver = r
}

func (m *InMemoryManager) Providers() []ProviderInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]ProviderInfo, 0, len(m.providers))
	for _, provider := range m.providers {
		out = append(out, ProviderInfo{
			Name:         provider.Name(),
			Capabilities: provider.Capabilities(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *InMemoryManager) Inventory(ctx context.Context) ([]ModelRecord, error) {
	providers := m.providerSnapshot()
	var out []ModelRecord
	var errs []error
	for _, provider := range providers {
		models, err := provider.Discover(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s discover: %w", provider.Name(), err))
			continue
		}
		out = append(out, models...)
	}
	out = m.overlayDownloads(out)
	sortModels(out)
	return out, errors.Join(errs...)
}

func (m *InMemoryManager) Instances(context.Context) ([]InstanceRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]InstanceRecord, 0, len(m.instances))
	for _, instance := range m.instances {
		out = append(out, instance)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *InMemoryManager) Endpoints(context.Context) ([]EndpointRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneEndpoints(m.endpoints), nil
}

func (m *InMemoryManager) SetEndpoints(endpoints []EndpointRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.endpoints = cloneEndpoints(endpoints)
}

func (m *InMemoryManager) UpdateInstance(instance InstanceRecord) {
	if instance.ID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instances[instance.ID] = instance
}

func (m *InMemoryManager) Start(ctx context.Context, req StartRequest) (*InstanceRecord, error) {
	provider, err := m.provider(req.Runtime)
	if err != nil {
		return nil, err
	}
	instance, err := provider.Start(ctx, req, m)
	if err != nil {
		if instance != nil {
			m.UpdateInstance(*instance)
		}
		return nil, err
	}
	if instance == nil {
		return nil, errors.New("runtime provider returned nil instance")
	}
	m.UpdateInstance(*instance)
	return instance, nil
}

func (m *InMemoryManager) Stop(ctx context.Context, req StopRequest) error {
	instance, ok := m.instance(req.InstanceID)
	if !ok {
		return fmt.Errorf("runtime instance %q not found", req.InstanceID)
	}
	provider, err := m.provider(instance.Runtime)
	if err != nil {
		return err
	}
	if err := provider.Stop(ctx, req.InstanceID); err != nil {
		return err
	}
	instance.State = StateStopped
	m.mu.Lock()
	m.instances[req.InstanceID] = instance
	m.mu.Unlock()
	return nil
}

func (m *InMemoryManager) Restart(ctx context.Context, req RestartRequest) (*InstanceRecord, error) {
	instance, ok := m.instance(req.InstanceID)
	runtimeName := req.Runtime
	modelID := req.ModelID
	if ok {
		runtimeName = instance.Runtime
		modelID = instance.ModelID
		if err := m.Stop(ctx, StopRequest{InstanceID: req.InstanceID}); err != nil {
			return nil, err
		}
	}
	return m.Start(ctx, StartRequest{Runtime: runtimeName, ModelID: modelID})
}

func (m *InMemoryManager) DownloadModel(ctx context.Context, req DownloadRequest) (*ModelRecord, error) {
	if strings.TrimSpace(req.Runtime) == "" {
		return nil, errors.New("runtime is required")
	}
	if strings.TrimSpace(req.ModelID) == "" {
		return nil, errors.New("model id is required")
	}
	if existing, ok := m.download(req.ModelID); ok && existing.DownloadState == "downloading" {
		return &existing, nil
	}
	model, err := m.findDownloadModel(ctx, req)
	if err != nil {
		return nil, err
	}
	if model.DownloadState == "downloaded" {
		return &model, nil
	}
	// Just-in-time OCI resolution: online-catalog entries (from
	// Ollama's public library) arrive with OllamaRef but no
	// DownloadURL, because we don't want to fetch a manifest for every
	// browsed model. Do the fetch now, right before the URL check, so
	// only models the user actually clicks Download on incur the cost.
	if model.DownloadURL == "" && model.OllamaRef != "" {
		if m.ociResolver == nil {
			return nil, fmt.Errorf("model %q needs OCI resolution but no OCIResolver is attached", req.ModelID)
		}
		url, size, resErr := m.ociResolver.Resolve(ctx, model.OllamaRef)
		if resErr != nil {
			return nil, fmt.Errorf("resolve OCI manifest for %s: %w", model.OllamaRef, resErr)
		}
		model.DownloadURL = url
		if model.DownloadTotalBytes == 0 {
			model.DownloadTotalBytes = size
		}
		if model.SizeBytes == 0 {
			model.SizeBytes = size
		}
		if model.Path == "" {
			// Default target: ~/.cercano/models/<name>-<tag>.gguf
			home, herr := os.UserHomeDir()
			if herr == nil {
				modelDir := filepath.Join(home, ".cercano", "models")
				// Sanitize the ref into a safe filename.
				filename := strings.ReplaceAll(model.OllamaRef, ":", "-") + ".gguf"
				model.Path = filepath.Join(modelDir, filename)
			}
		}
	}
	if model.DownloadURL == "" {
		return nil, fmt.Errorf("model %q does not have a download URL", req.ModelID)
	}
	if model.Path == "" {
		return nil, fmt.Errorf("model %q does not have a target path", req.ModelID)
	}
	total := model.DownloadTotalBytes
	if total == 0 {
		total = model.SizeBytes
	}
	model.DownloadState = "downloading"
	model.DownloadedBytes = 0
	model.DownloadTotalBytes = total
	model.DownloadError = ""
	downloadCtx, cancel := context.WithCancel(context.Background())
	job := &downloadJob{cancel: cancel}
	m.mu.Lock()
	m.downloads[model.ID] = model
	m.downloadJobs[model.ID] = job
	m.mu.Unlock()
	m.WriteLog(LogEntry{
		Source:  "cercano.runtime.download",
		Level:   "info",
		ModelID: model.ID,
		Message: "starting download for " + model.DisplayName,
	})
	go m.runDownload(downloadCtx, model, job)
	return &model, nil
}

func (m *InMemoryManager) CancelDownload(ctx context.Context, req DownloadRequest) (*ModelRecord, error) {
	if strings.TrimSpace(req.Runtime) == "" {
		return nil, errors.New("runtime is required")
	}
	if strings.TrimSpace(req.ModelID) == "" {
		return nil, errors.New("model id is required")
	}
	model, ok := m.download(req.ModelID)
	if !ok {
		found, err := m.findDownloadModel(ctx, req)
		if err != nil {
			return nil, err
		}
		model = found
	}
	if model.DownloadState != "downloading" {
		return &model, nil
	}

	m.mu.RLock()
	job := m.downloadJobs[model.ID]
	m.mu.RUnlock()
	if job != nil && job.cancel != nil {
		job.cancel()
	}
	m.markDownloadCancelled(model)
	cancelled, _ := m.download(model.ID)
	return &cancelled, nil
}

func (m *InMemoryManager) DeleteModel(ctx context.Context, req DeleteModelRequest) error {
	if strings.TrimSpace(req.Runtime) == "" {
		return errors.New("runtime is required")
	}
	if strings.TrimSpace(req.ModelID) == "" {
		return errors.New("model id is required")
	}
	model, err := m.findDownloadModel(ctx, DownloadRequest{Runtime: req.Runtime, ModelID: req.ModelID})
	if err != nil {
		return err
	}
	if model.DownloadState == "downloading" {
		return fmt.Errorf("model %q is downloading; cancel it before deleting", req.ModelID)
	}
	if model.DownloadURL == "" {
		return fmt.Errorf("model %q is not a managed download", req.ModelID)
	}
	if model.Path == "" {
		return fmt.Errorf("model %q does not have a target path", req.ModelID)
	}
	if err := os.Remove(model.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// A leftover partial from a failed attempt goes with the model.
	_ = os.Remove(model.Path + ".part")
	_ = os.Remove(model.Path + ".part")
	model.DownloadState = "not_downloaded"
	model.DownloadedBytes = 0
	if model.DownloadTotalBytes == 0 {
		model.DownloadTotalBytes = model.SizeBytes
	}
	model.DownloadError = ""
	model.RuntimeState = StateStopped
	model.ModifiedAt = time.Time{}
	m.updateDownload(model)
	m.WriteLog(LogEntry{
		Source:  "cercano.runtime.download",
		Level:   "info",
		ModelID: model.ID,
		Message: "deleted " + model.DisplayName,
	})
	return nil
}

func (m *InMemoryManager) Status(ctx context.Context) (*StatusSnapshot, error) {
	models, modelErr := m.Inventory(ctx)
	instances, instanceErr := m.Instances(ctx)
	endpoints, endpointErr := m.Endpoints(ctx)
	logs, logErr := m.Logs(ctx, LogRequest{Tail: 100})
	return &StatusSnapshot{
		Models:    models,
		Instances: instances,
		Endpoints: endpoints,
		Logs:      logs,
	}, errors.Join(modelErr, instanceErr, endpointErr, logErr)
}

func (m *InMemoryManager) Logs(_ context.Context, req LogRequest) ([]LogEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var filtered []LogEntry
	for _, entry := range m.logs {
		if req.Source != "" && entry.Source != req.Source {
			continue
		}
		if !req.Since.IsZero() && entry.Timestamp.Before(req.Since) {
			continue
		}
		filtered = append(filtered, entry)
	}
	if req.Tail > 0 && len(filtered) > req.Tail {
		filtered = filtered[len(filtered)-req.Tail:]
	}
	return cloneLogs(filtered), nil
}

func (m *InMemoryManager) findDownloadModel(ctx context.Context, req DownloadRequest) (ModelRecord, error) {
	// Check enrolled entries first. Server-side handlers may pre-enrol
	// online-catalog entries here so the download path can find them
	// without requiring the provider's Discover to know about them.
	if existing, ok := m.download(req.ModelID); ok && existing.Runtime == req.Runtime {
		return existing, nil
	}
	models, err := m.Inventory(ctx)
	if err != nil && len(models) == 0 {
		return ModelRecord{}, err
	}
	for _, model := range models {
		if model.Runtime == req.Runtime && model.ID == req.ModelID {
			return model, nil
		}
	}
	return ModelRecord{}, fmt.Errorf("runtime model %q not found", req.ModelID)
}

// EnrollDownload pre-registers a ModelRecord as a download target so
// findDownloadModel can find it even though no provider's Discover
// surfaces it. Used by the server's DownloadRuntimeModel handler to
// register online-catalog entries (from Ollama's library) before the
// runtime layer's inventory logic runs.
//
// The enrolled entry is expected to have OllamaRef set (for JIT
// resolution) or DownloadURL set (for direct download). State should
// be "not_downloaded" — the download loop transitions it forward.
func (m *InMemoryManager) EnrollDownload(model ModelRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.downloads[model.ID] = model
}

func (m *InMemoryManager) runDownload(ctx context.Context, model ModelRecord, job *downloadJob) {
	defer m.clearDownloadJob(model.ID, job)
	if err := os.MkdirAll(filepath.Dir(model.Path), 0755); err != nil {
		m.failDownload(model, err)
		return
	}
	tempPath := model.Path + ".part"
	// Resume support: a failed attempt's partial survives (see the
	// failure paths below), so a retry picks up where it left off via
	// a Range request instead of re-transferring gigabytes. Servers
	// that ignore Range reply 200 with the full body — handled by
	// starting over.
	var resumeFrom int64
	if fi, statErr := os.Stat(tempPath); statErr == nil && fi.Size() > 0 {
		resumeFrom = fi.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, model.DownloadURL, nil)
	if err != nil {
		m.failDownload(model, err)
		return
	}
	if resumeFrom > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeFrom))
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			m.markDownloadCancelled(model)
			return
		}
		m.failDownload(model, err)
		return
	}
	defer resp.Body.Close()
	switch {
	case resumeFrom > 0 && resp.StatusCode == http.StatusPartialContent:
		m.WriteLog(LogEntry{
			Source:  "cercano.runtime.download",
			Level:   "info",
			ModelID: model.ID,
			Message: fmt.Sprintf("resuming download from %d bytes", resumeFrom),
		})
		if total := contentRangeTotal(resp.Header.Get("Content-Range")); total > 0 {
			model.DownloadTotalBytes = total
			if model.SizeBytes == 0 {
				model.SizeBytes = total
			}
			m.updateDownload(model)
		}
	case resp.StatusCode == http.StatusOK:
		// Fresh download — or the server ignored our Range header and
		// sent the full body, in which case the partial is discarded.
		resumeFrom = 0
		if resp.ContentLength > 0 {
			model.DownloadTotalBytes = resp.ContentLength
			if model.SizeBytes == 0 {
				model.SizeBytes = resp.ContentLength
			}
			m.updateDownload(model)
		}
	default:
		m.failDownload(model, fmt.Errorf("download returned HTTP %d", resp.StatusCode))
		return
	}
	var file *os.File
	if resumeFrom > 0 {
		file, err = os.OpenFile(tempPath, os.O_WRONLY|os.O_APPEND, 0o644)
	} else {
		file, err = os.Create(tempPath)
	}
	if err != nil {
		m.failDownload(model, err)
		return
	}
	written := resumeFrom
	model.DownloadedBytes = written
	buf := make([]byte, 256*1024)
	lastUpdate := time.Now()
	for {
		if ctx.Err() != nil {
			_ = file.Close()
			_ = os.Remove(tempPath)
			m.markDownloadCancelled(model)
			return
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := file.Write(buf[:n]); err != nil {
				_ = file.Close()
				// Keep the partial — the next attempt resumes from it.
				m.failDownload(model, err)
				return
			}
			written += int64(n)
			model.DownloadedBytes = written
			if time.Since(lastUpdate) >= 250*time.Millisecond {
				m.updateDownload(model)
				lastUpdate = time.Now()
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = file.Close()
			if errors.Is(readErr, context.Canceled) || ctx.Err() != nil {
				// Deliberate cancel discards the partial.
				_ = os.Remove(tempPath)
				m.markDownloadCancelled(model)
				return
			}
			// Keep the partial — the next attempt resumes from it.
			m.failDownload(model, readErr)
			return
		}
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		m.failDownload(model, err)
		return
	}
	if err := os.Rename(tempPath, model.Path); err != nil {
		_ = os.Remove(tempPath)
		m.failDownload(model, err)
		return
	}
	model.DownloadState = "downloaded"
	model.DownloadedBytes = written
	if model.DownloadTotalBytes == 0 {
		model.DownloadTotalBytes = written
	}
	model.SizeBytes = model.DownloadTotalBytes
	model.ModifiedAt = time.Now()
	model.DownloadError = ""
	m.updateDownload(model)
	m.WriteLog(LogEntry{
		Source:  "cercano.runtime.download",
		Level:   "info",
		ModelID: model.ID,
		Message: "downloaded " + model.DisplayName,
	})
}

func (m *InMemoryManager) updateDownload(model ModelRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.downloads[model.ID] = model
}

func (m *InMemoryManager) clearDownloadJob(modelID string, job *downloadJob) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.downloadJobs[modelID] == job {
		delete(m.downloadJobs, modelID)
	}
}

func (m *InMemoryManager) failDownload(model ModelRecord, err error) {
	model.DownloadState = "failed"
	model.DownloadError = err.Error()
	m.updateDownload(model)
	m.WriteLog(LogEntry{
		Source:  "cercano.runtime.download",
		Level:   "error",
		ModelID: model.ID,
		Message: "download failed for " + model.DisplayName + ": " + err.Error(),
	})
}

func (m *InMemoryManager) markDownloadCancelled(model ModelRecord) {
	if existing, ok := m.download(model.ID); ok {
		if existing.DownloadState == "cancelled" {
			return
		}
		model = mergeDownloadRecord(model, existing)
	}
	model.DownloadState = "cancelled"
	model.DownloadError = ""
	m.updateDownload(model)
	m.WriteLog(LogEntry{
		Source:  "cercano.runtime.download",
		Level:   "info",
		ModelID: model.ID,
		Message: "cancelled download for " + model.DisplayName,
	})
}

func (m *InMemoryManager) download(modelID string) (ModelRecord, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	model, ok := m.downloads[modelID]
	return model, ok
}

func (m *InMemoryManager) overlayDownloads(models []ModelRecord) []ModelRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.downloads) == 0 {
		return models
	}
	out := append([]ModelRecord(nil), models...)
	seen := make(map[string]bool, len(out))
	for i := range out {
		if download, ok := m.downloads[out[i].ID]; ok {
			out[i] = mergeDownloadRecord(out[i], download)
		}
		seen[out[i].ID] = true
	}
	for id, download := range m.downloads {
		if !seen[id] {
			out = append(out, download)
		}
	}
	return out
}

func (m *InMemoryManager) WriteLog(entry LogEntry) {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	if entry.Level == "" {
		entry.Level = "info"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, entry)
	if m.logLimit > 0 && len(m.logs) > m.logLimit {
		m.logs = append([]LogEntry(nil), m.logs[len(m.logs)-m.logLimit:]...)
	}
}

func (m *InMemoryManager) providerSnapshot() []Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Provider, 0, len(m.providers))
	for _, provider := range m.providers {
		out = append(out, provider)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

func (m *InMemoryManager) provider(name string) (Provider, error) {
	if name == "" {
		return nil, errors.New("runtime is required")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	provider, ok := m.providers[name]
	if !ok {
		return nil, fmt.Errorf("runtime provider %q not found", name)
	}
	return provider, nil
}

func (m *InMemoryManager) instance(id string) (InstanceRecord, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	instance, ok := m.instances[id]
	return instance, ok
}

func sortModels(models []ModelRecord) {
	sort.Slice(models, func(i, j int) bool {
		if models[i].Runtime != models[j].Runtime {
			return models[i].Runtime < models[j].Runtime
		}
		if models[i].DisplayName != models[j].DisplayName {
			return models[i].DisplayName < models[j].DisplayName
		}
		return models[i].ID < models[j].ID
	})
}

func cloneEndpoints(in []EndpointRecord) []EndpointRecord {
	out := make([]EndpointRecord, len(in))
	copy(out, in)
	for i := range out {
		out[i].ActiveRoles = append([]string(nil), in[i].ActiveRoles...)
		out[i].Models = append([]string(nil), in[i].Models...)
	}
	return out
}

func cloneLogs(in []LogEntry) []LogEntry {
	out := make([]LogEntry, len(in))
	copy(out, in)
	return out
}

func mergeDownloadRecord(base, download ModelRecord) ModelRecord {
	base.DownloadState = download.DownloadState
	base.DownloadURL = download.DownloadURL
	base.DownloadedBytes = download.DownloadedBytes
	base.DownloadTotalBytes = download.DownloadTotalBytes
	base.DownloadError = download.DownloadError
	if download.SizeBytes > 0 {
		base.SizeBytes = download.SizeBytes
	}
	if !download.ModifiedAt.IsZero() {
		base.ModifiedAt = download.ModifiedAt
	}
	if download.Path != "" {
		base.Path = download.Path
	}
	return base
}
