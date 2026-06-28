package server

import (
	"context"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/legacymodels"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

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
	if len(resp.Profiles) != 2 {
		t.Fatalf("want 2 profiles, got %d", len(resp.Profiles))
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
	if err := s.secrets.Set("cc-one", "sk-test"); err != nil {
		t.Fatal(err)
	}
	resp, err := s.SetActiveCloudProfile(context.Background(), &proto.SetActiveCloudProfileRequest{Name: "cc-one"})
	if err != nil {
		t.Fatalf("SetActiveCloudProfile: %v", err)
	}
	if resp.Ok {
		t.Error("want Ok=false for unsupported flavor chat_completions")
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
