package config_test

import (
	"cercano/source/server/internal/anthropicauth"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/secrets"
	cfg "cercano/source/server/pkg/config"
	"context"
	"testing"
	"time"
)

func TestCredentialOwnerSurvivesBackendReplacement(t *testing.T) {
	service := cfgsvc.New("", cfg.Defaults(), nil)
	owner := service.Credentials()
	if owner == nil || service.Secrets() != nil {
		t.Fatal("incorrect nil-backend behavior")
	}
	service.SetSecrets(secrets.NewMemory())
	if service.Credentials() != owner || service.Secrets() != owner {
		t.Fatal("credential owner not shared with store facade")
	}
	view := owner.Anthropic("work", anthropicauth.Flow{})
	for _, access := range []string{"first", "second"} {
		service.SetSecrets(secrets.NewMemory())
		if err := anthropicauth.Save(service.Secrets(), "work", anthropicauth.TokenSet{Access: access, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if actual, err := view.Token(context.Background()); err != nil || actual != access {
			t.Fatalf("old view did not follow owner: %s %v", actual, err)
		}
	}
	service.SetSecrets(service.Secrets()) // must not wrap the owner in itself
	if _, err := view.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	service.SetSecrets(nil)
	if service.Credentials() != owner || service.Secrets() != nil {
		t.Fatal("reset replaced owner or lost nil convention")
	}
}
