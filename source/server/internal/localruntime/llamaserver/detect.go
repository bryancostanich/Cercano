// Package llamaserver — detect.go: headless detection of a usable llama-server
// runtime.
//
// Detect runs the same filesystem/PATH checks the setup wizard does, but
// without any interactive prompts or install shell-outs. It is safe to call
// from the config-watcher / UpdateConfig path (which runs inside the server
// process and has no controlling terminal): given a partially-configured
// LlamaServerConfig, it either populates the missing pieces (Binary,
// DefaultModel, defaults) or returns a *DetectError telling the caller
// exactly which prerequisite is unmet so the CLI can prompt the user
// appropriately.
//
// The setup wizard (main.go's ensureLlamaServerSetup) does its own detection
// today so this package can grow without forcing a wizard rewrite; a future
// commit can consolidate.
package llamaserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
)

// DetectError explains why Detect could not fully configure llama-server.
// Missing carries a machine-readable tag ("binary" or "model") so callers
// can pick the right recovery UI without matching on Cause's text.
type DetectError struct {
	Missing string // "binary" | "model"
	Cause   error
}

func (e *DetectError) Error() string {
	return fmt.Sprintf("llama-server detection: %s: %v", e.Missing, e.Cause)
}

func (e *DetectError) Unwrap() error { return e.Cause }

// SuggestedCommand returns the command a user should run to satisfy the
// missing prerequisite. Empty when there's no automated recovery (e.g., a
// user with 3 GGUFs needs to pick one; no command fixes that, and neither
// does a missing binary on a platform/setup with no managed install path).
//
// Must stay in sync with defaultInstallCommand (install.go): this is a
// suggestion the UI displays as the exact command "Install now" will attempt
// to run, so it can never name a command that platform can't actually run.
func (e *DetectError) SuggestedCommand() string {
	if e.Missing != "binary" {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		// llama.cpp is the upstream Homebrew formula that ships llama-server.
		// The setup wizard uses the same command.
		return "brew install llama.cpp"
	case "windows":
		// Only suggest winget when it's actually there to run — otherwise
		// "Install now" would just fail with the same unsupported error.
		if _, err := exec.LookPath("winget"); err == nil {
			return "winget " + strings.Join(wingetInstallArgs(), " ")
		}
		return ""
	default:
		return ""
	}
}

// Detect populates cfg in-place from filesystem inspection. On success cfg is
// ready for a llama-server subprocess launch (Binary + DefaultModel set,
// defaults filled in). On failure cfg may be partially populated (defaults
// applied, Binary sometimes set) and the returned *DetectError names the
// missing prerequisite.
//
// Non-interactive: no prompts, no installs, no downloads. This is the seam
// the config watcher calls when the user flips local_runtime to llama_server
// — swap-time must never block on I/O beyond a `LookPath` + directory scan.
func Detect(ctx context.Context, cfg *config.LlamaServerConfig) error {
	applyDefaults(cfg)

	// mkdir the model_dirs so Discover doesn't fail with ENOENT on a fresh
	// install. Idempotent; safe to re-run.
	if err := ensureModelDirs(cfg.ModelDirs); err != nil {
		return &DetectError{Missing: "binary", Cause: fmt.Errorf("prepare model_dirs: %w", err)}
	}

	binary, err := findBinary(*cfg)
	if err != nil {
		return &DetectError{Missing: "binary", Cause: err}
	}
	cfg.Binary = binary
	cfg.Enabled = true

	if strings.TrimSpace(cfg.DefaultModel) != "" {
		return nil
	}
	all, err := NewProvider(*cfg).Discover(ctx)
	if err != nil {
		return &DetectError{Missing: "model", Cause: err}
	}
	// Discover returns both on-disk .gguf files (DownloadState="downloaded")
	// and catalog entries for known-but-not-yet-downloaded models
	// (DownloadState="not_downloaded"). Both have Path populated (the catalog
	// path is where the file would live), so filtering on DownloadState is
	// the right way to isolate "actually usable right now."
	var present []localruntime.ModelRecord
	for _, m := range all {
		if m.DownloadState == localruntime.Downloaded {
			present = append(present, m)
		}
	}
	if len(present) == 0 {
		return &DetectError{Missing: "model", Cause: errors.New("no GGUF files in configured model_dirs")}
	}
	if len(present) == 1 {
		// A single present GGUF is the only possible default. We still set it
		// (there is no alternative), leaving capability warnings to the readiness
		// path rather than failing detection.
		cfg.DefaultModel = present[0].Path
		return nil
	}
	// Multiple GGUFs present. Prefer a model we have verified behaves correctly
	// under llama-server for agentic use (tool calls + multi-turn tool results +
	// plain chat) rather than forcing the user to disambiguate or, worse,
	// picking a model with known problems. GLM-4.5-Air loads and passes tool
	// probes but returns empty content on plain chat on the pinned build, so it
	// must never be auto-selected as the default.
	if pick := preferredPresentModel(present); pick != "" {
		cfg.DefaultModel = pick
		return nil
	}
	return &DetectError{
		Missing: "model",
		Cause:   fmt.Errorf("found %d GGUF models; set llama_server.default_model to disambiguate", len(present)),
	}
}

// preferredModelSubstrings ranks known-good llama-server models for autoselect.
// Earlier entries win. These are matched case-insensitively against the GGUF
// path/filename. Qwen3 instruct GGUFs are our verified tool+chat default.
var preferredModelSubstrings = []string{
	"qwen3-30b-a3b-instruct",
	"qwen3-14b",
	"qwen3",
}

// preferredPresentModel returns the path of the highest-ranked known-good model
// among the present GGUFs, or "" when none of the present models is on the
// preferred list (the caller then surfaces the disambiguation error rather than
// guessing).
func preferredPresentModel(present []localruntime.ModelRecord) string {
	for _, want := range preferredModelSubstrings {
		for _, m := range present {
			if strings.Contains(strings.ToLower(m.Path), want) {
				return m.Path
			}
		}
	}
	return ""
}

// applyDefaults populates fields that are unset with their config.Defaults()
// values. Mirrors the private applyLlamaServerDefaults in the config package
// but works on a LlamaServerConfig directly so callers with just the sub-
// struct in hand (like the watcher) can use it without threading the full
// Config through.
func applyDefaults(cfg *config.LlamaServerConfig) {
	defaults := config.Defaults().LlamaServer
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
}

func ensureModelDirs(dirs []string) error {
	for _, dir := range dirs {
		expanded, err := expandPath(dir)
		if err != nil {
			return err
		}
		if expanded == "" {
			continue
		}
		if err := os.MkdirAll(expanded, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// findBinary honors an explicit cfg.Binary path when set (expanding ~), else
// falls back to $PATH lookup. Matches findLlamaServerBinary in the setup
// wizard — same semantics so a hand-set path in the yaml behaves identically
// on both paths.
func findBinary(cfg config.LlamaServerConfig) (string, error) {
	if strings.TrimSpace(cfg.Binary) != "" {
		path, err := expandPath(cfg.Binary)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			return "", fmt.Errorf("llama-server binary %s is a directory", path)
		}
		return path, nil
	}
	return exec.LookPath("llama-server")
}

// expandPath is provided by provider.go in this package.
