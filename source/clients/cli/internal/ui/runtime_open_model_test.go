package ui

import (
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestOpenModelRowsNilConfig(t *testing.T) {
	rows := openModelRows(nil)
	if len(rows) != 1 {
		t.Fatalf("nil config: want 1 row, got %d", len(rows))
	}
	if rows[0].Value != "config unavailable" {
		t.Fatalf("nil config value: got %q", rows[0].Value)
	}
	if rows[0].Action.Kind != "" {
		t.Fatal("nil config: row should not be actionable")
	}
}

func TestOpenModelRowsValues(t *testing.T) {
	cfg := &agentclient.Config{
		OpenRuntime: "ollama",
		OpenModel:   "qwen3:latest",
		OllamaURL:   "http://localhost:11434",
	}
	rows := openModelRows(cfg)
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if rows[0].Label != "runtime" || rows[0].Value != "ollama" {
		t.Errorf("runtime row: label=%q value=%q", rows[0].Label, rows[0].Value)
	}
	if rows[0].Action.Kind != runtimeActionOpenRuntimePick {
		t.Errorf("runtime action kind: %q", rows[0].Action.Kind)
	}
	if rows[1].Label != "chat model" || rows[1].Value != "qwen3:latest" {
		t.Errorf("chat model row: label=%q value=%q", rows[1].Label, rows[1].Value)
	}
	if rows[1].Action.Kind != runtimeActionOpenModelPick {
		t.Errorf("chat model action kind: %q", rows[1].Action.Kind)
	}
	if rows[2].Label != "ollama URL" || rows[2].Value != "http://localhost:11434" {
		t.Errorf("ollama URL row: label=%q value=%q", rows[2].Label, rows[2].Value)
	}
	if rows[2].Action.Kind != runtimeActionOllamaURL {
		t.Errorf("ollama URL action kind: %q", rows[2].Action.Kind)
	}
}

func TestOpenModelRowsDefaultRuntime(t *testing.T) {
	rows := openModelRows(&agentclient.Config{})
	if rows[0].Value != "ollama" {
		t.Errorf("empty OpenRuntime should default to ollama, got %q", rows[0].Value)
	}
}

func TestOpenModelRowsEmptyModelAndURL(t *testing.T) {
	rows := openModelRows(&agentclient.Config{})
	if rows[1].Value != "—" {
		t.Errorf("empty chat model should show —, got %q", rows[1].Value)
	}
	if rows[2].Value != "—" {
		t.Errorf("empty ollama URL should show —, got %q", rows[2].Value)
	}
}

func TestOpenRuntimeSwitchCmdNilAgent(t *testing.T) {
	cmd := openRuntimeSwitchCmd(nil, "ollama")
	if cmd != nil {
		t.Fatal("nil agent should return nil cmd")
	}
}
