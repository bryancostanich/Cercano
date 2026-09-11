package server

import (
	"context"
	"testing"

	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// A model-only edit must not erase provider identity or AWS authentication metadata.
func TestRoutingContractProfileEditPreservesIdentity(t *testing.T) {
	s, _ := newTestServer()
	original := config.CloudProfile{Name: "aws", Flavor: "bedrock", Provider: "anthropic", Route: "direct", Region: "us-west-2", AWSProfile: "fixture", Model: "old"}
	s.cfgSvc.Set(config.Config{CloudProfiles: []config.CloudProfile{original}})
	resp, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{Name: "aws", Flavor: "bedrock", Model: "new"})
	if err != nil || !resp.GetOk() {
		t.Fatalf("upsert: %v, %v", resp, err)
	}
	got := s.cfgSvc.Get().CloudProfiles[0]
	t.Logf("profile after edit: %+v", got)
	if got.Region != original.Region || got.AWSProfile != original.AWSProfile || got.Provider != original.Provider || got.Route != original.Route {
		t.Errorf("identity changed: got %+v; original %+v", got, original)
	}
}

// The obsolete model field must not survive a new profile mutation.
func TestRoutingContractEmptyModelRetiresPin(t *testing.T) {
	s, _ := newTestServer()
	s.cfgSvc.Set(config.Config{CloudProfiles: []config.CloudProfile{{Name: "fixture", Flavor: "messages", Model: "legacy", ModelPinned: true}}})
	resp, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{Name: "fixture", Flavor: "messages"})
	if err != nil || !resp.GetOk() {
		t.Fatalf("upsert: %v, %v", resp, err)
	}
	got := s.cfgSvc.Get().CloudProfiles[0]
	t.Logf("empty model upsert: model=%q pinned=%v", got.Model, got.ModelPinned)
	if got.Model != "" || got.ModelPinned {
		t.Fatalf("baseline changed: %+v", got)
	}
}

// The provider chain captures a config snapshot. Editing a referenced backup
// must rebuild that chain even when the active profile itself is untouched.
func TestRoutingContractBackupEditRefreshesProviders(t *testing.T) {
	s, _ := newTestServer()
	c := config.Defaults()
	c.ActiveCloudProfile, c.BackupCloudProfile = "primary", "backup"
	c.CloudProfiles = []config.CloudProfile{{Name: "primary", Flavor: "messages"}, {Name: "backup", Flavor: "messages"}}
	s.cfgSvc.Set(c)
	s.cfgSvc.Secrets().Set("primary", "fixture-key")
	s.cfgSvc.Secrets().Set("backup", "fixture-key")
	if err := s.providerSvc.Rebuild(); err != nil {
		t.Fatal(err)
	}
	before := s.providerSvc.CloudLLMProvider()
	resp, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{Name: "backup", Flavor: "messages", BaseUrl: "https://example.invalid"})
	if err != nil || !resp.GetOk() {
		t.Fatalf("upsert: %v, %v", resp, err)
	}
	after := s.providerSvc.CloudLLMProvider()
	t.Logf("chain before=%p after=%p", before, after)
	if before == after {
		t.Error("backup edit retained stale provider chain")
	}
	key, err := s.cfgSvc.Secrets().Get("backup")
	if err != nil || key != "fixture-key" {
		t.Fatal("backup credential identity changed")
	}
}

func TestRoutingContractChoicePresenceAndClear(t *testing.T) {
	s, _ := newTestServer()
	original := config.CloudProfile{Name: "custom", Flavor: "chat_completions", Backend: "openai", BaseURL: "https://example.invalid", Provider: "openai", TierOverrides: map[config.CostTier]string{config.CostPremium: "text"}, ImageModel: "image"}
	s.cfgSvc.Set(config.Config{CloudProfiles: []config.CloudProfile{original}})
	for _, choices := range []*proto.ProfileModelChoices{nil, {}} {
		resp, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{Name: "custom", ModelChoices: choices})
		if err != nil || !resp.GetOk() {
			t.Fatalf("%v %v", resp, err)
		}
		got := s.cfgSvc.Get().CloudProfiles[0]
		if got.Backend != original.Backend || got.Provider != original.Provider || got.BaseURL != original.BaseURL {
			t.Fatalf("metadata lost: %+v", got)
		}
		if choices == nil {
			if got.ImageModel != "image" || got.TierOverrides[config.CostPremium] != "text" {
				t.Fatal("omission changed choices")
			}
		} else if got.ImageModel != "" || len(got.TierOverrides) != 0 {
			t.Fatal("clear did not remove choices")
		}
	}
}
