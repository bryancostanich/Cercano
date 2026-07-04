package ui

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

// TestTierRows pins the /m tiers section shape: a default-provider row plus
// one row per tier×provider slot, each carrying a tier-pick action, with
// configured values shown and empty slots dashed.
func TestTierRows(t *testing.T) {
	cfg := &agentclient.Config{
		ModelsDefaultProvider: "open",
		ModelTiers: map[string]string{
			"fast_light_text.open": "phi4:14b",
		},
	}
	rows := tierRows(cfg)
	if len(rows) != 10 {
		t.Fatalf("rows = %d, want 10 (default provider + 4 tiers × 2 providers + embedding)", len(rows))
	}
	if rows[0].Action.TierKey != "default_provider" || rows[0].Value != "open" {
		t.Errorf("row 0 = %+v, want the default-provider row", rows[0])
	}
	var sawConfigured, sawEmpty bool
	for _, r := range rows {
		if r.Action.Kind != runtimeActionTierPick {
			t.Errorf("row %q missing tier-pick action: %+v", r.Label, r.Action)
		}
		if r.Action.TierKey == "fast_light_text.open" {
			sawConfigured = true
			if r.Value != "phi4:14b" {
				t.Errorf("configured slot value = %q", r.Value)
			}
		}
		if r.Action.TierKey == "everyday.cloud" {
			sawEmpty = true
			if r.Value != "—" {
				t.Errorf("empty slot should render a dash, got %q", r.Value)
			}
		}
	}
	if !sawConfigured || !sawEmpty {
		t.Error("expected both a configured and an empty slot row")
	}

	// Nil config degrades to a single informational row, no actions.
	if rows := tierRows(nil); len(rows) != 1 || rows[0].Action.Kind != "" {
		t.Errorf("nil config rows = %+v, want one info row", rows)
	}
}

// TestTierPickerRows pins the picker candidates per slot kind: provider
// choices for default_provider; installed runtime models plus a clear row for
// .open slots; the current value is hinted.
func TestTierPickerRows(t *testing.T) {
	cfg := &agentclient.Config{
		ModelTiers: map[string]string{"fast_light_text.open": "phi4:14b"},
	}
	status := &agentclient.RuntimeStatus{
		Models: []agentclient.RuntimeModel{
			{ID: "phi4:14b", Runtime: "ollama", DownloadState: "downloaded"},
			{ID: "qwen3-coder:latest", Runtime: "ollama", DownloadState: "downloaded"},
		},
	}

	rows := tierPickerRows("default_provider", cfg, status)
	if len(rows) != 2 || rows[0].Key != "cloud" || rows[1].Key != "open" {
		t.Fatalf("default_provider rows = %+v", rows)
	}

	rows = tierPickerRows("fast_light_text.open", cfg, status)
	var keys []string
	var currentHinted bool
	for _, r := range rows {
		keys = append(keys, r.Key)
		if r.Key == "phi4:14b" && strings.Contains(r.Hint, "current") {
			currentHinted = true
		}
	}
	joined := strings.Join(keys, ",")
	if !strings.Contains(joined, "phi4:14b") || !strings.Contains(joined, "qwen3-coder:latest") {
		t.Errorf("open picker missing installed models: %v", keys)
	}
	if keys[len(keys)-1] != "-" {
		t.Errorf("last row should be the clear entry, got %v", keys)
	}
	if !currentHinted {
		t.Error("current assignment should carry a 'current' hint")
	}
}
