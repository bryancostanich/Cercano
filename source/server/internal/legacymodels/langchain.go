package legacymodels

import (
	"context"
	"fmt"
	"cercano/source/server/internal/agent"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/anthropic"
	"github.com/tmc/langchaingo/llms/googleai"
)

// CloudModelProvider wraps langchaingo's llms.Model.
type CloudModelProvider struct {
	providerName string
	modelName    string
	apiKey       string
	llm          llms.Model
}

// defaultModels maps provider names to sensible default models.
var defaultModels = map[string]string{
	"google":    "gemini-3-flash",
	"anthropic": "claude-sonnet-4-6",
}

// resolveModel returns the given model if it's non-empty and belongs to the
// provider, otherwise returns the provider's default model.
func resolveModel(provider, model string) string {
	if model == "" {
		return defaultModels[provider]
	}
	// Catch cross-provider model names (e.g. gemini model sent to anthropic)
	switch provider {
	case "google":
		if len(model) >= 6 && model[:6] == "claude" {
			return defaultModels[provider]
		}
	case "anthropic":
		if len(model) >= 6 && model[:6] == "gemini" {
			return defaultModels[provider]
		}
	}
	return model
}

// NewCloudModelProvider creates a new cloud model provider based on the type.
// baseURL is optional; when set with the anthropic provider, requests go to
// that endpoint instead of api.anthropic.com (used by Meridian and other
// Anthropic-compatible proxies). An empty apiKey is accepted only when
// baseURL is set — the proxy is expected to handle auth.
func NewCloudModelProvider(ctx context.Context, provider, model, apiKey, baseURL string) (*CloudModelProvider, error) {
	model = resolveModel(provider, model)

	var llm llms.Model
	var err error

	switch provider {
	case "google":
		llm, err = googleai.New(ctx, googleai.WithAPIKey(apiKey), googleai.WithDefaultModel(model))
	case "anthropic":
		opts := []anthropic.Option{anthropic.WithModel(model)}
		if apiKey != "" {
			opts = append(opts, anthropic.WithToken(apiKey))
		} else {
			// langchaingo's anthropic adapter wants a non-empty token; the
			// proxy will ignore it. Use a literal placeholder so the SDK
			// doesn't refuse to construct.
			opts = append(opts, anthropic.WithToken("dummy"))
		}
		if baseURL != "" {
			opts = append(opts, anthropic.WithBaseURL(baseURL))
		}
		llm, err = anthropic.New(opts...)
	default:
		return nil, fmt.Errorf("unsupported cloud provider: %s", provider)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create %s provider: %w", provider, err)
	}

	return &CloudModelProvider{
		providerName: provider,
		modelName:    model,
		apiKey:       apiKey,
		llm:          llm,
	}, nil
}

// Process handles an AI request by calling the cloud model via langchaingo.
func (c *CloudModelProvider) Process(ctx context.Context, req *agent.Request) (*agent.Response, error) {
	if c.llm == nil {
		return nil, fmt.Errorf("cloud model not initialized")
	}

	completion, err := c.llm.Call(ctx, req.Input)
	if err != nil {
		return nil, fmt.Errorf("failed to get completion from %s: %w", c.providerName, err)
	}

	return &agent.Response{
		Output: completion,
	}, nil
}

// Name returns the name of the provider.
func (c *CloudModelProvider) Name() string {
	return c.providerName
}
