package server

import (
	"cercano/source/server/pkg/accountidentity"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
	"context"
	"testing"
)

func TestAccountIdentitySettingsAndAuthPathChange(t *testing.T) {
	s, _ := newTestServer()
	c := config.Defaults()
	c.CloudProfiles = []config.CloudProfile{{Name: "claude-work", Flavor: "messages", Route: "subscription", AccountIdentity: accountidentity.Identity{Email: "work@example.com", Name: "Work"}}, {Name: "chatgpt", Flavor: "responses", Route: "chatgpt", AccountIdentity: accountidentity.Identity{Email: "personal@example.com"}}}
	s.cfgSvc.Set(c)
	response, err := s.GetCloudProviders(context.Background(), &proto.GetCloudProvidersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, provider := range response.Providers {
		for _, p := range provider.Profiles {
			found[p.Name] = p.AccountEmail
		}
	}
	if found["claude-work"] != "work@example.com" || found["chatgpt"] != "personal@example.com" {
		t.Fatal("settings lost account identities", found)
	}
	saved, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{Name: "claude-work", ModelChoices: &proto.ProfileModelChoices{ImageModel: "model"}})
	if err != nil || !saved.Ok {
		t.Fatal("save failed", err, saved)
	}
	got, _ := s.cfgSvc.Get().Profile("claude-work")
	if got.AccountIdentity.Email != "work@example.com" {
		t.Fatal("model edit lost identity")
	}
	saved, err = s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{Name: "claude-work", Structure: &proto.CloudProfileStructure{Flavor: "messages", Route: ""}})
	if err != nil || !saved.Ok {
		t.Fatal("auth path change failed", err, saved)
	}
	got, _ = s.cfgSvc.Get().Profile("claude-work")
	if got.AccountIdentity.Display() != "" {
		t.Fatal("new auth path inherited stale account email")
	}
}
