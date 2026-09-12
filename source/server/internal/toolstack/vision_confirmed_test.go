package toolstack

import (
	"context"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
)

// countingProvider records every image request that reaches a model.
type countingProvider struct {
	inference.Provider
	name  string
	calls int
}

func (p *countingProvider) Name() string { return p.name }
func (p *countingProvider) Capabilities() inference.Capabilities {
	// Transport can carry images. That must never be read as model capability.
	return inference.Capabilities{SupportsVision: true}
}
func (p *countingProvider) Chat(context.Context, inference.Call) (inference.Result, error) {
	p.calls++
	return llm.ChatResponse{Blocks: []llm.Block{{Type: llm.BlockText, Text: "answer from " + p.name}}}, nil
}

func inspectFixture(t *testing.T, d VisionDeps) (*countingProvider, *countingProvider, error) {
	t.Helper()
	cloud := &countingProvider{name: "cloud"}
	open := &countingProvider{name: "open"}
	if d.CloudProvider == nil {
		d.CloudProvider = func() inference.Provider { return cloud }
	} else {
		d.CloudProvider = func() inference.Provider { return cloud }
	}
	if d.Mode == nil {
		d.Mode = func() locus.Mode { return locus.DefaultMode }
	}
	store, svc := BuildVision(d)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	_ = open
	img := store.Add("conv", "image/png", []byte{0x89, 0x50, 0x4e, 0x47, 1, 2, 3})
	if img.Rejected || img.Attachment == nil {
		t.Fatal("image fixture rejected")
	}
	_, err := svc.Inspect(context.Background(), "conv", img.Attachment.ID, "what is shown?")
	return cloud, open, err
}

func TestCloudVision_ConfirmedModelReceivesImage(t *testing.T) {
	cloud, _, err := inspectFixture(t, VisionDeps{
		CloudVisionModel:     func() (string, bool) { return "confirmed/model", true },
		CloudVisionConfirmed: func(string) bool { return true },
	})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if cloud.calls != 1 {
		t.Fatalf("confirmed model received %d image requests, want 1", cloud.calls)
	}
}

func TestCloudVision_UnknownModelReceivesNoImage(t *testing.T) {
	cloud, _, err := inspectFixture(t, VisionDeps{
		CloudVisionModel:     func() (string, bool) { return "unknown/model", true },
		CloudVisionConfirmed: func(string) bool { return false },
	})
	if cloud.calls != 0 {
		t.Fatalf("unknown model received %d image requests; err=%v", cloud.calls, err)
	}
	if err == nil {
		t.Fatal("expected an unavailable result when no lane is confirmed")
	}
}

// A missing confirmation hook must close the cloud lane, not open it.
func TestCloudVision_NilConfirmationClosesCloudLane(t *testing.T) {
	cloud, _, err := inspectFixture(t, VisionDeps{
		CloudVisionModel: func() (string, bool) { return "unknown/model", true },
	})
	if cloud.calls != 0 {
		t.Fatalf("nil confirmation hook allowed %d image requests; err=%v", cloud.calls, err)
	}
}

// Blocking cloud must still allow the existing local fallback.
func TestCloudVision_UnconfirmedFallsBackToOpen(t *testing.T) {
	open := &countingProvider{name: "open"}
	cloud := &countingProvider{name: "cloud"}
	store, svc := BuildVision(VisionDeps{
		OpenProvider:         func() inference.Provider { return open },
		OpenVisionModel:      func() (string, bool) { return "local-vision", true },
		CloudProvider:        func() inference.Provider { return cloud },
		CloudVisionModel:     func() (string, bool) { return "unknown/model", true },
		CloudVisionConfirmed: func(string) bool { return false },
		Mode:                 func() locus.Mode { return locus.DefaultMode },
	})
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	img := store.Add("conv", "image/png", []byte{0x89, 0x50, 0x4e, 0x47, 1, 2, 3})
	if _, err := svc.Inspect(context.Background(), "conv", img.Attachment.ID, "q"); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if cloud.calls != 0 {
		t.Fatalf("unconfirmed cloud model received %d image requests", cloud.calls)
	}
	if open.calls != 1 {
		t.Fatalf("open fallback served %d requests, want 1", open.calls)
	}
}

// open_only remains a hard no-cloud boundary regardless of confirmation.
func TestCloudVision_OpenOnlyNeverRoutesCloud(t *testing.T) {
	cloud := &countingProvider{name: "cloud"}
	store, svc := BuildVision(VisionDeps{
		CloudProvider:        func() inference.Provider { return cloud },
		CloudVisionModel:     func() (string, bool) { return "confirmed/model", true },
		CloudVisionConfirmed: func(string) bool { return true },
		Mode:                 func() locus.Mode { return locus.OpenOnly },
	})
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	img := store.Add("conv", "image/png", []byte{0x89, 0x50, 0x4e, 0x47, 1, 2, 3})
	_, _ = svc.Inspect(context.Background(), "conv", img.Attachment.ID, "q")
	if cloud.calls != 0 {
		t.Fatalf("open_only routed %d cloud image requests", cloud.calls)
	}
}
