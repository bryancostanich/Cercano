// Package cloudfactory builds an inference.Provider from a cloud profile. It is the
// single extension point for new wire-format flavors: each later sub-project
// adds one case.
package cloudfactory

import (
	"fmt"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm/anthropic"
	"cercano/source/server/internal/llm/bedrock"
	"cercano/source/server/internal/llm/openai"
	"cercano/source/server/internal/llm/responses"
	"cercano/source/server/pkg/config"
)

const (
	FlavorMessages        = "messages"
	FlavorChatCompletions = "chat_completions"
	FlavorResponses       = "responses"
	FlavorBedrock         = "bedrock"
)

// RouteChatGPT re-exports the responses package's ChatGPT subscription route
// value so callers select it without importing responses directly.
const RouteChatGPT = responses.RouteChatGPT

// RouteSubscription re-exports the anthropic package's Claude Max/Pro
// subscription route value (messages flavor).
const RouteSubscription = anthropic.RouteSubscription

// Options carries optional dependencies for routes that need more than a
// static API key — the subscription auth flows, which authenticate with a
// refreshing token source over the keychain instead of a key.
type Options struct {
	// TokenSource supplies refreshing ChatGPT subscription bearers for the
	// responses flavor's chatgpt route.
	TokenSource responses.TokenSource
	// AnthropicTokenSource supplies refreshing Claude subscription bearers for
	// the messages flavor's subscription route.
	AnthropicTokenSource anthropic.TokenSource
}

// BuildCloudProvider maps a profile (+ its key) to an inference.Provider. Only the
// messages (Anthropic) flavor is implemented in the foundation.
func BuildCloudProvider(p config.CloudProfile, apiKey string, opts ...Options) (inference.Provider, error) {
	switch p.Flavor {
	case FlavorMessages:
		if p.Route == RouteSubscription {
			var ts anthropic.TokenSource
			if len(opts) > 0 {
				ts = opts[0].AnthropicTokenSource
			}
			if ts == nil {
				return nil, fmt.Errorf("messages route %q requires a token source", p.Route)
			}
			// Subscription pins api.anthropic.com; ignore any profile BaseURL
			// (a migrated Meridian profile may still carry the old proxy URL).
			return anthropic.NewClient(anthropic.Config{Model: p.Model, Route: p.Route, TokenSource: ts}), nil
		}
		return anthropic.NewClient(anthropic.Config{
			BaseURL: p.BaseURL,
			APIKey:  apiKey,
			Model:   p.Model,
			Route:   p.Route,
		}), nil
	case FlavorChatCompletions:
		return openai.NewClient(openai.Config{BaseURL: p.BaseURL, APIKey: apiKey, Model: p.Model, Backend: p.Backend}), nil
	case FlavorResponses:
		if p.Route == responses.RouteChatGPT {
			var ts responses.TokenSource
			if len(opts) > 0 {
				ts = opts[0].TokenSource
			}
			if ts == nil {
				return nil, fmt.Errorf("responses route %q requires a token source", p.Route)
			}
			return responses.NewClient(responses.Config{Model: p.Model, Route: p.Route, TokenSource: ts}), nil
		}
		return responses.NewClient(responses.Config{BaseURL: p.BaseURL, APIKey: apiKey, Model: p.Model}), nil
	case FlavorBedrock:
		return bedrock.NewClient(bedrock.Config{
			Region: p.Region, Model: p.Model, AWSProfile: p.AWSProfile, BaseURL: p.BaseURL,
		})
	default:
		return nil, fmt.Errorf("flavor %q not yet supported", p.Flavor)
	}
}
