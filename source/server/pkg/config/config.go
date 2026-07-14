package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultWorkerIdleTimeout is the idle window after which a warm per-conversation
// worker with no new turn is reaped. It is a NAMED, documented value (not a
// buried literal) so the policy is visible and overridable via config.
//
// Reasoning for 5 minutes: interactive conversations come in bursts (a user
// reads a reply, thinks, replies). A window shorter than a normal think-pause
// would cold-start the worker mid-conversation and re-pay spawn + provider-build
// latency — defeating the warm-pool. A window much longer would let a walked-away
// conversation pin a worker (and its GPU/provider connections) for a long time.
// Five minutes comfortably exceeds a typical human turn-gap while still freeing
// resources from truly-abandoned conversations promptly.
const DefaultWorkerIdleTimeout = 5 * time.Minute

// WorkerIdleTimeout resolves the effective idle-reap window from the config's
// WorkerIdleTimeoutSeconds field:
//
//	> 0 → that many seconds (an explicit operator override)
//	== 0 → DefaultWorkerIdleTimeout (the omitted-field / "use the default" case)
//	< 0 → 0 duration, which the reaper treats as DISABLED (never reap)
func (c Config) WorkerIdleTimeout() time.Duration {
	switch {
	case c.WorkerIdleTimeoutSeconds > 0:
		return time.Duration(c.WorkerIdleTimeoutSeconds) * time.Second
	case c.WorkerIdleTimeoutSeconds < 0:
		return 0 // disabled sentinel
	default:
		return DefaultWorkerIdleTimeout
	}
}

// CloudProfile is one named cloud provider configuration. The API key is NOT
// stored here — it lives in the OS keychain keyed by Name.
//
// Route names a specific access path with its own auth + identification
// conventions:
//   - "direct" (or "")  — vanilla path to the upstream provider (Anthropic
//     direct, OpenAI direct, etc.). API key auth, no special headers.
//   - "subscription"    — native OAuth subscription path. Cercano stores its
//     own token lineage and calls the upstream Messages API directly.
//
// Route is an open enum — future bridges (CCR, etc.) get their own value and
// adapter-specific handling. Empty string is treated as "direct".
type CloudProfile struct {
	Name    string `yaml:"name"`
	Flavor  string `yaml:"flavor"`            // messages | chat_completions | responses | bedrock
	Backend string `yaml:"backend,omitempty"` // chat_completions only: selects per-backend quirks (openai|gemini|groq|…); empty → defensive default
	Route   string `yaml:"route,omitempty"`   // direct (default) | subscription | ccr (future) | …
	// Provider names the vendor whose cost-tier table this profile draws its
	// per-request models from (anthropic|openai|google|…). It bridges "how I
	// connect" (route/flavor/auth on this profile) to "which vendor's model
	// lineup" (model_profiles.cloud.providers[Provider]). Empty is inferred
	// from Flavor/Backend at load (see inferProviderVendor).
	Provider   string `yaml:"provider,omitempty"`
	BaseURL    string `yaml:"base_url"`
	Model      string `yaml:"model"`
	Region     string `yaml:"region,omitempty"`      // bedrock: AWS region (required)
	AWSProfile string `yaml:"aws_profile,omitempty"` // bedrock: optional ~/.aws named profile
}

// CostTier names a closed-cloud pricing class. Unlike the capability Tier
// taxonomy (which spans open + cloud), cost tiers exist only for hosted
// vendor models, whose model lineups are priced economy → standard → premium.
type CostTier string

const (
	CostEconomy  CostTier = "economy"
	CostStandard CostTier = "standard"
	CostPremium  CostTier = "premium"
)

// CostTierModel holds the model id serving one vendor+cost-tier slot.
type CostTierModel struct {
	Model string `yaml:"model"`
}

// VendorCostTiers is one vendor's three-tier cost table. Empty slots mean the
// vendor doesn't distinguish that tier; resolution falls back to the active
// profile's own Model.
type VendorCostTiers struct {
	Economy  CostTierModel `yaml:"economy"`
	Standard CostTierModel `yaml:"standard"`
	Premium  CostTierModel `yaml:"premium"`
}

// CloudCostProfiles maps a vendor (anthropic|openai|google|…) to its cost
// table. The vendor key matches CloudProfile.Provider.
type CloudCostProfiles struct {
	Providers map[string]VendorCostTiers `yaml:"providers"`
}

// ModelProfiles is the vendor-keyed cloud model selection table. Closed cloud
// models are vendor-owned, so "which model" for a cost tier is keyed by the
// active profile's vendor, not by the capability tier's cloud slot (retired —
// see ModelTier.Cloud). Open/local models keep the capability-tier `open`
// slots untouched.
type ModelProfiles struct {
	Cloud CloudCostProfiles `yaml:"cloud"`
}

// ResolveCloud returns the model configured for a vendor+cost-tier pair.
// ok=false when the vendor is unknown or that tier's slot is empty — the
// caller falls back to the active profile's own Model.
func (m ModelProfiles) ResolveCloud(vendor string, tier CostTier) (string, bool) {
	if vendor == "" {
		return "", false
	}
	vt, ok := m.Cloud.Providers[vendor]
	if !ok {
		return "", false
	}
	var id string
	switch tier {
	case CostEconomy:
		id = vt.Economy.Model
	case CostStandard:
		id = vt.Standard.Model
	case CostPremium:
		id = vt.Premium.Model
	}
	if id == "" {
		return "", false
	}
	return id, true
}

// vendorHasModel reports whether model appears anywhere in the vendor's cost
// table. Used by the fail-loud guard to distinguish a table-backed model from
// a foreign-vendor id.
func (m ModelProfiles) vendorHasModel(vendor, model string) bool {
	vt, ok := m.Cloud.Providers[vendor]
	if !ok {
		return false
	}
	return model != "" && (vt.Economy.Model == model || vt.Standard.Model == model || vt.Premium.Model == model)
}

// ResolveCloudModelForTier picks the cloud model for a capability tier: it maps
// the tier to a cost tier, resolves the active profile's vendor against the
// cost table, and falls back to the profile's own Model when the tier has no
// cloud cost-tier meaning (embedding) or the vendor+tier slot is unset. The
// fail-loud guard runs on the chosen model before it's returned.
func (m ModelProfiles) ResolveCloudModelForTier(prof CloudProfile, tier Tier) string {
	vendor := prof.Provider
	if vendor == "" {
		vendor = inferProviderVendor(prof)
	}
	model := prof.Model
	if ct, ok := CostTierForCapability(tier); ok {
		if resolved, ok := m.ResolveCloud(vendor, ct); ok {
			model = resolved
		}
	}
	m.guardCloudModel(vendor, model, prof.Model)
	return model
}

// guardCloudModel emits a loud WARNING when a cloud model is about to be used
// that is neither in the active vendor's cost table nor the active profile's
// own Model. It never fails the request — the point is to surface the
// silent-rejection class of bug (an Anthropic id sent to a Codex vendor, which
// the vendor then rejects) that is otherwise invisible until the chat errors.
func (m ModelProfiles) guardCloudModel(vendor, model, profileModel string) {
	if model == "" || model == profileModel {
		return
	}
	if m.vendorHasModel(vendor, model) {
		return
	}
	log.Printf("[cloud] resolved model %q is not in vendor %q cost table and is not the active profile's model — the provider may reject it", model, vendor)
}

// CostTierForCapability maps an internal capability tier to the cost tier its
// cloud lane bills against. The embedding tier has no cloud cost-tier need
// (embeddings run on the local runtime), so it returns ok=false.
func CostTierForCapability(t Tier) (CostTier, bool) {
	switch t {
	case TierMostCapable:
		return CostPremium, true
	case TierEveryday:
		return CostStandard, true
	case TierFastLight:
		return CostEconomy, true
	case TierFastLightText:
		return CostEconomy, true
	}
	return "", false
}

// inferProviderVendor derives a profile's vendor from its wire flavor (and, for
// chat_completions, its backend) when Provider is unset. Used to give
// existing/legacy configs a vendor without a hand edit. Flavor strings are
// duplicated as literals here rather than imported from cloudfactory to avoid
// an import cycle (cloudfactory depends on config).
func inferProviderVendor(p CloudProfile) string {
	switch p.Flavor {
	case "responses":
		return "openai"
	case "messages":
		return "anthropic"
	case "bedrock":
		return "anthropic"
	case "chat_completions":
		if p.Backend != "" {
			return p.Backend
		}
		return "openai"
	}
	return ""
}

// Config holds all Cercano configuration values.
//
// The four "Cloud*" top-level fields (CloudProvider, CloudModel, CloudAPIKey,
// CloudBaseURL) are legacy — pre-profile shape that gets migrated into a
// provider-named entry in CloudProfiles on first load. They remain as
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
	BackupCloudProfile string `yaml:"backup_cloud_profile,omitempty"`
	LocusMode          string `yaml:"locus_mode"` // cloud_only|cloud_primary|open_primary|open_only
	Port               string `yaml:"port"`
	// ExecutionMode selects how a conversation's turns are executed:
	//   "worker"     — each turn runs in a dedicated child process ("cercano
	//                  worker") so a turn that panics/hangs/wedges takes down
	//                  only that conversation's worker, never the host. This is
	//                  the crash-isolated default (the raison d'être of the
	//                  worker-process work).
	//   "in_process" — turns run inside the host process (embedded mode / tests).
	// Empty is treated as "worker" (the production default from Defaults()).
	ExecutionMode string `yaml:"execution_mode,omitempty"`
	// WorkerIdleTimeoutSeconds bounds how long a per-conversation warm worker
	// (execution_mode: worker) may sit idle — no new turn — before the pool's
	// idle-reaper kills it. Idle conversations otherwise pin a worker process
	// (and its provider connections) indefinitely on a long-lived host.
	//
	// Sentinel: 0 means "use the default" (DefaultWorkerIdleTimeout via
	// WorkerIdleTimeout()), NOT "disabled" — omitting the field should still
	// reap. To DISABLE reaping entirely set a negative value (< 0).
	WorkerIdleTimeoutSeconds int               `yaml:"worker_idle_timeout_seconds,omitempty"`
	LlamaServer              LlamaServerConfig `yaml:"llama_server"`
	Compaction               CompactionConfig  `yaml:"compaction"`
	Watchdog                 WatchdogConfig    `yaml:"watchdog"`
	ToolLoop                 ToolLoopConfig    `yaml:"tool_loop"`
	Models                   ModelsConfig      `yaml:"models"`
	// ModelProfiles is the vendor-keyed cloud model selection table (top-level
	// key model_profiles). Closed cloud model selection resolves here — keyed
	// by the active profile's vendor — retiring the per-tier models.tiers.*.cloud
	// slots (kept load-tolerant, no longer read; see ModelTier.Cloud).
	ModelProfiles ModelProfiles `yaml:"model_profiles"`
	// Catalog selects the active model-catalog backend for browse/search.
	Catalog CatalogConfig `yaml:"catalog,omitempty"`
}

// CatalogConfig selects the active model-catalog backend. Exactly one backend
// is active at a time; adding a source (HuggingFace, Ollama, …) is a new
// backend, not a change to this shape.
type CatalogConfig struct {
	// Backend is the active catalog source: "huggingface" (default) or
	// "ollama". An unknown value fails loud when the registry is wired.
	Backend string `yaml:"backend,omitempty"`
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

const (
	// DefaultToolLoopMaxIterations is the default cap on LLM round-trips in one
	// agentic turn. It is the single source of truth for the runtime default and
	// for the generated default config.
	DefaultToolLoopMaxIterations = 200
	// UnlimitedToolLoopMaxIterations disables the turn-level tool-loop cap.
	UnlimitedToolLoopMaxIterations = -1
)

// ToolLoopConfig controls the agentic tool loop.
// MaxIterations caps LLM round-trips per turn; 0 means use the default, and -1
// disables the cap.
type ToolLoopConfig struct {
	MaxIterations int `yaml:"max_iterations"`
}

// EffectiveMaxIterations resolves the configured tool-loop cap. The boolean is
// true when the cap is disabled.
func EffectiveMaxIterations(n int) (max int, unlimited bool) {
	if n == UnlimitedToolLoopMaxIterations {
		return 0, true
	}
	if n > 0 {
		return n, false
	}
	return DefaultToolLoopMaxIterations, false
}

// ValidateToolLoopMaxIterations validates the public config/RPC value.
func ValidateToolLoopMaxIterations(n int) bool {
	return n >= UnlimitedToolLoopMaxIterations
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
		OllamaURL:     "http://localhost:11434",
		OpenRuntime:   "llama_server",
		LocusMode:     "cloud_primary",
		Catalog:       CatalogConfig{Backend: "huggingface"},
		Port:          "50052",
		ExecutionMode: "worker",
		// 0 is the "use the default" sentinel; WorkerIdleTimeout() resolves it to
		// DefaultWorkerIdleTimeout. Left as the zero value so an omitted field and
		// the default behave identically.
		WorkerIdleTimeoutSeconds: 0,
		Models:                   ModelsConfig{DefaultProvider: ProviderOpen},
		// Seed the vendor-keyed cloud cost tables. Closed cloud model
		// selection resolves here — keyed by the active profile's vendor.
		// Only the vendors Cercano ships a default lineup for are seeded;
		// others (google, …) are added by the user's config.
		ModelProfiles: ModelProfiles{
			Cloud: CloudCostProfiles{
				Providers: map[string]VendorCostTiers{
					"anthropic": {
						Economy:  CostTierModel{Model: "claude-haiku-4-5"},
						Standard: CostTierModel{Model: "claude-opus-4-8"},
						Premium:  CostTierModel{Model: "claude-fable-5"},
					},
					"openai": {
						Economy:  CostTierModel{Model: "gpt-5-mini"},
						Standard: CostTierModel{Model: "gpt-5.5"},
						Premium:  CostTierModel{Model: "gpt-5.5"},
					},
				},
			},
		},
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
			Checks:        []string{"systematic-debugging", "design-decisions", "verification-strategy", "compute-before-simulate", "commit-checkpoint", "plain-english", "worktree-first", "follow-through"},
			Model:         "",
			EscalateAfter: 2,
			Echo:          false,
		},
		ToolLoop: ToolLoopConfig{
			MaxIterations: DefaultToolLoopMaxIterations,
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

// migrateCloudProfiles synthesizes a provider-named profile from the legacy
// single-cloud fields when no profiles exist yet. Metadata only — the inline
// cloud_api_key is relocated to the keychain by the startup wiring, not here
// (config has no keychain dependency). No-op if profiles already exist or no
// legacy cloud is configured.
func migrateCloudProfiles(cfg *Config) {
	if len(cfg.CloudProfiles) > 0 || cfg.CloudProvider == "" {
		return
	}
	name := legacyCloudProfileName(cfg.CloudProvider)
	flavor := ""
	if cfg.CloudProvider == "anthropic" {
		flavor = "messages"
	}
	cfg.CloudProfiles = []CloudProfile{{
		Name:    name,
		Flavor:  flavor,
		BaseURL: cfg.CloudBaseURL,
		Model:   cfg.CloudModel,
	}}
	cfg.ActiveCloudProfile = name
}

func legacyCloudProfileName(provider string) string {
	provider = strings.TrimSpace(provider)
	switch provider {
	case "":
		return "default"
	case "anthropic":
		return "anthropic"
	default:
		return provider
	}
}

// migrateMeridianToSubscription rewrites legacy Meridian profiles to the native
// subscription route on load. The external Meridian OAuth proxy has been
// removed; the subscription route calls api.anthropic.com directly with our own
// OAuth token. Any profile explicitly on route=meridian — or an un-routed
// profile still pointing at Meridian's default local port (a pre-route config)
// — is flipped to route=subscription with its proxy BaseURL cleared (the
// subscription route pins api.anthropic.com; a leftover :3456 URL would send a
// direct call at a dead proxy).
//
// The migrated profile has no token in our keychain (Meridian read what `claude
// login` wrote, not our own OAuth store), so it lands "absent" until the user
// signs in through the loopback flow — the intended one-time re-auth.
func migrateMeridianToSubscription(cfg *Config) {
	migrated := make([]string, 0, len(cfg.CloudProfiles))
	profileExists := func(name string) bool {
		for _, p := range cfg.CloudProfiles {
			if p.Name == name {
				return true
			}
		}
		return false
	}
	for i := range cfg.CloudProfiles {
		p := &cfg.CloudProfiles[i]
		isMeridian := p.Route == "meridian" ||
			(p.Route == "" && (strings.Contains(p.BaseURL, "127.0.0.1:3456") || strings.Contains(p.BaseURL, "localhost:3456")))
		if !isMeridian {
			continue
		}
		migrated = append(migrated, p.Name)
		p.Route = "subscription"
		p.BaseURL = ""
		if p.Flavor == "" {
			p.Flavor = "messages"
		}
	}
	if len(migrated) == 0 {
		return
	}
	if cfg.ActiveCloudProfile == "" || cfg.ActiveCloudProfile == "meridian" || !profileExists(cfg.ActiveCloudProfile) {
		cfg.ActiveCloudProfile = migrated[0]
	}
	if cfg.BackupCloudProfile == "meridian" || (cfg.BackupCloudProfile != "" && !profileExists(cfg.BackupCloudProfile)) {
		cfg.BackupCloudProfile = migrated[0]
	}
}

// collapseLegacySubscriptionAliases removes stale subscription-route aliases once
// the canonical native subscription profile exists. During the Meridian
// switchover, some configs accumulated default/anthropic subscription profiles
// alongside claude; leaving all three makes the settings UI look like the user
// has multiple Anthropic subscription accounts to sign into. Keep only the
// canonical profile and repair active/backup pointers that referenced a removed
// alias. Direct API-key Anthropic profiles are intentionally untouched because
// they have Route != subscription.
func collapseLegacySubscriptionAliases(cfg *Config) {
	const canonical = "claude"
	canonicalIdx := -1
	for i, p := range cfg.CloudProfiles {
		if p.Name == canonical && p.Flavor == "messages" && p.Route == "subscription" {
			canonicalIdx = i
			break
		}
	}
	if canonicalIdx == -1 {
		return
	}
	removed := map[string]bool{}
	out := cfg.CloudProfiles[:0]
	for i, p := range cfg.CloudProfiles {
		if i != canonicalIdx && isLegacySubscriptionAlias(p) {
			removed[p.Name] = true
			continue
		}
		out = append(out, p)
	}
	cfg.CloudProfiles = out
	if removed[cfg.ActiveCloudProfile] {
		cfg.ActiveCloudProfile = canonical
	}
	if removed[cfg.BackupCloudProfile] {
		cfg.BackupCloudProfile = canonical
	}
}

func isLegacySubscriptionAlias(p CloudProfile) bool {
	if p.Flavor != "messages" || p.Route != "subscription" {
		return false
	}
	switch p.Name {
	case "default", "anthropic", "meridian":
		return true
	}
	return false
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
		if cfg.Catalog.Backend == "" {
			cfg.Catalog.Backend = defaults.Catalog.Backend
		}
		applyLlamaServerDefaults(&cfg.LlamaServer, defaults.LlamaServer)
		applyToolLoopDefaults(&cfg.ToolLoop, defaults.ToolLoop)
	}

	applyEnvOverrides(&cfg)
	finalizeModelTiers(&cfg)
	migrateCloudProfiles(&cfg)
	migrateMeridianToSubscription(&cfg)
	collapseLegacySubscriptionAliases(&cfg)
	if !ValidateToolLoopMaxIterations(cfg.ToolLoop.MaxIterations) {
		return cfg, fmt.Errorf("tool_loop.max_iterations must be -1 or a non-negative integer, got %d", cfg.ToolLoop.MaxIterations)
	}
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

func applyToolLoopDefaults(cfg *ToolLoopConfig, defaults ToolLoopConfig) {
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = defaults.MaxIterations
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

	// ModelProfiles.Cloud.Providers is a map — reallocate so clone and original
	// don't alias the vendor cost table. VendorCostTiers is all-scalar, so a
	// value copy per entry suffices.
	if c.ModelProfiles.Cloud.Providers != nil {
		out.ModelProfiles.Cloud.Providers = make(map[string]VendorCostTiers, len(c.ModelProfiles.Cloud.Providers))
		for k, v := range c.ModelProfiles.Cloud.Providers {
			out.ModelProfiles.Cloud.Providers[k] = v
		}
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
