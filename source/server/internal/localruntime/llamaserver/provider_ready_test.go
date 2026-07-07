package llamaserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/server/internal/localruntime"
)

// TestWaitReadyFailsFastWhenInstanceDied: a dead process never becomes
// ready, so the readiness poll must return immediately with the recorded
// exit error instead of watching a closed port until the deadline.
func TestWaitReadyFailsFastWhenInstanceDied(t *testing.T) {
	p := &Provider{
		client:  http.DefaultClient,
		running: map[string]*managedInstance{},
	}
	p.running["dead"] = &managedInstance{record: localruntime.InstanceRecord{
		ID:        "dead",
		State:     localruntime.StateFailed,
		LastError: "exit status 1",
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	err := p.waitReady(ctx, "dead", "http://127.0.0.1:1")
	if err == nil || !strings.Contains(err.Error(), "exited during startup") {
		t.Fatalf("err = %v, want exited-during-startup", err)
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("err = %v, want the instance's LastError included", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waitReady took %s — death must fail fast, not poll out the window", elapsed)
	}
}

// TestFinishReadinessFlipsStartingToRunning: the background poller keeps
// waiting past the caller's readiness window and marks the instance running
// once /health finally answers — the mechanism that turns a slow-loading
// model into a warm, reusable instance.
func TestFinishReadinessFlipsStartingToRunning(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable) // still loading
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := &Provider{
		client:  server.Client(),
		running: map[string]*managedInstance{},
	}
	p.running["slow"] = &managedInstance{record: localruntime.InstanceRecord{
		ID:    "slow",
		State: localruntime.StateStarting,
	}}

	p.finishReadiness("slow", server.URL, nil)

	state, lastErr := p.instanceStatus("slow")
	if state != localruntime.StateRunning {
		t.Fatalf("state = %q, want running", state)
	}
	if lastErr != "" {
		t.Fatalf("lastError = %q, want cleared", lastErr)
	}
	p.mu.RLock()
	readyAt := p.running["slow"].record.ReadyAt
	p.mu.RUnlock()
	if readyAt.IsZero() {
		t.Fatal("ReadyAt not set")
	}
}

// TestFinishReadinessLeavesStoppedInstanceAlone: a user-stopped instance
// must not be resurrected to running by a late health response.
func TestFinishReadinessLeavesStoppedInstanceAlone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := &Provider{
		client:  server.Client(),
		running: map[string]*managedInstance{},
	}
	p.running["stopped"] = &managedInstance{record: localruntime.InstanceRecord{
		ID:    "stopped",
		State: localruntime.StateStopped,
	}}

	p.finishReadiness("stopped", server.URL, nil)

	state, _ := p.instanceStatus("stopped")
	if state != localruntime.StateStopped {
		t.Fatalf("state = %q — finishReadiness must not resurrect a stopped instance", state)
	}
}
