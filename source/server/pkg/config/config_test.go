package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestWorkerIdleTimeout verifies the config default is sensible and the 0 /
// positive / negative sentinels resolve as documented.
func TestWorkerIdleTimeout(t *testing.T) {
	// Default config: 0 field → DefaultWorkerIdleTimeout (a sensible few minutes).
	if got := Defaults().WorkerIdleTimeout(); got != DefaultWorkerIdleTimeout {
		t.Fatalf("default idle timeout = %v, want %v", got, DefaultWorkerIdleTimeout)
	}
	if DefaultWorkerIdleTimeout < time.Minute || DefaultWorkerIdleTimeout > time.Hour {
		t.Fatalf("default idle window %v is not a sane interactive value", DefaultWorkerIdleTimeout)
	}
	// 0 → use the default (omitted-field case), NOT disabled.
	if got := (Config{WorkerIdleTimeoutSeconds: 0}).WorkerIdleTimeout(); got != DefaultWorkerIdleTimeout {
		t.Fatalf("0 must resolve to the default, got %v", got)
	}
	// Positive → explicit override in seconds.
	if got := (Config{WorkerIdleTimeoutSeconds: 90}).WorkerIdleTimeout(); got != 90*time.Second {
		t.Fatalf("90 → %v, want 90s", got)
	}
	// Negative → disabled (0 duration; the reaper starts no goroutine).
	if got := (Config{WorkerIdleTimeoutSeconds: -1}).WorkerIdleTimeout(); got != 0 {
		t.Fatalf("negative must disable (0 duration), got %v", got)
	}
}

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.OllamaURL != "http://localhost:11434" {
		t.Errorf("expected default OllamaURL, got %q", cfg.OllamaURL)
	}
	if cfg.OpenRuntime != "llama_server" {
		t.Errorf("expected default OpenRuntime llama_server, got %q", cfg.OpenRuntime)
	}
	// The legacy model fields are retired: Defaults() leaves them blank and
	// the stock models land in the tier slots at Load time (finalizeModelTiers).
	if cfg.OpenModel != "" || cfg.EmbeddingModel != "" {
		t.Errorf("legacy model fields = (%q, %q), want blank in Defaults", cfg.OpenModel, cfg.EmbeddingModel)
	}
	if cfg.Port != "50052" {
		t.Errorf("expected default Port, got %q", cfg.Port)
	}
	if cfg.LlamaServer.Host != "127.0.0.1" {
		t.Errorf("expected default llama-server host, got %q", cfg.LlamaServer.Host)
	}
	if cfg.LlamaServer.Restart.MaxAttempts != 3 {
		t.Errorf("expected default llama-server restart attempts, got %d", cfg.LlamaServer.Restart.MaxAttempts)
	}
}

func TestLoad_NoFile(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if cfg.OllamaURL != "http://localhost:11434" {
		t.Errorf("expected default OllamaURL, got %q", cfg.OllamaURL)
	}
}

func TestLoad_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("ollama_url: http://mac-studio.local:11434\nlocal_model: GLM-4.7-Flash\n"), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.OllamaURL != "http://mac-studio.local:11434" {
		t.Errorf("expected OllamaURL from file, got %q", cfg.OllamaURL)
	}
	if got := cfg.OpenChatModel(); got != "GLM-4.7-Flash" {
		t.Errorf("expected chat model from file (via legacy local_model migration), got %q", got)
	}
	// Defaults should fill in unset fields
	if got := cfg.OpenEmbeddingModel(); got != "nomic-embed-text" {
		t.Errorf("expected default embedding model, got %q", got)
	}
	if cfg.Port != "50052" {
		t.Errorf("expected default Port, got %q", cfg.Port)
	}
	if cfg.LlamaServer.ContextSize != 8192 {
		t.Errorf("expected default llama-server context size, got %d", cfg.LlamaServer.ContextSize)
	}
}

func TestLoad_LlamaServerRestartCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
llama_server:
  enabled: true
  restart:
    enabled: false
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.LlamaServer.Restart.Enabled {
		t.Fatal("expected explicit restart.enabled=false to be preserved")
	}
	if cfg.LlamaServer.Restart.MaxAttempts != 3 {
		t.Errorf("expected restart max attempts default, got %d", cfg.LlamaServer.Restart.MaxAttempts)
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("ollama_url: http://from-file:11434\nlocal_model: file-model\n"), 0644)

	t.Setenv("OLLAMA_URL", "http://from-env:11434")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	// Env var should override file
	if cfg.OllamaURL != "http://from-env:11434" {
		t.Errorf("expected env override for OllamaURL, got %q", cfg.OllamaURL)
	}
	// File value should remain where no env var exists
	if got := cfg.OpenChatModel(); got != "file-model" {
		t.Errorf("expected chat model from file, got %q", got)
	}
}

func TestLoad_EnvOverridesDefaults(t *testing.T) {
	t.Setenv("CERCANO_LOCAL_MODEL", "env-model")
	t.Setenv("CERCANO_PORT", "9999")

	cfg, err := Load("/nonexistent/config.yaml")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got := cfg.OpenChatModel(); got != "env-model" {
		t.Errorf("expected env chat model, got %q", got)
	}
	if cfg.Port != "9999" {
		t.Errorf("expected env Port, got %q", cfg.Port)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("{{invalid yaml"), 0644)

	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoad_EmptyPath(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("expected no error for empty path, got %v", err)
	}
	if cfg.OllamaURL != "http://localhost:11434" {
		t.Errorf("expected defaults for empty path, got %q", cfg.OllamaURL)
	}
}

func TestLoad_GeminiKeySetsCloudDefaults(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key-123")

	cfg, err := Load("/nonexistent/config.yaml")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.CloudAPIKey != "test-key-123" {
		t.Errorf("expected CloudAPIKey from env, got %q", cfg.CloudAPIKey)
	}
	if cfg.CloudProvider != "google" {
		t.Errorf("expected default CloudProvider 'google', got %q", cfg.CloudProvider)
	}
	if cfg.CloudModel != "gemini-3-flash" {
		t.Errorf("expected default CloudModel, got %q", cfg.CloudModel)
	}
}

func TestSave_CreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "config.yaml")

	cfg := Defaults()
	cfg.OllamaURL = "http://saved:11434"

	err := Save(cfg, path)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Read back and verify
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load after save failed: %v", err)
	}
	if loaded.OllamaURL != "http://saved:11434" {
		t.Errorf("expected saved OllamaURL, got %q", loaded.OllamaURL)
	}
}

func TestSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg := Config{
		OllamaURL:      "http://studio.local:11434",
		OpenModel:      "GLM-4.7-Flash",
		EmbeddingModel: "nomic-embed-text",
		CloudProvider:  "google",
		CloudModel:     "gemini-2.0-flash",
		Port:           "50053",
	}

	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.OllamaURL != cfg.OllamaURL {
		t.Errorf("OllamaURL mismatch: %q vs %q", loaded.OllamaURL, cfg.OllamaURL)
	}
	// open_model migrates to the everyday tier on load; the resolver is the
	// round-trip-stable accessor.
	if loaded.OpenChatModel() != cfg.OpenModel {
		t.Errorf("chat model mismatch: %q vs %q", loaded.OpenChatModel(), cfg.OpenModel)
	}
	if loaded.CloudProvider != cfg.CloudProvider {
		t.Errorf("CloudProvider mismatch: %q vs %q", loaded.CloudProvider, cfg.CloudProvider)
	}
	if loaded.Port != cfg.Port {
		t.Errorf("Port mismatch: %q vs %q", loaded.Port, cfg.Port)
	}
}

func TestDefaults_Compaction(t *testing.T) {
	c := Defaults()
	if !c.Compaction.Enabled {
		t.Error("compaction should default to enabled")
	}
	if c.Compaction.ActivationFloorTokens != 40000 ||
		c.Compaction.SegmentTokens != 8000 ||
		c.Compaction.VerbatimRecent != 6 {
		t.Errorf("compaction defaults = %+v", c.Compaction)
	}
	if c.Compaction.HardOverridePct != 0.9 {
		t.Errorf("hard override = %v, want 0.9", c.Compaction.HardOverridePct)
	}
}

func TestDefaultPath(t *testing.T) {
	path := DefaultPath()
	if path == "" {
		t.Skip("could not determine home directory")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("expected absolute path, got %q", path)
	}
	if filepath.Base(path) != "config.yaml" {
		t.Errorf("expected config.yaml filename, got %q", filepath.Base(path))
	}
}

func TestDefaults_Retention(t *testing.T) {
	r := Defaults().Compaction.Retention
	if r.RawRetentionDays != 90 || r.CompactedRetentionDays != 180 || r.KeepForever {
		t.Errorf("retention defaults = %+v, want {90,180,false}", r)
	}
}

func TestDefaultsLocusMode(t *testing.T) {
	if got := Defaults().LocusMode; got != "cloud_primary" {
		t.Errorf("Defaults().LocusMode = %q; want cloud_primary", got)
	}
}

func TestLoad_LegacyLocalKeysBackwardCompat(t *testing.T) {
	// Pre-rename YAML with `local_model` / `local_runtime` / legacy locus_mode
	// values should still load into the new Open* fields and normalized modes.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
local_model: legacy-model
local_runtime: llama_server
locus_mode: local_primary
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got := cfg.OpenChatModel(); got != "legacy-model" {
		t.Errorf("expected legacy local_model to migrate into the everyday tier, got %q", got)
	}
	if cfg.OpenRuntime != "llama_server" {
		t.Errorf("expected legacy local_runtime to populate OpenRuntime, got %q", cfg.OpenRuntime)
	}
	if cfg.LocusMode != "open_primary" {
		t.Errorf("expected locus_mode local_primary to normalize to open_primary, got %q", cfg.LocusMode)
	}
}

func TestLoad_OpenKeysWinOverLegacy(t *testing.T) {
	// When both `open_*` and `local_*` are set, `open_*` wins.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
open_model: new-key-model
local_model: legacy-model
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got := cfg.OpenChatModel(); got != "new-key-model" {
		t.Errorf("open_model should win over local_model, got %q", got)
	}
}

func TestMigrateLegacyCloudToProfile(t *testing.T) {
	// A config with only the legacy single-cloud fields set migrates to one
	// "default" profile + active selection on Load. We exercise the helper
	// directly to avoid file IO.
	cfg := Config{CloudProvider: "anthropic", CloudModel: "claude-sonnet-4-6", CloudBaseURL: "http://x"}
	migrateCloudProfiles(&cfg)
	if len(cfg.CloudProfiles) != 1 || cfg.ActiveCloudProfile != "default" {
		t.Fatalf("profiles=%+v active=%q", cfg.CloudProfiles, cfg.ActiveCloudProfile)
	}
	p := cfg.CloudProfiles[0]
	if p.Name != "default" || p.Flavor != "messages" || p.Model != "claude-sonnet-4-6" || p.BaseURL != "http://x" {
		t.Errorf("profile = %+v", p)
	}
}

func TestMigrateNoLegacyNoProfiles(t *testing.T) {
	cfg := Config{} // nothing set
	migrateCloudProfiles(&cfg)
	if len(cfg.CloudProfiles) != 0 || cfg.ActiveCloudProfile != "" {
		t.Errorf("expected no migration, got %+v / %q", cfg.CloudProfiles, cfg.ActiveCloudProfile)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	cfg := Config{CloudProvider: "anthropic", CloudProfiles: []CloudProfile{{Name: "x", Flavor: "messages"}}}
	migrateCloudProfiles(&cfg)
	if len(cfg.CloudProfiles) != 1 || cfg.CloudProfiles[0].Name != "x" {
		t.Errorf("should not overwrite existing profiles: %+v", cfg.CloudProfiles)
	}
}

func TestCloudProfileBackendYAML(t *testing.T) {
	var p CloudProfile
	y := "name: g\nflavor: chat_completions\nbackend: gemini\nbase_url: x\nmodel: m\n"
	if err := yaml.Unmarshal([]byte(y), &p); err != nil {
		t.Fatal(err)
	}
	if p.Backend != "gemini" {
		t.Errorf("backend=%q, want gemini", p.Backend)
	}
}

// Profiles pointed at Meridian's default port get auto-promoted to
// route: meridian on load. Without this, users upgrading from a pre-route
// config silently lose Meridian's OpenCode-adapter treatment.
func TestMigrateMeridianToSubscription(t *testing.T) {
	cases := []struct {
		name        string
		profile     CloudProfile
		wantRoute   string
		wantBaseURL string
	}{
		{"explicit meridian route", CloudProfile{Name: "a", Flavor: "messages", Route: "meridian", BaseURL: "http://127.0.0.1:3456"}, "subscription", ""},
		{"un-routed 127 default port", CloudProfile{Name: "b", BaseURL: "http://127.0.0.1:3456"}, "subscription", ""},
		{"un-routed localhost default port", CloudProfile{Name: "c", BaseURL: "http://localhost:3456"}, "subscription", ""},
		{"non-default port untouched", CloudProfile{Name: "d", BaseURL: "http://127.0.0.1:9999"}, "", "http://127.0.0.1:9999"},
		{"public URL untouched", CloudProfile{Name: "e", BaseURL: "https://api.anthropic.com"}, "", "https://api.anthropic.com"},
		{"explicit direct route preserved", CloudProfile{Name: "f", Route: "direct", BaseURL: "http://127.0.0.1:3456"}, "direct", "http://127.0.0.1:3456"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{CloudProfiles: []CloudProfile{tc.profile}}
			migrateMeridianToSubscription(cfg)
			got := cfg.CloudProfiles[0]
			if got.Route != tc.wantRoute {
				t.Errorf("Route = %q, want %q", got.Route, tc.wantRoute)
			}
			if got.BaseURL != tc.wantBaseURL {
				t.Errorf("BaseURL = %q, want %q", got.BaseURL, tc.wantBaseURL)
			}
		})
	}
}

// Save must strip the four legacy cloud_* fields when profiles are present.
// The profile is the source of truth; leaving the legacy mirrors on disk is
// what made it possible for cloud_model and the active profile's model to
// disagree (split-state bug).
func TestSave_StripsLegacyCloudFieldsWhenProfilesPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := Config{
		CloudProvider:      "anthropic",
		CloudModel:         "claude-stale-mirror",
		CloudAPIKey:        "sk-leaky",
		CloudBaseURL:       "http://127.0.0.1:3456",
		CloudProfiles:      []CloudProfile{{Name: "default", Flavor: "messages", Model: "claude-real"}},
		ActiveCloudProfile: "default",
	}
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	text := string(data)
	for _, banned := range []string{"cloud_provider:", "cloud_model:", "cloud_api_key:", "cloud_base_url:"} {
		if strings.Contains(text, banned) {
			t.Errorf("legacy key %q must not be saved when profiles exist:\n%s", banned, text)
		}
	}
	if !strings.Contains(text, "claude-real") {
		t.Errorf("profile model missing from saved YAML:\n%s", text)
	}
	// Round-trip: in-memory cfg keeps its mirrors (unchanged), but a fresh
	// Load gives empty legacy fields and the profile intact.
	if cfg.CloudModel != "claude-stale-mirror" {
		t.Errorf("Save must not mutate caller's struct")
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.CloudModel != "" || reloaded.CloudProvider != "" || reloaded.CloudAPIKey != "" || reloaded.CloudBaseURL != "" {
		t.Errorf("reloaded config has stale legacy fields: %+v", reloaded)
	}
	if len(reloaded.CloudProfiles) != 1 || reloaded.CloudProfiles[0].Model != "claude-real" {
		t.Errorf("profile lost on round-trip: %+v", reloaded.CloudProfiles)
	}
}

func TestWatchdogDefaults(t *testing.T) {
	w := Defaults().Watchdog
	if w.Enabled {
		t.Error("watchdog should default to disabled")
	}
	if w.Mode != "challenge-and-justify" {
		t.Errorf("Mode = %q, want challenge-and-justify", w.Mode)
	}
	if len(w.Checks) != 8 || w.Checks[0] != "systematic-debugging" || w.Checks[1] != "design-decisions" || w.Checks[2] != "verification-strategy" || w.Checks[3] != "compute-before-simulate" || w.Checks[4] != "commit-checkpoint" || w.Checks[5] != "plain-english" || w.Checks[6] != "worktree-first" || w.Checks[7] != "follow-through" {
		t.Errorf("Checks = %v, want [systematic-debugging design-decisions verification-strategy compute-before-simulate commit-checkpoint plain-english worktree-first follow-through]", w.Checks)
	}
	if w.EscalateAfter != 2 {
		t.Errorf("EscalateAfter = %d, want 2", w.EscalateAfter)
	}
	if w.Echo {
		t.Error("Echo should default to false")
	}
}

func TestWatchdogParsesFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
watchdog:
  enabled: true
  mode: strict
  checks:
    - systematic-debugging
  escalate_after: 3
  echo: true
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	w := cfg.Watchdog
	if !w.Enabled {
		t.Error("expected Enabled=true from YAML")
	}
	if w.Mode != "strict" {
		t.Errorf("Mode = %q, want strict", w.Mode)
	}
	if len(w.Checks) != 1 || w.Checks[0] != "systematic-debugging" {
		t.Errorf("Checks = %v, want [systematic-debugging]", w.Checks)
	}
	if w.EscalateAfter != 3 {
		t.Errorf("EscalateAfter = %d, want 3", w.EscalateAfter)
	}
	if !w.Echo {
		t.Error("expected Echo=true from YAML")
	}
}

// Save must NOT strip legacy fields when no profiles exist yet — that's
// the upgrade path where the legacy fields are the actual source of truth
// and migrateCloudProfiles will synthesize a profile from them on next load.
func TestSave_KeepsLegacyCloudFieldsBeforeMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := Config{
		CloudProvider: "anthropic",
		CloudModel:    "claude-pre-migration",
	}
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "cloud_model: claude-pre-migration") {
		t.Errorf("legacy model field dropped before migration:\n%s", data)
	}
}

func TestCloudProfileBedrockYAML(t *testing.T) {
	var p CloudProfile
	y := "name: b\nflavor: bedrock\nregion: us-east-1\naws_profile: sso\nmodel: anthropic.claude-x\n"
	if err := yaml.Unmarshal([]byte(y), &p); err != nil {
		t.Fatal(err)
	}
	if p.Region != "us-east-1" || p.AWSProfile != "sso" {
		t.Errorf("region=%q aws_profile=%q", p.Region, p.AWSProfile)
	}
}

func TestDefaults_ToolLoop(t *testing.T) {
	cfg := Defaults()
	if cfg.ToolLoop.MaxIterations != DefaultToolLoopMaxIterations {
		t.Fatalf("tool loop max iterations default = %d, want %d", cfg.ToolLoop.MaxIterations, DefaultToolLoopMaxIterations)
	}
}

func TestLoad_ToolLoopDefaultsAndUnlimited(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte("tool_loop:\n  max_iterations: -1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.ToolLoop.MaxIterations != UnlimitedToolLoopMaxIterations {
		t.Fatalf("tool loop max iterations = %d, want unlimited sentinel", cfg.ToolLoop.MaxIterations)
	}

	if err := os.WriteFile(path, []byte("tool_loop:\n  max_iterations: 0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.ToolLoop.MaxIterations != DefaultToolLoopMaxIterations {
		t.Fatalf("zero max iterations = %d, want default %d", cfg.ToolLoop.MaxIterations, DefaultToolLoopMaxIterations)
	}
}

func TestLoad_ToolLoopRejectsBelowUnlimited(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("tool_loop:\n  max_iterations: -2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "tool_loop.max_iterations") {
		t.Fatalf("Load error = %v, want tool_loop.max_iterations validation error", err)
	}
}

func TestEffectiveMaxIterations(t *testing.T) {
	if got, unlimited := EffectiveMaxIterations(0); got != DefaultToolLoopMaxIterations || unlimited {
		t.Fatalf("zero effective = (%d,%v), want (%d,false)", got, unlimited, DefaultToolLoopMaxIterations)
	}
	if got, unlimited := EffectiveMaxIterations(37); got != 37 || unlimited {
		t.Fatalf("positive effective = (%d,%v), want (37,false)", got, unlimited)
	}
	if got, unlimited := EffectiveMaxIterations(UnlimitedToolLoopMaxIterations); got != 0 || !unlimited {
		t.Fatalf("unlimited effective = (%d,%v), want (0,true)", got, unlimited)
	}
	if !ValidateToolLoopMaxIterations(-1) || !ValidateToolLoopMaxIterations(0) || !ValidateToolLoopMaxIterations(200) {
		t.Fatalf("expected -1, 0, and positive values to be valid")
	}
	if ValidateToolLoopMaxIterations(-2) {
		t.Fatalf("expected values below -1 to be invalid")
	}
}
