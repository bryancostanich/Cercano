package providers

import (
	"cercano/source/server/internal/cloudfactory"
	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/secrets"
	"cercano/source/server/pkg/config"
	"errors"
	"strings"
	"testing"
)

type failedKeyStore struct{ secrets.Store }

func (s failedKeyStore) Get(string) (string, error) {
	return "", errors.New("private-credential-payload")
}
func TestDeepInfraCredentialFailuresRemainTypedAndNonInteractive(t *testing.T) {
	profile := config.CloudProfile{Name: "named-deepinfra", Provider: "deepinfra", Flavor: cloudfactory.FlavorChatCompletions, Backend: "openai", BaseURL: "https://api.deepinfra.com/v1/openai"}
	for _, tc := range []struct {
		name   string
		store  secrets.Store
		class  llm.ErrorClass
		reason string
	}{
		{"missing", secrets.NewMemory(), llm.ErrAuth, llm.CredentialMissing},
		{"store", failedKeyStore{secrets.NewMemory()}, llm.ErrCredential, llm.CredentialStore},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := config.Defaults()
			c.ActiveCloudProfile = profile.Name
			c.CloudProfiles = []config.CloudProfile{profile}
			owner := cfgsvc.New("", c, tc.store)
			service := &service{cfgSvc: owner, router: &recordingRouter{}}
			_, err := service.buildProfile(profile)
			var credential *llm.CredentialError
			if llm.ClassOf(err) != tc.class || !errors.As(err, &credential) || credential.Profile != profile.Name || credential.Reason != tc.reason || credential.Method == llm.AuthSubscription {
				t.Fatalf("credential identity/classification lost: %v", err)
			}
			if strings.Contains(err.Error(), "private-credential-payload") {
				t.Fatal("credential payload leaked")
			}
		})
	}
}
