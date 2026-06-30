package gitflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds project-level gitflow settings, read from .cercano/gitflow.yaml.
type Config struct {
	Trunk          string            `yaml:"trunk"`
	TestCommand    string            `yaml:"test_command"`
	SensitivePaths []string          `yaml:"sensitive_paths"`
	ReviewFloor    int               `yaml:"review_floor"` // hand-edited file count that forces review; default 5
	Regen          map[string]string `yaml:"regen"`        // path-glob -> regen command
}

// Resolve returns a Config for the repo applying the following priority:
//  1. non-zero override field wins
//  2. .cercano/gitflow.yaml in the repo dir (missing file is not an error)
//  3. for Trunk only: auto-detect via git symbolic-ref refs/remotes/origin/HEAD
//
// An unresolved Trunk is an error. ReviewFloor defaults to 5 when unset.
func Resolve(ctx context.Context, r *Repo, override Config) (Config, error) {
	// Read file config (missing is OK).
	var fileCfg Config
	cfgPath := filepath.Join(r.Dir, ".cercano", "gitflow.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("gitflow: reading %s: %w", cfgPath, err)
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &fileCfg); err != nil {
			return Config{}, fmt.Errorf("gitflow: parsing %s: %w", cfgPath, err)
		}
	}

	// Merge: override > file.
	out := fileCfg
	if override.Trunk != "" {
		out.Trunk = override.Trunk
	}
	if override.TestCommand != "" {
		out.TestCommand = override.TestCommand
	}
	if len(override.SensitivePaths) > 0 {
		out.SensitivePaths = override.SensitivePaths
	}
	if override.ReviewFloor != 0 {
		out.ReviewFloor = override.ReviewFloor
	}
	if len(override.Regen) > 0 {
		out.Regen = override.Regen
	}

	// Auto-detect trunk from remote HEAD if still unset.
	if out.Trunk == "" {
		if det, err := r.run(ctx, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
			out.Trunk = strings.TrimPrefix(strings.TrimSpace(det), "refs/remotes/origin/")
			out.Trunk = strings.TrimPrefix(out.Trunk, "origin/")
		}
	}
	if out.Trunk == "" {
		return Config{}, fmt.Errorf("gitflow: trunk branch unresolved — set `trunk` in .cercano/gitflow.yaml or pass an override")
	}
	if out.ReviewFloor == 0 {
		out.ReviewFloor = 5
	}
	return out, nil
}
