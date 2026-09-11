package worker

import (
	"cercano/source/server/internal/cloudfactory"
	pkgcfg "cercano/source/server/pkg/config"
	"context"
	"errors"
	"testing"
)

type unavailableCredentials struct{ calls int }

func (c *unavailableCredentials) Fetch(context.Context, string) (string, string, error) {
	c.calls++
	return "", "", errors.New("login missing")
}
func TestSubscriptionBackupDoesNotFetchCredentialsDuringBuild(t *testing.T) {
	for _, profile := range []pkgcfg.CloudProfile{
		{Name: "backup", Flavor: cloudfactory.FlavorMessages, Route: cloudfactory.RouteSubscription, Model: "claude-test", ModelPinned: true},
		{Name: "backup", Flavor: cloudfactory.FlavorResponses, Route: cloudfactory.RouteChatGPT, Model: "gpt-test", ModelPinned: true},
	} {
		t.Run(profile.Route, func(t *testing.T) {
			cfg := pkgcfg.Defaults()
			cfg.ActiveCloudProfile = "primary"
			cfg.BackupCloudProfile = "backup"
			cfg.CloudProfiles = []pkgcfg.CloudProfile{profile}
			creds := &unavailableCredentials{}
			provider, _, ok := buildWorkerBackup(context.Background(), "primary", cfg, creds, nil)
			if !ok || provider == nil || creds.calls != 0 {
				t.Fatalf("backup built=%v credential calls=%d", ok, creds.calls)
			}
		})
	}
}
