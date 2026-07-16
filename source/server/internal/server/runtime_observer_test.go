package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
)

// recordingStartProvider records Start requests so the observer test can assert
// the sidecar was warmed. Discover reports one downloadable model.
type recordingStartProvider struct {
	mu      sync.Mutex
	started []localruntime.StartRequest
	model   localruntime.ModelRecord
}

func (p *recordingStartProvider) Name() string { return "llama_server" }
func (p *recordingStartProvider) Capabilities() localruntime.RuntimeCapabilities {
	return localruntime.RuntimeCapabilities{}
}
func (p *recordingStartProvider) Discover(context.Context) ([]localruntime.ModelRecord, error) {
	return []localruntime.ModelRecord{p.model}, nil
}
func (p *recordingStartProvider) Start(_ context.Context, req localruntime.StartRequest, _ localruntime.LogSink) (*localruntime.InstanceRecord, error) {
	p.mu.Lock()
	p.started = append(p.started, req)
	p.mu.Unlock()
	return &localruntime.InstanceRecord{ID: "inst-1", Runtime: req.Runtime, ModelID: req.ModelID, State: localruntime.InstanceStarting}, nil
}
func (p *recordingStartProvider) Stop(context.Context, string) error { return nil }
func (p *recordingStartProvider) Probe(context.Context, string) (*localruntime.InstanceHealth, error) {
	return &localruntime.InstanceHealth{State: localruntime.InstanceHealthy}, nil
}

func (p *recordingStartProvider) startCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.started)
}

// TestObserverAutoStartsActiveDefaultOnDownload proves the end-to-end payoff:
// when the ACTIVE runtime's default model reports Downloaded, the observer
// warms the sidecar. The observer dispatches its work to a goroutine, so the
// assertion polls briefly.
func TestObserverAutoStartsActiveDefaultOnDownload(t *testing.T) {
	model := localruntime.ModelRecord{
		ID:            "llama_server:catalog:default-model",
		DisplayName:   "Default Model",
		Runtime:       "llama_server",
		Source:        "catalog",
		DownloadState: localruntime.Downloaded,
	}
	prov := &recordingStartProvider{model: model}
	mgr := localruntime.NewManager()
	mgr.RegisterProvider(prov)

	srv := NewServer(nil, nil, nil, nil, nil)
	srv.SetRuntimeManager(mgr) // registers srv as observer
	srv.SetConfigPersistence("", config.Config{
		OpenRuntime: "llama_server",
		// DefaultModel is matched by MatchesModel; the canonical match is the
		// record's full ID (as a real config would carry).
		LlamaServer: config.LlamaServerConfig{DefaultModel: model.ID},
	})

	// Fire the transition the download worker would fire.
	srv.OnDownloadStateChange(localruntime.DownloadEvent{
		Model: model,
		Prev:  localruntime.Downloading,
		Next:  localruntime.Downloaded,
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if prov.startCount() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := prov.startCount(); got != 1 {
		t.Fatalf("expected exactly one auto-start, got %d", got)
	}
	if prov.started[0].ModelID != model.ID {
		t.Errorf("auto-started model = %q, want %q", prov.started[0].ModelID, model.ID)
	}
}

// TestObserverIgnoresInactiveRuntimeDownload verifies a completed download for a
// runtime that is NOT active does not warm anything (D3: single active runtime).
func TestObserverIgnoresInactiveRuntimeDownload(t *testing.T) {
	model := localruntime.ModelRecord{
		ID:            "llama_server:catalog:other",
		Runtime:       "llama_server",
		DownloadState: localruntime.Downloaded,
	}
	prov := &recordingStartProvider{model: model}
	mgr := localruntime.NewManager()
	mgr.RegisterProvider(prov)

	srv := NewServer(nil, nil, nil, nil, nil)
	srv.SetRuntimeManager(mgr)
	// Active runtime is mistralrs, but the download is for llama_server.
	srv.SetConfigPersistence("", config.Config{
		OpenRuntime: "mistralrs",
		MistralRS:   config.MistralRSConfig{DefaultModel: "something-else"},
	})

	srv.OnDownloadStateChange(localruntime.DownloadEvent{
		Model: model,
		Prev:  localruntime.Downloading,
		Next:  localruntime.Downloaded,
	})

	time.Sleep(200 * time.Millisecond)
	if got := prov.startCount(); got != 0 {
		t.Fatalf("expected no auto-start for inactive runtime, got %d", got)
	}
}
