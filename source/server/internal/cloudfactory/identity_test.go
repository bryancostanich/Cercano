package cloudfactory

import (
	"cercano/source/server/pkg/config"
	"testing"
)

func TestPhysicalEndpointMatchesAdapterRouting(t *testing.T) {
	for _, tc := range []struct {
		profile            config.CloudProfile
		provider, endpoint string
	}{
		{config.CloudProfile{Flavor: FlavorChatCompletions}, "openai", "https://api.openai.com/v1"},
		{config.CloudProfile{Flavor: FlavorChatCompletions, BaseURL: "https://custom.example/v1/", Backend: "deepinfra"}, "openai", "https://custom.example/v1"},
		{config.CloudProfile{Flavor: FlavorResponses, Route: RouteChatGPT, BaseURL: "https://ignored.example"}, "openai", "https://chatgpt.com/backend-api/codex"},
		{config.CloudProfile{Flavor: FlavorMessages, Route: RouteSubscription, BaseURL: "https://ignored.example"}, "anthropic", "https://api.anthropic.com"},
		{config.CloudProfile{Flavor: FlavorMessages, BaseURL: "https://custom.example/proxy"}, "anthropic", "https://custom.example/proxy"},
		{config.CloudProfile{Flavor: FlavorBedrock, Region: "us-east-1"}, "bedrock", "https://bedrock-runtime.us-east-1.amazonaws.com"},
		{config.CloudProfile{Flavor: FlavorBedrock, BaseURL: "https://vpc.example"}, "bedrock", "https://vpc.example"},
	} {
		provider, endpoint, err := PhysicalEndpoint(t.Context(), tc.profile)
		if err != nil || provider != tc.provider || endpoint != tc.endpoint {
			t.Fatalf("%s: provider=%q endpoint=%q err=%v", tc.profile.Flavor, provider, endpoint, err)
		}
	}
	if _, _, err := PhysicalEndpoint(t.Context(), config.CloudProfile{Flavor: FlavorBedrock}); err == nil {
		t.Fatal("implicit AWS region was guessed")
	}
}
