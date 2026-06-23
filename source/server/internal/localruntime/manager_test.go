package localruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"cercano/source/server/internal/config"
)

type fakeProvider struct {
	name      string
	models    []ModelRecord
	startedID string
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Capabilities() RuntimeCapabilities {
	return RuntimeCapabilities{
		ManagedProcesses: true,
		CanStart:         true,
		CanStop:          true,
		CanRestart:       true,
		CanListModels:    true,
		CanStreamLogs:    true,
		SupportsChat:     true,
	}
}

func (f *fakeProvider) Discover(context.Context) ([]ModelRecord, error) {
	return append([]ModelRecord(nil), f.models...), nil
}

func (f *fakeProvider) Start(_ context.Context, req StartRequest, sink LogSink) (*InstanceRecord, error) {
	if req.ModelID == "" {
		return nil, errors.New("model id required")
	}
	f.startedID = "fake-" + req.ModelID
	sink.WriteLog(LogEntry{Source: "fake", Message: "started " + req.ModelID})
	return &InstanceRecord{
		ID:      f.startedID,
		Runtime: f.name,
		ModelID: req.ModelID,
		State:   StateRunning,
	}, nil
}

func (f *fakeProvider) Stop(context.Context, string) error { return nil }

func (f *fakeProvider) Probe(context.Context, string) (*InstanceHealth, error) {
	return &InstanceHealth{State: StateHealthy}, nil
}

func TestInMemoryManagerStatusAggregatesProvidersEndpointsAndLogs(t *testing.T) {
	manager := NewManager(WithEndpoints([]EndpointRecord{{
		ID:          "ollama",
		Kind:        "ollama",
		DisplayName: "Ollama",
		State:       StateUnknown,
	}}))
	manager.RegisterProvider(&fakeProvider{
		name: "llama_server",
		models: []ModelRecord{{
			ID:          "model-a",
			DisplayName: "Model A",
			Runtime:     "llama_server",
		}},
	})
	manager.WriteLog(LogEntry{
		Timestamp: time.Unix(100, 0),
		Source:    "test",
		Message:   "hello",
	})

	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if len(status.Models) != 1 || status.Models[0].ID != "model-a" {
		t.Fatalf("unexpected models: %#v", status.Models)
	}
	if len(status.Endpoints) != 1 || status.Endpoints[0].ID != "ollama" {
		t.Fatalf("unexpected endpoints: %#v", status.Endpoints)
	}
	if len(status.Logs) != 1 || status.Logs[0].Message != "hello" {
		t.Fatalf("unexpected logs: %#v", status.Logs)
	}
}

func TestInMemoryManagerStartStop(t *testing.T) {
	manager := NewManager()
	provider := &fakeProvider{name: "llama_server"}
	manager.RegisterProvider(provider)

	instance, err := manager.Start(context.Background(), StartRequest{
		Runtime: "llama_server",
		ModelID: "model-a",
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if instance.State != StateRunning {
		t.Fatalf("expected running instance, got %#v", instance)
	}

	if err := manager.Stop(context.Background(), StopRequest{InstanceID: instance.ID}); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	instances, err := manager.Instances(context.Background())
	if err != nil {
		t.Fatalf("Instances returned error: %v", err)
	}
	if len(instances) != 1 || instances[0].State != StateStopped {
		t.Fatalf("expected stopped instance, got %#v", instances)
	}
}

func TestEndpointsFromConfigIncludesExternalEndpointInfo(t *testing.T) {
	cfg := config.Config{
		OllamaURL:      "http://mac-studio.local:11434",
		LocalModel:     "qwen3-coder",
		EmbeddingModel: "nomic-embed-text",
		CloudProvider:  "anthropic",
		CloudModel:     "claude-test",
		CloudBaseURL:   "http://127.0.0.1:3456",
	}

	endpoints := EndpointsFromConfig(cfg)
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %#v", endpoints)
	}
	if endpoints[0].ID != "ollama" || endpoints[0].Scope != "lan" {
		t.Fatalf("unexpected ollama endpoint: %#v", endpoints[0])
	}
	if endpoints[1].Kind != "anthropic_proxy" || endpoints[1].AuthState != "configured" {
		t.Fatalf("unexpected cloud endpoint: %#v", endpoints[1])
	}
}
