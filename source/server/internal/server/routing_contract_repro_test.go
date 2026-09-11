package server

import (
	"context"
	"testing"

	"cercano/source/server/internal/inference"
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

func TestReferencedCredentialAndRemovalRefresh(t *testing.T) {
	for _, operation := range []string{"key", "remove"} {
		t.Run(operation, func(t *testing.T) {
			s, _ := newTestServer()
			c := config.Defaults()
			c.ActiveCloudProfile = "p"
			c.BackupCloudProfile = "b"
			c.CloudProfiles = []config.CloudProfile{{Name: "p", Flavor: "messages"}, {Name: "b", Flavor: "messages"}}
			s.cfgSvc.Set(c)
			s.cfgSvc.Secrets().Set("p", "fixture-p")
			s.cfgSvc.Secrets().Set("b", "fixture-b")
			if err := s.rebuildCloud(); err != nil {
				t.Fatal(err)
			}
			before := s.providerSvc.Cloud()
			if operation == "key" {
				resp, err := s.SetCloudProfileKey(context.Background(), &proto.SetCloudProfileKeyRequest{Name: "b", ApiKey: "new-fixture"})
				if err != nil || !resp.Ok {
					t.Fatal(resp, err)
				}
			} else {
				resp, err := s.RemoveCloudProfile(context.Background(), &proto.RemoveCloudProfileRequest{Name: "b"})
				if err != nil || !resp.Ok {
					t.Fatal(resp, err)
				}
			}
			if s.providerSvc.Cloud() == before {
				t.Fatal("referenced provider retained stale credentials/removed backup")
			}
		})
	}
}

func TestCompleteStructuralDraftCanClearFields(t *testing.T) {
	s, _ := newTestServer()
	s.cfgSvc.Set(config.Config{CloudProfiles: []config.CloudProfile{{Name: "p", Flavor: "messages", Backend: "old", BaseURL: "https://old.invalid", Route: "direct", Region: "region", AWSProfile: "aws"}}})
	response, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{Name: "p", Structure: &proto.CloudProfileStructure{Flavor: "messages", Region: "region", AwsProfile: "aws"}})
	if err != nil || !response.Ok {
		t.Fatal(response, err)
	}
	p := s.cfgSvc.Get().CloudProfiles[0]
	if p.BaseURL != "" || p.Backend != "" || p.Route != "" || p.Region != "region" || p.AWSProfile != "aws" {
		t.Fatalf("complete structural draft not applied: %+v", p)
	}
}

func TestProviderCandidatesRetainBuiltProfileIdentity(t *testing.T) {
	s, _ := newTestServer()
	c := config.Defaults()
	c.CloudProfiles = []config.CloudProfile{{Name: "p", Flavor: "messages"}, {Name: "b", Flavor: "messages"}}
	c.ActiveCloudProfile = "p"
	s.cfgSvc.Set(c)
	s.cfgSvc.Secrets().Set("p", "fixture-p")
	s.cfgSvc.Secrets().Set("b", "fixture-b")
	if err := s.rebuildCloud(); err != nil {
		t.Fatal(err)
	}
	c.ActiveCloudProfile = "b"
	s.cfgSvc.Set(c)
	candidates := s.providerSvc.Candidates()
	route := inference.TargetFor(candidates.Cloud, "", "most_capable")
	if candidates.Destinations[config.DestinationPrimary].Profile != route.Profile {
		t.Fatalf("new config identity paired with old provider: candidate=%s actual=%s", candidates.Destinations[config.DestinationPrimary].Profile, route.Profile)
	}
	if err := s.rebuildCloud(); err != nil {
		t.Fatal(err)
	}
	if got := s.providerSvc.Candidates().Destinations[config.DestinationPrimary].Profile; got != "b" {
		t.Fatalf("rebuild did not install new identity: %q", got)
	}
}

func TestProviderGraphRetainsTaskAssignmentsUntilRebuild(t *testing.T) {
	s, _ := newTestServer()
	c := config.Defaults()
	c.LocusMode = "cloud_only"
	c.CloudProfiles = []config.CloudProfile{{Name: "p", Flavor: "messages"}, {Name: "b", Flavor: "messages"}}
	c.ActiveCloudProfile = "p"
	c.SecondaryCloudProfile = "p"
	s.cfgSvc.Set(c)
	s.cfgSvc.Secrets().Set("p", "fixture-p")
	s.cfgSvc.Secrets().Set("b", "fixture-b")
	if err := s.rebuildCloud(); err != nil {
		t.Fatal(err)
	}
	c.ActiveCloudProfile = "b"
	c.SecondaryCloudProfile = "b"
	c.TaskAssignments = map[config.Task]config.TaskAssignment{config.TaskChat: {Quality: config.CostEconomy}, config.TaskDispatch: {Quality: config.CostEconomy}}
	s.cfgSvc.Set(c)
	candidates := s.providerSvc.Candidates()
	if candidates.TaskFor(config.TaskDispatch).Quality != config.CostPremium {
		t.Fatal("unbuilt task configuration leaked into provider graph")
	}
	main, _, _, err := s.providerSvc.Main()
	if err != nil {
		t.Fatal(err)
	}
	assignment, ok := inference.TaskAssignmentFor(main, config.TaskChat)
	if !ok || assignment.Quality != config.CostPremium {
		t.Fatal("main quality detached from selected provider")
	}
	model, ok := inference.TaskModelFor(main)
	if !ok || model != c.ModelProfiles.ResolveCloudModelForTier(c.CloudProfiles[0], config.TierMostCapable) {
		t.Fatalf("main model detached: %q", model)
	}
	if err := s.rebuildCloud(); err != nil {
		t.Fatal(err)
	}
	if s.providerSvc.Candidates().TaskFor(config.TaskDispatch).Quality != config.CostEconomy {
		t.Fatal("new assignments not installed")
	}
}

func TestProviderGraphConcurrentReadAndRebuild(t *testing.T) {
	s, _ := newTestServer()
	c := config.Defaults()
	c.ActiveCloudProfile = "p"
	c.CloudProfiles = []config.CloudProfile{{Name: "p", Flavor: "messages"}}
	s.cfgSvc.Set(c)
	s.cfgSvc.Secrets().Set("p", "fixture")
	if err := s.rebuildCloud(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 20; i++ {
			if err := s.rebuildCloud(); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 200; i++ {
		candidates := s.providerSvc.Candidates()
		if candidates.Cloud == nil {
			t.Fatal("partially published provider graph")
		}
		_ = s.providerSvc.CloudLLMProvider()
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
