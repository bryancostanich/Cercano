package server

import (
	"context"
	"testing"

	"cercano/source/server/internal/agent"
	"cercano/source/server/internal/cloudfactory"
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
	last agent.TurnRunner
}

func (f *fakeRouter) SetOpenProvider(p agent.TurnRunner)  {}
func (f *fakeRouter) SetCloudProvider(p agent.TurnRunner) { f.last = p }
func (f *fakeRouter) Tiers() agent.Tiers                  { return agent.Tiers{} }

func newTestServer() (*Server, *fakeRouter) {
	r := &fakeRouter{}
	s := NewServer(nil, r, nil, nil, nil)
	s.cfgSvc.Set(config.Config{
		CloudProfiles: []config.CloudProfile{
			{Name: "messages-one", Flavor: "messages", Model: "claude-3-5-haiku-20241022"},
			{Name: "cc-one", Flavor: "chat_completions", Model: "gpt-4o"},
			{Name: "unsup-one", Flavor: "bedrock", Model: "x"},
		},
	})
	s.cfgSvc.SetSecrets(secrets.NewMemory())
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
	if err := s.cfgSvc.Secrets().Set("messages-one", "sk-test"); err != nil {
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

func TestSetActiveCloudProfileCancelsInFlightTurns(t *testing.T) {
	s, _ := newTestServer()
	s.cfgSvc.Set(config.Config{
		ActiveCloudProfile: "claude",
		BackupCloudProfile: "openai-responses",
		CloudProfiles: []config.CloudProfile{
			{Name: "claude", Flavor: "messages", Route: "subscription", Model: "claude-opus-5-0", ModelPinned: true},
			{Name: "openai-responses", Flavor: "responses", Route: "chatgpt", Model: "gpt-5.5", ModelPinned: true},
		},
	})
	if err := s.cfgSvc.Secrets().Set("openai-responses", "sk-test"); err != nil {
		t.Fatal(err)
	}
	turnCtx, gen, release := s.turnBroker.BeginTurn(context.Background(), "conv-switch")
	defer release()
	if !s.turnBroker.IsCurrent("conv-switch", gen) {
		t.Fatal("test turn should be current before profile switch")
	}

	resp, err := s.SetActiveCloudProfile(context.Background(), &proto.SetActiveCloudProfileRequest{Name: "openai-responses"})
	if err != nil {
		t.Fatalf("SetActiveCloudProfile: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("want Ok=true, got false: %s", resp.Error)
	}
	select {
	case <-turnCtx.Done():
	default:
		t.Fatal("profile switch should cancel in-flight turns")
	}
	if s.turnBroker.IsCurrent("conv-switch", gen) {
		t.Fatal("profile switch should fence stale turn generation")
	}
}

func TestSetActiveCloudProfileSwapsCurrentBackupToPreviousActive(t *testing.T) {
	s, _ := newTestServer()
	s.cfgSvc.Set(config.Config{
		ActiveCloudProfile: "openai-responses",
		BackupCloudProfile: "claude",
		CloudProfiles: []config.CloudProfile{
			{Name: "openai-responses", Flavor: "responses", Route: "chatgpt", Model: "gpt-5.5", ModelPinned: true},
			{Name: "claude", Flavor: "messages", Route: "subscription", Model: "claude-opus-5-0", ModelPinned: true},
		},
	})
	if err := s.cfgSvc.Secrets().Set("claude", "sk-test"); err != nil {
		t.Fatal(err)
	}

	resp, err := s.SetActiveCloudProfile(context.Background(), &proto.SetActiveCloudProfileRequest{Name: "claude"})
	if err != nil {
		t.Fatalf("SetActiveCloudProfile: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("want Ok=true, got false: %s", resp.Error)
	}
	cfg := s.cfgSvc.Get()
	if cfg.ActiveCloudProfile != "claude" {
		t.Fatalf("active = %q, want claude", cfg.ActiveCloudProfile)
	}
	if cfg.BackupCloudProfile != "openai-responses" {
		t.Fatalf("backup = %q, want previous active openai-responses", cfg.BackupCloudProfile)
	}
}

func TestSetActiveCloudProfileMessagesOk(t *testing.T) {
	s, _ := newTestServer()
	// Set a key so rebuildCloud can succeed.
	if err := s.cfgSvc.Secrets().Set("messages-one", "sk-test"); err != nil {
		t.Fatal(err)
	}
	resp, err := s.SetActiveCloudProfile(context.Background(), &proto.SetActiveCloudProfileRequest{Name: "messages-one"})
	if err != nil {
		t.Fatalf("SetActiveCloudProfile: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("want Ok=true, got false: %s", resp.Error)
	}
	if s.CloudLLMProvider() == nil {
		t.Error("cloudLLMProvider should be non-nil after successful rebuildCloud")
	}
}

func TestSetActiveCloudProfileUnsupportedFlavorGoesAbsent(t *testing.T) {
	s, r := newTestServer()
	if err := s.cfgSvc.Secrets().Set("unsup-one", "sk-test"); err != nil {
		t.Fatal(err)
	}
	resp, err := s.SetActiveCloudProfile(context.Background(), &proto.SetActiveCloudProfileRequest{Name: "unsup-one"})
	if err != nil {
		t.Fatalf("SetActiveCloudProfile: %v", err)
	}
	if resp.Ok {
		t.Error("want Ok=false for unsupported flavor bedrock")
	}
	if s.CloudLLMProvider() != nil {
		t.Error("cloudLLMProvider should be nil (cleared) on build failure")
	}
	if _, ok := r.last.(*agent.AbsentCloudProvider); !ok {
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
	s.cfgSvc.Mutate(func(c *config.Config) {
		c.ActiveCloudProfile = "messages-one"
	})
	// No key stored in memory store — secrets.Get will fail.

	err := s.rebuildCloud()
	if err == nil {
		t.Fatal("want error from rebuildCloud with no key and no BaseURL")
	}
	if s.CloudLLMProvider() != nil {
		t.Error("cloudLLMProvider should be nil (absent) when no key")
	}
	if _, ok := r.last.(*agent.AbsentCloudProvider); !ok {
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
	s.cfgSvc.Mutate(func(c *config.Config) {
		c.CloudProfiles = append(c.CloudProfiles, proxyProfile)
		c.ActiveCloudProfile = "proxy-one"
	})
	// No key stored — but BaseURL is set, so guard must not trigger.

	err := s.rebuildCloud()
	if err != nil {
		t.Fatalf("rebuildCloud with BaseURL but no key should not go absent: %v", err)
	}
	if s.CloudLLMProvider() == nil {
		t.Error("cloudLLMProvider should be non-nil when BaseURL carve-out applies")
	}
	// Router should hold a real provider, not AbsentCloudProvider.
	if _, ok := r.last.(*agent.AbsentCloudProvider); ok {
		t.Error("router should NOT hold AbsentCloudProvider when BaseURL carve-out applies")
	}
}

// TestInstallAbsentCloudCoordinatorNilSafe locks in the coordinator-on-failure
// behavior that is observable from tests.  Because the coordinator is the
// concrete type *loop.ADKCoordinator (which requires real session/validator
// dependencies to construct), the test server always has coordinator == nil
// (passed as nil to NewServer → providers.New). The nil guard inside
// installAbsentCloud makes the coordinator path a safe no-op; this test
// asserts that no-op is indeed panic-free and that the router + CloudLLMProvider
// receive the absent sentinel — the same value coordinator.SetCloudProvider
// receives in production.
func TestInstallAbsentCloudCoordinatorNilSafe(t *testing.T) {
	s, r := newTestServer()
	// coordinator is always nil in newTestServer (passed as nil to NewServer).

	// Must not panic even with nil coordinator.
	s.installAbsentCloud("test: no key")

	if s.CloudLLMProvider() != nil {
		t.Error("cloudLLMProvider should be nil after installAbsentCloud")
	}
	if _, ok := r.last.(*agent.AbsentCloudProvider); !ok {
		t.Errorf("router should hold AbsentCloudProvider after installAbsentCloud, got %T", r.last)
	}
}

func TestUpsertCloudProfileCreatesAndUpdates(t *testing.T) {
	s, _ := newTestServer()
	// Create a new profile.
	resp, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "openai", Flavor: "chat_completions", Backend: "openai",
		BaseUrl: "https://api.openai.com/v1", Model: "gpt-x",
	})
	if err != nil {
		t.Fatalf("UpsertCloudProfile: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("want Ok, got error: %s", resp.Error)
	}
	c := s.cfgSvc.Get()
	p, ok := profileByName(c.CloudProfiles, "openai")
	if !ok {
		t.Fatal("profile openai was not added")
	}
	if p.Backend != "openai" || p.BaseURL != "https://api.openai.com/v1" || p.Model != "gpt-x" {
		t.Fatalf("created profile wrong: %+v", p)
	}
	// Update the same name in place (no duplicate row).
	if _, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "openai", Flavor: "chat_completions", Backend: "openai",
		BaseUrl: "https://api.openai.com/v1", Model: "gpt-y",
	}); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, pr := range s.cfgSvc.Get().CloudProfiles {
		if pr.Name == "openai" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("update should not duplicate; got %d openai rows", count)
	}
	p2, _ := profileByName(s.cfgSvc.Get().CloudProfiles, "openai")
	if p2.Model != "gpt-y" {
		t.Fatalf("update did not change model: %+v", p2)
	}
}

func TestUpsertCloudProfileRoute(t *testing.T) {
	s, _ := newTestServer()
	// Create a meridian-routed profile (the setup wizard's meridian path).
	resp, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "anthropic", Flavor: "messages", BaseUrl: "http://127.0.0.1:3456", Route: "meridian",
	})
	if err != nil {
		t.Fatalf("UpsertCloudProfile: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("want Ok, got error: %s", resp.Error)
	}
	p, ok := profileByName(s.cfgSvc.Get().CloudProfiles, "anthropic")
	if !ok {
		t.Fatal("profile anthropic was not added")
	}
	if p.Route != "meridian" {
		t.Fatalf("route dropped on create: %+v", p)
	}
	// A metadata update that omits route must preserve the existing one —
	// clients that don't know about routes can't demote meridian to direct.
	if _, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "anthropic", Flavor: "messages", BaseUrl: "http://127.0.0.1:3456", Model: "claude-fable-5",
	}); err != nil {
		t.Fatal(err)
	}
	p2, _ := profileByName(s.cfgSvc.Get().CloudProfiles, "anthropic")
	if p2.Route != "meridian" {
		t.Fatalf("route lost on routeless update: %+v", p2)
	}
	if p2.Model != "claude-fable-5" {
		t.Fatalf("update did not apply: %+v", p2)
	}
	// An explicit route replaces the existing one.
	if _, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "anthropic", Flavor: "messages", BaseUrl: "", Route: "direct",
	}); err != nil {
		t.Fatal(err)
	}
	p3, _ := profileByName(s.cfgSvc.Get().CloudProfiles, "anthropic")
	if p3.Route != "direct" {
		t.Fatalf("explicit route not applied: %+v", p3)
	}
}

func TestUpsertCloudProfileModelPreservedOnEmptyUpdate(t *testing.T) {
	s, _ := newTestServer()
	// A metadata update that omits the model must preserve the existing one —
	// the wizard's meridian/key commits send no model, and a modelless active
	// profile means an empty model on every cloud request (and no header chip).
	if _, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "messages-one", Flavor: "messages", BaseUrl: "http://127.0.0.1:3456", Route: "meridian",
	}); err != nil {
		t.Fatal(err)
	}
	p, _ := profileByName(s.cfgSvc.Get().CloudProfiles, "messages-one")
	if p.Model != "claude-3-5-haiku-20241022" {
		t.Fatalf("model lost on modelless update: %+v", p)
	}
	// An explicit model still replaces the existing one.
	if _, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "messages-one", Flavor: "messages", Model: "claude-fable-5",
	}); err != nil {
		t.Fatal(err)
	}
	p2, _ := profileByName(s.cfgSvc.Get().CloudProfiles, "messages-one")
	if p2.Model != "claude-fable-5" {
		t.Fatalf("explicit model not applied: %+v", p2)
	}
}

func TestUpsertCloudProfileRebuildsActiveProvider(t *testing.T) {
	s, _ := newTestServer()
	// Set a key so rebuildCloud can succeed for the messages flavor.
	if err := s.cfgSvc.Secrets().Set("messages-one", "sk-test"); err != nil {
		t.Fatal(err)
	}
	// Make messages-one the active profile and trigger an initial rebuild.
	s.cfgSvc.Mutate(func(c *config.Config) {
		c.ActiveCloudProfile = "messages-one"
	})
	if err := s.rebuildCloud(); err != nil {
		t.Fatalf("initial rebuildCloud: %v", err)
	}
	if s.CloudLLMProvider() == nil {
		t.Fatal("cloudLLMProvider should be non-nil after initial rebuild")
	}
	// Upsert the active profile with a new model.
	resp, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "messages-one", Flavor: "messages", Model: "claude-opus-5",
	})
	if err != nil {
		t.Fatalf("UpsertCloudProfile: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("want Ok, got: %s", resp.Error)
	}
	// rebuildCloud sets cfgSvc CloudModel from the active profile's Model.
	if s.CloudLLMProvider() == nil {
		t.Error("cloudLLMProvider should be non-nil after upsert of active profile")
	}
	if s.cfgSvc.Get().CloudModel != "claude-opus-5" {
		t.Errorf("CloudModel = %q after rebuild, want claude-opus-5", s.cfgSvc.Get().CloudModel)
	}
}

func TestUpsertCloudProfileValidation(t *testing.T) {
	s, _ := newTestServer()
	// Empty name rejected.
	if resp, _ := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "", Flavor: "messages",
	}); resp.Ok {
		t.Error("empty name should be rejected")
	}
	// chat_completions requires a base_url.
	if resp, _ := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "x", Flavor: "chat_completions", BaseUrl: "",
	}); resp.Ok {
		t.Error("chat_completions without base_url should be rejected")
	}
	// Unknown flavor rejected.
	if resp, _ := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "x", Flavor: "not_a_flavor",
	}); resp.Ok {
		t.Error("unknown flavor should be rejected")
	}
}

func TestRemoveCloudProfile(t *testing.T) {
	s, r := newTestServer()
	// Seed a key for messages-one so we can verify deletion.
	if err := s.cfgSvc.Secrets().Set("messages-one", "sk-test"); err != nil {
		t.Fatal(err)
	}
	s.cfgSvc.Mutate(func(c *config.Config) {
		c.ActiveCloudProfile = "messages-one"
	})
	resp, err := s.RemoveCloudProfile(context.Background(), &proto.RemoveCloudProfileRequest{Name: "messages-one"})
	if err != nil {
		t.Fatalf("RemoveCloudProfile: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("want Ok, got: %s", resp.Error)
	}
	c := s.cfgSvc.Get()
	if _, ok := profileByName(c.CloudProfiles, "messages-one"); ok {
		t.Error("profile should be gone")
	}
	if _, err := s.cfgSvc.Secrets().Get("messages-one"); err == nil {
		t.Error("key should be deleted from keychain")
	}
	if c.ActiveCloudProfile != "" {
		t.Errorf("active should be cleared, got %q", c.ActiveCloudProfile)
	}
	if s.CloudLLMProvider() != nil {
		t.Error("cloudLLMProvider should be nil after removing active profile")
	}
	if _, ok := r.last.(*agent.AbsentCloudProvider); !ok {
		t.Errorf("router should hold AbsentCloudProvider after removing active profile, got %T", r.last)
	}
}

func TestRemoveCloudProfileNonexistent(t *testing.T) {
	s, _ := newTestServer()
	resp, err := s.RemoveCloudProfile(context.Background(), &proto.RemoveCloudProfileRequest{Name: "no-such"})
	if err != nil {
		t.Fatalf("RemoveCloudProfile: %v", err)
	}
	if resp.Ok {
		t.Error("want Ok=false for nonexistent profile")
	}
}

func TestGetCloudProfilesReportsBackend(t *testing.T) {
	s, _ := newTestServer()
	s.cfgSvc.Mutate(func(c *config.Config) {
		c.CloudProfiles = append(c.CloudProfiles,
			config.CloudProfile{Name: "g", Flavor: "chat_completions", Backend: "gemini", BaseURL: "u", Model: "m"})
	})
	resp, _ := s.GetCloudProfiles(context.Background(), &proto.GetCloudProfilesRequest{})
	var found bool
	for _, p := range resp.Profiles {
		if p.Name == "g" {
			found = true
			if p.Backend != "gemini" {
				t.Errorf("Backend = %q, want gemini", p.Backend)
			}
		}
	}
	if !found {
		t.Fatal("profile g not returned")
	}
}

// UpdateConfig with cloud_model must update the active profile's model —
// the profile is the source of truth, and any stale-mirror bug like the one
// at #commit-pre-c99faa3 (cloud_model edited, active profile untouched,
// requests still went to the old model) lives in this seam. Guards the
// SoT switch.
func TestUpdateConfig_CloudModel_UpdatesActiveProfile(t *testing.T) {
	s, _ := newTestServer()
	s.cfgSvc.Mutate(func(c *config.Config) {
		c.ActiveCloudProfile = "messages-one"
	})
	if err := s.cfgSvc.Secrets().Set("messages-one", "sk-test"); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	s.events = newEventHub()
	ch, unsub := s.events.subscribe()
	defer unsub()

	resp, err := s.UpdateConfig(t.Context(), &proto.UpdateConfigRequest{
		CloudModel: "claude-opus-5-0",
	})
	if err != nil || !resp.Success {
		t.Fatalf("UpdateConfig: err=%v resp=%+v", err, resp)
	}

	if got := s.activeCloudModel(); got != "claude-opus-5-0" {
		t.Errorf("activeCloudModel() = %q, want claude-opus-5-0", got)
	}
	p, ok := s.activeProfile()
	if !ok || p.Model != "claude-opus-5-0" {
		t.Errorf("active profile model = %q (ok=%v), want claude-opus-5-0", p.Model, ok)
	}

	select {
	case ev := <-ch:
		if ev.GetConfigChanged() == nil {
			t.Errorf("expected ConfigChanged event, got %T", ev.Event)
		}
	default:
		// Broadcast happens after the rebuild block but before the early
		// return; if it's missing the watcher loop won't drive UI updates.
		t.Errorf("expected ConfigChanged broadcast for cloud_model, none received")
	}
}

func TestSetActiveCloudProfileBedrockKeylessOk(t *testing.T) {
	s, r := newTestServer()
	s.cfgSvc.Mutate(func(c *config.Config) {
		c.CloudProfiles = append(c.CloudProfiles,
			config.CloudProfile{Name: "bedrock-one", Flavor: "bedrock", Region: "us-east-1", Model: "anthropic.claude-x"})
	})
	// No key set for bedrock-one — the keyless guard must NOT send it to absent.
	resp, err := s.SetActiveCloudProfile(context.Background(), &proto.SetActiveCloudProfileRequest{Name: "bedrock-one"})
	if err != nil {
		t.Fatalf("SetActiveCloudProfile: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("want Ok=true for keyless bedrock (creds via AWS chain), got false: %s", resp.Error)
	}
	if _, absent := r.last.(*agent.AbsentCloudProvider); absent {
		t.Error("keyless bedrock should NOT install the absent provider")
	}
}

// TestUpsertCloudProfile_ActiveBroadcastsCloudModel verifies that editing the
// ACTIVE profile's model pushes a cloud_model ConfigChanged event, so the CLI
// header chip updates live instead of showing the stale model.
func TestUpsertCloudProfile_ActiveBroadcastsCloudModel(t *testing.T) {
	s, _ := newTestServer()
	if err := s.cfgSvc.Secrets().Set("messages-one", "sk-test"); err != nil {
		t.Fatal(err)
	}
	s.cfgSvc.Mutate(func(c *config.Config) {
		c.ActiveCloudProfile = "messages-one"
	})
	if err := s.rebuildCloud(); err != nil {
		t.Fatalf("initial rebuildCloud: %v", err)
	}
	s.events = newEventHub()
	ch, unsub := s.events.subscribe()
	defer unsub()

	resp, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "messages-one", Flavor: "messages", Model: "claude-fable-5",
	})
	if err != nil || !resp.Ok {
		t.Fatalf("UpsertCloudProfile: err=%v resp=%+v", err, resp)
	}

	select {
	case ev := <-ch:
		cc := ev.GetConfigChanged()
		if cc == nil {
			t.Fatalf("expected ConfigChanged event, got %T", ev.Event)
		}
		if cc.Field != "cloud_model" || cc.Value != "claude-fable-5" {
			t.Errorf("event = %s/%s, want cloud_model/claude-fable-5", cc.Field, cc.Value)
		}
	default:
		t.Error("expected a cloud_model ConfigChanged broadcast for active-profile upsert, got none")
	}
}

// TestUpsertCloudProfile_InactiveDoesNotBroadcast pins the inverse: editing a
// non-active profile must not touch the chip.
func TestUpsertCloudProfile_InactiveDoesNotBroadcast(t *testing.T) {
	s, _ := newTestServer()
	s.cfgSvc.Mutate(func(c *config.Config) {
		c.ActiveCloudProfile = "messages-one"
	})
	s.events = newEventHub()
	ch, unsub := s.events.subscribe()
	defer unsub()

	resp, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "other-profile", Flavor: "messages", Model: "claude-fable-5",
	})
	if err != nil || !resp.Ok {
		t.Fatalf("UpsertCloudProfile: err=%v resp=%+v", err, resp)
	}

	select {
	case ev := <-ch:
		if ev.GetConfigChanged() != nil {
			t.Errorf("unexpected ConfigChanged broadcast for non-active profile: %+v", ev.GetConfigChanged())
		}
	default:
		// good — no broadcast
	}
}
