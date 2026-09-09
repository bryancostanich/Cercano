package config

import "testing"

func sampleProfiles() ModelProfiles {
	return ModelProfiles{Cloud: CloudCostProfiles{Providers: map[string]VendorCostTiers{
		"anthropic": {
			Economy:  CostTierModel{Model: "claude-haiku-4-5"},
			Standard: CostTierModel{Model: "claude-opus-5"},
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
		{"anthropic", CostStandard, "claude-opus-5", true},
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
			Standard: CostTierModel{Model: "claude-opus-5"},
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
		{"table hit standard", anthro, TierEveryday, "claude-opus-5"},
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

// The ChatGPT subscription route rejects the mini models that the direct
// OpenAI API accepts, so economy-tier work (compaction, summarization) must
// not resolve to gpt-5-mini when the profile carries route: chatgpt.
func TestResolveCloudModelForTierIsRouteAware(t *testing.T) {
	d := Defaults()

	chatgpt := CloudProfile{Flavor: "responses", Route: "chatgpt"} // Provider empty -> inferred
	directAPI := CloudProfile{Flavor: "chat_completions", Backend: "openai"}

	// The regression: economy on the ChatGPT route must not be a mini model.
	got := d.ModelProfiles.ResolveCloudModelForTier(chatgpt, TierFastLightText)
	if got == "gpt-5-mini" {
		t.Errorf("chatgpt route economy resolved to %q — Codex rejects mini models with a 400", got)
	}
	if got != "gpt-6-astra" {
		t.Errorf("chatgpt route economy = %q, want gpt-6-astra", got)
	}

	// The direct API keeps its cheaper economy model: the fix must not make
	// every OpenAI caller pay premium prices.
	if got := d.ModelProfiles.ResolveCloudModelForTier(directAPI, TierFastLightText); got != "gpt-5-mini" {
		t.Errorf("direct openai economy = %q, want gpt-5-mini (unchanged)", got)
	}
}

// A config carrying only the bare vendor key (hand-written, or predating the
// route-qualified tables) must still resolve a model rather than silently
// returning "" and failing every request.
func TestResolveCloudModelForTierFallsBackToBaseVendor(t *testing.T) {
	m := ModelProfiles{Cloud: CloudCostProfiles{Providers: map[string]VendorCostTiers{
		"openai": {Economy: CostTierModel{Model: "gpt-5-mini"}},
	}}}
	chatgpt := CloudProfile{Flavor: "responses", Route: "chatgpt"}
	if got := m.ResolveCloudModelForTier(chatgpt, TierFastLightText); got != "gpt-5-mini" {
		t.Errorf("missing route table: got %q, want fallback to base vendor gpt-5-mini", got)
	}
}

// An explicit provider: names a table directly and must be honored verbatim,
// so a user who pinned a vendor table keeps addressing exactly that table.
func TestExplicitProviderIsNotRouteQualified(t *testing.T) {
	m := ModelProfiles{Cloud: CloudCostProfiles{Providers: map[string]VendorCostTiers{
		"openai":         {Economy: CostTierModel{Model: "gpt-5-mini"}},
		"openai-chatgpt": {Economy: CostTierModel{Model: "gpt-5.5"}},
	}}}
	explicit := CloudProfile{Provider: "openai", Flavor: "responses", Route: "chatgpt"}
	if got := m.ResolveCloudModelForTier(explicit, TierFastLightText); got != "gpt-5-mini" {
		t.Errorf("explicit provider = %q, want gpt-5-mini (taken at face value)", got)
	}
}

func TestDefaultsSeedsCostTables(t *testing.T) {
	d := Defaults()
	if got, ok := d.ModelProfiles.ResolveCloud("anthropic", CostPremium); !ok || got != "claude-fable-5" {
		t.Errorf("anthropic premium => %q,%v want claude-fable-5,true", got, ok)
	}
	for _, vendor := range []string{"openai", "openai-chatgpt"} {
		for _, tier := range []CostTier{CostEconomy, CostStandard, CostPremium} {
			want := "gpt-6-astra"
			if vendor == "openai" && tier == CostEconomy {
				want = "gpt-5-mini"
			}
			if got, ok := d.ModelProfiles.ResolveCloud(vendor, tier); !ok || got != want {
				t.Errorf("%s %s => %q,%v want %s,true", vendor, tier, got, ok, want)
			}
		}
	}
}

func TestRetiredOpenAIDefaultFollowsCatalog(t *testing.T) {
	for _, profile := range []CloudProfile{
		{Flavor: "responses", Route: "chatgpt"},
		{Flavor: "responses"},
		{Provider: "openai-chatgpt", Flavor: "responses", Route: "chatgpt"},
	} {
		for _, pinned := range []bool{false, true} {
			profile.Model = "gpt-5.5"
			profile.ModelPinned = pinned
			cfg := Defaults()
			cfg.CloudProfiles = []CloudProfile{profile}
			normalizeCloudModelDefaults(&cfg)
			want := "gpt-6-astra"
			if pinned {
				want = "gpt-5.5"
			}
			if got := cfg.ModelProfiles.ResolveCloudModelForTier(cfg.CloudProfiles[0], TierEveryday); got != want {
				t.Errorf("profile %+v: got %q, want %q", profile, got, want)
			}
		}
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
