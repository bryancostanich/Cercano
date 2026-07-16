package server

import (
	"context"
	"testing"

	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// readinessProvider is a minimal provider that reports one model in a chosen
// download state, so we can drive openRuntimeReadinessFor end-to-end through a
// real InMemoryManager inventory.
type readinessProvider struct {
	name  string
	model localruntime.ModelRecord
}

func (p *readinessProvider) Name() string { return p.name }
func (p *readinessProvider) Capabilities() localruntime.RuntimeCapabilities {
	return localruntime.RuntimeCapabilities{}
}
func (p *readinessProvider) Discover(context.Context) ([]localruntime.ModelRecord, error) {
	return []localruntime.ModelRecord{p.model}, nil
}
func (p *readinessProvider) Start(context.Context, localruntime.StartRequest, localruntime.LogSink) (*localruntime.InstanceRecord, error) {
	return &localruntime.InstanceRecord{}, nil
}
func (p *readinessProvider) Stop(context.Context, string) error { return nil }
func (p *readinessProvider) Probe(context.Context, string) (*localruntime.InstanceHealth, error) {
	return &localruntime.InstanceHealth{}, nil
}

// serverWithModel builds a Server whose runtime manager reports one mistralrs
// model in the given download state, and whose config points the mistralrs
// default at that model.
func serverWithModel(t *testing.T, state localruntime.DownloadState) (*Server, config.Config) {
	t.Helper()
	model := localruntime.ModelRecord{
		ID:            "mistralrs:catalog:qwen3-1.7b",
		DisplayName:   "Qwen3 1.7B",
		Runtime:       "mistralrs",
		Source:        "catalog",
		DownloadState: state,
	}
	mgr := localruntime.NewManager()
	mgr.RegisterProvider(&readinessProvider{name: "mistralrs", model: model})

	srv := NewServer(nil, nil, nil, nil, nil)
	srv.SetRuntimeManager(mgr)
	cfg := config.Config{
		OpenRuntime: "mistralrs",
		MistralRS:   config.MistralRSConfig{DefaultModel: model.ID},
	}
	srv.SetConfigPersistence("", cfg)
	return srv, cfg
}

func TestReadiness_MistralRS_Downloaded(t *testing.T) {
	srv, cfg := serverWithModel(t, localruntime.Downloaded)
	r := srv.openRuntimeReadinessFor(context.Background(), cfg, "mistralrs")
	if r.State != readyToServe {
		t.Fatalf("state = %v, want readyToServe", r.State)
	}
	st := srv.openRuntimeStatus(context.Background(), cfg, "mistralrs")
	if !st.Ok || st.Downloading {
		t.Errorf("downloaded model → Ok=true Downloading=false, got %+v", st)
	}
}

func TestReadiness_MistralRS_Downloading(t *testing.T) {
	srv, cfg := serverWithModel(t, localruntime.Downloading)
	r := srv.openRuntimeReadinessFor(context.Background(), cfg, "mistralrs")
	if r.State != readyDownloading {
		t.Fatalf("state = %v, want readyDownloading", r.State)
	}
	st := srv.openRuntimeStatus(context.Background(), cfg, "mistralrs")
	// This is the bug Phase 1 fixes: a downloading model reports downloading,
	// NOT missing/(F1).
	if st.Ok {
		t.Errorf("downloading model must not be Ok, got %+v", st)
	}
	if !st.Downloading {
		t.Errorf("Downloading should be true, got %+v", st)
	}
	if st.Missing != "" {
		t.Errorf("Missing must be empty while downloading, got %q", st.Missing)
	}
}

func TestReadiness_MistralRS_NotDownloaded(t *testing.T) {
	srv, cfg := serverWithModel(t, localruntime.DownloadNotStarted)
	r := srv.openRuntimeReadinessFor(context.Background(), cfg, "mistralrs")
	if r.State != readyMissing {
		t.Fatalf("state = %v, want readyMissing", r.State)
	}
	st := srv.openRuntimeStatus(context.Background(), cfg, "mistralrs")
	if st.Ok || st.Downloading {
		t.Errorf("not-downloaded → Ok=false Downloading=false, got %+v", st)
	}
	if st.Missing != "model" {
		t.Errorf("Missing = %q, want model", st.Missing)
	}
}

// TestReadiness_PullPathNoLongerGatesMistralRS proves the specific regression
// that started Phase 1: GetOpenRuntimeStatus used to return unconditional
// ok=true for any runtime != llama_server, hiding the chip. Now it routes
// through the agnostic readiness, so a not-downloaded mistralrs model reports
// missing on the PULL path too.
func TestReadiness_PullPathNoLongerGatesMistralRS(t *testing.T) {
	srv, _ := serverWithModel(t, localruntime.DownloadNotStarted)
	resp, err := srv.GetOpenRuntimeStatus(context.Background(), &proto.GetOpenRuntimeStatusRequest{Runtime: "mistralrs"})
	if err != nil {
		t.Fatalf("GetOpenRuntimeStatus: %v", err)
	}
	if resp.GetStatus().GetOk() {
		t.Errorf("pull path must report not-ok for a not-downloaded mistralrs model, got %+v", resp.GetStatus())
	}
	if resp.GetStatus().GetMissing() != "model" {
		t.Errorf("pull path Missing = %q, want model", resp.GetStatus().GetMissing())
	}
}
