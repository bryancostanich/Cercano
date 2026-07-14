package ui

import (
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestRuntimeConfigRowsNilConfig(t *testing.T) {
	rows := runtimeConfigRows(nil)
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

func TestRuntimeConfigRowsOllama(t *testing.T) {
	cfg := &agentclient.Config{
		OpenRuntime: "ollama",
		OllamaURL:   "http://localhost:11434",
	}
	rows := runtimeConfigRows(cfg)
	// runtime + ollama URL — the legacy "chat model" row is gone.
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0].Label != "runtime" || rows[0].Value != "ollama" {
		t.Errorf("runtime row: label=%q value=%q", rows[0].Label, rows[0].Value)
	}
	if rows[0].Action.Kind != runtimeActionOpenRuntimePick {
		t.Errorf("runtime action kind: %q", rows[0].Action.Kind)
	}
	if rows[1].Label != "ollama URL" || rows[1].Value != "http://localhost:11434" {
		t.Errorf("ollama URL row: label=%q value=%q", rows[1].Label, rows[1].Value)
	}
	if rows[1].Action.Kind != runtimeActionOllamaURL {
		t.Errorf("ollama URL action kind: %q", rows[1].Action.Kind)
	}
}

func TestRuntimeConfigRowsLlamaServerHidesOllamaURL(t *testing.T) {
	rows := runtimeConfigRows(&agentclient.Config{OpenRuntime: "llama_server"})
	if len(rows) != 1 {
		t.Fatalf("llama_server: want 1 row (no ollama URL), got %d", len(rows))
	}
	if rows[0].Value != "llama_server" {
		t.Errorf("runtime row value: %q", rows[0].Value)
	}
}

func TestRuntimeConfigRowsDefaultRuntime(t *testing.T) {
	rows := runtimeConfigRows(&agentclient.Config{})
	if rows[0].Value != "ollama" {
		t.Errorf("empty OpenRuntime should default to ollama, got %q", rows[0].Value)
	}
}

func TestRuntimeConfigRowsEmptyURL(t *testing.T) {
	rows := runtimeConfigRows(&agentclient.Config{OpenRuntime: "ollama"})
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[1].Value != "—" {
		t.Errorf("empty ollama URL should show —, got %q", rows[1].Value)
	}
}

func TestOpenRuntimeSwitchCmdNilAgent(t *testing.T) {
	cmd := openRuntimeSwitchCmd(nil, "ollama")
	if cmd != nil {
		t.Fatal("nil agent should return nil cmd")
	}
}
