// source/server/internal/mcp_host/manager_test.go
package mcphost

import (
	"context"
	"sync"
	"testing"
	"time"

	"cercano/source/server/internal/agenttools"
)

func TestManagerRegistersToolsOnStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := agenttools.NewRegistry()
	m := New(reg, t.TempDir(), time.Second)
	// Inject in-memory dial: ignore cfg, connect to a live test server.
	m.dialFn = func(ctx context.Context, _ ServerConfig) (*conn, error) {
		return dial(ctx, startTestServer(t, ctx))
	}

	m.startServer(ctx, "test", ServerConfig{Command: "ignored"})

	if _, ok := reg.Get("mcp__test__echo"); !ok {
		t.Fatalf("echo tool not registered; have %d tools", len(reg.All()))
	}
	st := m.List()
	if len(st) != 1 || st[0].State != StateReady || st[0].ToolCount != 1 {
		t.Fatalf("status = %+v", st)
	}
}

func TestManagerMarksFailedOnDialError(t *testing.T) {
	reg := agenttools.NewRegistry()
	m := New(reg, t.TempDir(), time.Second)
	m.dialFn = func(ctx context.Context, _ ServerConfig) (*conn, error) {
		return nil, context.DeadlineExceeded
	}
	m.startServer(context.Background(), "broken", ServerConfig{Command: "x"})

	st := m.List()
	if len(st) != 1 || st[0].State != StateFailed {
		t.Fatalf("want failed, got %+v", st)
	}
	if len(reg.All()) != 0 {
		t.Fatalf("failed server must register no tools")
	}
}

func TestManagerRestartReregisters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := agenttools.NewRegistry()
	m := New(reg, t.TempDir(), time.Second)
	m.dialFn = func(ctx context.Context, _ ServerConfig) (*conn, error) {
		return dial(ctx, startTestServer(t, ctx))
	}
	m.startServer(ctx, "test", ServerConfig{Command: "x"})
	if _, ok := reg.Get("mcp__test__echo"); !ok {
		t.Fatal("precondition: echo registered")
	}
	if err := m.Restart(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("mcp__test__echo"); !ok {
		t.Fatal("echo should be re-registered after restart")
	}
}

func TestManagerRemoveUnregisters(t *testing.T) {
	ctx := context.Background()
	reg := agenttools.NewRegistry()
	m := New(reg, t.TempDir(), time.Second)
	m.dialFn = func(ctx context.Context, _ ServerConfig) (*conn, error) {
		return dial(ctx, startTestServer(t, ctx))
	}
	m.startServer(ctx, "test", ServerConfig{Command: "x"})
	if err := m.Remove(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("mcp__test__echo"); ok {
		t.Fatal("echo should be gone after remove")
	}
	if len(m.List()) != 0 {
		t.Fatal("server should be dropped from status")
	}
}

// TestManagerDefunctHandleRegistersNothing reproduces the restart-during-warm-window
// race deterministically. The first dialFn call blocks on a channel; Restart is called
// while the goroutine is parked inside the dial, marking the handle defunct and
// cancelling its context. When the dial unblocks (via ctx cancellation), the old
// goroutine calls fail() and exits without registering. The registry ends up with
// exactly one echo registration from the new handle started by Restart.
func TestManagerDefunctHandleRegistersNothing(t *testing.T) {
	ctx := context.Background()
	reg := agenttools.NewRegistry()
	m := New(reg, t.TempDir(), time.Second)

	// published is closed when the first dial is entered (h is in m.servers by then).
	// released is a belt-and-suspenders fallback in case cancel didn't fire.
	published := make(chan struct{})
	released := make(chan struct{})
	var once sync.Once

	m.dialFn = func(ctx context.Context, _ ServerConfig) (*conn, error) {
		first := false
		once.Do(func() { first = true })
		if first {
			close(published) // h is now in m.servers; signal main goroutine
			select {
			case <-ctx.Done():
				return nil, ctx.Err() // torn down by teardown's h.cancel()
			case <-released:
				// fallback: proceed to connect (shouldn't normally be needed)
			}
		}
		return dial(ctx, startTestServer(t, ctx))
	}

	// Launch first startServer in background (simulates Start()).
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.startServer(ctx, "test", ServerConfig{Command: "x"})
	}()

	// Wait until the goroutine is parked inside dialFn (h is published to m.servers).
	<-published

	// Restart: marks old handle defunct, cancels its context, then starts a new handle
	// synchronously. By the time Restart returns the new handle's tools are registered.
	if err := m.Restart(ctx, "test"); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	// Belt-and-suspenders: unblock the old dial in case cancel didn't propagate.
	close(released)
	// Wait for the old goroutine to finish.
	<-done

	// Registry must have exactly one echo tool — from the new handle only.
	if _, ok := reg.Get("mcp__test__echo"); !ok {
		t.Fatal("echo tool should be registered by the new handle")
	}
	if n := len(reg.All()); n != 1 {
		t.Fatalf("want exactly 1 tool in registry; got %d", n)
	}
}
