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
	OllamaURL          string            `yaml:"ollama_url"`
	LocalRuntime       string            `yaml:"local_runtime"`
	LocalModel         string            `yaml:"local_model"`
	EmbeddingModel     string            `yaml:"embedding_model"`
	CloudProvider      string            `yaml:"cloud_provider,omitempty"`
	CloudModel         string            `yaml:"cloud_model,omitempty"`
	CloudAPIKey        string            `yaml:"cloud_api_key,omitempty"`
	CloudBaseURL       string            `yaml:"cloud_base_url,omitempty"`
	CloudProfiles      []CloudProfile    `yaml:"cloud_profiles"`
	ActiveCloudProfile string            `yaml:"active_cloud_profile"`
	LocusMode          string            `yaml:"locus_mode"` // cloud_only|cloud_primary|local_primary|local_only
	Port               string            `yaml:"port"`
	LlamaServer        LlamaServerConfig `yaml:"llama_server"`
	Compaction         CompactionConfig  `yaml:"compaction"`
	Watchdog           WatchdogConfig    `yaml:"watchdog"`
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
	// summarization. Empty falls back to LocalModel (the main-loop model).
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
		OllamaURL:      "http://localhost:11434",
		LocalRuntime:   "ollama",
		LocalModel:     "qwen3-coder",
		EmbeddingModel: "nomic-embed-text",
		LocusMode:      "local_primary",
		Port:           "50052",
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
			Checks:        []string{"debug-loop", "commit-checkpoint", "plain-english"},
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
				return cfg, nil
			}
			return cfg, fmt.Errorf("failed to read config file %q: %w", path, err)
		}

		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("failed to parse config file %q: %w", path, err)
		}

		// Re-apply defaults for any fields not set in the file
		defaults := Defaults()
		if cfg.OllamaURL == "" {
			cfg.OllamaURL = defaults.OllamaURL
		}
		if cfg.LocalRuntime == "" {
			cfg.LocalRuntime = defaults.LocalRuntime
		}
		if cfg.LocalModel == "" {
			cfg.LocalModel = defaults.LocalModel
		}
		if cfg.EmbeddingModel == "" {
			cfg.EmbeddingModel = defaults.EmbeddingModel
		}
		if cfg.Port == "" {
			cfg.Port = defaults.Port
		}
		applyLlamaServerDefaults(&cfg.LlamaServer, defaults.LlamaServer)
	}

	applyEnvOverrides(&cfg)
	migrateCloudProfiles(&cfg)
	autoDetectMeridianRoute(&cfg)
	return cfg, nil
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
	if v := os.Getenv("CERCANO_LOCAL_MODEL"); v != "" {
		cfg.LocalModel = v
	}
	if v := os.Getenv("CERCANO_LOCAL_RUNTIME"); v != "" {
		cfg.LocalRuntime = v
	}
	if v := os.Getenv("CERCANO_EMBEDDING_MODEL"); v != "" {
		cfg.EmbeddingModel = v
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
