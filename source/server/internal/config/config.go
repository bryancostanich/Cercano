package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds all Cercano configuration values.
type Config struct {
	OllamaURL      string `yaml:"ollama_url"`
	LocalModel     string `yaml:"local_model"`
	EmbeddingModel string `yaml:"embedding_model"`
	CloudProvider  string `yaml:"cloud_provider"`
	CloudModel     string `yaml:"cloud_model"`
	CloudAPIKey    string `yaml:"cloud_api_key"`
	Port           string `yaml:"port"`
}

// Defaults returns a Config with default values.
func Defaults() Config {
	return Config{
		OllamaURL:      "http://localhost:11434",
		LocalModel:     "qwen3-coder",
		EmbeddingModel: "nomic-embed-text",
		Port:           "50052",
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
		if cfg.LocalModel == "" {
			cfg.LocalModel = defaults.LocalModel
		}
		if cfg.EmbeddingModel == "" {
			cfg.EmbeddingModel = defaults.EmbeddingModel
		}
		if cfg.Port == "" {
			cfg.Port = defaults.Port
		}
	}

	applyEnvOverrides(&cfg)
	return cfg, nil
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

// Save writes the config to the given path, creating directories as needed.
func Save(cfg Config, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory %q: %w", dir, err)
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
