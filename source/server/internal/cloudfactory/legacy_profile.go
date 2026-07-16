package cloudfactory

import (
	"strings"

	"cercano/source/server/pkg/config"
)

// LegacyProfile maps the loose (provider, model, baseURL) string triple — the
// signature the old agent.CloudFactory closure carried, and the shape the
// retired langchain CloudModelProvider consumed — into a config.CloudProfile
// suitable for BuildCloudProvider. It centralizes the vendor→wire-flavor
// mapping in one tested place so the two cmd/*/main.go cloud factories build
// the modern inference.Provider path instead of the langchain one.
//
// Vendor→flavor (matching the vendors the langchain path supported, plus the
// openai flavor the inference path already implements):
//   - anthropic → messages
//   - openai    → chat_completions (openai backend)
//   - google    → chat_completions (gemini backend)
//   - (default) → chat_completions with the provider as backend hint
//
// baseURL, when set, overrides the vendor endpoint (Meridian and other
// Anthropic-compatible local proxies).
func LegacyProfile(provider, model, baseURL string) config.CloudProfile {
	provider = strings.ToLower(strings.TrimSpace(provider))
	p := config.CloudProfile{
		Name:     provider,
		Provider: provider,
		Model:    model,
		BaseURL:  baseURL,
	}
	switch provider {
	case "anthropic":
		p.Flavor = FlavorMessages
	case "openai":
		p.Flavor = FlavorChatCompletions
		p.Backend = "openai"
	case "google", "gemini":
		p.Flavor = FlavorChatCompletions
		p.Backend = "gemini"
	default:
		// Unknown vendor: default to the broadly-compatible chat_completions
		// wire format, using the provider name as the backend quirk hint.
		p.Flavor = FlavorChatCompletions
		p.Backend = provider
	}
	return p
}
