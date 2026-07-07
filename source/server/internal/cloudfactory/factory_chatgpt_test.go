package cloudfactory

import (
	"context"
	"testing"

	"cercano/source/server/pkg/config"
)

type fakeTokenSource struct{}

func (fakeTokenSource) Token(ctx context.Context) (string, string, error) {
	return "tok", "acct", nil
}

func TestBuildResponsesChatGPTRouteRequiresTokenSource(t *testing.T) {
	_, err := BuildCloudProvider(config.CloudProfile{Name: "x", Flavor: FlavorResponses, Route: RouteChatGPT, Model: "gpt-5.5"}, "")
	if err == nil {
		t.Fatal("want error when chatgpt route has no token source")
	}
}

func TestBuildResponsesChatGPTRouteWithTokenSource(t *testing.T) {
	p, err := BuildCloudProvider(
		config.CloudProfile{Name: "x", Flavor: FlavorResponses, Route: RouteChatGPT, Model: "gpt-5.5"},
		"", Options{TokenSource: fakeTokenSource{}})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
}
