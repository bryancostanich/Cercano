package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// CloudProfile is one named cloud provider configuration. The API key is NOT
// stored here — it lives in the OS keychain keyed by Name.
//
// Route names a specific access path with its own auth + identification
// conventions:
//   - "direct" (or "")  — vanilla path to the upstream provider (Anthropic
//     direct, OpenAI direct, etc.). API key auth, no special headers.
//   - "meridian"        — Meridian local OAuth bridge. Cercano emits
//     opencode-style identification headers so Meridian routes through its
//     OpenCode adapter (4-turn SDK cap vs. the default 3-turn). Borrowed
//     identity; see anthropic/client.go for the TODO to negotiate a native
//     Cercano adapter upstream.
//
// Route is an open enum — future bridges (CCR, etc.) get their own value and
// adapter-specific handling. Empty string is treated as "direct".
type CloudProfile struct {
	Name       string `yaml:"name"`
	Flavor     string `yaml:"flavor"`            // messages | chat_completions | responses | bedrock
	Backend    string `yaml:"backend,omitempty"` // chat_completions only: selects per-backend quirks (openai|gemini|groq|…); empty → defensive default
	Route      string `yaml:"route,omitempty"`   // direct (default) | meridian | ccr (future) | …
	BaseURL    string `yaml:"base_url"`
	Model      string `yaml:"model"`
	Region     string `yaml:"region,omitempty"`      // bedrock: AWS region (required)
	AWSProfile string `yaml:"aws_profile,omitempty"` // bedrock: optional ~/.aws named profile
}

// Config holds all Cercano configuration values.
//
// The four "Cloud*" top-level fields (CloudProvider, CloudModel, CloudAPIKey,
// CloudBaseURL) are legacy — pre-profile shape that gets migrated into a
// "default" entry in CloudProfiles on first load. They remain as
// load-tolerant inputs and as in-memory mirrors for proto reporting, but Save
// strips them so disk reflects the profile-only world. New code should
// always read through the active profile (see Server.activeCloudModel).
type Config struct {
	OllamaURL          string         `yaml:"ollama_url"`
	OpenRuntime        string         `yaml:"open_runtime"`
	OpenModel          string         `yaml:"open_model,omitempty"`
	EmbeddingModel     string         `yaml:"embedding_model,omitempty"`
	CloudProvider      string         `yaml:"cloud_provider,omitempty"`
	CloudModel         string         `yaml:"cloud_model,omitempty"`
	CloudAPIKey        string         `yaml:"cloud_api_key,omitempty"`
	CloudBaseURL       string         `yaml:"cloud_base_url,omitempty"`
	CloudProfiles      []CloudProfile `yaml:"cloud_profiles"`
	ActiveCloudProfile string         `yaml:"active_cloud_profile"`
	// BackupCloudProfile names the profile that serves a request when the
	// active profile's provider fails (see internal/llm/fallback for what
	// counts as a failure worth failing over). Empty = no fallback.
	BackupCloudProfile string            `yaml:"backup_cloud_profile,omitempty"`
	LocusMode          string            `yaml:"locus_mode"` // cloud_only|cloud_primary|open_primary|open_only
	Port               string            `yaml:"port"`
	LlamaServer        LlamaServerConfig `yaml:"llama_server"`
	Compaction         CompactionConfig  `yaml:"compaction"`
	Watchdog           WatchdogConfig    `yaml:"watchdog"`
	Models             ModelsConfig      `yaml:"models"`
}

// CompactionConfig controls background context compaction. Thresholds are token
// counts; HardOverridePct is a fraction of the cloud model's max context above
// which the request path compacts synchronously.
type CompactionConfig struct {
	Enabled               bool            `yaml:"enabled"`
	ActivationFloorTokens int             `yaml:"activation_floor_tokens"`
	SegmentTokens         int             `yaml:"segment_tokens"`
	VerbatimRecent        int             `yaml:"verbatim_recent"`
	HardOverridePct       float64         `yaml:"hard_override_pct"`
	Retention             RetentionConfig `yaml:"retention"`
	// ElideToolResults, when true, runs the mechanical superseded-tool-result
	// dedup over every assembled history — independent of Enabled. Lossless and
	// LLM-free, so it's safe to run even while the summarizer is disabled.
	// Useful as an interim mitigation for a broken summarizer, and as an
	// always-on savings pass in the live tail when the summarizer is back on.
	ElideToolResults bool `yaml:"elide_tool_results"`
	// LossyToolElision, when true, stubs older tool_result content down to a
	// one-line marker so only the most recent tool results (keep-last-N, see
	// compaction.DefaultLossyElisionKeepLast) carry their full content. Not
	// lossless — the model cannot recall the exact bytes of an older tool
	// result — but the raw turns are still in the persistent store and the
	// model can always re-invoke the tool. Recovers materially more tokens
	// than byte-identical elision (measured ~58% vs ~0.4% on a 190-turn
	// real conversation).
	LossyToolElision bool `yaml:"lossy_tool_elision"`
	// SummarizerModel overrides the local model used for compaction
	// summarization. Empty falls back to the fast_light tier's open model.
	// Useful because a code-focused model (qwen3-coder) tends to fabricate
	// when asked to write extractive summaries; a text-focused model
	// (phi4, llama3.1) grounds better. Not applied to the main tool loop.
	SummarizerModel string `yaml:"summarizer_model,omitempty"`
}

// RetentionConfig bounds how long raw turn bodies and the compacted layer are
// kept. CompactedRetentionDays should be >= RawRetentionDays.
type RetentionConfig struct {
	RawRetentionDays       int  `yaml:"raw_retention_days"`
	CompactedRetentionDays int  `yaml:"compacted_retention_days"`
	KeepForever            bool `yaml:"keep_forever"`
}

// WatchdogConfig controls the protocol-enforcement supervisor. Disabled by
// default (opt-in). Mode is "challenge-and-justify" or "strict".
type WatchdogConfig struct {
	Enabled       bool     `yaml:"enabled"`
	Mode          string   `yaml:"mode"`
	Checks        []string `yaml:"checks"`
	Model         string   `yaml:"model"`
	EscalateAfter int      `yaml:"escalate_after"`
	Echo          bool     `yaml:"echo"`
}

// LlamaServerConfig controls the optional managed llama-server sidecar.
type LlamaServerConfig struct {
	Enabled          bool          `yaml:"enabled"`
	Binary           string        `yaml:"binary"`
	ModelDirs        []string      `yaml:"model_dirs"`
	DefaultModel     string        `yaml:"default_model"`
	Host             string        `yaml:"host"`
	Port             int           `yaml:"port"`
	ContextSize      int           `yaml:"context_size"`
	GPULayers        string        `yaml:"gpu_layers"`
	Threads          int           `yaml:"threads"`
	ExtraArgs        []string      `yaml:"extra_args"`
	ReadinessTimeout string        `yaml:"readiness_timeout"`
	Restart          RestartConfig `yaml:"restart"`
}

// RestartConfig controls sidecar restart behavior.
type RestartConfig struct {
	Enabled     bool   `yaml:"enabled"`
	MaxAttempts int    `yaml:"max_attempts"`
	Backoff     string `yaml:"backoff"`
	enabledSet  bool   `yaml:"-"`
}

// UnmarshalYAML tracks whether enabled was explicitly present, letting config
// defaults fill omitted values without overriding an intentional false.
func (r *RestartConfig) UnmarshalYAML(value *yaml.Node) error {
	type restartConfig RestartConfig
	var raw restartConfig
	if err := value.Decode(&raw); err != nil {
		return err
	}
	for i := 0; i+1 < len(value.Content); i += 2 {
		if value.Content[i].Value == "enabled" {
			raw.enabledSet = true
			break
		}
	}
	*r = RestartConfig(raw)
	return nil
}

// Defaults returns a Config with default values.
func Defaults() Config {
	return Config{
		OllamaURL:   "http://localhost:11434",
		OpenRuntime: "ollama",
		LocusMode:   "open_primary",
		Port:        "50052",
		Models:      ModelsConfig{DefaultProvider: ProviderOpen},
		LlamaServer: LlamaServerConfig{
			ModelDirs:        []string{"~/.cercano/models"},
			Host:             "127.0.0.1",
			ContextSize:      8192,
			GPULayers:        "auto",
			ReadinessTimeout: "60s",
			Restart: RestartConfig{
				Enabled:     true,
				MaxAttempts: 3,
				Backoff:     "2s",
			},
		},
		Compaction: CompactionConfig{
			Enabled:               true,
			ActivationFloorTokens: 40000,
			SegmentTokens:         8000,
			VerbatimRecent:        6,
			HardOverridePct:       0.9,
			Retention: RetentionConfig{
				RawRetentionDays:       90,
				CompactedRetentionDays: 180,
				KeepForever:            false,
			},
		},
		Watchdog: WatchdogConfig{
			Enabled:       false,
			Mode:          "challenge-and-justify",
			Checks:        []string{"systematic-debugging", "design-decisions", "commit-checkpoint", "plain-english", "worktree-first", "follow-through"},
			Model:         "",
			EscalateAfter: 2,
			Echo:          false,
		},
	}
}

// DefaultPath returns the default config file path (~/.config/cercano/config.yaml).
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "cercano", "config.yaml")
}

// migrateCloudProfiles synthesizes a "default" profile from the legacy
// single-cloud fields when no profiles exist yet. Metadata only — the inline
// cloud_api_key is relocated to the keychain by the startup wiring, not here
// (config has no keychain dependency). No-op if profiles already exist or no
// legacy cloud is configured.
func migrateCloudProfiles(cfg *Config) {
	if len(cfg.CloudProfiles) > 0 || cfg.CloudProvider == "" {
		return
	}
	flavor := ""
	if cfg.CloudProvider == "anthropic" {
		flavor = "messages"
	}
	cfg.CloudProfiles = []CloudProfile{{
		Name:    "default",
		Flavor:  flavor,
		BaseURL: cfg.CloudBaseURL,
		Model:   cfg.CloudModel,
	}}
	cfg.ActiveCloudProfile = "default"
}

// autoDetectMeridianRoute promotes profiles that look like Meridian (default
// local OAuth bridge port) to route=meridian on first load. Without this,
// users upgrading from a pre-route config would silently lose Meridian's
// OpenCode-adapter treatment (3-turn SDK cap instead of 4). Heuristic only —
// users can hand-edit afterwards. Safe: never overrides an explicit Route.
//
// The "127.0.0.1:3456" needle is Meridian's documented default port. If you
// run Meridian on a different host/port, set route: meridian manually.
func autoDetectMeridianRoute(cfg *Config) {
	for i := range cfg.CloudProfiles {
		p := &cfg.CloudProfiles[i]
		if p.Route != "" {
			continue
		}
		if strings.Contains(p.BaseURL, "127.0.0.1:3456") || strings.Contains(p.BaseURL, "localhost:3456") {
			p.Route = "meridian"
		}
	}
}

// migrateModelTiers seeds the model taxonomy from the legacy standalone
// model keys: open_model → tiers.everyday.open, embedding_model →
// tiers.embedding.open. Seeding only fills EMPTY slots — a file that already
// assigns a tier slot wins over the legacy key.
//
// Like applyLegacyLocalKeys, this must check raw-YAML key presence rather
// than the merged Config value: Defaults() pre-populates OpenModel and
// EmbeddingModel before unmarshal, so a merged non-empty value can't
// distinguish "the user chose this" from "nobody set it." Only values the
// file actually contains migrate into the tiers.
//
// The legacy fields are left populated for now so not-yet-rewired readers
// keep working; the tier slot is the source of truth and readers migrate to
// it (design: docs/features/local-model-taxonomy/design.md).
func migrateModelTiers(data []byte, cfg *Config) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return
	}
	filePresent := func(keys ...string) bool {
		for _, k := range keys {
			if _, ok := raw[k]; ok {
				return true
			}
		}
		return false
	}
	if cfg.Models.Tiers.Everyday.Open == "" && cfg.OpenModel != "" && filePresent("open_model", "local_model") {
		cfg.Models.Tiers.Everyday.Open = cfg.OpenModel
	}
	if cfg.Models.Tiers.Embedding.Open == "" && cfg.EmbeddingModel != "" && filePresent("embedding_model") {
		cfg.Models.Tiers.Embedding.Open = cfg.EmbeddingModel
	}
}

// finalizeModelTiers completes the taxonomy after migration and env
// overrides: still-empty slots get the stock local models, and the retired
// legacy fields are blanked so Save (omitempty) drops open_model /
// embedding_model from the file for good. Runs LAST in both Load paths —
// defaults must never mask a file, migration, or env choice.
func finalizeModelTiers(cfg *Config) {
	if cfg.Models.Tiers.Everyday.Open == "" {
		cfg.Models.Tiers.Everyday.Open = "qwen3-coder"
	}
	if cfg.Models.Tiers.Embedding.Open == "" {
		cfg.Models.Tiers.Embedding.Open = "nomic-embed-text"
	}
	cfg.OpenModel = ""
	cfg.EmbeddingModel = ""
}

// Load reads config from the given path, merges with defaults, then applies
// environment variable overrides. Returns defaults if the file doesn't exist.
func Load(path string) (Config, error) {
	cfg := Defaults()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				// No config file — use defaults + env vars
				applyEnvOverrides(&cfg)
				finalizeModelTiers(&cfg)
				return cfg, nil
			}
			return cfg, fmt.Errorf("failed to read config file %q: %w", path, err)
		}

		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("failed to parse config file %q: %w", path, err)
		}

		// Backward-compat: accept the pre-rename `local_model` /
		// `local_runtime` YAML keys (and `locus_mode` values
		// `local_primary` / `local_only`). Prefer the new `open_*`
		// keys when both are present. Save() only writes the new
		// spelling, so this shim is read-only.
		applyLegacyLocalKeys(data, &cfg)

		// Seed the model taxonomy from the legacy standalone model keys —
		// raw-presence-gated so a defaulted "qwen3-coder" never
		// masquerades as a user's everyday-tier choice.
		migrateModelTiers(data, &cfg)

		// Re-apply defaults for any fields not set in the file
		defaults := Defaults()
		if cfg.OllamaURL == "" {
			cfg.OllamaURL = defaults.OllamaURL
		}
		if cfg.OpenRuntime == "" {
			cfg.OpenRuntime = defaults.OpenRuntime
		}
		if cfg.Port == "" {
			cfg.Port = defaults.Port
		}
		applyLlamaServerDefaults(&cfg.LlamaServer, defaults.LlamaServer)
	}

	applyEnvOverrides(&cfg)
	finalizeModelTiers(&cfg)
	migrateCloudProfiles(&cfg)
	autoDetectMeridianRoute(&cfg)
	return cfg, nil
}

// applyLegacyLocalKeys parses the raw YAML for the pre-rename `local_model`
// and `local_runtime` keys, and normalizes `locus_mode` values
// `local_primary` / `local_only` to their `open_*` equivalents.
//
// The check has to look at raw YAML presence (not the merged Config value)
// because Defaults() pre-populates OpenModel/OpenRuntime, so we can't
// distinguish "YAML didn't set it, so defaults remain" from "YAML set it to
// the same value as defaults." Presence wins: if the file has a legacy key
// and no new-name key, we use the legacy value.
//
// Save() emits only the new spelling; this shim is a one-way read migration.
func applyLegacyLocalKeys(data []byte, cfg *Config) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return
	}
	_, hasOpenModel := raw["open_model"]
	if legacy, ok := raw["local_model"].(string); ok && !hasOpenModel && legacy != "" {
		cfg.OpenModel = legacy
	}
	_, hasOpenRuntime := raw["open_runtime"]
	if legacy, ok := raw["local_runtime"].(string); ok && !hasOpenRuntime && legacy != "" {
		cfg.OpenRuntime = legacy
	}
	switch cfg.LocusMode {
	case "local_primary":
		cfg.LocusMode = "open_primary"
	case "local_only":
		cfg.LocusMode = "open_only"
	}
}

func applyLlamaServerDefaults(cfg *LlamaServerConfig, defaults LlamaServerConfig) {
	if len(cfg.ModelDirs) == 0 {
		cfg.ModelDirs = defaults.ModelDirs
	}
	if cfg.Host == "" {
		cfg.Host = defaults.Host
	}
	if cfg.ContextSize == 0 {
		cfg.ContextSize = defaults.ContextSize
	}
	if cfg.GPULayers == "" {
		cfg.GPULayers = defaults.GPULayers
	}
	if cfg.ReadinessTimeout == "" {
		cfg.ReadinessTimeout = defaults.ReadinessTimeout
	}
	if cfg.Restart.MaxAttempts == 0 {
		cfg.Restart.MaxAttempts = defaults.Restart.MaxAttempts
	}
	if cfg.Restart.Backoff == "" {
		cfg.Restart.Backoff = defaults.Restart.Backoff
	}
	if !cfg.Restart.enabledSet && defaults.Restart.Enabled {
		cfg.Restart.Enabled = true
	}
}

// applyEnvOverrides applies environment variable overrides to the config.
// Environment variables take precedence over file values.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("OLLAMA_URL"); v != "" {
		cfg.OllamaURL = v
	}
	// CERCANO_OPEN_* is the primary naming; CERCANO_LOCAL_* is accepted for
	// backward compat with earlier releases. When both are set the new
	// spelling wins.
	if v := os.Getenv("CERCANO_LOCAL_MODEL"); v != "" {
		cfg.Models.Tiers.Everyday.Open = v
	}
	if v := os.Getenv("CERCANO_OPEN_MODEL"); v != "" {
		cfg.Models.Tiers.Everyday.Open = v
	}
	if v := os.Getenv("CERCANO_LOCAL_RUNTIME"); v != "" {
		cfg.OpenRuntime = v
	}
	if v := os.Getenv("CERCANO_OPEN_RUNTIME"); v != "" {
		cfg.OpenRuntime = v
	}
	if v := os.Getenv("CERCANO_EMBEDDING_MODEL"); v != "" {
		cfg.Models.Tiers.Embedding.Open = v
	}
	if v := os.Getenv("CERCANO_PORT"); v != "" {
		cfg.Port = v
	}
	if v := os.Getenv("GEMINI_API_KEY"); v != "" {
		cfg.CloudAPIKey = v
		if cfg.CloudProvider == "" {
			cfg.CloudProvider = "google"
		}
		if cfg.CloudModel == "" {
			cfg.CloudModel = "gemini-3-flash"
		}
	}
}

// Clone returns a deep copy of c. Every reference-type field (slices inside
// CloudProfiles, LlamaServer, and Watchdog) is independently allocated so that
// mutations to the original or the clone cannot race through a shared backing
// array.
func (c Config) Clone() Config {
	out := c // copy all scalar and nested-struct fields

	// CloudProfiles: independent slice + elements are all-scalar structs, so a
	// slice copy suffices.
	if c.CloudProfiles != nil {
		out.CloudProfiles = make([]CloudProfile, len(c.CloudProfiles))
		copy(out.CloudProfiles, c.CloudProfiles)
	}

	// LlamaServer contains two slices.
	if c.LlamaServer.ModelDirs != nil {
		out.LlamaServer.ModelDirs = make([]string, len(c.LlamaServer.ModelDirs))
		copy(out.LlamaServer.ModelDirs, c.LlamaServer.ModelDirs)
	}
	if c.LlamaServer.ExtraArgs != nil {
		out.LlamaServer.ExtraArgs = make([]string, len(c.LlamaServer.ExtraArgs))
		copy(out.LlamaServer.ExtraArgs, c.LlamaServer.ExtraArgs)
	}

	// Watchdog.Checks is a []string.
	if c.Watchdog.Checks != nil {
		out.Watchdog.Checks = make([]string, len(c.Watchdog.Checks))
		copy(out.Watchdog.Checks, c.Watchdog.Checks)
	}

	return out
}

// VenvDir returns the path to Cercano's Python venv (~/.config/cercano/venv/).
func VenvDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "cercano", "venv")
}

// VenvPython returns the path to the Python binary inside Cercano's venv.
func VenvPython() string {
	return filepath.Join(VenvDir(), "bin", "python3")
}

// Save writes the config to the given path, creating directories as needed.
// When CloudProfiles is populated (post-migration), the four legacy cloud
// fields are stripped from the saved YAML — they're maintained in memory as
// mirrors for legacy proto reporting, but disk should reflect the
// profile-only world so users editing the file don't get bitten by the
// split-state bug that motivated this refactor (cloud_model edited, profile
// untouched, runtime still on the old model).
func Save(cfg Config, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory %q: %w", dir, err)
	}

	if len(cfg.CloudProfiles) > 0 {
		cfg.CloudProvider = ""
		cfg.CloudModel = ""
		cfg.CloudAPIKey = ""
		cfg.CloudBaseURL = ""
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file %q: %w", path, err)
	}
	return nil
}
