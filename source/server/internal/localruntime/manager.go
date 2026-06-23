package localruntime

import (
	"context"
	"errors"
	"fmt"
	"sort"
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

// InMemoryManager is the first runtime manager implementation. It keeps
// dashboard state in memory and delegates real runtime behavior to providers.
type InMemoryManager struct {
	mu        sync.RWMutex
	providers map[string]Provider
	instances map[string]InstanceRecord
	endpoints []EndpointRecord
	logs      []LogEntry
	logLimit  int
}

func NewManager(opts ...Option) *InMemoryManager {
	m := &InMemoryManager{
		providers: make(map[string]Provider),
		instances: make(map[string]InstanceRecord),
		logLimit:  defaultLogLimit,
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
