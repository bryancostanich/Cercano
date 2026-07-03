package server

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/agenttools"
)

// buildPermsServer returns a *Server with a toolRegistry containing one R-tier
// tool ("r_read") and one W-tier tool ("w_write"), ready for grantedRegistry tests.
func buildPermsServer(t *testing.T) *Server {
	t.Helper()
	rTool := stubDispatchTool{name: "r_read", perm: agenttools.PermR}
	wTool := stubDispatchTool{name: "w_write", perm: agenttools.PermW}

	reg := agenttools.NewRegistry()
	reg.MustRegister(rTool)
	reg.MustRegister(wTool)

	srv := NewServer(nil, nil, nil, nil, nil, nil)
	srv.SetToolRegistry(reg)
	return srv
}

func hasToolNamed(reg *agenttools.Registry, name string) bool {
	_, ok := reg.Get(name)
	return ok
}

// TestGrantedRegistry_Strict verifies W tools are dropped under strict mode.
func TestGrantedRegistry_Strict(t *testing.T) {
	srv := buildPermsServer(t)
	reg, err := srv.grantedRegistry([]string{"r_read", "w_write"}, agent.ModeStrict)
	if err != nil {
		t.Fatalf("grantedRegistry: %v", err)
	}

	if !hasToolNamed(reg, "r_read") {
		t.Error("strict: expected r_read to be present")
	}
	if hasToolNamed(reg, "w_write") {
		t.Error("strict: expected w_write to be dropped")
	}
}

// TestGrantedRegistry_Permissive verifies W tools are dropped under permissive mode.
func TestGrantedRegistry_Permissive(t *testing.T) {
	srv := buildPermsServer(t)
	reg, err := srv.grantedRegistry([]string{"r_read", "w_write"}, agent.ModePermissive)
	if err != nil {
		t.Fatalf("grantedRegistry: %v", err)
	}

	if !hasToolNamed(reg, "r_read") {
		t.Error("permissive: expected r_read to be present")
	}
	if hasToolNamed(reg, "w_write") {
		t.Error("permissive: expected w_write to be dropped")
	}
}

// TestGrantedRegistry_Bypass verifies both R and W tools are kept under bypass mode.
func TestGrantedRegistry_Bypass(t *testing.T) {
	srv := buildPermsServer(t)
	reg, err := srv.grantedRegistry([]string{"r_read", "w_write"}, agent.ModeBypass)
	if err != nil {
		t.Fatalf("grantedRegistry: %v", err)
	}

	if !hasToolNamed(reg, "r_read") {
		t.Error("bypass: expected r_read to be present")
	}
	if !hasToolNamed(reg, "w_write") {
		t.Error("bypass: expected w_write to be present")
	}
}

// TestGrantedRegistry_LogsUnknownToolNames verifies that requested tool names
// matching no registered tool are surfaced (no silent caps). Uses bypass mode
// so the W/X-drop log can't be the source of the asserted output.
func TestGrantedRegistry_LogsUnknownToolNames(t *testing.T) {
	srv := buildPermsServer(t)

	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()

	if _, err := srv.grantedRegistry([]string{"r_read", "bogus_tool"}, agent.ModeBypass); err != nil {
		t.Fatalf("grantedRegistry: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "bogus_tool") {
		t.Fatalf("expected unknown tool name to be logged, got: %q", out)
	}
	if strings.Contains(out, "r_read") {
		t.Errorf("known tool r_read should not be reported as unknown: %q", out)
	}
}

// TestGrantedRegistry_AllUnknownReturnsError verifies that when every requested
// tool name is unknown (e.g. the caller passed prefixed names like
// "mcp__oc__Read" that don't match Cercano's plain-name registry), the sub-agent
// is not spawned with an empty catalog — the caller gets a clear error.
func TestGrantedRegistry_AllUnknownReturnsError(t *testing.T) {
	srv := buildPermsServer(t)

	_, err := srv.grantedRegistry([]string{"mcp__oc__Read", "mcp__oc__Glob"}, agent.ModeBypass)
	if err == nil {
		t.Fatal("expected error when all requested tools are unknown, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "mcp__oc__Read") {
		t.Errorf("expected error to name the unknown tool, got: %q", msg)
	}
	if !strings.Contains(msg, "host prefix") {
		t.Errorf("expected error to hint about host prefixes, got: %q", msg)
	}
}

// TestGrantedRegistry_AllWTierUnderNonBypassReturnsError verifies that when
// every requested tool is registered but gets dropped by the permission-mode
// binding (all W/X under strict or permissive), the caller gets a clear error
// rather than an empty catalog.
func TestGrantedRegistry_AllWTierUnderNonBypassReturnsError(t *testing.T) {
	srv := buildPermsServer(t)

	_, err := srv.grantedRegistry([]string{"w_write"}, agent.ModePermissive)
	if err == nil {
		t.Fatal("expected error when every requested tool is dropped by permission mode, got nil")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("expected error to mention the read-only bound, got: %q", err.Error())
	}
}

// TestGrantedRegistry_NilTools_Strict verifies the R-tier default (nil tools) under strict
// mode: only R-tier tools, no W.
func TestGrantedRegistry_NilTools_Strict(t *testing.T) {
	srv := buildPermsServer(t)
	reg, err := srv.grantedRegistry(nil, agent.ModeStrict)
	if err != nil {
		t.Fatalf("grantedRegistry: %v", err)
	}

	if !hasToolNamed(reg, "r_read") {
		t.Error("nil+strict: expected r_read to be present")
	}
	if hasToolNamed(reg, "w_write") {
		t.Error("nil+strict: expected w_write to be absent (default is R-only, bound is R-only)")
	}
	// Confirm total count matches expectations.
	all := reg.All()
	if len(all) != 1 {
		t.Errorf("nil+strict: expected 1 tool, got %d", len(all))
	}
}
