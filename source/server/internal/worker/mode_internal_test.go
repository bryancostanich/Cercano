package worker

import (
	"context"
	"testing"

	"cercano/source/server/internal/agent"
	agenttools "cercano/source/server/internal/agenttools"
	providers "cercano/source/server/internal/hostsvc/providers"
	"cercano/source/server/internal/runner"
	"cercano/source/server/pkg/proto"
)

type modeTestTools struct{}

func (modeTestTools) Registry() *agenttools.Registry { return agenttools.NewRegistry() }
func (modeTestTools) GrantedRegistry([]string) (*agenttools.Registry, []string, []string, error) {
	return agenttools.NewRegistry(), nil, nil, nil
}

// TestBuildDeps_PermissionModeMatchesHost pins that the worker's permission
// store adopts the HOST's mode (carried in StartTurn.permission_mode), not a
// hardcoded default. Defaulting to permissive when the host is strict would
// silently auto-run write tools the user asked to be prompted for — a security
// regression the worker boundary must not introduce.
func TestBuildDeps_PermissionModeMatchesHost(t *testing.T) {
	ws := NewWithFactories(
		func(*proto.StartTurn) (providers.Resolver, error) { return nil, nil },
		func(*proto.StartTurn) (runner.ToolSvc, error) { return modeTestTools{}, nil },
	)
	cases := []struct {
		in   string
		want agent.PermissionMode
	}{
		{"strict", agent.ModeStrict},
		{"permissive", agent.ModePermissive},
		{"bypass", agent.ModeBypass},
		{"", agent.ModePermissive},        // empty falls back to the host default
		{"garbage", agent.ModePermissive}, // unparseable falls back, never panics
	}
	for _, tc := range cases {
		deps, err := ws.buildDeps(context.Background(), &proto.StartTurn{
			Config:         &proto.ConfigSnapshot{},
			PermissionMode: tc.in,
		}, nil, nil, nil, nil)
		if err != nil {
			t.Fatalf("buildDeps(mode=%q): %v", tc.in, err)
		}
		if got := deps.Perms.Store().Mode(); got != tc.want {
			t.Errorf("permission_mode=%q → worker store mode %q, want %q", tc.in, got, tc.want)
		}
	}
}
