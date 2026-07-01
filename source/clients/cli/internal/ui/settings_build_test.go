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

func TestDeveloperSectionPresent(t *testing.T) {
	cfg := &agentclient.Config{WatchdogEnabled: true, WatchdogEcho: false}
	secs := buildSettingsSections(cfg, "permissive", "palette:accent")
	found := false
	for _, s := range secs {
		if s.Title == "Developer" {
			found = true
			if len(s.Fields) != 2 {
				t.Fatalf("Developer section: want 2 fields, got %d", len(s.Fields))
			}
			if s.Fields[0].Key() != "watchdog-enabled" || s.Fields[1].Key() != "watchdog-echo" {
				t.Fatalf("unexpected field keys: %s, %s", s.Fields[0].Key(), s.Fields[1].Key())
			}
		}
	}
	if !found {
		t.Fatal("no Developer section")
	}
}

func TestClassifyCommit_Watchdog(t *testing.T) {
	a := classifyCommit("watchdog-enabled", "true")
	if a.kind != commitConfig || a.update.WatchdogEnabled != "true" {
		t.Fatalf("watchdog-enabled: %+v", a)
	}
	b := classifyCommit("watchdog-echo", "false")
	if b.kind != commitConfig || b.update.WatchdogEcho != "false" {
		t.Fatalf("watchdog-echo: %+v", b)
	}
}
