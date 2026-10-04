package cloudfactory

import (
	"context"
	"fmt"
	"strings"

	"cercano/source/server/internal/llm/responses"
	"cercano/source/server/pkg/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// PhysicalEndpoint describes the endpoint actually selected by this profile's
// adapter. Subscription routes deliberately ignore the editable BaseURL. This
// metadata never reads or returns a provider credential.
func PhysicalEndpoint(ctx context.Context, p config.CloudProfile) (provider, endpoint string, err error) {
	endpoint = strings.TrimRight(p.BaseURL, "/")
	switch p.Flavor {
	case FlavorMessages:
		provider = "anthropic"
		if endpoint == "" || p.Route == RouteSubscription {
			endpoint = "https://api.anthropic.com"
		}
	case FlavorChatCompletions:
		provider = "openai"
		if endpoint == "" {
			endpoint = "https://api.openai.com/v1"
		}
	case FlavorResponses:
		provider = "openai"
		if p.Route == RouteChatGPT {
			endpoint = responses.CodexBaseURL
		} else if endpoint == "" {
			endpoint = "https://api.openai.com/v1"
		}
	case FlavorBedrock:
		provider = "bedrock"
		if endpoint == "" {
			if p.Region == "" {
				return "", "", fmt.Errorf("managed Bedrock profile %q needs an explicit region or endpoint", p.Name)
			}
			resolved, e := bedrockruntime.NewDefaultEndpointResolverV2().ResolveEndpoint(ctx, (bedrockruntime.EndpointParameters{Region: &p.Region}).WithDefaults())
			if e != nil {
				return "", "", e
			}
			endpoint = strings.TrimRight(resolved.URI.String(), "/")
		}
	default:
		return "", "", fmt.Errorf("unsupported managed profile flavor %q", p.Flavor)
	}
	return provider, endpoint, nil
}
