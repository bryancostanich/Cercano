package localruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"cercano/source/server/pkg/config"
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

// WithConfigLoader injects a function that re-reads the on-disk configuration.
// Restart calls it so a runtime relaunch picks up config edits (e.g. a changed
// mistralrs.max_seq_len) instead of relaunching the process with the stale
// config captured at server boot. Nil or unset means restart reuses whatever
// config the providers already hold.
func WithConfigLoader(loader func() (config.Config, error)) Option {
	return func(m *InMemoryManager) {
		m.configLoader = loader
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
	observers    []Observer
	configLoader func() (config.Config, error)
}

// ConfigReloader is an optional capability a Provider may implement so the
// manager can push a freshly loaded configuration into it before a restart.
// Providers that don't implement it simply keep the config they were built
// with. The manager selects the matching slice of config.Config by provider
// name (see refreshProviderConfig), so each provider only ever sees its own
// runtime's config.
type ConfigReloader interface {
	ReloadConfig(config.Config)
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

// RegisterObserver adds an Observer notified on every download/instance state
// transition. Not safe to call concurrently with transitions; wire observers at
// construction/startup before the manager begins mutating state.
func (m *InMemoryManager) RegisterObserver(obs Observer) {
	if obs == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.observers = append(m.observers, obs)
}

// notifyDownload fires a DownloadEvent to every observer. Callers must NOT hold
// m.mu — observers may call back into the manager. A snapshot of the observer
// slice is taken under the lock to avoid racing RegisterObserver.
func (m *InMemoryManager) notifyDownload(ev DownloadEvent) {
	m.mu.RLock()
	obs := append([]Observer(nil), m.observers...)
	m.mu.RUnlock()
	for _, o := range obs {
		o.OnDownloadStateChange(ev)
	}
}

// notifyInstance fires an InstanceEvent to every observer. Same locking
// contract as notifyDownload.
func (m *InMemoryManager) notifyInstance(ev InstanceEvent) {
	m.mu.RLock()
	obs := append([]Observer(nil), m.observers...)
	m.mu.RUnlock()
	for _, o := range obs {
		o.OnInstanceStateChange(ev)
	}
}

func (m *InMemoryManager) RegisterProvider(provider Provider) {
	if provider == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers[provider.Name()] = provider
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

// UpdateInstance is the single guarded funnel for instance-record writes.
// Providers build the record with State already set to the target InstanceState
// and hand it here; UpdateInstance validates the move against the transition
// table (logging and refusing an illegal move rather than corrupting state),
// stores the record, and — when the state actually changed — notifies
// observers. A first-seen instance (no prior record) starts from
// InstanceUnknown, which may transition to anything.
func (m *InMemoryManager) UpdateInstance(instance InstanceRecord) {
	if instance.ID == "" {
		return
	}
	m.mu.Lock()
	prev := InstanceUnknown
	if existing, ok := m.instances[instance.ID]; ok {
		prev = existing.State
	}
	if !prev.CanTransitionTo(instance.State) {
		m.mu.Unlock()
		err := illegalTransition{kind: "instance", from: prev.String(), to: instance.State.String()}
		m.WriteLog(LogEntry{
			Source:  "cercano.runtime.instance",
			Level:   "error",
			ModelID: instance.ModelID,
			Message: err.Error() + " (refused for instance " + instance.ID + ")",
		})
		return
	}
	changed := prev != instance.State
	m.instances[instance.ID] = instance
	m.mu.Unlock()

	if changed {
		m.notifyInstance(InstanceEvent{Instance: instance, Prev: prev, Next: instance.State})
	}
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
	instance.State = InstanceStopped
	m.UpdateInstance(instance)
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
	// Re-read config from disk and push it into the target provider before the
	// relaunch, so a restart reflects config edits made since server boot
	// rather than reusing the stale config captured at construction. A load
	// failure is non-fatal: fall back to the provider's existing config so a
	// malformed edit can't wedge restart entirely.
	m.refreshProviderConfig(runtimeName)
	return m.Start(ctx, StartRequest{Runtime: runtimeName, ModelID: modelID})
}

// refreshProviderConfig re-reads the on-disk config and, if the named provider
// implements ConfigReloader, hands it the fresh config so its next launch uses
// updated args. No-ops when no loader is wired, the load fails, or the provider
// isn't reloadable.
func (m *InMemoryManager) refreshProviderConfig(runtimeName string) {
	m.mu.RLock()
	loader := m.configLoader
	m.mu.RUnlock()
	if loader == nil {
		return
	}
	provider, err := m.provider(runtimeName)
	if err != nil {
		return
	}
	reloader, ok := provider.(ConfigReloader)
	if !ok {
		return
	}
	cfg, err := loader()
	if err != nil {
		m.WriteLog(LogEntry{
			Source:  "cercano.runtime",
			Level:   "warn",
			Message: fmt.Sprintf("restart: config reload failed, reusing existing config for %q: %v", runtimeName, err),
		})
		return
	}
	reloader.ReloadConfig(cfg)
}

func (m *InMemoryManager) DownloadModel(ctx context.Context, req DownloadRequest) (*ModelRecord, error) {
	if strings.TrimSpace(req.Runtime) == "" {
		return nil, errors.New("runtime is required")
	}
	if strings.TrimSpace(req.ModelID) == "" {
		return nil, errors.New("model id is required")
	}
	if existing, ok := m.download(req.ModelID); ok && existing.DownloadState == Downloading {
		return &existing, nil
	}
	model, err := m.findDownloadModel(ctx, req)
	if err != nil {
		return nil, err
	}
	if model.DownloadState == Downloaded {
		return &model, nil
	}
	if model.DownloadURL == "" && len(model.DownloadURLs) == 0 {
		return nil, fmt.Errorf("model %q does not have a download URL", req.ModelID)
	}
	if model.Path == "" {
		return nil, fmt.Errorf("model %q does not have a target path", req.ModelID)
	}
	total := model.DownloadTotalBytes
	if total == 0 {
		total = model.SizeBytes
	}
	model.DownloadedBytes = 0
	model.DownloadTotalBytes = total
	downloadCtx, cancel := context.WithCancel(context.Background())
	job := &downloadJob{cancel: cancel}
	// Claim the download slot atomically. The pre-check at the top of the
	// function races the slow findDownloadModel gap (it may run Inventory), so
	// re-check under the lock, keyed by the resolved model.ID: whoever registers
	// the job first wins; a concurrent caller finds the existing "downloading"
	// record and spawns nothing, so two goroutines never write the same .part
	// file or clobber each other's cancelable job. We register the cancel job
	// under this same lock so a racing CancelDownload can find it, then drive
	// the guarded transition into Downloading (the sole writer of m.downloads,
	// which validates the move and fires observers).
	m.mu.Lock()
	if existing, ok := m.downloads[model.ID]; ok && existing.DownloadState == Downloading {
		m.mu.Unlock()
		cancel()
		return &existing, nil
	}
	m.downloadJobs[model.ID] = job
	m.mu.Unlock()
	model = m.setDownloadState(model, Downloading, "")
	if model.DownloadState != Downloading {
		// The guard refused the transition (e.g. an unexpected current state);
		// undo the job registration and report the current record.
		m.clearDownloadJob(model.ID, job)
		return &model, nil
	}
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
	if model.DownloadState != Downloading {
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
	if model.DownloadState == Downloading {
		return fmt.Errorf("model %q is downloading; cancel it before deleting", req.ModelID)
	}
	if model.DownloadURL == "" && len(model.DownloadURLs) == 0 {
		return fmt.Errorf("model %q is not a managed download", req.ModelID)
	}
	if model.Path == "" {
		return fmt.Errorf("model %q does not have a target path", req.ModelID)
	}
	// Remove every shard (a single-file model has just one) along with any
	// leftover .part from a failed or interrupted attempt.
	for _, p := range shardTargets(model) {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		_ = os.Remove(p + ".part")
	}
	model.DownloadedBytes = 0
	if model.DownloadTotalBytes == 0 {
		model.DownloadTotalBytes = model.SizeBytes
	}
	model.RuntimeState = InstanceStopped
	model.ModifiedAt = time.Time{}
	m.setDownloadState(model, DownloadNotStarted, "")
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
// The enrolled entry is expected to have DownloadURL (or DownloadURLs for a
// sharded model) set. State should be "not_downloaded" — the download loop
// transitions it forward.
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
	urls := model.DownloadURLs
	if len(urls) == 0 {
		urls = []string{model.DownloadURL}
	}
	targets := shardTargets(model)
	// completed accumulates bytes across finished shards so the progress meter
	// reflects the whole model, not just the shard currently in flight.
	var completed int64
	for i, url := range urls {
		destPath := targets[i]
		// A shard already on disk (finished by a prior run, or a single-file
		// target that already exists) counts toward progress and is skipped.
		if fi, statErr := os.Stat(destPath); statErr == nil && fi.Size() > 0 {
			completed += fi.Size()
			model.DownloadedBytes = completed
			m.storeDownload(model)
			continue
		}
		written, outcome := m.downloadShardWithRetry(ctx, url, destPath, &model, completed)
		if outcome != shardOK {
			// Cancelled, or failed after exhausting retries — downloadShard
			// already recorded the terminal state.
			return
		}
		completed += written
		model.DownloadedBytes = completed
		m.storeDownload(model)
	}
	model.DownloadedBytes = completed
	if model.DownloadTotalBytes == 0 {
		model.DownloadTotalBytes = completed
	}
	model.SizeBytes = model.DownloadTotalBytes
	model.ModifiedAt = time.Now()
	m.setDownloadState(model, Downloaded, "")
	m.WriteLog(LogEntry{
		Source:  "cercano.runtime.download",
		Level:   "info",
		ModelID: model.ID,
		Message: "downloaded " + model.DisplayName,
	})
}

// storeDownload writes the record to the map without any transition check. Use
// this ONLY for mutations that do not change DownloadState (progress-byte
// ticks). State changes must go through setDownloadState so the transition is
// validated and observers fire.
func (m *InMemoryManager) storeDownload(model ModelRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.downloads[model.ID] = model
}

// setDownloadState is the single guarded funnel for every DownloadState change.
// It validates the move against the transition table (logging and refusing an
// illegal move rather than corrupting the machine), writes the record, and
// notifies observers. errText is stored on the record and carried in the event
// when moving into DownloadFailed. It returns the stored record.
//
// The prior state is read from the map (the authoritative current value), not
// from the passed model, so a stale caller copy can't smuggle an illegal move
// past the guard. Callers pass the fully-updated record (bytes, timestamps,
// etc.) with DownloadState already set to the target.
func (m *InMemoryManager) setDownloadState(model ModelRecord, next DownloadState, errText string) ModelRecord {
	m.mu.Lock()
	prev := DownloadNotStarted
	if existing, ok := m.downloads[model.ID]; ok {
		prev = existing.DownloadState
	}
	if !prev.CanTransitionTo(next) {
		m.mu.Unlock()
		err := illegalTransition{kind: "download", from: prev.String(), to: next.String()}
		m.WriteLog(LogEntry{
			Source:  "cercano.runtime.download",
			Level:   "error",
			ModelID: model.ID,
			Message: err.Error() + " (refused for " + model.DisplayName + ")",
		})
		// Return the unchanged current record; the machine is left intact.
		if existing, ok := m.download(model.ID); ok {
			return existing
		}
		return model
	}
	model.DownloadState = next
	model.DownloadError = errText
	m.downloads[model.ID] = model
	m.mu.Unlock()

	m.notifyDownload(DownloadEvent{Model: model, Prev: prev, Next: next, Err: errText})
	return model
}

func (m *InMemoryManager) clearDownloadJob(modelID string, job *downloadJob) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.downloadJobs[modelID] == job {
		delete(m.downloadJobs, modelID)
	}
}

func (m *InMemoryManager) failDownload(model ModelRecord, err error) {
	m.setDownloadState(model, DownloadFailed, err.Error())
	m.WriteLog(LogEntry{
		Source:  "cercano.runtime.download",
		Level:   "error",
		ModelID: model.ID,
		Message: "download failed for " + model.DisplayName + ": " + err.Error(),
	})
}

func (m *InMemoryManager) markDownloadCancelled(model ModelRecord) {
	if existing, ok := m.download(model.ID); ok {
		if existing.DownloadState == DownloadCancelled {
			return
		}
		model = mergeDownloadRecord(model, existing)
	}
	m.setDownloadState(model, DownloadCancelled, "")
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
