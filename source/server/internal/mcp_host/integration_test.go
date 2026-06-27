// source/server/internal/mcp_host/integration_test.go
package mcphost

import (
	"context"
	"testing"
	"time"

	"cercano/source/server/internal/agenttools"
)

func TestEndToEndRegisterAndCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := agenttools.NewRegistry()
	m := New(reg, t.TempDir(), 2*time.Second)
	m.dialFn = func(ctx context.Context, _ ServerConfig) (*conn, error) {
		return dial(ctx, startTestServer(t, ctx))
	}
	m.startServer(ctx, "test", ServerConfig{Command: "x"})

	tl, ok := reg.Get("mcp__test__echo")
	if !ok {
		t.Fatal("tool not registered")
	}
	if agenttools.OriginOf(tl) != agenttools.OriginMCP {
		t.Fatal("origin must be mcp (gate relies on it)")
	}
	res, err := tl.Execute(ctx, []byte(`{"text":"e2e"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "echo:e2e" {
		t.Fatalf("text = %q", res.Text)
	}
}
