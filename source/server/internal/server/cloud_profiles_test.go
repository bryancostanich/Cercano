package server

import (
	"context"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/cloudfactory"
	"cercano/source/server/internal/legacymodels"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// coordinatorFlavorConcreteNote: s.coordinator is *loop.ADKCoordinator, a
// concrete type that requires real session/validator/model-provider dependencies
// to construct.  It cannot be faked with a struct literal in tests.  The nil
// guard (if s.coordinator != nil) inside installAbsentCloud and rebuildCloud
// means the coordinator path is always a safe no-op in the test server.
// The router assertions below cover the same absent-sentinel value that
// coordinator.SetCloudProvider would receive in production.

// fakeRouter satisfies RouterCloudUpdater for tests.
type fakeRouter struct {
	last agent.ModelProvider
}

func (f *fakeRouter) SetCloudProvider(p agent.ModelProvider)              { f.last = p }
func (f *fakeRouter) GetModelProviders() map[string]agent.ModelProvider   { return nil }

func newTestServer() (*Server, *fakeRouter) {
	r := &fakeRouter{}
	s := &Server{
		router: r,
		currentConfig: config.Config{
			CloudProfiles: []config.CloudProfile{
				{Name: "messages-one", Flavor: "messages", Model: "claude-3-5-haiku-20241022"},
				{Name: "cc-one", Flavor: "chat_completions", Model: "gpt-4o"},
				{Name: "unsup-one", Flavor: "responses", Model: "x"},
			},
		},
	}
	s.SetSecrets(secrets.NewMemory())
	return s, r
}

func TestGetCloudProfilesListsBoth(t *testing.T) {
	s, _ := newTestServer()
	resp, err := s.GetCloudProfiles(context.Background(), &proto.GetCloudProfilesRequest{})
	if err != nil {
		t.Fatalf("GetCloudProfiles: %v", err)
	}
	if len(resp.Profiles) != 3 {
		t.Fatalf("want 3 profiles, got %d", len(resp.Profiles))
	}
	// No keys set yet — both should be false.
	for _, p := range resp.Profiles {
		if p.HasKey {
			t.Errorf("profile %q: HasKey should be false before any key set", p.Name)
		}
	}

	// Set a key for messages-one.
	if err := s.secrets.Set("messages-one", "sk-test"); err != nil {
		t.Fatal(err)
	}
	resp2, _ := s.GetCloudProfiles(context.Background(), &proto.GetCloudProfilesRequest{})
	hasKeyFor := map[string]bool{}
	for _, p := range resp2.Profiles {
		hasKeyFor[p.Name] = p.HasKey
	}
	if !hasKeyFor["messages-one"] {
		t.Error("messages-one: HasKey should be true after Set")
	}
	if hasKeyFor["cc-one"] {
		t.Error("cc-one: HasKey should still be false")
	}
}

func TestSetActiveCloudProfileMessagesOk(t *testing.T) {
	s, _ := newTestServer()
	// Set a key so rebuildCloud can succeed.
	if err := s.secrets.Set("messages-one", "sk-test"); err != nil {
		t.Fatal(err)
	}
	resp, err := s.SetActiveCloudProfile(context.Background(), &proto.SetActiveCloudProfileRequest{Name: "messages-one"})
	if err != nil {
		t.Fatalf("SetActiveCloudProfile: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("want Ok=true, got false: %s", resp.Error)
	}
	if s.cloudLLMProvider == nil {
		t.Error("cloudLLMProvider should be non-nil after successful rebuildCloud")
	}
}

func TestSetActiveCloudProfileUnsupportedFlavorGoesAbsent(t *testing.T) {
	s, r := newTestServer()
	if err := s.secrets.Set("unsup-one", "sk-test"); err != nil {
		t.Fatal(err)
	}
	resp, err := s.SetActiveCloudProfile(context.Background(), &proto.SetActiveCloudProfileRequest{Name: "unsup-one"})
	if err != nil {
		t.Fatalf("SetActiveCloudProfile: %v", err)
	}
	if resp.Ok {
		t.Error("want Ok=false for unsupported flavor responses")
	}
	if s.cloudLLMProvider != nil {
		t.Error("cloudLLMProvider should be nil (cleared) on build failure")
	}
	if _, ok := r.last.(*legacymodels.AbsentCloudProvider); !ok {
		t.Errorf("router should hold AbsentCloudProvider after build failure, got %T", r.last)
	}
}

func TestSetActiveCloudProfileNonexistent(t *testing.T) {
	s, _ := newTestServer()
	resp, err := s.SetActiveCloudProfile(context.Background(), &proto.SetActiveCloudProfileRequest{Name: "nope"})
	if err != nil {
		t.Fatalf("SetActiveCloudProfile: %v", err)
	}
	if resp.Ok {
		t.Error("want Ok=false for nonexistent profile")
	}
}

func TestSetCloudProfileKey(t *testing.T) {
	t.Run("existing profile sets key and has_key becomes true", func(t *testing.T) {
		s, _ := newTestServer()
		resp, err := s.SetCloudProfileKey(context.Background(), &proto.SetCloudProfileKeyRequest{
			Name:   "messages-one",
			ApiKey: "sk-test-key",
		})
		if err != nil {
			t.Fatalf("SetCloudProfileKey: %v", err)
		}
		if !resp.Ok {
			t.Fatalf("want Ok=true, got false: %s", resp.Error)
		}
		// GetCloudProfiles should now report has_key=true for messages-one.
		listResp, err := s.GetCloudProfiles(context.Background(), &proto.GetCloudProfilesRequest{})
		if err != nil {
			t.Fatalf("GetCloudProfiles: %v", err)
		}
		hasKey := map[string]bool{}
		for _, p := range listResp.Profiles {
			hasKey[p.Name] = p.HasKey
		}
		if !hasKey["messages-one"] {
			t.Error("messages-one: HasKey should be true after SetCloudProfileKey")
		}
	})

	t.Run("nonexistent profile returns Ok=false", func(t *testing.T) {
		s, _ := newTestServer()
		resp, err := s.SetCloudProfileKey(context.Background(), &proto.SetCloudProfileKeyRequest{
			Name:   "no-such-profile",
			ApiKey: "sk-test-key",
		})
		if err != nil {
			t.Fatalf("SetCloudProfileKey: %v", err)
		}
		if resp.Ok {
			t.Error("want Ok=false for nonexistent profile name")
		}
	})
}

// TestRebuildCloudKeylessGoesAbsent verifies Fix 1: when the active profile has
// no stored key and no BaseURL, rebuildCloud installs the absent sentinel rather
// than wiring a dead provider.
func TestRebuildCloudKeylessGoesAbsent(t *testing.T) {
	s, r := newTestServer()
	s.currentConfig.ActiveCloudProfile = "messages-one"
	// No key stored in memory store — secrets.Get will fail.

	err := s.rebuildCloud()
	if err == nil {
		t.Fatal("want error from rebuildCloud with no key and no BaseURL")
	}
	if s.cloudLLMProvider != nil {
		t.Error("cloudLLMProvider should be nil (absent) when no key")
	}
	if _, ok := r.last.(*legacymodels.AbsentCloudProvider); !ok {
		t.Errorf("router should hold AbsentCloudProvider when no key, got %T", r.last)
	}
}

// TestRebuildCloudKeylessBaseURLCarveout verifies Fix 1's proxy carve-out: when
// BaseURL is set (proxy/Meridian handles auth), an empty key is acceptable and
// the profile proceeds to BuildCloudProvider rather than going absent.
func TestRebuildCloudKeylessBaseURLCarveout(t *testing.T) {
	s, r := newTestServer()
	// Add a messages profile with a BaseURL but no key.
	proxyProfile := config.CloudProfile{
		Name:    "proxy-one",
		Flavor:  cloudfactory.FlavorMessages,
		Model:   "claude-3-5-haiku-20241022",
		BaseURL: "http://proxy.example.com",
	}
	s.currentConfig.CloudProfiles = append(s.currentConfig.CloudProfiles, proxyProfile)
	s.currentConfig.ActiveCloudProfile = "proxy-one"
	// No key stored — but BaseURL is set, so guard must not trigger.

	err := s.rebuildCloud()
	if err != nil {
		t.Fatalf("rebuildCloud with BaseURL but no key should not go absent: %v", err)
	}
	if s.cloudLLMProvider == nil {
		t.Error("cloudLLMProvider should be non-nil when BaseURL carve-out applies")
	}
	// Router should hold a real provider, not AbsentCloudProvider.
	if _, ok := r.last.(*legacymodels.AbsentCloudProvider); ok {
		t.Error("router should NOT hold AbsentCloudProvider when BaseURL carve-out applies")
	}
}

// TestInstallAbsentCloudCoordinatorNilSafe locks in the coordinator-on-failure
// behavior that is observable from tests.  Because s.coordinator is the concrete
// type *loop.ADKCoordinator (which requires real session/validator dependencies
// to construct), the test server always has coordinator == nil.  The nil guard
// inside installAbsentCloud makes the coordinator path a safe no-op; this test
// asserts that no-op is indeed panic-free and that the router + cloudLLMProvider
// receive the absent sentinel — the same value coordinator.SetCloudProvider
// receives in production.
func TestInstallAbsentCloudCoordinatorNilSafe(t *testing.T) {
	s, r := newTestServer()
	if s.coordinator != nil {
		// If this ever fires, a fake coordinator is now constructible and the
		// test should be upgraded to assert coordinator.last as well.
		t.Fatal("expected coordinator to be nil in test server — update this test")
	}

	// Must not panic even with nil coordinator.
	s.installAbsentCloud("test: no key")

	if s.cloudLLMProvider != nil {
		t.Error("cloudLLMProvider should be nil after installAbsentCloud")
	}
	if _, ok := r.last.(*legacymodels.AbsentCloudProvider); !ok {
		t.Errorf("router should hold AbsentCloudProvider after installAbsentCloud, got %T", r.last)
	}
}
