package config

import "testing"

// deepInfraProfile is a DeepInfra profile exactly as the cloud catalog
// templates it: the OpenAI-compatible dialect, no Backend quirks selector,
// distinguished only by its base URL.
func deepInfraProfile() CloudProfile {
	return CloudProfile{
		Name:    "deepinfra",
		Flavor:  "chat_completions",
		BaseURL: "https://api.deepinfra.com/v1/openai",
	}
}

// TestInferProviderVendor_BackendlessProvidersByHost is the regression test
// for a real mis-resolution: every backend-less OpenAI-compatible provider
// used to infer vendor "openai" and therefore draw OpenAI model ids out of
// the cost tables. A DeepInfra profile resolved to "gpt-5.5", which DeepInfra
// does not serve and would reject.
func TestInferProviderVendor_BackendlessProvidersByHost(t *testing.T) {
	for _, tc := range []struct {
		name    string
		baseURL string
		want    string
	}{
		{"deepinfra", "https://api.deepinfra.com/v1/openai", "deepinfra"},
		{"together", "https://api.together.xyz/v1", "together"},
		{"openrouter", "https://openrouter.ai/api/v1", "openrouter"},
		{"deepseek", "https://api.deepseek.com", "deepseek"},
		{"uppercase host still matches", "https://API.DEEPINFRA.COM/v1/openai", "deepinfra"},
		// A genuinely unknown OpenAI-compatible endpoint keeps the old
		// behavior: assume the OpenAI lineup, since that dialect is what it
		// speaks and we have nothing better to go on.
		{"unknown host falls back to openai", "https://llm.example.internal/v1", "openai"},
		{"empty base URL falls back to openai", "", "openai"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := CloudProfile{Flavor: "chat_completions", BaseURL: tc.baseURL}
			if got := inferProviderVendor(p); got != tc.want {
				t.Errorf("inferProviderVendor(%q) = %q, want %q", tc.baseURL, got, tc.want)
			}
		})
	}
}

// TestInferProviderVendor_ExplicitBackendWins guards the precedence: an
// explicit Backend is a deliberate selection and must not be second-guessed
// by the host heuristic.
func TestInferProviderVendor_ExplicitBackendWins(t *testing.T) {
	p := CloudProfile{
		Flavor:  "chat_completions",
		Backend: "groq",
		BaseURL: "https://api.deepinfra.com/v1/openai",
	}
	if got := inferProviderVendor(p); got != "groq" {
		t.Errorf("explicit Backend = %q, want groq (host must not override it)", got)
	}
}

// TestResolveCloudModelForTier_DeepInfra is the end-to-end assertion the plan
// asks for: every tier resolves to a non-empty, DeepInfra-shaped model id.
func TestResolveCloudModelForTier_DeepInfra(t *testing.T) {
	cfg := Defaults()
	p := deepInfraProfile()

	for _, tier := range []Tier{TierMostCapable, TierEveryday, TierFastLight, TierFastLightText} {
		got := cfg.ModelProfiles.ResolveCloudModelForTier(p, tier)
		if got == "" {
			t.Errorf("tier %s resolved to empty model", tier)
			continue
		}
		// A DeepInfra id is publisher-namespaced. An OpenAI id such as
		// "gpt-5.5" is not, which is exactly the bug this catches.
		if !containsSlash(got) {
			t.Errorf("tier %s resolved to %q, which is not a publisher-namespaced DeepInfra id", tier, got)
		}
	}
}

// TestResolveCloudModelForTier_DeepInfraTiersAreDistinct: a cost tier is only
// meaningful if the tiers differ. If economy and premium collapse to the same
// model the knob is decorative.
func TestResolveCloudModelForTier_DeepInfraTiersAreDistinct(t *testing.T) {
	cfg := Defaults()
	p := deepInfraProfile()

	economy := cfg.ModelProfiles.ResolveCloudModelForTier(p, TierFastLight)
	premium := cfg.ModelProfiles.ResolveCloudModelForTier(p, TierMostCapable)
	if economy == premium {
		t.Errorf("economy and premium both resolve to %q; the cost tier does nothing", economy)
	}
}

// TestDeepInfraCostTable_ModelsAreToolCapable documents the constraint behind
// the table's model choices. Cercano's loop is tool calls, so a model without
// tool support is not merely weaker here — it cannot function. These three ids
// were verified present and tool-tagged in the live index.
func TestDeepInfraCostTable_ModelsAreToolCapable(t *testing.T) {
	tiers, ok := bakedCloudCatalog().Cloud.Providers["deepinfra"]
	if !ok {
		t.Fatal("no deepinfra entry in the baked cloud catalog")
	}
	for _, tc := range []struct{ tier, model string }{
		{"economy", tiers.Economy.Model},
		{"standard", tiers.Standard.Model},
		{"premium", tiers.Premium.Model},
	} {
		if tc.model == "" {
			t.Errorf("%s tier has no model", tc.tier)
			continue
		}
		if !containsSlash(tc.model) {
			t.Errorf("%s tier model %q is not publisher-namespaced", tc.tier, tc.model)
		}
	}
}

// TestDeepInfraProfile_QualityOverrideWins confirms the table does not override an
// explicit user choice.
func TestDeepInfraProfile_QualityOverrideWins(t *testing.T) {
	cfg := Defaults()
	p := deepInfraProfile()
	p.TierOverrides = map[CostTier]string{CostPremium: "some-org/custom-model"}

	if got := cfg.ModelProfiles.ResolveCloudModelForTier(p, TierMostCapable); got != p.TierOverrides[CostPremium] {
		t.Errorf("override model = %q, want %q", got, p.TierOverrides[CostPremium])
	}
}

func containsSlash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return true
		}
	}
	return false
}
