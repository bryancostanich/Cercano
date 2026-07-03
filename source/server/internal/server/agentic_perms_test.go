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
// tool name is unknown (after prefix normalization), the sub-agent is not
// spawned with an empty catalog — the caller gets a clear error naming the
// offending inputs and the registered tools available.
func TestGrantedRegistry_AllUnknownReturnsError(t *testing.T) {
	srv := buildPermsServer(t)

	_, err := srv.grantedRegistry([]string{"totally_bogus", "also_bogus"}, agent.ModeBypass)
	if err == nil {
		t.Fatal("expected error when all requested tools are unknown, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "totally_bogus") {
		t.Errorf("expected error to name the unknown tool, got: %q", msg)
	}
	if !strings.Contains(msg, "available tools") {
		t.Errorf("expected error to list available tools, got: %q", msg)
	}
	// Under bypass, both R and W tools should show as available.
	if !strings.Contains(msg, "r_read") || !strings.Contains(msg, "w_write") {
		t.Errorf("bypass hint should list all registered tools (R and W), got: %q", msg)
	}
}

// TestGrantedRegistry_PrefixedNameResolvesToPlainTool verifies that when a
// caller passes a host-prefixed name like "mcp__oc__r_read" and no tool is
// registered under that exact name, grantedRegistry strips the prefix and
// finds "r_read" — so a misgranted-but-recognizable name still works.
func TestGrantedRegistry_PrefixedNameResolvesToPlainTool(t *testing.T) {
	srv := buildPermsServer(t)

	reg, err := srv.grantedRegistry([]string{"mcp__oc__r_read"}, agent.ModeBypass)
	if err != nil {
		t.Fatalf("prefix normalization should resolve mcp__oc__r_read to r_read, got: %v", err)
	}
	if !hasToolNamed(reg, "r_read") {
		t.Errorf("expected r_read to be in the resulting registry after prefix strip")
	}
}

// TestGrantedRegistry_ExactNameWinsOverStrippedForm verifies the "exact match
// first" rule: if a tool is registered under the literal fully-qualified name
// mcp__oc__X (as happens when Cercano hosts an MCP server named "oc"), the
// grant resolves to that tool, NOT to a plain-named X that happens to exist.
func TestGrantedRegistry_ExactNameWinsOverStrippedForm(t *testing.T) {
	// Register a plain "widget" AND a fully-qualified "mcp__oc__widget" —
	// both real tools. Grant the fully-qualified name; expect the exact one.
	plain := stubDispatchTool{name: "widget", perm: agenttools.PermR}
	hosted := stubDispatchTool{name: "mcp__oc__widget", perm: agenttools.PermR}

	reg := agenttools.NewRegistry()
	reg.MustRegister(plain)
	reg.MustRegister(hosted)

	srv := NewServer(nil, nil, nil, nil, nil, nil)
	srv.SetToolRegistry(reg)

	out, err := srv.grantedRegistry([]string{"mcp__oc__widget"}, agent.ModeBypass)
	if err != nil {
		t.Fatalf("grantedRegistry: %v", err)
	}
	if !hasToolNamed(out, "mcp__oc__widget") {
		t.Errorf("exact match should have resolved to the fully-qualified tool, got registry: %+v", out.All())
	}
	if hasToolNamed(out, "widget") {
		t.Errorf("exact match should NOT have fallen through to the stripped form")
	}
}

// TestGrantedRegistry_AllWTierUnderNonBypassReturnsError verifies that when
// every requested tool is registered but gets dropped by the permission-mode
// binding (all W/X under strict or permissive), the caller gets a clear error
// naming both the requested tools and the available R-tier alternatives.
func TestGrantedRegistry_AllWTierUnderNonBypassReturnsError(t *testing.T) {
	srv := buildPermsServer(t)

	_, err := srv.grantedRegistry([]string{"w_write"}, agent.ModePermissive)
	if err == nil {
		t.Fatal("expected error when every requested tool is dropped by permission mode, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "read-tier") {
		t.Errorf("expected error to mention the read-tier bound, got: %q", msg)
	}
	if !strings.Contains(msg, "w_write") {
		t.Errorf("expected error to name the requested tool, got: %q", msg)
	}
	if !strings.Contains(msg, "r_read") {
		t.Errorf("permissive-mode hint should suggest available R-tier tools, got: %q", msg)
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
