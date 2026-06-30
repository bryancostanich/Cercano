package builtins

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/gitflow"
)

type gitSquashCap struct{}

// GitSquash constructs the git_squash capability. W-tier.
func GitSquash() capabilities.Capability { return gitSquashCap{} }

func (gitSquashCap) Name() string            { return "git_squash" }
func (gitSquashCap) Tier() capabilities.Tier { return capabilities.TierW }
func (gitSquashCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (gitSquashCap) Description() string {
	return "Squash all branch commits since trunk into one commit. Args: {subject: string, body?: string, trunk?: string, cwd?: string}."
}
func (gitSquashCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["subject"],
		"properties": {
			"subject": {"type": "string"},
			"body":    {"type": "string"},
			"trunk":   {"type": "string"},
			"cwd":     {"type": "string"}
		}
	}`)
}

type gitSquashArgs struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Trunk   string `json:"trunk"`
	Cwd     string `json:"cwd"`
}

func (gitSquashCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a gitSquashArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("git_squash: parse args: %w", err)
	}
	dir := a.Cwd
	if dir == "" {
		dir = call.WorkDir
	}
	r, err := gitflow.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("git_squash: %w", err)
	}
	cfg, err := gitflow.Resolve(ctx, r, gitflow.Config{Trunk: a.Trunk})
	if err != nil {
		return nil, fmt.Errorf("git_squash: %w", err)
	}
	sha, err := r.SquashToOne(ctx, cfg.Trunk, a.Subject, a.Body)
	if err != nil {
		return nil, err
	}
	return capabilities.NewTextResult(fmt.Sprintf("squashed branch to one commit %s", sha[:min(12, len(sha))])), nil
}
