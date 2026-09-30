package config_test

import (
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/accountidentity"
	cfg "cercano/source/server/pkg/config"
	"context"
	"path/filepath"
	"testing"
)

func TestSignInIdentityPersistsWithoutChangingAccountKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	p := cfg.CloudProfile{Name: "claude-work", Flavor: "messages", Route: "subscription", TierOverrides: map[cfg.CostTier]string{cfg.CostPremium: "custom-model"}}
	c := cfg.Defaults()
	c.CloudProfiles = []cfg.CloudProfile{p}
	c.ActiveCloudProfile = p.Name
	store := secrets.NewMemory()
	svc := cfgsvc.New(path, c, store)
	signIn := func(identity accountidentity.Identity) {
		t.Helper()
		login, err := svc.BeginCloudLogin(context.Background(), p, false, true)
		if err != nil {
			t.Fatal(err)
		}
		defer login.Close()
		got, err := login.Commit("test-token", identity)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Reauthenticated || got.Profile.Name != p.Name || got.Profile.TierOverrides[cfg.CostPremium] != "custom-model" {
			t.Fatal("changed account settings")
		}
		svc.Persist()
	}
	signIn(accountidentity.Identity{Email: "person@example.com", Name: "Person"})
	loaded, err := cfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := loaded.Profile(p.Name)
	if !ok || got.AccountIdentity.Email != "person@example.com" {
		t.Fatal("identity missing after reload")
	}
	if raw, _ := store.Get(p.Name); raw != "test-token" {
		t.Fatal("credential key changed")
	}
	if raw, _ := store.Get("person@example.com"); raw != "" {
		t.Fatal("email used as credential key")
	}
	signIn(accountidentity.Identity{Email: "different@example.com"})
	got, _ = svc.Get().Profile(p.Name)
	if got.AccountIdentity.Email != "different@example.com" {
		t.Fatal("reauth retained previous account label")
	}
	signIn(accountidentity.Identity{})
	got, _ = svc.Get().Profile(p.Name)
	if got.AccountIdentity.Display() != "" {
		t.Fatal("unknown identity displayed previous sign-in email")
	}
}
