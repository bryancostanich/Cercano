package ui

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestCloudPresetsCoverAllProviders(t *testing.T) {
	presets := cloudPresets()
	got := map[string]cloudPreset{}
	for _, p := range presets {
		got[p.ID] = p
	}

	// Assert exactly 10 presets exist.
	if len(got) != 10 {
		t.Fatalf("expected 10 presets, got %d", len(got))
	}

	// Table of expected values for each preset.
	expectations := []struct {
		id      string
		flavor  string
		backend string
		baseURL string
		tier    cloudTier
	}{
		{id: "anthropic", flavor: "messages", backend: "", baseURL: "", tier: tierVerified},
		{id: "openai", flavor: "chat_completions", backend: "openai", baseURL: "https://api.openai.com/v1", tier: tierUntested},
		{id: "gemini", flavor: "chat_completions", backend: "gemini", baseURL: "https://generativelanguage.googleapis.com/v1beta/openai", tier: tierVerified},
		{id: "groq", flavor: "chat_completions", backend: "groq", baseURL: "https://api.groq.com/openai/v1", tier: tierUntested},
		{id: "deepinfra", flavor: "chat_completions", backend: "", baseURL: "https://api.deepinfra.com/v1/openai", tier: tierUntested},
		{id: "together", flavor: "chat_completions", backend: "", baseURL: "https://api.together.xyz/v1", tier: tierUntested},
		{id: "openrouter", flavor: "chat_completions", backend: "", baseURL: "https://openrouter.ai/api/v1", tier: tierUntested},
		{id: "deepseek", flavor: "chat_completions", backend: "", baseURL: "https://api.deepseek.com", tier: tierUntested},
		{id: "bedrock", flavor: "bedrock", backend: "", baseURL: "", tier: tierComingSoon},
		{id: "openai-responses", flavor: "responses", backend: "", baseURL: "https://api.openai.com/v1", tier: tierComingSoon},
	}

	for _, exp := range expectations {
		preset, ok := got[exp.id]
		if !ok {
			t.Errorf("missing preset %q", exp.id)
			continue
		}
		if preset.Flavor != exp.flavor {
			t.Errorf("preset %q flavor: want %q, got %q", exp.id, exp.flavor, preset.Flavor)
		}
		if preset.Backend != exp.backend {
			t.Errorf("preset %q backend: want %q, got %q", exp.id, exp.backend, preset.Backend)
		}
		if preset.BaseURL != exp.baseURL {
			t.Errorf("preset %q baseURL: want %q, got %q", exp.id, exp.baseURL, preset.BaseURL)
		}
		if preset.Tier != exp.tier {
			t.Errorf("preset %q tier: want %v, got %v", exp.id, exp.tier, preset.Tier)
		}
	}
}

func TestBuildCloudRowsOrderAndStatus(t *testing.T) {
	profiles := []agentclient.CloudProfileInfo{
		{Name: "work-openai", Flavor: "chat_completions", Backend: "openai", BaseURL: "u", Model: "m", HasKey: true},
	}
	rows := buildCloudRows(cloudPresets(), profiles, "work-openai")
	if rows[0].ID != "profile:work-openai" || !rows[0].IsProfile {
		t.Fatalf("first row should be the configured profile, got %+v", rows[0])
	}
	if !rows[0].Active || !rows[0].HasKey {
		t.Error("configured active profile row should be Active + HasKey")
	}
	if rows[len(rows)-1].ID != "other" {
		t.Fatalf("last row should be 'other', got %q", rows[len(rows)-1].ID)
	}
	// A template row for each preset exists.
	haveTemplate := map[string]bool{}
	for _, r := range rows {
		if strings.HasPrefix(r.ID, "template:") {
			haveTemplate[strings.TrimPrefix(r.ID, "template:")] = true
		}
	}
	if !haveTemplate["bedrock"] || !haveTemplate["gemini"] {
		t.Error("expected template rows for bedrock and gemini")
	}
}

func TestRowAnnotation(t *testing.T) {
	tests := []struct {
		name     string
		row      cloudRow
		expected string
	}{
		{
			name:     "profile row with key + active",
			row:      cloudRow{ID: "profile:x", IsProfile: true, HasKey: true, Active: true},
			expected: "✓ key  (active)",
		},
		{
			name:     "profile row without key, not active",
			row:      cloudRow{ID: "profile:x", IsProfile: true, HasKey: false, Active: false},
			expected: "— no key",
		},
		{
			name:     "profile row with model + key + active",
			row:      cloudRow{ID: "profile:x", IsProfile: true, HasKey: true, Active: true, Profile: &agentclient.CloudProfileInfo{Model: "claude-opus-4-8"}},
			expected: "claude-opus-4-8  ✓ key  (active)",
		},
		{
			name:     "profile row with meridian route hides no-key text",
			row:      cloudRow{ID: "profile:x", IsProfile: true, HasKey: false, Active: true, Profile: &agentclient.CloudProfileInfo{Model: "claude-opus-4-8", Route: "meridian"}},
			expected: "claude-opus-4-8  meridian  (active)",
		},
		{
			name:     "profile row with meridian route and no model",
			row:      cloudRow{ID: "profile:x", IsProfile: true, HasKey: false, Active: false, Profile: &agentclient.CloudProfileInfo{Route: "meridian"}},
			expected: "— no model  meridian",
		},
		{
			name:     "other row",
			row:      cloudRow{ID: "other"},
			expected: "(custom endpoint)",
		},
		{
			name:     "template row (verified tier)",
			row:      cloudRow{ID: "template:gemini", Tier: tierVerified},
			expected: "",
		},
		{
			name:     "template row (coming soon)",
			row:      cloudRow{ID: "template:bedrock", Tier: tierComingSoon, ComingSoon: true},
			expected: "(coming soon)",
		},
		{
			name:     "template row (untested)",
			row:      cloudRow{ID: "template:openai", Tier: tierUntested},
			expected: "(untested)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rowAnnotation(tt.row)
			if got != tt.expected {
				t.Errorf("annotation: want %q, got %q", tt.expected, got)
			}
		})
	}
}
