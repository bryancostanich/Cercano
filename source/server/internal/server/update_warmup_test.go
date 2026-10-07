package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
)

type updateBlockingWarmProvider struct {
	recordingStartProvider
	entered chan struct{}
	finish  chan struct{}
}

func (p *updateBlockingWarmProvider) Start(ctx context.Context, r localruntime.StartRequest, l localruntime.LogSink) (*localruntime.InstanceRecord, error) {
	close(p.entered)
	<-p.finish
	return p.recordingStartProvider.Start(ctx, r, l)
}
func TestUpdateAdmissionTracksWarmupBeforeGoroutineRuns(t *testing.T) {
	model := localruntime.ModelRecord{ID: "llama_server:catalog:default-model", Runtime: "llama_server", DownloadState: localruntime.Downloaded}
	p := &updateBlockingWarmProvider{recordingStartProvider: recordingStartProvider{model: model}, entered: make(chan struct{}), finish: make(chan struct{})}
	mgr := localruntime.NewManager()
	mgr.RegisterProvider(p)
	s := NewServer(nil, nil, nil, nil, nil)
	s.SetRuntimeManager(mgr)
	if s.updateRuntimeTrackingErr != nil {
		t.Fatal(s.updateRuntimeTrackingErr)
	}
	s.SetConfigPersistence("", config.Config{OpenRuntime: "llama_server", LlamaServer: config.LlamaServerConfig{DefaultModel: model.ID}})
	s.OnDownloadStateChange(localruntime.DownloadEvent{Model: model, Prev: localruntime.Downloading, Next: localruntime.Downloaded})
	// The lease is already retained when OnDownloadStateChange returns.
	s.updateWork.mu.Lock()
	active := s.updateWork.active
	s.updateWork.mu.Unlock()
	if active == 0 {
		t.Fatal("warmup exposes an idle gap")
	}
	select {
	case <-p.entered:
	case <-time.After(2 * time.Second):
		close(p.finish)
		t.Fatal("warmup did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if release, err := s.updateWork.pauseWhenIdle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		if release != nil {
			release()
		}
		close(p.finish)
		t.Fatalf("warmup ignored: %v", err)
	}
	close(p.finish)
	done, cancelDone := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelDone()
	release, err := s.updateWork.pauseWhenIdle(done)
	if err != nil {
		t.Fatal(err)
	}
	release()
	s.SetRuntimeManager(mgr)
	if s.updateRuntimeTrackingErr != nil {
		t.Fatal("repeat wiring lost tracking")
	}
}
