package main

import (
	"context"
	"testing"

	"cercano/source/server/pkg/config"
)

func TestBuildRuntimeManagerRegistersLlamaServerCatalogWhenOllamaActive(t *testing.T) {
	cfg := config.Defaults()
	cfg.OpenRuntime = "ollama"
	cfg.LlamaServer.Enabled = false
	cfg.LlamaServer.DefaultModel = ""

	manager := buildRuntimeManager(cfg)
	models, err := manager.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory returned error: %v", err)
	}
	foundCatalog := false
	for _, model := range models {
		if model.ID == "llama_server:catalog:qwen2.5-coder-1.5b-q4_k_m" {
			foundCatalog = true
			break
		}
	}
	if !foundCatalog {
		t.Fatalf("expected llama-server catalog model with ollama active, got %#v", models)
	}
	instances, err := manager.Instances(context.Background())
	if err != nil {
		t.Fatalf("Instances returned error: %v", err)
	}
	if len(instances) != 0 {
		t.Fatalf("expected no auto-started instances, got %#v", instances)
	}
}
