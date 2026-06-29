package ui

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestCloudPresetsCoverAllProviders(t *testing.T) {
	got := map[string]cloudPreset{}
	for _, p := range cloudPresets() {
		got[p.ID] = p
	}
	for _, id := range []string{"anthropic", "openai", "gemini", "groq", "deepinfra", "together", "openrouter", "deepseek", "bedrock", "openai-responses"} {
		if _, ok := got[id]; !ok {
			t.Errorf("missing preset %q", id)
		}
	}
	if got["gemini"].BaseURL != "https://generativelanguage.googleapis.com/v1beta/openai" {
		t.Errorf("gemini base URL wrong: %q", got["gemini"].BaseURL)
	}
	if got["gemini"].Tier != tierVerified || got["anthropic"].Tier != tierVerified {
		t.Error("anthropic and gemini must be tierVerified")
	}
	if got["openai"].Tier != tierUntested {
		t.Error("openai must be tierUntested")
	}
	if got["bedrock"].Tier != tierComingSoon || got["openai-responses"].Tier != tierComingSoon {
		t.Error("bedrock and openai-responses must be tierComingSoon")
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
	profileRow := cloudRow{ID: "profile:x", IsProfile: true, HasKey: true, Active: true}
	a := rowAnnotation(profileRow)
	if !strings.Contains(a, "✓ key") || !strings.Contains(a, "active") {
		t.Errorf("profile annotation wrong: %q", a)
	}
	tmpl := cloudRow{ID: "template:bedrock", Tier: tierComingSoon, ComingSoon: true}
	if rowAnnotation(tmpl) != "(coming soon)" {
		t.Errorf("coming-soon annotation wrong: %q", rowAnnotation(tmpl))
	}
	tmpl2 := cloudRow{ID: "template:openai", Tier: tierUntested}
	if rowAnnotation(tmpl2) != "(untested)" {
		t.Errorf("untested annotation wrong: %q", rowAnnotation(tmpl2))
	}
}
