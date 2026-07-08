package server

import (
	"context"
	"testing"

	"cercano/source/server/internal/meridian"
	"cercano/source/server/pkg/config"
)

func TestMeridianPortFromBaseURL(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"http://127.0.0.1:3456", 3456},
		{"http://127.0.0.1:9999", 9999},
		{"http://localhost:4567/", 4567},
		{"", 3456},                          // default
		{"http://127.0.0.1", 3456},          // no port → default
		{"://gibberish::", 3456},            // unparseable → default
		{"http://127.0.0.1:notanint", 3456}, // bad port → default
	}
	for _, c := range cases {
		if got := meridianPortFromBaseURL(c.in); got != c.want {
			t.Errorf("meridianPortFromBaseURL(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestSyncMeridianForProfile_NilManagerIsNoOp(t *testing.T) {
	s := &Server{} // meridianMgr nil
	// Must not panic regardless of profile.
	s.syncMeridianForProfile(config.CloudProfile{Route: "meridian", BaseURL: "http://127.0.0.1:3456"}, config.Config{})
	s.syncMeridianForProfile(config.CloudProfile{Route: "direct"}, config.Config{})
}

func TestSyncMeridianForProfile_NonMeridianRouteStopsManager(t *testing.T) {
	// Stop is the only side-effect on a non-meridian route. With a fresh
	// manager whose state is Disabled, calling Stop again is a safe no-op —
	// and we can verify by checking the state stays Disabled (no spurious
	// transition).
	m := meridian.New(nil, "")
	s := &Server{meridianMgr: m}

	s.syncMeridianForProfile(config.CloudProfile{Route: "direct"}, config.Config{})

	if got := m.Status().State; got != meridian.StateDisabled {
		t.Errorf("state = %s, want disabled (non-meridian route → Stop)", got)
	}
}

func TestSyncMeridianForProfile_MeridianRouteCallsEnsure(t *testing.T) {
	// We can't easily check "Ensure was called" without exposing fakes from
	// the meridian package. Instead, verify the observable: the manager's
	// status leaves StateDisabled after sync. Wire fakes via direct field
	// access (same-package test would be required for cross-package — we're
	// in package server, meridian fields are private — so we settle for
	// black-box: pass a profile with prereqs that will fail (claude auth
	// missing on a fresh keychain probe in CI/test env) and assert the
	// state moves OFF Disabled to one of the gated terminal states.
	m := meridian.New(nil, "")
	s := &Server{meridianMgr: m}

	// Sync against a meridian-routed profile.
	s.syncMeridianForProfile(config.CloudProfile{
		Route:   "meridian",
		BaseURL: "http://127.0.0.1:3456",
	}, config.Config{})

	// On any host that doesn't have Node 22+ AND a Claude keychain entry
	// AND nothing on port 3456, the state will be one of:
	//   prereqs_missing | needs_auth | external | starting | ready
	// Any of those proves Ensure ran. Disabled would prove it didn't.
	if got := m.Status().State; got == meridian.StateDisabled {
		t.Errorf("state = disabled after meridian-routed sync; expected Ensure to have run")
	}

	// Cleanup: any spawned supervisor is torn down by Stop.
	m.Stop()
	_ = context.Background()
}

func TestMeridianStatusToProto_RoundTrip(t *testing.T) {
	st := meridian.Status{
		State:       meridian.StateNeedsAuth,
		Message:     "sign in",
		Port:        3456,
		MissingDeps: []string{"Node 22+"},
	}
	p := meridianStatusToProto(st)
	if p.State != "needs_auth" {
		t.Errorf("state = %q, want needs_auth", p.State)
	}
	if p.Message != "sign in" || p.Port != 3456 {
		t.Errorf("message/port = (%q, %d), want (sign in, 3456)", p.Message, p.Port)
	}
	if len(p.MissingDeps) != 1 || p.MissingDeps[0] != "Node 22+" {
		t.Errorf("MissingDeps = %v, want [Node 22+]", p.MissingDeps)
	}
}
