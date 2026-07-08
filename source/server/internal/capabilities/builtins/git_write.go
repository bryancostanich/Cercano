package builtins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"

	"cercano/source/server/internal/capabilities"
)

// gitAddCap stages files for the next commit.
type gitAddCap struct{}

// GitAdd constructs the git_add capability. W-tier.
func GitAdd() capabilities.Capability { return gitAddCap{} }

func (gitAddCap) Name() string                  { return "git_add" }
func (gitAddCap) Tier() capabilities.Tier        { return capabilities.TierW }
func (gitAddCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (gitAddCap) Description() string {
	return "Stage files for the next commit. Args: {paths: [string], cwd?: string}."
}
func (gitAddCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["paths"],
		"properties": {
			"paths": {"type": "array", "items": {"type": "string"}},
			"cwd":   {"type": "string"}
		}
	}`)
}

type gitAddArgs struct {
	Paths []string `json:"paths"`
	Cwd   string   `json:"cwd"`
}

func (gitAddCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a gitAddArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("git_add: parse args: %w", err)
	}
	if len(a.Paths) == 0 {
		return nil, errors.New("git_add: paths is required and must contain at least one entry")
	}
	args := append([]string{"add", "--"}, a.Paths...)
	cmd := exec.CommandContext(ctx, "git", args...)
	dir := a.Cwd
	if dir == "" {
		dir = call.WorkDir
	}
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git_add: %w: %s", err, string(bytes.TrimSpace(out)))
	}
	res := capabilities.NewTextResult(fmt.Sprintf("staged %d path(s)", len(a.Paths)))
	res.Detail = countLabel(len(a.Paths), "path", "paths")
	return res, nil
}

// gitCommitCap creates a commit with the given message. Optional --no-verify
// is intentionally NOT exposed — bypassing hooks is exactly the kind of thing
// we want the user to consciously type into run_command if they really need.
type gitCommitCap struct{}

// GitCommit constructs the git_commit capability. W-tier.
func GitCommit() capabilities.Capability { return gitCommitCap{} }

func (gitCommitCap) Name() string                  { return "git_commit" }
func (gitCommitCap) Tier() capabilities.Tier        { return capabilities.TierW }
func (gitCommitCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (gitCommitCap) Description() string {
	return "Create a commit with the given message from currently staged changes. Args: {message: string, cwd?: string}."
}
func (gitCommitCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["message"],
		"properties": {
			"message": {"type": "string"},
			"cwd":     {"type": "string"}
		}
	}`)
}

type gitCommitArgs struct {
	Message string `json:"message"`
	Cwd     string `json:"cwd"`
}

func (gitCommitCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a gitCommitArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("git_commit: parse args: %w", err)
	}
	if a.Message == "" {
		return nil, errors.New("git_commit: message is required")
	}
	cmd := exec.CommandContext(ctx, "git", "commit", "-m", a.Message)
	dir := a.Cwd
	if dir == "" {
		dir = call.WorkDir
	}
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git_commit: %w: %s", err, string(bytes.TrimSpace(out)))
	}
	return capabilities.NewTextResult(string(bytes.TrimSpace(out))), nil
}

// gitPushCap pushes to a remote. X-tier — pushing publishes changes
// outside the user's local checkout, which is the threshold where mistakes
// become other-people-visible.
type gitPushCap struct{}

// GitPush constructs the git_push capability. X-tier.
func GitPush() capabilities.Capability { return gitPushCap{} }

func (gitPushCap) Name() string                  { return "git_push" }
func (gitPushCap) Tier() capabilities.Tier        { return capabilities.TierX }
func (gitPushCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (gitPushCap) Description() string {
	return "Push to a remote. Defaults: remote=origin, current branch. Args: {remote?: string, branch?: string, force?: bool (default false), cwd?: string}."
}
func (gitPushCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"properties": {
			"remote": {"type": "string", "default": "origin"},
			"branch": {"type": "string"},
			"force":  {"type": "boolean", "default": false},
			"cwd":    {"type": "string"}
		}
	}`)
}

type gitPushArgs struct {
	Remote string `json:"remote"`
	Branch string `json:"branch"`
	Force  bool   `json:"force"`
	Cwd    string `json:"cwd"`
}

func (gitPushCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a gitPushArgs
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &a); err != nil {
			return nil, fmt.Errorf("git_push: parse args: %w", err)
		}
	}
	args := []string{"push"}
	if a.Force {
		// Use --force-with-lease, not --force — much safer: still refuses
		// to push if the remote moved out from under you.
		args = append(args, "--force-with-lease")
	}
	if a.Remote != "" {
		args = append(args, a.Remote)
		if a.Branch != "" {
			args = append(args, a.Branch)
		}
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	dir := a.Cwd
	if dir == "" {
		dir = call.WorkDir
	}
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git_push: %w: %s", err, string(bytes.TrimSpace(out)))
	}
	return capabilities.NewTextResult(string(bytes.TrimSpace(out))), nil
}

// gitResetHardCap wipes uncommitted changes back to the given revision.
// X-tier — data loss potential.
type gitResetHardCap struct{}

// GitResetHard constructs the git_reset_hard capability. X-tier.
func GitResetHard() capabilities.Capability { return gitResetHardCap{} }

func (gitResetHardCap) Name() string                  { return "git_reset_hard" }
func (gitResetHardCap) Tier() capabilities.Tier        { return capabilities.TierX }
func (gitResetHardCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (gitResetHardCap) Description() string {
	return "Reset the working tree to the given revision, discarding uncommitted changes. Args: {revision: string (e.g. 'HEAD', 'HEAD~3', a SHA), cwd?: string}."
}
func (gitResetHardCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["revision"],
		"properties": {
			"revision": {"type": "string"},
			"cwd":      {"type": "string"}
		}
	}`)
}

type gitResetHardArgs struct {
	Revision string `json:"revision"`
	Cwd      string `json:"cwd"`
}

func (gitResetHardCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a gitResetHardArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("git_reset_hard: parse args: %w", err)
	}
	if a.Revision == "" {
		return nil, errors.New("git_reset_hard: revision is required")
	}
	cmd := exec.CommandContext(ctx, "git", "reset", "--hard", a.Revision)
	dir := a.Cwd
	if dir == "" {
		dir = call.WorkDir
	}
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git_reset_hard: %w: %s", err, string(bytes.TrimSpace(out)))
	}
	return capabilities.NewTextResult(string(bytes.TrimSpace(out))), nil
}
