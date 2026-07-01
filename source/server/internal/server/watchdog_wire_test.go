package server

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/locus"
	"cercano/source/server/internal/watchdog"
	"cercano/source/server/pkg/config"
)

// TestBuildWatchdogDisabledByDefault: the watchdog is opt-in, so with the
// zero-value (Enabled=false) config, buildWatchdog returns nil and the server
// behaves exactly as today.
func TestBuildWatchdogDisabledByDefault(t *testing.T) {
	s := &Server{}
	s.currentConfig.Watchdog = config.WatchdogConfig{Enabled: false}
	if wd := s.buildWatchdog(); wd != nil {
		t.Fatalf("expected nil watchdog when disabled, got %v", wd)
	}
}

// TestBuildWatchdogEnabled: with Enabled=true and a non-nil dispatch engine,
// buildWatchdog constructs a live watchdog (the OneShot closure captures the
// engine but is not invoked here).
func TestBuildWatchdogEnabled(t *testing.T) {
	eng := dispatch.NewEngine(
		func() dispatch.Providers { return dispatch.Providers{} },
		func() locus.Mode { return locus.LocalOnly },
		nil,
	)
	s := &Server{dispatchEngine: eng}
	s.currentConfig.Watchdog = config.WatchdogConfig{
		Enabled:       true,
		Mode:          "challenge-and-justify",
		Checks:        []string{"debug-loop"},
		EscalateAfter: 2,
	}
	wd := s.buildWatchdog()
	if wd == nil {
		t.Fatal("expected non-nil watchdog when enabled")
	}
}

// TestBuildWatchdogSkipsUnknownChecks: unknown check names are future checks and
// must be skipped without failing construction.
func TestBuildWatchdogSkipsUnknownChecks(t *testing.T) {
	eng := dispatch.NewEngine(
		func() dispatch.Providers { return dispatch.Providers{} },
		func() locus.Mode { return locus.LocalOnly },
		nil,
	)
	s := &Server{dispatchEngine: eng}
	s.currentConfig.Watchdog = config.WatchdogConfig{
		Enabled: true,
		Mode:    "strict",
		Checks:  []string{"future-check", "debug-loop"},
	}
	if wd := s.buildWatchdog(); wd == nil {
		t.Fatal("expected non-nil watchdog with a mix of known/unknown checks")
	}
}

// TestGateAdapterMapsDecisionFields: the per-request gate closure maps a
// watchdog.Decision to agent.WatchdogDecision field-for-field. A debug-loop
// violation is only surfaced by the OneShot model, so with a stub OneShot that
// reports a violation we assert the adapter carries Action/Protocol/Challenge
// through unchanged.
func TestGateAdapterMapsDecisionFields(t *testing.T) {
	oneShot := func(_ context.Context, _ string) (string, error) {
		return "VIOLATION: yes\nCHALLENGE: reduce to smallest failing case", nil
	}
	wd := watchdog.New(
		watchdog.Config{Mode: watchdog.ModeChallenge, EscalateAfter: 2},
		[]watchdog.Check{watchdog.DebugLoopCheck()},
		oneShot,
	)

	// Adapter identical to the one built in the runMainLoop caller.
	gate := func(ctx context.Context, toolName string, args json.RawMessage, transcript []llm.Message) agent.WatchdogDecision {
		d := wd.Gate(ctx, "conv-1", watchdog.Action{Kind: "tool_call", ToolName: toolName, ToolArgs: args, Transcript: transcript})
		return agent.WatchdogDecision{Action: d.Action, Protocol: d.Protocol, Challenge: d.Challenge}
	}

	// edit_file with no debug evidence in the transcript → challenge.
	got := gate(context.Background(), "edit_file", json.RawMessage(`{"path":"x.go"}`), nil)
	if got.Action != "challenge" {
		t.Fatalf("expected challenge action, got %q", got.Action)
	}
	if got.Protocol != "debug-loop" {
		t.Fatalf("expected debug-loop protocol, got %q", got.Protocol)
	}
	if got.Challenge == "" {
		t.Fatal("expected non-empty challenge text carried through the adapter")
	}
}
