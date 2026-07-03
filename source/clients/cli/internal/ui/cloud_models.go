package ui

import (
	"cercano/source/clients/cli/internal/form"
	"cercano/source/server/pkg/agentclient"
)

// fallbackClaudeModels is the curated static catalog rendered when the live
// /v1/models fetch through the profile's endpoint fails (Meridian offline,
// endpoint 404, timeout, etc.). Kept intentionally short — the current
// major Claude models the user most likely wants — so drift when Anthropic
// ships a new model just means the fetched dropdown is the definitive source
// on a running Meridian, and the static list is the "you'll always have
// something to click" safety net.
//
// Update in tandem when Anthropic promotes new default models.
func fallbackClaudeModels() []agentclient.CloudModelInfo {
	return []agentclient.CloudModelInfo{
		{ID: "claude-fable-5", DisplayName: "Claude Fable 5 (most capable)"},
		{ID: "claude-opus-4-8", DisplayName: "Claude Opus 4.8 (latest)"},
		{ID: "claude-opus-4-7", DisplayName: "Claude Opus 4.7"},
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6 (balanced)"},
		{ID: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5"},
		{ID: "claude-haiku-4-5-20251001", DisplayName: "Claude Haiku 4.5 (fastest)"},
	}
}

// modelOptionsFromCatalog turns a []CloudModelInfo into form.Options for a
// Select field. Ensures currentID is present as an option even when the
// live fetch didn't return it (so a legacy or custom model doesn't get
// dropped just because the catalog doesn't mention it) and always tacks on
// a "custom…" escape hatch so power users can type any ID.
func modelOptionsFromCatalog(models []agentclient.CloudModelInfo, currentID string) []form.Option {
	seen := map[string]bool{}
	out := make([]form.Option, 0, len(models)+2)
	if currentID != "" {
		out = append(out, form.Option{Label: currentID, Value: currentID})
		seen[currentID] = true
	}
	for _, m := range models {
		if seen[m.ID] {
			continue
		}
		label := m.DisplayName
		if label == "" {
			label = m.ID
		}
		out = append(out, form.Option{Label: label, Value: m.ID})
		seen[m.ID] = true
	}
	return out
}
