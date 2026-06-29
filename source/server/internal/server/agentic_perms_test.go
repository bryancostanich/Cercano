package server

import (
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
	reg := srv.grantedRegistry([]string{"r_read", "w_write"}, agent.ModeStrict)

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
	reg := srv.grantedRegistry([]string{"r_read", "w_write"}, agent.ModePermissive)

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
	reg := srv.grantedRegistry([]string{"r_read", "w_write"}, agent.ModeBypass)

	if !hasToolNamed(reg, "r_read") {
		t.Error("bypass: expected r_read to be present")
	}
	if !hasToolNamed(reg, "w_write") {
		t.Error("bypass: expected w_write to be present")
	}
}

// TestGrantedRegistry_NilTools_Strict verifies the R-tier default (nil tools) under strict
// mode: only R-tier tools, no W.
func TestGrantedRegistry_NilTools_Strict(t *testing.T) {
	srv := buildPermsServer(t)
	reg := srv.grantedRegistry(nil, agent.ModeStrict)

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
