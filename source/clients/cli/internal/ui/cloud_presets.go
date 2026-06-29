package ui

import "cercano/source/server/pkg/agentclient"

type cloudTier int

const (
	tierVerified cloudTier = iota
	tierUntested
	tierComingSoon
	tierCustom
)

// cloudPreset is a known-provider template: pre-filled flavor/backend/base URL.
type cloudPreset struct {
	ID, Label, Flavor, Backend, BaseURL string
	Tier                                cloudTier
}

// cloudPresets returns the known providers, in display order. Base URLs are
// best-effort defaults; the user can edit them in the detail editor.
func cloudPresets() []cloudPreset {
	return []cloudPreset{
		{ID: "anthropic", Label: "anthropic", Flavor: "messages", BaseURL: "", Tier: tierVerified},
		{ID: "openai", Label: "openai", Flavor: "chat_completions", Backend: "openai", BaseURL: "https://api.openai.com/v1", Tier: tierUntested},
		{ID: "gemini", Label: "gemini", Flavor: "chat_completions", Backend: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Tier: tierVerified},
		{ID: "groq", Label: "groq", Flavor: "chat_completions", Backend: "groq", BaseURL: "https://api.groq.com/openai/v1", Tier: tierUntested},
		{ID: "deepinfra", Label: "deepinfra", Flavor: "chat_completions", Backend: "", BaseURL: "https://api.deepinfra.com/v1/openai", Tier: tierUntested},
		{ID: "together", Label: "together", Flavor: "chat_completions", Backend: "", BaseURL: "https://api.together.xyz/v1", Tier: tierUntested},
		{ID: "openrouter", Label: "openrouter", Flavor: "chat_completions", Backend: "", BaseURL: "https://openrouter.ai/api/v1", Tier: tierUntested},
		{ID: "deepseek", Label: "deepseek", Flavor: "chat_completions", Backend: "", BaseURL: "https://api.deepseek.com", Tier: tierUntested},
		{ID: "bedrock", Label: "bedrock", Flavor: "bedrock", BaseURL: "", Tier: tierComingSoon},
		{ID: "openai-responses", Label: "openai (responses)", Flavor: "responses", BaseURL: "https://api.openai.com/v1", Tier: tierComingSoon},
	}
}

// cloudRow is one rendered list entry: a configured profile, a known-provider
// template, or the trailing custom "other" row.
type cloudRow struct {
	ID, Label  string
	Tier       cloudTier
	IsProfile  bool
	HasKey     bool
	Active     bool
	ComingSoon bool
	Preset     *cloudPreset
	Profile    *agentclient.CloudProfileInfo
}

// buildCloudRows merges configured profiles (first, in order) with the preset
// templates (next) and a trailing "other" custom row.
func buildCloudRows(presets []cloudPreset, profiles []agentclient.CloudProfileInfo, active string) []cloudRow {
	rows := make([]cloudRow, 0, len(profiles)+len(presets)+1)
	for i := range profiles {
		p := profiles[i]
		rows = append(rows, cloudRow{
			ID: "profile:" + p.Name, Label: p.Name, Tier: tierCustom,
			IsProfile: true, HasKey: p.HasKey, Active: p.Name == active, Profile: &profiles[i],
		})
	}
	for i := range presets {
		pr := presets[i]
		rows = append(rows, cloudRow{
			ID: "template:" + pr.ID, Label: pr.Label, Tier: pr.Tier,
			ComingSoon: pr.Tier == tierComingSoon, Preset: &presets[i],
		})
	}
	rows = append(rows, cloudRow{ID: "other", Label: "+ other", Tier: tierCustom})
	return rows
}

// rowAnnotation is the right-side status text for a row.
func rowAnnotation(r cloudRow) string {
	if r.ID == "other" {
		return "(custom endpoint)"
	}
	if r.IsProfile {
		s := "— no key"
		if r.HasKey {
			s = "✓ key"
		}
		if r.Active {
			s += "  (active)"
		}
		return s
	}
	switch r.Tier {
	case tierUntested:
		return "(untested)"
	case tierComingSoon:
		return "(coming soon)"
	}
	return ""
}
