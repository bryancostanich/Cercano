package cloudfactory

import (
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
	"errors"
	"io/fs"
	"net/url"
	"strings"
)

// RequiresStaticKey distinguishes direct vendor endpoints from intentionally
// anonymous/authenticating proxies. A populated BaseURL alone is not proof that
// a proxy owns authentication (DeepInfra's normal endpoint is a BaseURL too).
func RequiresStaticKey(p config.CloudProfile) bool {
	if IsSubscription(p) || p.Flavor == FlavorBedrock {
		return false
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return true
	}
	endpoint, err := url.Parse(p.BaseURL)
	if err != nil {
		return false
	}
	switch strings.ToLower(endpoint.Hostname()) {
	case "api.deepinfra.com", "api.openai.com", "api.anthropic.com":
		return true
	}
	return false
}

// ValidateStaticCredential preserves identity and sanitizes store errors. API
// keys are never classified as subscription login, and a failed store read must
// not silently become an anonymous request or a credential-bypassing fallback.
func ValidateStaticCredential(p config.CloudProfile, key string, err error) error {
	if IsSubscription(p) || p.Flavor == FlavorBedrock {
		return nil
	}
	provider := p.Provider
	if provider == "" {
		provider = p.Backend
	}
	if provider == "" {
		provider = p.Flavor
	}
	class, reason := llm.ErrCredential, llm.CredentialStore
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		if key != "" || !RequiresStaticKey(p) {
			return nil
		}
		class, reason = llm.ErrAuth, llm.CredentialMissing
	}
	return &llm.CredentialError{Class: class, Provider: provider, Profile: p.Name, Method: "api_key", Reason: reason, Cause: err}
}
