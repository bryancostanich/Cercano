package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/overlay"
	"cercano/source/server/pkg/agentclient"
)

const runtimeActionTierPick = "tier_pick"

// tierSlotOrder fixes the display order of the /m tiers section: the default
// provider first, then each tier's cloud/open pair, most capable → fastest.
var tierSlotOrder = []struct{ Key, Label string }{
	{"default_provider", "default provider"},
	{"most_capable.cloud", "most-capable · cloud"},
	{"most_capable.open", "most-capable · open"},
	{"everyday.cloud", "everyday · cloud"},
	{"everyday.open", "everyday · open"},
	{"fast_light.cloud", "fast-light · cloud"},
	{"fast_light.open", "fast-light · open"},
	{"fast_light_text.cloud", "fast-light-text · cloud"},
	{"fast_light_text.open", "fast-light-text · open"},
	// The embedding slot is tier UI over the embedding_model config
	// field (single source of truth server-side), not a taxonomy entry.
	{"embedding.open", "embedding · open"},
}

// tierRows builds the /m "model tiers" section: every taxonomy slot as an
// actionable row — Enter opens a model picker for that slot.
func tierRows(cfg *agentclient.Config) []runtimeDashboardActionRow {
	if cfg == nil {
		return []runtimeDashboardActionRow{{Label: "model tiers", Value: "config unavailable"}}
	}
	rows := make([]runtimeDashboardActionRow, 0, len(tierSlotOrder))
	for _, slot := range tierSlotOrder {
		value := ""
		switch slot.Key {
		case "default_provider":
			value = cfg.ModelsDefaultProvider
		case "embedding.open":
			value = cfg.EmbeddingModel
		default:
			value = cfg.ModelTiers[slot.Key]
		}
		if value == "" {
			value = "—"
		}
		rows = append(rows, runtimeDashboardActionRow{
			Label:  slot.Label,
			Value:  value,
			Hint:   "enter to change",
			Action: runtimeDashboardAction{Kind: runtimeActionTierPick, TierKey: slot.Key},
		})
	}
	return rows
}

// tierPickerRows builds the candidate list for one slot. default_provider
// offers the two sides; .open slots offer the installed runtime models;
// .cloud slots are filled by the caller from the live catalog (with the
// static fallback) — this function only handles the locally-known kinds and
// the shared trailing clear row. The current assignment is hinted.
func tierPickerRows(tierKey string, cfg *agentclient.Config, status *agentclient.RuntimeStatus) []overlay.Row {
	if tierKey == "default_provider" {
		current := ""
		if cfg != nil {
			current = cfg.ModelsDefaultProvider
		}
		return []overlay.Row{
			{Key: "cloud", Label: "cloud", Value: "hosted API side", Hint: currentHint("cloud", current)},
			{Key: "open", Label: "open", Value: "open-weight side", Hint: currentHint("open", current)},
		}
	}
	current := ""
	if cfg != nil {
		current = cfg.ModelTiers[tierKey]
	}
	var rows []overlay.Row
	for _, m := range downloadedRuntimeModels(runtimeStatusModels(status)) {
		rows = append(rows, overlay.Row{
			Key:   m.ID,
			Label: firstNonEmpty(m.DisplayName, m.ID),
			Value: m.Runtime,
			Hint:  currentHint(m.ID, current),
		})
	}
	rows = append(rows, overlay.Row{Key: "-", Label: "(clear)", Value: "unset this slot"})
	return rows
}

// embeddingTierPickerRows lists candidates for the embedding slot: the
// open engine's own installed models first (ollama /api/tags — that's
// what the embedding engine serves through today), then downloaded
// GGUFs from the runtime inventory.
func embeddingTierPickerRows(ag *agentclient.Client, cfg *agentclient.Config, status *agentclient.RuntimeStatus) []overlay.Row {
	current := ""
	if cfg != nil {
		current = cfg.EmbeddingModel
	}
	var rows []overlay.Row
	if ag != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if models, err := ag.ListModels(ctx); err == nil {
			for _, m := range models {
				rows = append(rows, overlay.Row{
					Key:   m.Name,
					Label: m.Name,
					Value: formatBytes(m.SizeBytes),
					Hint:  currentHint(m.Name, current),
				})
			}
		}
		cancel()
	}
	for _, m := range downloadedRuntimeModels(runtimeStatusModels(status)) {
		rows = append(rows, overlay.Row{
			Key:   m.ID,
			Label: firstNonEmpty(m.DisplayName, m.ID),
			Value: m.Runtime,
			Hint:  currentHint(m.ID, current),
		})
	}
	rows = append(rows, overlay.Row{Key: "-", Label: "(clear)", Value: "unset embedding model"})
	return rows
}

// cloudTierPickerRows builds .cloud slot candidates from a fetched catalog,
// falling back to the curated static Claude list when the fetch failed.
func cloudTierPickerRows(tierKey string, cfg *agentclient.Config, catalog []agentclient.CloudModelInfo) []overlay.Row {
	current := ""
	if cfg != nil {
		current = cfg.ModelTiers[tierKey]
	}
	models := catalog
	if len(models) == 0 {
		models = fallbackClaudeModels()
	}
	rows := make([]overlay.Row, 0, len(models)+1)
	for _, m := range models {
		rows = append(rows, overlay.Row{
			Key:   m.ID,
			Label: firstNonEmpty(m.DisplayName, m.ID),
			Hint:  currentHint(m.ID, current),
		})
	}
	rows = append(rows, overlay.Row{Key: "-", Label: "(clear)", Value: "unset this slot"})
	return rows
}

func currentHint(id, current string) string {
	if id != "" && id == current {
		return "current"
	}
	return ""
}

// isCloudTierKey reports whether the slot belongs to the cloud side.
func isCloudTierKey(key string) bool {
	return len(key) > len(".cloud") && key[len(key)-len(".cloud"):] == ".cloud"
}

// openTierPicker builds and installs the picker overlay for one tier slot.
// Cloud slots fetch the live model catalog through the active profile (3s
// budget, static fallback on failure) — same source the cloud settings use.
func (d *runtimeDashboard) openTierPicker(tierKey string) {
	var rows []overlay.Row
	if tierKey == "embedding.open" {
		rows = embeddingTierPickerRows(d.agent, d.snapshot.Config, d.snapshot.Status)
	} else if isCloudTierKey(tierKey) {
		var catalog []agentclient.CloudModelInfo
		if d.agent != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			if _, active, err := d.agent.GetCloudProfiles(ctx); err == nil && active != "" {
				if models, _, err := d.agent.ListCloudProfileModels(ctx, active); err == nil {
					catalog = models
				}
			}
			cancel()
		}
		rows = cloudTierPickerRows(tierKey, d.snapshot.Config, catalog)
	} else {
		rows = tierPickerRows(tierKey, d.snapshot.Config, d.snapshot.Status)
	}

	hooks := overlay.Hooks{
		OnSelect: func(row overlay.Row) (string, bool, tea.Cmd) {
			if d.agent == nil {
				return "no agent connection", false, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			msg, err := d.agent.UpdateConfig(ctx, agentclient.ConfigUpdate{
				ModelTierKey: tierKey, ModelTierValue: row.Key,
			})
			if err != nil {
				return "set failed: " + err.Error(), false, nil
			}
			return msg, true, nil
		},
	}
	picker := overlay.New("model tier — "+tierKey, rows, hooks)
	d.tierPicker = &picker
}
