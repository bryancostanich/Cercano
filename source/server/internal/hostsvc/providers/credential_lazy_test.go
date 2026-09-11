package providers

import (
	"cercano/source/server/internal/cloudfactory"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/config"
	"context"
	"errors"
	"testing"
)

type observedStore struct {
	secrets.Store
	reads int
}

func (s *observedStore) Get(name string) (string, error) { s.reads++; return s.Store.Get(name) }
func TestSubscriptionConstructionIsLazyAndKeepsMissingLogin(t *testing.T) {
	for _, profile := range []config.CloudProfile{
		{Name: "named", Flavor: cloudfactory.FlavorMessages, Route: cloudfactory.RouteSubscription, Model: "claude-test", ModelPinned: true},
		{Name: "named", Flavor: cloudfactory.FlavorResponses, Route: cloudfactory.RouteChatGPT, Model: "gpt-test", ModelPinned: true},
	} {
		t.Run(profile.Route, func(t *testing.T) {
			store := &observedStore{Store: secrets.NewMemory()}
			cfg := config.Defaults()
			cfg.ActiveCloudProfile = "named"
			cfg.CloudProfiles = []config.CloudProfile{profile}
			owner := cfgsvc.New("", cfg, store)
			svc := &service{cfgSvc: owner, router: &recordingRouter{}}
			if err := svc.rebuildCloud(); err != nil {
				t.Fatal(err)
			}
			firstOwner := owner.Credentials()
			if err := svc.rebuildCloud(); err != nil {
				t.Fatal(err)
			}
			if store.reads != 0 || owner.Credentials() != firstOwner {
				t.Fatal("construction read credentials or replaced their owner")
			}
			cfg.BackupCloudProfile = "named"
			if _, _, ok := svc.buildBackup("primary", cfg); !ok || store.reads != 0 {
				t.Fatal("missing-login backup removed or eagerly read")
			}
			_, err := svc.CloudLLMProvider().Chat(context.Background(), inference.Call{Model: profile.Model, Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "hello"}}}}})
			var credential *llm.CredentialError
			if llm.ClassOf(err) != llm.ErrLoginRequired || !errors.As(err, &credential) || credential.Profile != "named" {
				t.Fatalf("missing login not actionable: %v", err)
			}
		})
	}
}
