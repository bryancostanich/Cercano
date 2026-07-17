package builtins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"cercano/source/server/internal/capabilities"
)

// githubIssueCloseCap closes a GitHub issue through the authenticated gh CLI.
// It is intentionally narrow so agents do not need generic Bash access for the
// common "close this issue with a comment" finishing step.
type githubIssueCloseCap struct{}

// GitHubIssueClose constructs the github_issue_close capability.
func GitHubIssueClose() capabilities.Capability { return githubIssueCloseCap{} }

func (githubIssueCloseCap) Name() string            { return "github_issue_close" }
func (githubIssueCloseCap) Tier() capabilities.Tier { return capabilities.TierX }
func (githubIssueCloseCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (githubIssueCloseCap) Description() string {
	return "Close a GitHub issue using the gh CLI. Args: {number: int, comment?: string, repo?: string, cwd?: string}."
}
func (githubIssueCloseCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type":"object",
		"properties":{
			"number":{"type":"integer"},
			"comment":{"type":"string"},
			"repo":{"type":"string"},
			"cwd":{"type":"string"}
		},
		"required":["number"]
	}`)
}

type githubIssueCloseArgs struct {
	Number  int    `json:"number"`
	Comment string `json:"comment"`
	Repo    string `json:"repo"`
	Cwd     string `json:"cwd"`
}

func (githubIssueCloseCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a githubIssueCloseArgs
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &a); err != nil {
			return nil, fmt.Errorf("github_issue_close: parse args: %w", err)
		}
	}
	if a.Number <= 0 {
		return nil, fmt.Errorf("github_issue_close: number must be positive")
	}
	args := []string{"issue", "close", strconv.Itoa(a.Number)}
	if strings.TrimSpace(a.Comment) != "" {
		args = append(args, "--comment", a.Comment)
	}
	if strings.TrimSpace(a.Repo) != "" {
		args = append(args, "--repo", a.Repo)
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	dir := a.Cwd
	if dir == "" {
		dir = call.WorkDir
	}
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	trimmed := string(bytes.TrimSpace(out))
	if err != nil {
		return nil, fmt.Errorf("github_issue_close: %w: %s", err, trimmed)
	}
	res := capabilities.NewTextResult(trimmed)
	res.Detail = "closed #" + strconv.Itoa(a.Number)
	return res, nil
}
