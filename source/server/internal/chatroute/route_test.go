package chatroute

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/pkg/config"
	"testing"
)

func TestSessionModelProfileCopyAndLocusBoundary(t *testing.T) {
	cfg := config.Defaults()
	cfg.CloudProfiles = []config.CloudProfile{{Name: "deepinfra", Model: "default-model", Flavor: "chat_completions", BaseURL: "https://example.invalid/v1"}}
	route := Route{Profile: "deepinfra", Model: "exact-model"}
	p, err := ProfileFor(cfg, route)
	if err != nil || p.Model != route.Model || p.BaseURL != cfg.CloudProfiles[0].BaseURL {
		t.Fatal(p, err)
	}
	if cfg.CloudProfiles[0].Model != "default-model" {
		t.Fatal("global profile mutated")
	}
	cfg.LocusMode = "open_only"
	if _, err := ProfileFor(cfg, route); err == nil {
		t.Fatal("local-only privacy boundary bypassed")
	}
	cfg.LocusMode = "cloud_only"
	for _, route := range []Route{{Profile: "missing", Model: "model"}, {Profile: "deepinfra"}, {Model: "model"}} {
		if _, err := ProfileFor(cfg, route); err == nil {
			t.Fatal("invalid route accepted", route)
		}
	}
}

type sessionModelProvider struct{ inference.Provider }

func (*sessionModelProvider) Name() string { return "fixture" }
func TestSessionModelRetainsAccountIdentity(t *testing.T) {
	cfg := config.Defaults()
	cfg.CloudProfiles = []config.CloudProfile{{Name: "deepinfra", Flavor: "chat_completions"}}
	provider, err := Build(cfg, Route{Profile: "deepinfra", Model: "exact-model"}, func(config.CloudProfile) (inference.Provider, error) { return &sessionModelProvider{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	model, ok := inference.TaskModelFor(provider)
	if !ok || model != "exact-model" {
		t.Fatal("model binding lost", model)
	}
	target := inference.TargetForCall(provider, inference.Call{Model: model})
	if target.Profile != "deepinfra" || target.Model != "exact-model" {
		t.Fatalf("account identity lost: %+v", target)
	}
}
