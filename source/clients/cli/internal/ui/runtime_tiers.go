package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/overlay"
	"cercano/source/server/pkg/agentclient"
)

const runtimeActionTierPick = "tier_pick"

// tierSlotOrder fixes the display order of the /m tiers section: each open
// tier, most capable → fastest. Cloud is configured through its own
// vendor-keyed profile path (not per-tier slots), and the retired
// default_provider knob is gone — so only open slots appear here.
var tierSlotOrder = []struct{ Key, Label string }{
	{"most_capable.open", "most-capable · open"},
	{"everyday.open", "everyday · open"},
	{"fast_light.open", "fast-light · open"},
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

// tierPickerRows builds the candidate list for one open tier slot: the
// installed runtime models plus a trailing clear row, with the current
// assignment hinted. (Cloud tiers are not configured here; they resolve via
// the vendor-keyed profile path.)
func tierPickerRows(tierKey string, cfg *agentclient.Config, status *agentclient.RuntimeStatus) []overlay.Row {
	current := ""
	if cfg != nil {
		current = cfg.ModelTiers[tierKey]
	}
	var rows []overlay.Row
	for _, m := range downloadedRuntimeModels(runtimeStatusModels(status)) {
		// Commit the human-readable name, not the runtime's hash ID: the
		// stored value is what the tier list and config.yaml render, and the
		// path-derived ID goes stale when the file moves. Legacy configs may
		// still hold an ID, so the current-marker checks both.
		name := firstNonEmpty(m.DisplayName, m.ID)
		hint := currentHint(name, current)
		if hint == "" {
			hint = currentHint(m.ID, current)
		}
		rows = append(rows, overlay.Row{
			Key:   name,
			Label: name,
			Value: m.Runtime,
			Hint:  hint,
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
		// Same name-over-ID rule as tierPickerRows: the stored value is
		// user-visible, and legacy configs holding an ID still get the
		// current marker.
		name := firstNonEmpty(m.DisplayName, m.ID)
		hint := currentHint(name, current)
		if hint == "" {
			hint = currentHint(m.ID, current)
		}
		rows = append(rows, overlay.Row{
			Key:   name,
			Label: name,
			Value: m.Runtime,
			Hint:  hint,
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
