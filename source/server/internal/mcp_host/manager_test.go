// source/server/internal/mcp_host/manager_test.go
package mcphost

import (
	"context"
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
