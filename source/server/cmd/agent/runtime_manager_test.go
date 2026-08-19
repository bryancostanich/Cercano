package main

import (
	"context"
	"testing"

	"cercano/source/server/pkg/config"
)

func TestBuildRuntimeManagerRegistersManagedRuntimeCatalogsWhenOllamaActive(t *testing.T) {
	cfg := config.Defaults()
	cfg.OpenRuntime = "ollama"
	cfg.LlamaServer.Enabled = false
	cfg.LlamaServer.DefaultModel = ""
	cfg.MistralRS.Enabled = false
	cfg.MistralRS.DefaultModel = ""

	manager := buildRuntimeManager(cfg, nil) // nil event log: durable recording is a no-op
	models, err := manager.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory returned error: %v", err)
	}
	foundLlamaCatalog := false
	foundMistralCatalog := false
	for _, model := range models {
		if model.Runtime == "llama_server" && model.Source == "catalog" {
			foundLlamaCatalog = true
		}
		if model.Runtime == "mistralrs" && model.Source == "catalog" {
			foundMistralCatalog = true
		}
	}
	if !foundLlamaCatalog || !foundMistralCatalog {
		t.Fatalf("expected llama-server and mistral.rs catalog models with ollama active, got %#v", models)
	}
}

func TestOpenTurnModelUsesEffectiveOverride(t *testing.T) {
	cfg := config.Defaults()
	cfg.OpenRuntime = "mistralrs"
	cfg.MistralRS.DefaultModel = "stale-runtime-default"
	cfg.Models.SetOverride("mistralrs", config.TierEveryday, "custom-override")

	if got := openTurnModel(cfg); got != "custom-override" {
		t.Fatalf("openTurnModel = %q, want custom-override", got)
	}
}
