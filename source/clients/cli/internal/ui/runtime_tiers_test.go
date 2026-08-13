package ui

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

// TestTierRows pins the /m tiers section shape: one row per open tier slot,
// each carrying a tier-pick action, with configured values shown and empty
// slots dashed. (No default-provider row; cloud is not a tier slot.)
func TestTierRows(t *testing.T) {
	cfg := &agentclient.Config{
		OpenRuntime: "llama_server",
		ModelTiers: map[string]string{
			"llama_server.fast_light_text": "phi4:14b",
		},
	}
	rows := tierRows(cfg)
	if len(rows) != 6 {
		t.Fatalf("rows = %d, want 6 (4 chat tiers + vision + embedding)", len(rows))
	}
	var sawConfigured, sawEmpty, sawVision bool
	for _, r := range rows {
		if r.Action.Kind != runtimeActionTierPick {
			t.Errorf("row %q missing tier-pick action: %+v", r.Label, r.Action)
		}
		if r.Action.TierKey == "llama_server.fast_light_text" {
			sawConfigured = true
			if r.Value != "phi4:14b" {
				t.Errorf("configured slot value = %q", r.Value)
			}
		}
		if r.Action.TierKey == "llama_server.most_capable" {
			sawEmpty = true
			if r.Value != "—" {
				t.Errorf("empty slot should render a dash, got %q", r.Value)
			}
		}
		if r.Action.TierKey == "llama_server.vision" {
			sawVision = true
			if r.Label != "vision · open" {
				t.Errorf("vision label = %q", r.Label)
			}
			if r.Value != "—" {
				t.Errorf("unset vision slot should render a dash, got %q", r.Value)
			}
		}
	}
	if !sawConfigured || !sawEmpty || !sawVision {
		t.Error("expected configured, empty, and vision slot rows")
	}

	// Nil config degrades to a single informational row, no actions.
	if rows := tierRows(nil); len(rows) != 1 || rows[0].Action.Kind != "" {
		t.Errorf("nil config rows = %+v, want one info row", rows)
	}
}

// TestTierPickerRows pins the picker candidates for an open slot: installed
// runtime models plus a clear row, with the current value hinted.
func TestTierPickerRows(t *testing.T) {
	cfg := &agentclient.Config{
		OpenRuntime: "llama_server",
		ModelTiers:  map[string]string{"llama_server.fast_light_text": "phi4:14b"},
	}
	status := &agentclient.RuntimeStatus{
		Models: []agentclient.RuntimeModel{
			{ID: "phi4:14b", Runtime: "ollama", DownloadState: "downloaded"},
			{ID: "qwen3-coder:latest", Runtime: "ollama", DownloadState: "downloaded"},
		},
	}

	rows := tierPickerRows("llama_server.fast_light_text", cfg, status)
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
