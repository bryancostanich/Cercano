package agenttools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
)

// gitAddTool stages files for the next commit.
type gitAddTool struct{}

// GitAdd constructs the git_add tool.
func GitAdd() Tool { return gitAddTool{} }

func (gitAddTool) Name() string             { return "git_add" }
func (gitAddTool) Permission() Permission   { return PermW }
func (gitAddTool) Description() string {
	return "Stage files for the next commit. Args: {paths: [string], cwd?: string}."
}
func (gitAddTool) Schema() json.RawMessage {
	return json.RawMessage(`{
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

func (gitAddTool) Execute(ctx context.Context, raw json.RawMessage) (*Result, error) {
	var a gitAddArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("git_add: parse args: %w", err)
	}
	if len(a.Paths) == 0 {
		return nil, errors.New("git_add: paths is required and must contain at least one entry")
	}
	args := append([]string{"add", "--"}, a.Paths...)
	cmd := exec.CommandContext(ctx, "git", args...)
	if a.Cwd != "" {
		cmd.Dir = a.Cwd
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git_add: %w: %s", err, string(bytes.TrimSpace(out)))
	}
	return &Result{
		Type:   ResultText,
		Text:   fmt.Sprintf("staged %d path(s)", len(a.Paths)),
		Detail: countLabel(len(a.Paths), "path", "paths"),
	}, nil
}

// gitCommitTool creates a commit with the given message. Optional --no-verify
// is intentionally NOT exposed — bypassing hooks is exactly the kind of thing
// we want the user to consciously type into run_command if they really need.
type gitCommitTool struct{}

// GitCommit constructs the git_commit tool.
func GitCommit() Tool { return gitCommitTool{} }

func (gitCommitTool) Name() string             { return "git_commit" }
func (gitCommitTool) Permission() Permission   { return PermW }
func (gitCommitTool) Description() string {
	return "Create a commit with the given message from currently staged changes. Args: {message: string, cwd?: string}."
}
func (gitCommitTool) Schema() json.RawMessage {
	return json.RawMessage(`{
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

func (gitCommitTool) Execute(ctx context.Context, raw json.RawMessage) (*Result, error) {
	var a gitCommitArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("git_commit: parse args: %w", err)
	}
	if a.Message == "" {
		return nil, errors.New("git_commit: message is required")
	}
	cmd := exec.CommandContext(ctx, "git", "commit", "-m", a.Message)
	if a.Cwd != "" {
		cmd.Dir = a.Cwd
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git_commit: %w: %s", err, string(bytes.TrimSpace(out)))
	}
	return &Result{Type: ResultText, Text: string(bytes.TrimSpace(out))}, nil
}

// gitPushTool pushes to a remote. X-tier — pushing publishes changes
// outside the user's local checkout, which is the threshold where mistakes
// become other-people-visible.
type gitPushTool struct{}

// GitPush constructs the git_push tool. X-tier.
func GitPush() Tool { return gitPushTool{} }

func (gitPushTool) Name() string             { return "git_push" }
func (gitPushTool) Permission() Permission   { return PermX }
func (gitPushTool) Description() string {
	return "Push to a remote. Defaults: remote=origin, current branch. Args: {remote?: string, branch?: string, force?: bool (default false), cwd?: string}."
}
func (gitPushTool) Schema() json.RawMessage {
	return json.RawMessage(`{
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

func (gitPushTool) Execute(ctx context.Context, raw json.RawMessage) (*Result, error) {
	var a gitPushArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
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
	if a.Cwd != "" {
		cmd.Dir = a.Cwd
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git_push: %w: %s", err, string(bytes.TrimSpace(out)))
	}
	return &Result{Type: ResultText, Text: string(bytes.TrimSpace(out))}, nil
}

// gitResetHardTool wipes uncommitted changes back to the given revision.
// X-tier — data loss potential.
type gitResetHardTool struct{}

// GitResetHard constructs the git_reset_hard tool. X-tier.
func GitResetHard() Tool { return gitResetHardTool{} }

func (gitResetHardTool) Name() string             { return "git_reset_hard" }
func (gitResetHardTool) Permission() Permission   { return PermX }
func (gitResetHardTool) Description() string {
	return "Reset the working tree to the given revision, discarding uncommitted changes. Args: {revision: string (e.g. 'HEAD', 'HEAD~3', a SHA), cwd?: string}."
}
func (gitResetHardTool) Schema() json.RawMessage {
	return json.RawMessage(`{
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

func (gitResetHardTool) Execute(ctx context.Context, raw json.RawMessage) (*Result, error) {
	var a gitResetHardArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("git_reset_hard: parse args: %w", err)
	}
	if a.Revision == "" {
		return nil, errors.New("git_reset_hard: revision is required")
	}
	cmd := exec.CommandContext(ctx, "git", "reset", "--hard", a.Revision)
	if a.Cwd != "" {
		cmd.Dir = a.Cwd
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git_reset_hard: %w: %s", err, string(bytes.TrimSpace(out)))
	}
	return &Result{Type: ResultText, Text: string(bytes.TrimSpace(out))}, nil
}
