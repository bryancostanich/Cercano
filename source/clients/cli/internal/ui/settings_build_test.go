package ui

import (
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestBuildSettingsSectionsCoversKeys(t *testing.T) {
	cfg := &agentclient.Config{
		LocalRuntime: "ollama", LocalModel: "qwen3-coder", OllamaURL: "http://x",
		EmbeddingModel: "nomic", CloudProvider: "anthropic", CloudModel: "claude",
		CloudBaseURL: "http://m", CloudAPIKeySet: true, CloudState: "ok",
		Port: "50052", LocusMode: "cloud_primary",
	}
	secs := buildSettingsSections(cfg, "permissive", "palette:accent")
	titles := map[string]bool{}
	keys := map[string]bool{}
	for _, s := range secs {
		titles[s.Title] = true
		for _, f := range s.Fields {
			keys[f.Key()] = true
		}
	}
	// "Cloud" section is removed; "Cloud Providers" is now built by buildCloudSection.
	for _, want := range []string{"Local Model", "Routing", "Permissions", "Server"} {
		if !titles[want] {
			t.Errorf("missing section %q", want)
		}
	}
	if titles["Cloud"] {
		t.Errorf("legacy \"Cloud\" section should not exist in buildSettingsSections output")
	}
	for _, want := range []string{
		"local-runtime", "local-model", "ollama-url", "embedding-model",
		"locus-mode", "permission-mode", "port",
	} {
		if !keys[want] {
			t.Errorf("missing field %q", want)
		}
	}
}

func TestClassifyCommit(t *testing.T) {
	if a := classifyCommit("local-model", "qwen"); a.kind != commitConfig || a.update.LocalModel != "qwen" {
		t.Fatalf("local-model -> %+v", a)
	}
	if a := classifyCommit("locus-mode", "local_only"); a.kind != commitConfig || a.update.LocusMode != "local_only" {
		t.Fatalf("locus-mode -> %+v", a)
	}
	if a := classifyCommit("permission-mode", "bypass"); a.kind != commitPermission || a.value != "bypass" {
		t.Fatalf("permission-mode -> %+v", a)
	}
	if a := classifyCommit("accent-color", "palette:info"); a.kind != commitColor || a.value != "palette:info" {
		t.Fatalf("accent-color -> %+v", a)
	}
	if a := classifyCommit("unknown", "x"); a.kind != commitNoop {
		t.Fatalf("unknown -> %+v", a)
	}
}
