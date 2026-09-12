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
			preferred := profile.Clone()
			preferred.Name = "primary"
			cfg.CloudProfiles = []pkgcfg.CloudProfile{profile, preferred}
			cfg.SecondaryCloudProfile = "primary"
			cfg.SecondaryBackupCloudProfile = "backup"
			creds := &unavailableCredentials{}
			resolver, err := buildWorkerProviders(context.Background(), cfg, creds, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if resolver.Cloud() == nil || resolver.Candidates().Destinations[pkgcfg.DestinationSecondary].Provider == nil || creds.calls != 0 {
				t.Fatalf("credential calls during construction=%d", creds.calls)
			}
		})
	}
}
