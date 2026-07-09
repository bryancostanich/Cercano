package runtimes

import (
	"testing"

	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/meridian"
	"cercano/source/server/pkg/config"
)

func TestPortFromBaseURL(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"http://127.0.0.1:3456", 3456},
		{"http://127.0.0.1:9999", 9999},
		{"http://localhost:4567/", 4567},
		{"", 3456},
		{"http://127.0.0.1", 3456},
		{"://gibberish::", 3456},
		{"http://127.0.0.1:notanint", 3456},
	}
	for _, c := range cases {
		if got := PortFromBaseURL(c.in); got != c.want {
			t.Errorf("PortFromBaseURL(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestSyncMeridianForProfile_NilManagerIsNoOp(t *testing.T) {
	cfgService := cfgsvc.New("", config.Config{}, nil)
	svc := New(cfgService)
	// No manager set — must not panic.
	svc.SyncMeridianForProfile(config.CloudProfile{Route: "meridian", BaseURL: "http://127.0.0.1:3456"})
	svc.SyncMeridianForProfile(config.CloudProfile{Route: "direct"})
}

func TestSyncMeridianForProfile_NonMeridianRouteStopsManager(t *testing.T) {
	cfgService := cfgsvc.New("", config.Config{}, nil)
	svc := New(cfgService)
	m := meridian.New(nil, "")
	svc.SetMeridianMgr(m)

	svc.SyncMeridianForProfile(config.CloudProfile{Route: "direct"})

	if got := m.Status().State; got != meridian.StateDisabled {
		t.Errorf("state = %s, want disabled (non-meridian route → Stop)", got)
	}
}

func TestMeridianStatus_NoManager(t *testing.T) {
	svc := New(nil)
	_, ok := svc.MeridianStatus()
	if ok {
		t.Error("MeridianStatus should return ok=false when no manager is set")
	}
}

func TestMeridianStatus_WithManager(t *testing.T) {
	svc := New(nil)
	m := meridian.New(nil, "")
	svc.SetMeridianMgr(m)

	st, ok := svc.MeridianStatus()
	if !ok {
		t.Error("MeridianStatus should return ok=true when manager is set")
	}
	if st.State != meridian.StateDisabled {
		t.Errorf("fresh manager state = %s, want disabled", st.State)
	}
}

func TestStopMeridian_NilIsNoOp(t *testing.T) {
	svc := New(nil)
	svc.StopMeridian() // must not panic
}
