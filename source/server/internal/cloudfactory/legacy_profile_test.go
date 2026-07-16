package cloudfactory

import "testing"

// TestLegacyProfile pins the vendor→wire-flavor mapping that replaced the
// langchain CloudModelProvider: the loose (provider, model, baseURL) triple the
// old CloudFactory carried maps onto a CloudProfile the inference path builds.
func TestLegacyProfile(t *testing.T) {
	cases := []struct {
		provider    string
		wantFlavor  string
		wantBackend string
	}{
		{"anthropic", FlavorMessages, ""},
		{"Anthropic", FlavorMessages, ""}, // case-insensitive
		{"openai", FlavorChatCompletions, "openai"},
		{"google", FlavorChatCompletions, "gemini"},
		{"gemini", FlavorChatCompletions, "gemini"},
		{"groq", FlavorChatCompletions, "groq"}, // unknown → chat_completions, provider as backend hint
	}
	for _, c := range cases {
		p := LegacyProfile(c.provider, "some-model", "")
		if p.Flavor != c.wantFlavor {
			t.Errorf("LegacyProfile(%q).Flavor = %q, want %q", c.provider, p.Flavor, c.wantFlavor)
		}
		if p.Backend != c.wantBackend {
			t.Errorf("LegacyProfile(%q).Backend = %q, want %q", c.provider, p.Backend, c.wantBackend)
		}
		if p.Model != "some-model" {
			t.Errorf("model should carry through, got %q", p.Model)
		}
	}
}

// TestLegacyProfile_BaseURLOverride proves a proxy baseURL (Meridian and other
// Anthropic-compatible local proxies) is preserved.
func TestLegacyProfile_BaseURLOverride(t *testing.T) {
	p := LegacyProfile("anthropic", "claude", "http://localhost:9000")
	if p.BaseURL != "http://localhost:9000" {
		t.Errorf("BaseURL = %q, want the proxy URL", p.BaseURL)
	}
	if p.Flavor != FlavorMessages {
		t.Errorf("Flavor = %q, want messages", p.Flavor)
	}
}
