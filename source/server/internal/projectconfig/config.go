// Package projectconfig loads .cercano/config.yaml for a project directory.
package projectconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the on-disk schema for .cercano/config.yaml.
type Config struct {
	Validator ValidatorConfig `yaml:"validator"`
}

type ValidatorConfig struct {
	Command string `yaml:"command"`
	Skip    bool   `yaml:"skip"`
}

// Load reads .cercano/config.yaml under workDir. A missing file returns the
// zero-value Config and no error. A malformed file returns an error wrapping
// the parse failure with the prefix "invalid .cercano/config.yaml".
func Load(workDir string) (Config, error) {
	path := filepath.Join(workDir, ".cercano", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("invalid .cercano/config.yaml: %w", err)
	}
	return cfg, nil
}
