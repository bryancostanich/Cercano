package config

import "testing"

func sampleProfiles() ModelProfiles {
	return ModelProfiles{Cloud: CloudCostProfiles{Providers: map[string]VendorCostTiers{
		"anthropic": {
			Economy:  CostTierModel{Model: "claude-haiku-4-5"},
			Standard: CostTierModel{Model: "claude-opus-5-0"},
			Premium:  CostTierModel{Model: "claude-fable-5"},
		},
		"openai": {
			Standard: CostTierModel{Model: "gpt-5.5"}, // economy/premium intentionally unset
		},
	}}}
}

func TestResolveCloud(t *testing.T) {
	m := sampleProfiles()
	cases := []struct {
		vendor string
		tier   CostTier
		want   string
		ok     bool
	}{
		{"anthropic", CostEconomy, "claude-haiku-4-5", true},
		{"anthropic", CostStandard, "claude-opus-5-0", true},
		{"anthropic", CostPremium, "claude-fable-5", true},
		{"openai", CostStandard, "gpt-5.5", true},
		{"openai", CostEconomy, "", false},  // slot unset
		{"google", CostStandard, "", false}, // unknown vendor
		{"", CostStandard, "", false},       // empty vendor
	}
	for _, c := range cases {
		got, ok := m.ResolveCloud(c.vendor, c.tier)
		if got != c.want || ok != c.ok {
			t.Errorf("ResolveCloud(%q,%q)=%q,%v want %q,%v", c.vendor, c.tier, got, ok, c.want, c.ok)
		}
	}
}

func TestCostTierForCapability(t *testing.T) {
	cases := []struct {
		in   Tier
		want CostTier
		ok   bool
	}{
		{TierMostCapable, CostPremium, true},
		{TierEveryday, CostStandard, true},
		{TierFastLight, CostEconomy, true},
		{TierFastLightText, CostEconomy, true},
		{TierEmbedding, "", false},
	}
	for _, c := range cases {
		got, ok := CostTierForCapability(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("CostTierForCapability(%q)=%q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestInferProviderVendor(t *testing.T) {
	cases := []struct {
		p    CloudProfile
		want string
	}{
		{CloudProfile{Flavor: "responses"}, "openai"},
		{CloudProfile{Flavor: "messages"}, "anthropic"},
		{CloudProfile{Flavor: "bedrock"}, "anthropic"},
		{CloudProfile{Flavor: "chat_completions", Backend: "groq"}, "groq"},
		{CloudProfile{Flavor: "chat_completions"}, "openai"},
		{CloudProfile{Flavor: ""}, ""},
	}
	for _, c := range cases {
		if got := inferProviderVendor(c.p); got != c.want {
			t.Errorf("inferProviderVendor(%+v)=%q want %q", c.p, got, c.want)
		}
	}
}

// TestResolveCloudModelForTier covers cloud's split semantics: unpinned
// profiles follow the baked vendor+tier table, while a profile Model is an
// explicit pin that overrides every tier.
func TestResolveCloudModelForTier(t *testing.T) {
	m := ModelProfiles{Cloud: CloudCostProfiles{Providers: map[string]VendorCostTiers{
		"anthropic": {
			Standard: CostTierModel{Model: "claude-opus-5-0"},
			Premium:  CostTierModel{Model: "claude-fable-5"},
		},
	}}}
	anthro := CloudProfile{Provider: "anthropic", Flavor: "messages"}
	pinned := CloudProfile{Provider: "anthropic", Model: "claude-custom", ModelPinned: true, Flavor: "messages"}
	openaiPinned := CloudProfile{Model: "gpt-5.5", ModelPinned: true, Flavor: "responses"} // Provider empty -> inferred openai

	cases := []struct {
		name string
		prof CloudProfile
		tier Tier
		want string
	}{
		{"table hit premium", anthro, TierMostCapable, "claude-fable-5"},
		{"table hit standard", anthro, TierEveryday, "claude-opus-5-0"},
		{"tier slot unset -> empty", anthro, TierFastLight, ""},
		{"embedding has no cost tier -> empty", anthro, TierEmbedding, ""},
		{"profile pin overrides table", pinned, TierMostCapable, "claude-custom"},
		{"inferred openai pinned model", openaiPinned, TierEveryday, "gpt-5.5"},
	}
	for _, c := range cases {
		if got := m.ResolveCloudModelForTier(c.prof, c.tier); got != c.want {
			t.Errorf("%s: ResolveCloudModelForTier=%q want %q", c.name, got, c.want)
		}
	}

	// No cost table at all: an explicit profile pin still yields its own model,
	// never a foreign-vendor model.
	empty := ModelProfiles{}
	if got := empty.ResolveCloudModelForTier(openaiPinned, TierMostCapable); got != "gpt-5.5" {
		t.Errorf("empty table: got %q want gpt-5.5 (must never cross vendors)", got)
	}
}

func TestDefaultsSeedsCostTables(t *testing.T) {
	d := Defaults()
	if got, ok := d.ModelProfiles.ResolveCloud("anthropic", CostPremium); !ok || got != "claude-fable-5" {
		t.Errorf("anthropic premium => %q,%v want claude-fable-5,true", got, ok)
	}
	if got, ok := d.ModelProfiles.ResolveCloud("openai", CostStandard); !ok || got != "gpt-5.5" {
		t.Errorf("openai standard => %q,%v want gpt-5.5,true", got, ok)
	}
}

func TestCloneModelProfilesIndependent(t *testing.T) {
	c := Defaults()
	clone := c.Clone()
	if clone.ModelProfiles.Cloud.Providers == nil {
		t.Fatal("clone dropped ModelProfiles providers")
	}
	clone.ModelProfiles.Cloud.Providers["anthropic"] = VendorCostTiers{Standard: CostTierModel{Model: "mutated"}}
	if got, _ := c.ModelProfiles.ResolveCloud("anthropic", CostStandard); got == "mutated" {
		t.Error("Clone aliased ModelProfiles map — mutation leaked to the original")
	}
}
