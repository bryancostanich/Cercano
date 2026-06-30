package builtins

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/gitflow"
)

type gitRecoverCap struct{}

// GitRecover constructs the git_recover capability. X-tier.
func GitRecover() capabilities.Capability { return gitRecoverCap{} }

func (gitRecoverCap) Name() string            { return "git_recover" }
func (gitRecoverCap) Tier() capabilities.Tier { return capabilities.TierX }
func (gitRecoverCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (gitRecoverCap) Description() string {
	return "Recover from a failed git workflow: abort an in-progress rebase/merge (mode=abort) or reset a branch to a safety ref (mode=undo). Args: {mode: \"abort\"|\"undo\", label?: string, branch?: string, cwd?: string}."
}
func (gitRecoverCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["mode"],
		"properties": {
			"mode":   {"type": "string", "enum": ["abort", "undo"]},
			"label":  {"type": "string"},
			"branch": {"type": "string"},
			"cwd":    {"type": "string"}
		}
	}`)
}

type gitRecoverArgs struct {
	Mode   string `json:"mode"`
	Label  string `json:"label"`
	Branch string `json:"branch"`
	Cwd    string `json:"cwd"`
}

func (gitRecoverCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a gitRecoverArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("git_recover: parse args: %w", err)
	}
	dir := a.Cwd
	if dir == "" {
		dir = call.WorkDir
	}
	r, err := gitflow.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("git_recover: %w", err)
	}
	switch a.Mode {
	case "abort":
		what, err := r.AbortInProgress(ctx)
		if err != nil {
			return nil, err
		}
		return capabilities.NewTextResult(fmt.Sprintf("aborted %s", what)), nil
	case "undo":
		label := a.Label
		if label == "" {
			label = "land"
		}
		branch := a.Branch
		if branch == "" {
			cfg, err := gitflow.Resolve(ctx, r, gitflow.Config{})
			if err != nil {
				return nil, fmt.Errorf("git_recover: %w", err)
			}
			branch = cfg.Trunk
		}
		if err := r.ResetToSafety(ctx, label, branch); err != nil {
			return nil, err
		}
		return capabilities.NewTextResult(fmt.Sprintf("reset %s to safety ref %q", branch, label)), nil
	default:
		return nil, fmt.Errorf("git_recover: mode must be \"abort\" or \"undo\"")
	}
}
