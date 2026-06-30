package builtins

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/gitflow"
)

type gitBisectCap struct{}

// GitBisect constructs the git_bisect capability. W-tier.
func GitBisect() capabilities.Capability { return gitBisectCap{} }

func (gitBisectCap) Name() string            { return "git_bisect" }
func (gitBisectCap) Tier() capabilities.Tier { return capabilities.TierW }
func (gitBisectCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (gitBisectCap) Description() string {
	return "Bisect the range good..bad using a test command to find the first bad commit. Args: {good: string, bad?: string, test_command?: string, cwd?: string}."
}
func (gitBisectCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["good"],
		"properties": {
			"good":         {"type": "string"},
			"bad":          {"type": "string"},
			"test_command": {"type": "string"},
			"cwd":          {"type": "string"}
		}
	}`)
}

type gitBisectArgs struct {
	Good        string `json:"good"`
	Bad         string `json:"bad"`
	TestCommand string `json:"test_command"`
	Cwd         string `json:"cwd"`
}

func (gitBisectCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a gitBisectArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("git_bisect: parse args: %w", err)
	}
	dir := a.Cwd
	if dir == "" {
		dir = call.WorkDir
	}
	if a.Good == "" {
		return nil, fmt.Errorf("git_bisect: good is required (a known-good commit/ref)")
	}
	r, err := gitflow.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("git_bisect: %w", err)
	}
	bad := a.Bad
	if bad == "" {
		bad = "HEAD"
	}
	testCmd := a.TestCommand
	if testCmd == "" {
		cfg, err := gitflow.Resolve(ctx, r, gitflow.Config{})
		if err != nil {
			return nil, fmt.Errorf("git_bisect: %w", err)
		}
		testCmd = cfg.TestCommand
	}
	if testCmd == "" {
		return nil, fmt.Errorf("git_bisect: a test_command is required (arg or .cercano/gitflow.yaml)")
	}
	sha, err := r.BisectRun(ctx, a.Good, bad, testCmd)
	if err != nil {
		return nil, err
	}
	return capabilities.NewTextResult(fmt.Sprintf("first bad commit: %s", sha)), nil
}
