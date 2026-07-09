package builtins

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/gitflow"
)

type checkpointCap struct{}

// Checkpoint constructs the checkpoint capability. W-tier.
func Checkpoint() capabilities.Capability { return checkpointCap{} }

func (checkpointCap) Name() string            { return "checkpoint" }
func (checkpointCap) Tier() capabilities.Tier { return capabilities.TierW }
func (checkpointCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (checkpointCap) Description() string {
	return "Commit a solved unit of work on the current branch (never pushed). By default this refuses trunk and stages all changes; for explicit current-branch work, pass allow_trunk with paths so only those files are saved. Args: {subject: string, body?: string, trunk?: string, cwd?: string, paths?: [string], allow_trunk?: bool}."
}
func (checkpointCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["subject"],
		"properties": {
			"subject":     {"type": "string"},
			"body":        {"type": "string"},
			"trunk":       {"type": "string"},
			"cwd":         {"type": "string"},
			"paths":       {"type": "array", "items": {"type": "string"}},
			"allow_trunk": {"type": "boolean"}
		}
	}`)
}

type checkpointArgs struct {
	Subject    string   `json:"subject"`
	Body       string   `json:"body"`
	Trunk      string   `json:"trunk"`
	Cwd        string   `json:"cwd"`
	Paths      []string `json:"paths"`
	AllowTrunk bool     `json:"allow_trunk"`
}

func (checkpointCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a checkpointArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("checkpoint: parse args: %w", err)
	}
	dir := a.Cwd
	if dir == "" {
		dir = call.WorkDir
	}
	r, err := gitflow.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: %w", err)
	}
	cfg, err := gitflow.Resolve(ctx, r, gitflow.Config{Trunk: a.Trunk})
	if err != nil {
		return nil, fmt.Errorf("checkpoint: %w", err)
	}
	sha, err := r.CheckpointWithOptions(ctx, a.Subject, a.Body, cfg.Trunk, gitflow.CheckpointOptions{
		AllowTrunk: a.AllowTrunk,
		Paths:      a.Paths,
	})
	if err != nil {
		return nil, err
	}
	return capabilities.NewTextResult(fmt.Sprintf("checkpoint committed %s on the current branch (not pushed)", sha[:min(12, len(sha))])), nil
}
