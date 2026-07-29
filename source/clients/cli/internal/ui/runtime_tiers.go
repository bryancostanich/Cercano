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
// default_provider knob is gone — so only active-runtime open overrides appear
// here.
var tierSlotOrder = []struct{ Tier, Label string }{
	{"most_capable", "most-capable · open"},
	{"everyday", "everyday · open"},
	{"fast_light", "fast-light · open"},
	{"fast_light_text", "fast-light-text · open"},
	{"embedding", "embedding · open"},
}

func activeTierKey(cfg *agentclient.Config, tier string) string {
	runtime := ""
	if cfg != nil {
		runtime = cfg.OpenRuntime
	}
	if runtime == "" {
		runtime = "llama_server"
	}
	return runtime + "." + tier
}

// tierRows builds the /m "model tiers" section: active-runtime open override
// slots as actionable rows — Enter opens a model picker for that slot.
func tierRows(cfg *agentclient.Config) []runtimeDashboardActionRow {
	if cfg == nil {
		return []runtimeDashboardActionRow{{Label: "model tiers", Value: "config unavailable"}}
	}
	rows := make([]runtimeDashboardActionRow, 0, len(tierSlotOrder))
	for _, slot := range tierSlotOrder {
		key := activeTierKey(cfg, slot.Tier)
		value := cfg.ModelTiers[key]
		if slot.Tier == "embedding" && value == "" {
			// GetConfig still reports the effective embedding model in the legacy
			// EmbeddingModel field for UI compatibility.
			value = cfg.EmbeddingModel
		}
		if value == "" {
			value = "—"
		}
		rows = append(rows, runtimeDashboardActionRow{
			Label:  slot.Label,
			Value:  value,
			Hint:   "enter to change",
			Action: runtimeDashboardAction{Kind: runtimeActionTierPick, TierKey: key},
		})
	}
	return rows
}

// tierPickerRows builds the candidate list for one active-runtime open tier
// override: installed runtime models plus a trailing clear row, with the current
// assignment hinted. (Cloud tiers are not configured here; they resolve via the
// vendor-keyed profile path.)
func tierPickerRows(tierKey string, cfg *agentclient.Config, status *agentclient.RuntimeStatus) []overlay.Row {
	current := ""
	if cfg != nil {
		current = cfg.ModelTiers[tierKey]
	}
	var rows []overlay.Row
	for _, m := range downloadedRuntimeModels(runtimeStatusModels(status)) {
		// Commit the human-readable name, not the runtime's hash ID: the stored
		// value is what the tier list and config.yaml render, and the
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

// embeddingTierPickerRows lists candidates for the embedding slot: the open
// engine's own installed models first (ollama /api/tags — that's what the
// embedding engine serves through today), then downloaded GGUFs from the runtime
// inventory.
func embeddingTierPickerRows(ag *agentclient.Client, tierKey string, cfg *agentclient.Config, status *agentclient.RuntimeStatus) []overlay.Row {
	current := ""
	if cfg != nil {
		current = cfg.ModelTiers[tierKey]
		if current == "" {
			current = cfg.EmbeddingModel
		}
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
		// user-visible, and legacy configs holding an ID still get the current
		// marker.
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

func currentHint(id, current string) string {
	if id != "" && id == current {
		return "current"
	}
	return ""
}

// openTierPicker builds and installs the picker overlay for one active-runtime
// open tier override.
func (d *runtimeDashboard) openTierPicker(tierKey string) {
	var rows []overlay.Row
	if tierKey == activeTierKey(d.snapshot.Config, "embedding") {
		rows = embeddingTierPickerRows(d.agent, tierKey, d.snapshot.Config, d.snapshot.Status)
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
