package cloudfactory

import (
	"errors"
	"testing"

	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
)

// cerebrasProfile is a Cerebras profile exactly as the cloud catalog templates
// it: the OpenAI-compatible dialect, no Backend quirks selector, distinguished
// only by its base URL.
func cerebrasProfile() config.CloudProfile {
	return config.CloudProfile{
		Name:    "cerebras",
		Flavor:  FlavorChatCompletions,
		BaseURL: "https://api.cerebras.ai/v1",
	}
}

// api.cerebras.ai is a direct vendor endpoint that serves its catalog only with
// the profile's key, so it must be classified alongside api.deepinfra.com: a
// missing static key is an auth failure, not a silent anonymous request.
func TestRequiresStaticKey_CerebrasHost(t *testing.T) {
	if !RequiresStaticKey(cerebrasProfile()) {
		t.Error("api.cerebras.ai is a direct vendor endpoint and must require a static key")
	}
	// A non-vendor host keeps proxy semantics even when the profile is named
	// or labelled cerebras — host identity, not the label, is authority.
	if RequiresStaticKey(config.CloudProfile{Name: "cerebras", Flavor: FlavorChatCompletions, BaseURL: "https://gw.example.com/v1"}) {
		t.Error("unrecognized host must not be classified as a direct vendor endpoint")
	}
}

func TestValidateStaticCredential_CerebrasMissingKeyIsAuthError(t *testing.T) {
	err := ValidateStaticCredential(cerebrasProfile(), "", nil)
	var ce *llm.CredentialError
	if !errors.As(err, &ce) {
		t.Fatalf("missing key: want CredentialError, got %v", err)
	}
	if ce.Class != llm.ErrAuth || ce.Reason != llm.CredentialMissing || ce.Profile != "cerebras" {
		t.Errorf("missing key: got class=%v reason=%v profile=%v", ce.Class, ce.Reason, ce.Profile)
	}
}

func TestValidateStaticCredential_CerebrasWithKeyPasses(t *testing.T) {
	if err := ValidateStaticCredential(cerebrasProfile(), "csk-test", nil); err != nil {
		t.Errorf("populated key should validate, got %v", err)
	}
}

func TestValidateStaticCredential_CerebrasStoreFailureStaysCredentialError(t *testing.T) {
	err := ValidateStaticCredential(cerebrasProfile(), "", errors.New("keychain unavailable"))
	var ce *llm.CredentialError
	if !errors.As(err, &ce) {
		t.Fatalf("store failure: want CredentialError, got %v", err)
	}
	if ce.Class != llm.ErrCredential || ce.Reason != llm.CredentialStore {
		t.Errorf("store failure: got class=%v reason=%v", ce.Class, ce.Reason)
	}
}
