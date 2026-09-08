package resilience

import (
	"context"
	"testing"

	"cercano/source/server/internal/inference"
)

// New must honor the PrimaryModelFor option. Other tests assign the private
// field directly, so only this test covers the constructor wiring: dropping
// the option silently let a stale/foreign request Model reach the primary.
func TestNew_AppliesPrimaryModelForOption(t *testing.T) {
	primary := &fakeProvider{name: "primary"}
	p := New(primary, Options{PrimaryModelFor: func(string) string { return "tier-model" }})
	if _, err := p.Chat(context.Background(), inference.Call{Model: "stale-foreign", Tier: "everyday"}); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if len(primary.models) != 1 || primary.models[0] != "tier-model" {
		t.Fatalf("primary saw %v, want [tier-model]", primary.models)
	}
}

func TestNew_PrimaryModelForLeavesUntieredRequestAlone(t *testing.T) {
	primary := &fakeProvider{name: "primary"}
	p := New(primary, Options{PrimaryModelFor: func(string) string { return "tier-model" }})
	if _, err := p.Chat(context.Background(), inference.Call{Model: "explicit-override"}); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if len(primary.models) != 1 || primary.models[0] != "explicit-override" {
		t.Fatalf("primary saw %v, want [explicit-override]", primary.models)
	}
}
