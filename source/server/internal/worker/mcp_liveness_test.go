package worker

import (
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/llm"
)

// The tool loop re-reads mode and allowlist per gate decision so a mid-turn
// tightening (leaving /bypass, or removing an mcp_allow pattern from another
// client) takes effect IMMEDIATELY — see the comment at toolloop.go's gate.
// A worker store that pinned both at StartTurn would keep gating on stale,
// more-permissive values for the rest of the turn.
//
// These tests assert the worker store honors a live update, which is what the
// host applies when it observes a permission change mid-turn.
func TestWorkerStoreReflectsMidTurnModeTightening(t *testing.T) {
	s := agent.NewStaticPermissionStoreWithMCPAllow(agent.ModeBypass, nil)
	if got := s.Mode(); got != agent.ModeBypass {
		t.Fatalf("initial mode = %q, want bypass", got)
	}
	// Under bypass nothing gates.
	if agent.GateDecisionForMCP(s.Mode(), llm.PermW, true, false) {
		t.Fatal("precondition: bypass must not gate")
	}

	s.ApplyRuntimeUpdate(agent.ModeStrict, nil)

	if got := s.Mode(); got != agent.ModeStrict {
		t.Errorf("after tightening, mode = %q, want strict — worker gated on a stale mode", got)
	}
	if !agent.GateDecisionForMCP(s.Mode(), llm.PermW, true, false) {
		t.Error("after tightening to strict, a non-allowlisted MCP tool must gate")
	}
}

func TestWorkerStoreReflectsMidTurnAllowlistRemoval(t *testing.T) {
	const name = "srv__tool"
	s := agent.NewStaticPermissionStoreWithMCPAllow(agent.ModePermissive, []string{name})
	if !s.IsMCPAllowed(name) {
		t.Fatal("precondition: tool must start allowlisted")
	}

	// The operator removes the pattern mid-turn.
	s.ApplyRuntimeUpdate(agent.ModePermissive, []string{})

	if s.IsMCPAllowed(name) {
		t.Error("tool still allowlisted after removal — worker gated on a stale allowlist")
	}
	if !agent.GateDecisionForMCP(s.Mode(), llm.PermW, true, s.IsMCPAllowed(name)) {
		t.Error("after allowlist removal the MCP tool must gate")
	}
}
