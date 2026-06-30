package builtins

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/gitflow"
)

type gitWorktreeCap struct{}

// GitWorktree constructs the git_worktree capability. W-tier.
func GitWorktree() capabilities.Capability { return gitWorktreeCap{} }

func (gitWorktreeCap) Name() string            { return "git_worktree" }
func (gitWorktreeCap) Tier() capabilities.Tier { return capabilities.TierW }
func (gitWorktreeCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (gitWorktreeCap) Description() string {
	return "Create a linked git worktree on a new branch. Args: {path: string, branch: string, trunk?: string, cwd?: string}."
}
func (gitWorktreeCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["path", "branch"],
		"properties": {
			"path":   {"type": "string"},
			"branch": {"type": "string"},
			"trunk":  {"type": "string"},
			"cwd":    {"type": "string"}
		}
	}`)
}

type gitWorktreeArgs struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Trunk  string `json:"trunk"`
	Cwd    string `json:"cwd"`
}

func (gitWorktreeCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a gitWorktreeArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("git_worktree: parse args: %w", err)
	}
	dir := a.Cwd
	if dir == "" {
		dir = call.WorkDir
	}
	r, err := gitflow.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("git_worktree: %w", err)
	}
	cfg, err := gitflow.Resolve(ctx, r, gitflow.Config{Trunk: a.Trunk})
	if err != nil {
		return nil, fmt.Errorf("git_worktree: %w", err)
	}
	path, err := r.CreateWorktree(ctx, a.Path, a.Branch, cfg.Trunk)
	if err != nil {
		return nil, err
	}
	return capabilities.NewTextResult(fmt.Sprintf("created worktree at %s on branch %s", path, a.Branch)), nil
}
