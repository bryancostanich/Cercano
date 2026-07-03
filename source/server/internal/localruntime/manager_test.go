package localruntime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cercano/source/server/pkg/config"
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

func TestInMemoryManagerDownloadModelTracksProgressAndWritesFile(t *testing.T) {
	body := []byte("gguf test body")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "14")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), "model.gguf")
	manager := NewManager()
	manager.RegisterProvider(&fakeProvider{
		name: "llama_server",
		models: []ModelRecord{{
			ID:                 "catalog:model",
			DisplayName:        "Catalog Model",
			Runtime:            "llama_server",
			Source:             "catalog",
			Path:               target,
			Format:             "gguf",
			DownloadState:      "not_downloaded",
			DownloadURL:        server.URL,
			DownloadTotalBytes: int64(len(body)),
			SizeBytes:          int64(len(body)),
			SupportsChat:       true,
		}},
	})

	model, err := manager.DownloadModel(context.Background(), DownloadRequest{
		Runtime: "llama_server",
		ModelID: "catalog:model",
	})
	if err != nil {
		t.Fatalf("DownloadModel returned error: %v", err)
	}
	if model.DownloadState != "downloading" {
		t.Fatalf("expected downloading model, got %#v", model)
	}

	deadline := time.Now().Add(2 * time.Second)
	var downloaded ModelRecord
	for time.Now().Before(deadline) {
		status, err := manager.Status(context.Background())
		if err != nil {
			t.Fatalf("Status returned error: %v", err)
		}
		if got, ok := findModelRecord(status.Models, "catalog:model"); ok && got.DownloadState == "downloaded" {
			downloaded = got
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if downloaded.DownloadState != "downloaded" {
		t.Fatalf("model did not finish downloading: %#v", downloaded)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected downloaded file: %v", err)
	}
	if string(data) != string(body) {
		t.Fatalf("downloaded data = %q, want %q", string(data), string(body))
	}
	logs, err := manager.Logs(context.Background(), LogRequest{Source: "cercano.runtime.download"})
	if err != nil {
		t.Fatalf("Logs returned error: %v", err)
	}
	if len(logs) == 0 {
		t.Fatal("expected download logs")
	}
}

func TestInMemoryManagerCancelDownloadStopsActiveJob(t *testing.T) {
	started := make(chan struct{})
	released := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(released)
	}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), "model.gguf")
	manager := NewManager()
	manager.RegisterProvider(&fakeProvider{
		name: "llama_server",
		models: []ModelRecord{{
			ID:                 "catalog:model",
			DisplayName:        "Catalog Model",
			Runtime:            "llama_server",
			Source:             "catalog",
			Path:               target,
			Format:             "gguf",
			DownloadState:      "not_downloaded",
			DownloadURL:        server.URL,
			DownloadTotalBytes: 1024,
			SizeBytes:          1024,
			SupportsChat:       true,
		}},
	})

	if _, err := manager.DownloadModel(context.Background(), DownloadRequest{Runtime: "llama_server", ModelID: "catalog:model"}); err != nil {
		t.Fatalf("DownloadModel returned error: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("download server was not reached")
	}
	model, err := manager.CancelDownload(context.Background(), DownloadRequest{Runtime: "llama_server", ModelID: "catalog:model"})
	if err != nil {
		t.Fatalf("CancelDownload returned error: %v", err)
	}
	if model.DownloadState != "cancelled" {
		t.Fatalf("expected cancelled model, got %#v", model)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("download request was not cancelled")
	}
}

func TestInMemoryManagerDeleteModelRemovesDownloadedFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(target, []byte("downloaded"), 0644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	manager := NewManager()
	manager.RegisterProvider(&fakeProvider{
		name: "llama_server",
		models: []ModelRecord{{
			ID:                 "catalog:model",
			DisplayName:        "Catalog Model",
			Runtime:            "llama_server",
			Source:             "catalog",
			Path:               target,
			Format:             "gguf",
			DownloadState:      "downloaded",
			DownloadURL:        "https://example.test/model.gguf",
			DownloadTotalBytes: 10,
			SizeBytes:          10,
			SupportsChat:       true,
		}},
	})

	if err := manager.DeleteModel(context.Background(), DeleteModelRequest{Runtime: "llama_server", ModelID: "catalog:model"}); err != nil {
		t.Fatalf("DeleteModel returned error: %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected model file removed, stat err=%v", err)
	}
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	model, ok := findModelRecord(status.Models, "catalog:model")
	if !ok {
		t.Fatal("expected model in status")
	}
	if model.DownloadState != "not_downloaded" || model.DownloadedBytes != 0 {
		t.Fatalf("expected not_downloaded model after delete, got %#v", model)
	}
}

func TestEndpointsFromConfigIncludesExternalEndpointInfo(t *testing.T) {
	cfg := config.Config{
		OllamaURL:      "http://mac-studio.local:11434",
		OpenModel:     "qwen3-coder",
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

func findModelRecord(models []ModelRecord, id string) (ModelRecord, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return ModelRecord{}, false
}
