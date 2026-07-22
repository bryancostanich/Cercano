package ui

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

// sampleProvidersView mirrors what the agent's GetCloudProviders returns: the
// known-provider catalog with configured profiles grouped underneath. Anthropic
// carries two accounts (the active one primary); other providers are bare
// templates; plus one custom endpoint.
func sampleProvidersView() agentclient.CloudProvidersView {
	return agentclient.CloudProvidersView{
		Active: "work-anthropic",
		Providers: []agentclient.CloudProvider{
			{ID: "anthropic", Label: "anthropic", Flavor: "messages", Tier: "verified",
				PrimaryProfile: "work-anthropic",
				Profiles: []agentclient.CloudProfileInfo{
					{Name: "work-anthropic", Flavor: "messages", Route: "subscription", Model: "claude-opus-4-8"},
					{Name: "personal-anthropic", Flavor: "messages", HasKey: true, Model: "claude-sonnet"},
				}},
			{ID: "openai-responses", Label: "openai (responses)", Flavor: "responses", BaseURL: "https://api.openai.com/v1", Tier: "untested"},
			{ID: "openai", Label: "openai", Flavor: "chat_completions", Backend: "openai", BaseURL: "https://api.openai.com/v1", Tier: "untested"},
			{ID: "gemini", Label: "gemini", Flavor: "chat_completions", Backend: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Tier: "verified"},
			{ID: "bedrock", Label: "bedrock", Flavor: "bedrock", Tier: "coming_soon"},
		},
		CustomProfiles: []agentclient.CloudProfileInfo{
			{Name: "my-llm", Flavor: "chat_completions", BaseURL: "https://api.example.com/v1", HasKey: true},
		},
	}
}

func TestBuildCloudRowsFromProvidersMergesAndDedupes(t *testing.T) {
	rows := buildCloudRowsFromProviders(sampleProvidersView())
	byID := map[string]cloudRow{}
	var labels []string
	for _, r := range rows {
		byID[r.ID] = r
		labels = append(labels, r.Label)
	}

	// Merged anthropic provider row: friendly provider label, primary = the
	// active meridian profile, and NO duplicate template row.
	m, ok := byID["profile:work-anthropic"]
	if !ok || m.Label != "anthropic" || !m.IsProfile || !m.Active {
		t.Fatalf("merged anthropic row wrong: %+v (present=%v)", m, ok)
	}
	if _, dup := byID["template:anthropic"]; dup {
		t.Error("anthropic must not render both a profile row and a template row")
	}

	// Extra anthropic account: an indented sub-row, not active.
	sub, ok := byID["profile:personal-anthropic"]
	if !ok || !sub.SubProfile || sub.Active || sub.Label != profileSubIndent+"personal-anthropic" {
		t.Errorf("extra profile sub-row wrong: %+v (present=%v)", sub, ok)
	}

	// A provider with no configured profiles renders a bare template row.
	if _, ok := byID["template:gemini"]; !ok {
		t.Error("gemini (no profiles) should render a template row")
	}
	if b, ok := byID["template:bedrock"]; !ok || !b.ComingSoon {
		t.Errorf("bedrock should render a coming-soon template row: %+v (present=%v)", b, ok)
	}

	// Custom (unmatched) profile gets its own row.
	if _, ok := byID["profile:my-llm"]; !ok {
		t.Error("custom profile should render its own row")
	}

	// Trailing "other" row.
	if rows[len(rows)-1].ID != "other" {
		t.Errorf("last row should be other, got %q", rows[len(rows)-1].ID)
	}

	// Exactly one row labeled "anthropic" — the dedup guarantee.
	n := 0
	for _, l := range labels {
		if l == "anthropic" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("expected exactly one 'anthropic' row, got %d (labels=%v)", n, labels)
	}
}

func TestRowAnnotation(t *testing.T) {
	tests := []struct {
		name     string
		row      cloudRow
		expected string
	}{
		{
			name:     "primary direct key → plain primary",
			row:      cloudRow{ID: "profile:x", IsProfile: true, HasKey: true, Active: true},
			expected: "primary",
		},
		{
			name:     "inactive without key",
			row:      cloudRow{ID: "profile:x", IsProfile: true, HasKey: false, Active: false},
			expected: "— no key",
		},
		{
			name:     "primary with model, direct key",
			row:      cloudRow{ID: "profile:x", IsProfile: true, HasKey: true, Active: true, Profile: &agentclient.CloudProfileInfo{Model: "claude-opus-4-8"}},
			expected: "claude-opus-4-8  primary",
		},
		{
			name:     "primary subscription → primary (subscription)",
			row:      cloudRow{ID: "profile:x", IsProfile: true, Active: true, Profile: &agentclient.CloudProfileInfo{Model: "claude-opus-4-8", Route: "subscription"}},
			expected: "claude-opus-4-8  primary (subscription)",
		},
		{
			name:     "primary responses → primary (ChatGPT OAuth)",
			row:      cloudRow{ID: "profile:x", IsProfile: true, Active: true, Profile: &agentclient.CloudProfileInfo{Model: "gpt-5.5", Flavor: "responses"}},
			expected: "gpt-5.5  primary (ChatGPT OAuth)",
		},
		{
			name:     "inactive subscription shows route as auth hint",
			row:      cloudRow{ID: "profile:x", IsProfile: true, Active: false, Profile: &agentclient.CloudProfileInfo{Route: "subscription"}},
			expected: "— no model  subscription",
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

func TestBackupMarkerOnRows(t *testing.T) {
	view := agentclient.CloudProvidersView{
		Providers: []agentclient.CloudProvider{
			{ID: "anthropic", Label: "anthropic", Tier: "verified", PrimaryProfile: "anthropic",
				Profiles: []agentclient.CloudProfileInfo{{Name: "anthropic", Flavor: "messages", Model: "claude-fable-5"}}},
			{ID: "openai", Label: "openai", Tier: "untested", PrimaryProfile: "openai",
				Profiles: []agentclient.CloudProfileInfo{{Name: "openai", Flavor: "chat_completions", Backend: "openai", Model: "gpt-5.5", HasKey: true}}},
		},
		Active: "anthropic",
		Backup: "openai",
	}
	rows := buildCloudRowsFromProviders(view)
	var openaiRow, anthropicRow *cloudRow
	for i := range rows {
		switch rows[i].ID {
		case "profile:openai":
			openaiRow = &rows[i]
		case "profile:anthropic":
			anthropicRow = &rows[i]
		}
	}
	if openaiRow == nil || !openaiRow.Backup {
		t.Fatalf("openai row should carry Backup, got %+v", openaiRow)
	}
	if anthropicRow.Backup {
		t.Fatal("primary anthropic row must not carry Backup")
	}
	if ann := rowAnnotation(*openaiRow); !strings.Contains(ann, "backup") {
		t.Fatalf("backup row annotation %q missing marker", ann)
	}
	if ann := rowAnnotation(*anthropicRow); strings.Contains(ann, "backup") {
		t.Fatalf("primary row annotation %q must not carry backup marker", ann)
	}
}
