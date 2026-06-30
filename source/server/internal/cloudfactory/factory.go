// Package cloudfactory builds an llm.Provider from a cloud profile. It is the
// single extension point for new wire-format flavors: each later sub-project
// adds one case.
package cloudfactory

import (
	"fmt"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/anthropic"
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

// BuildCloudProvider maps a profile (+ its key) to an llm.Provider. Only the
// messages (Anthropic) flavor is implemented in the foundation.
func BuildCloudProvider(p config.CloudProfile, apiKey string) (llm.Provider, error) {
	switch p.Flavor {
	case FlavorMessages:
		return anthropic.NewClient(anthropic.Config{
			BaseURL: p.BaseURL,
			APIKey:  apiKey,
			Model:   p.Model,
			Route:   p.Route,
		}), nil
	case FlavorChatCompletions:
		return openai.NewClient(openai.Config{BaseURL: p.BaseURL, APIKey: apiKey, Model: p.Model, Backend: p.Backend}), nil
	case FlavorResponses:
		return responses.NewClient(responses.Config{BaseURL: p.BaseURL, APIKey: apiKey, Model: p.Model}), nil
	default:
		return nil, fmt.Errorf("flavor %q not yet supported", p.Flavor)
	}
}
