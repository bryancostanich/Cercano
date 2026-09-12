package config_test

import (
	"context"
	"reflect"
	"testing"

	"cercano/source/server/internal/cloudfactory"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/secrets"
	cfg "cercano/source/server/pkg/config"
)

func TestReauthenticationPreservesEntireConfiguration(t *testing.T) {
	for _, profile := range []cfg.CloudProfile{
		{Name: "named", Flavor: cloudfactory.FlavorMessages, Route: cloudfactory.RouteSubscription, Provider: "anthropic", Model: "custom", ModelPinned: true, BaseURL: "https://proxy.invalid", Region: "region", AWSProfile: "unrelated"},
		{Name: "named", Flavor: cloudfactory.FlavorResponses, Route: cloudfactory.RouteChatGPT, Provider: "openai", Model: "custom", ModelPinned: true, BaseURL: "https://proxy.invalid"},
	} {
		for _, active := range []string{"named", "other"} {
			t.Run(profile.Route+"/"+active, func(t *testing.T) {
				c := cfg.Defaults()
				c.CloudProfiles = []cfg.CloudProfile{profile, {Name: "other"}}
				c.ActiveCloudProfile = active
				c.BackupCloudProfile = "backup"
				service := cfgsvc.New("", c, secrets.NewMemory())
				before := service.Get()
				// Even supplied activation/model settings must not mutate a reauthentication.
				proposed := cfg.CloudProfile{Name: profile.Name, Flavor: profile.Flavor, Route: profile.Route, Model: "unwanted"}
				login, err := service.BeginCloudLogin(context.Background(), proposed, true, true)
				if err != nil {
					t.Fatal(err)
				}
				defer login.Close()
				result, err := login.Commit("new-token")
				if err != nil {
					t.Fatal(err)
				}
				if !result.Reauthenticated || !result.Profile.Equal(profile) || !reflect.DeepEqual(before, service.Get()) {
					t.Fatal("reauthentication changed configuration")
				}
				if raw, _ := service.Secrets().Get(profile.Name); raw != "new-token" {
					t.Fatal("credentials not updated")
				}
			})
		}
	}
}
func TestLoginProfileMutationRejectsCredentialCommit(t *testing.T) {
	c := cfg.Defaults()
	p := cfg.CloudProfile{Name: "work", Flavor: cloudfactory.FlavorMessages, Route: cloudfactory.RouteSubscription, Model: "before"}
	c.CloudProfiles = []cfg.CloudProfile{p}
	service := cfgsvc.New("", c, secrets.NewMemory())
	service.Secrets().Set("work", "old")
	login, err := service.BeginCloudLogin(context.Background(), p, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer login.Close()
	changed := p
	changed.Model = "after"
	service.UpsertProfile(changed)
	if _, err := login.Commit("late"); err == nil {
		t.Fatal("profile change ignored")
	}
	if raw, _ := service.Secrets().Get("work"); raw != "old" {
		t.Fatal("rejected login wrote credentials")
	}
}
func TestReauthenticationRequiresExistingMatchingProfile(t *testing.T) {
	service := cfgsvc.New("", cfg.Defaults(), secrets.NewMemory())
	p := cfg.CloudProfile{Name: "missing", Flavor: cloudfactory.FlavorMessages, Route: cloudfactory.RouteSubscription}
	if _, err := service.BeginCloudLogin(context.Background(), p, false, true); err == nil {
		t.Fatal("created profile during reauth")
	}
	service.UpsertProfile(cfg.CloudProfile{Name: "missing", Flavor: cloudfactory.FlavorMessages, Route: "api"})
	if _, err := service.BeginCloudLogin(context.Background(), p, false, true); err == nil {
		t.Fatal("converted API key profile during reauth")
	}
}
func TestInitialLoginStillCreatesAndActivates(t *testing.T) {
	c := cfg.Defaults()
	c.CloudProfiles = []cfg.CloudProfile{{Name: "previous"}}
	c.ActiveCloudProfile = "previous"
	service := cfgsvc.New("", c, secrets.NewMemory())
	p := cfg.CloudProfile{Name: "new", Flavor: cloudfactory.FlavorResponses, Route: cloudfactory.RouteChatGPT, Model: "chosen", ModelPinned: true}
	login, err := service.BeginCloudLogin(context.Background(), p, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer login.Close()
	result, err := login.Commit("new-token")
	if err != nil {
		t.Fatal(err)
	}
	got := service.Get()
	if !result.Active || result.Reauthenticated || got.ActiveCloudProfile != "new" || got.BackupCloudProfile != "" {
		t.Fatalf("setup behavior changed: %+v", result)
	}
}

func TestRemovedAndRecreatedProfileDoesNotReviveLogin(t *testing.T) {
	c := cfg.Defaults()
	p := cfg.CloudProfile{Name: "work", Flavor: cloudfactory.FlavorMessages, Route: cloudfactory.RouteSubscription}
	c.CloudProfiles = []cfg.CloudProfile{p}
	service := cfgsvc.New("", c, secrets.NewMemory())
	service.Secrets().Set("work", "old")
	login, err := service.BeginCloudLogin(context.Background(), p, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer login.Close()
	service.RemoveProfile("work")
	service.UpsertProfile(p)
	if _, err := login.Commit("obsolete"); err == nil {
		t.Fatal("removed login revived by identical replacement profile")
	}
}

func TestLoginPreservesAndIsolatesSparseProfileChoices(t *testing.T) {
	c := cfg.Defaults()
	profile := cfg.CloudProfile{Name: "named", Flavor: cloudfactory.FlavorMessages, Route: cloudfactory.RouteSubscription, TierOverrides: map[cfg.CostTier]string{cfg.CostPremium: "custom-premium"}, ImageModel: "custom-image", Region: "kept", AWSProfile: "kept-aws"}
	c.CloudProfiles = []cfg.CloudProfile{profile}
	service := cfgsvc.New("", c, secrets.NewMemory())
	login, err := service.BeginCloudLogin(context.Background(), profile, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer login.Close()
	profile.TierOverrides[cfg.CostPremium] = "external-mutation"
	result, err := login.Commit("first-token")
	if err != nil {
		t.Fatal(err)
	}
	if result.Active || result.Profile.TierOverrides[cfg.CostPremium] != "custom-premium" || result.Profile.ImageModel != "custom-image" || result.Profile.AWSProfile != "kept-aws" {
		t.Fatalf("reauth changed choices: %+v", result)
	}
	result.Profile.TierOverrides[cfg.CostPremium] = "result-mutation"
	current := service.Get().CloudProfiles[0]
	if current.TierOverrides[cfg.CostPremium] != "custom-premium" {
		t.Fatal("login result aliases live configuration")
	}
	pending, err := service.BeginCloudLogin(context.Background(), current, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Close()
	if err := service.Mutate(func(c *cfg.Config) { c.CloudProfiles[0].TierOverrides[cfg.CostPremium] = "changed-during-login" }); err != nil {
		t.Fatal(err)
	}
	if _, err := pending.Commit("late-token"); err == nil {
		t.Fatal("quality edit did not invalidate pending login")
	}
	if token, _ := service.Secrets().Get("named"); token != "first-token" {
		t.Fatal("invalidated login overwrote credentials")
	}
}
