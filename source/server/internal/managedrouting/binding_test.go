package managedrouting

import (
	"context"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/pkg/config"
	v1 "github.com/bryancostanich/Cercano/source/enterpriseapi/v1"
)

func TestManagedBindingDoesNotRedirectProviderCredentials(t *testing.T) {
	c := config.Config{ActiveCloudProfile: "personal", SecondaryCloudProfile: "work", CloudProfiles: []config.CloudProfile{
		{Name: "personal", Flavor: "chat_completions", BaseURL: "https://models.example/v1", Model: "personal-model"},
		{Name: "work", Flavor: "chat_completions", BaseURL: "https://models.example/v1", Model: "work-model"},
	}}
	route := v1.Route{ID: "approved", Provider: "openai", Endpoint: "https://models.example/v1", Model: "admin-model", Placement: "external"}
	var built []config.CloudProfile
	build := func(p config.CloudProfile) (inference.Provider, error) {
		built = append(built, p)
		return &routeFixture{route: route}, nil
	}
	candidate, err := BindRoute(context.Background(), c, route, config.DestinationSecondary, nil, build)
	if err != nil || candidate.Profile != "work" || len(built) != 1 || built[0].Model != "admin-model" {
		t.Fatalf("candidate=%+v built=%+v err=%v", candidate, built, err)
	}
	if c.CloudProfiles[1].Model != "work-model" {
		t.Fatal("managed binding overwrote personal configuration")
	}
	for _, change := range []string{"provider", "endpoint"} {
		other := route
		if change == "provider" {
			other.Provider = "anthropic"
		} else {
			other.Endpoint = "https://unconfigured.example/v1"
		}
		if _, err := BindRoute(context.Background(), c, other, config.DestinationPrimary, nil, build); err == nil {
			t.Fatal("bound a key to an unconfigured identity")
		}
	}
	if len(built) != 1 {
		t.Fatal("unapproved endpoint reached credential builder")
	}
}
func TestManagedOllamaBindingPreservesPhysicalPlacement(t *testing.T) {
	var calls []string
	for _, tc := range []struct{ endpoint, placement string }{{"http://127.0.0.1:11434", "local"}, {"https://ollama.example", "external"}} {
		route := v1.Route{ID: "ollama", Provider: "ollama", Endpoint: tc.endpoint, Model: "approved", Placement: tc.placement}
		raw := &routeFixture{route: route, calls: &calls}
		c := config.Config{OpenRuntime: "ollama", OllamaURL: tc.endpoint}
		candidate, err := BindRoute(context.Background(), c, route, config.DestinationPrimary, raw, nil)
		if err != nil || candidate.IsCloud != (tc.placement == "external") {
			t.Fatalf("placement lost: %+v %v", candidate, err)
		}
		route.Endpoint = "https://other.example"
		if _, err := BindRoute(context.Background(), c, route, config.DestinationPrimary, raw, nil); err == nil {
			t.Fatal("runtime endpoint mismatch accepted")
		}
	}
}
