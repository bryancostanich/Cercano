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
	if a := classifyCommit("local-model", "qwen", nil); a.kind != commitConfig || a.update.LocalModel != "qwen" {
		t.Fatalf("local-model -> %+v", a)
	}
	if a := classifyCommit("locus-mode", "local_only", nil); a.kind != commitConfig || a.update.LocusMode != "local_only" {
		t.Fatalf("locus-mode -> %+v", a)
	}
	if a := classifyCommit("permission-mode", "bypass", nil); a.kind != commitPermission || a.value != "bypass" {
		t.Fatalf("permission-mode -> %+v", a)
	}
	if a := classifyCommit("accent-color", "palette:info", nil); a.kind != commitColor || a.value != "palette:info" {
		t.Fatalf("accent-color -> %+v", a)
	}
	if a := classifyCommit("unknown", "x", nil); a.kind != commitNoop {
		t.Fatalf("unknown -> %+v", a)
	}
}

func TestWatchdogGroupFields(t *testing.T) {
	// The watchdog controls render as the "Watchdog" group inside the pinned
	// Development Tools section (settings_page.go); buildDevFields is the
	// group's field builder.
	cfg := &agentclient.Config{WatchdogEnabled: true, WatchdogEcho: false}
	fields := buildDevFields(cfg)
	if fields[0].Key() != "watchdog-enabled" || fields[1].Key() != "watchdog-echo" {
		t.Fatalf("unexpected first two field keys: %s, %s", fields[0].Key(), fields[1].Key())
	}
}

func TestClassifyCommit_Watchdog(t *testing.T) {
	a := classifyCommit("watchdog-enabled", "true", nil)
	if a.kind != commitConfig || a.update.WatchdogEnabled != "true" {
		t.Fatalf("watchdog-enabled: %+v", a)
	}
	b := classifyCommit("watchdog-echo", "false", nil)
	if b.kind != commitConfig || b.update.WatchdogEcho != "false" {
		t.Fatalf("watchdog-echo: %+v", b)
	}
}

func TestWatchdogGroupModeChecksEscalateFields(t *testing.T) {
	cfg := &agentclient.Config{
		WatchdogEnabled: true, WatchdogMode: "strict",
		WatchdogChecks: []string{"debug-loop"}, WatchdogEscalateAfter: 2,
	}
	keys := map[string]bool{}
	for _, f := range buildDevFields(cfg) {
		keys[f.Key()] = true
	}
	for _, want := range []string{"watchdog-mode", "watchdog-check-debug-loop", "watchdog-check-commit-checkpoint", "watchdog-check-plain-english", "watchdog-escalate-after"} {
		if !keys[want] {
			t.Fatalf("missing field %q", want)
		}
	}
}

func TestClassifyCommit_WatchdogModeChecksEscalate(t *testing.T) {
	cur := []string{"debug-loop", "commit-checkpoint", "plain-english"}
	// mode
	if a := classifyCommit("watchdog-mode", "strict", cur); a.kind != commitConfig || a.update.WatchdogMode != "strict" {
		t.Fatalf("mode: %+v", a)
	}
	// escalate
	if a := classifyCommit("watchdog-escalate-after", "4", cur); a.kind != commitConfig || a.update.WatchdogEscalateAfter != "4" {
		t.Fatalf("escalate: %+v", a)
	}
	// turn a check OFF → new full list without it
	a := classifyCommit("watchdog-check-plain-english", "false", cur)
	if a.kind != commitConfig || a.update.WatchdogChecks != "debug-loop,commit-checkpoint" {
		t.Fatalf("check off: %+v", a)
	}
	// turn a check ON when absent → appended (known-order)
	b := classifyCommit("watchdog-check-plain-english", "true", []string{"debug-loop"})
	if b.update.WatchdogChecks != "debug-loop,plain-english" {
		t.Fatalf("check on: %+v", b)
	}
	// last check OFF → "-" sentinel
	c := classifyCommit("watchdog-check-debug-loop", "false", []string{"debug-loop"})
	if c.update.WatchdogChecks != "-" {
		t.Fatalf("empty sentinel: %+v", c)
	}
}
