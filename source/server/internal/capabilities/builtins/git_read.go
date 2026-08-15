package builtins

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"cercano/source/server/internal/capabilities"
)

// gitStatusCap reports the working tree's status using `git status
// --porcelain=v2` and parses each row into structured fields.
type gitStatusCap struct{}

// GitStatus constructs the git_status capability.
func GitStatus() capabilities.Capability { return gitStatusCap{} }

func (gitStatusCap) Name() string            { return "git_status" }
func (gitStatusCap) Tier() capabilities.Tier { return capabilities.TierR }
func (gitStatusCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (gitStatusCap) Description() string {
	return "Show working-tree status as rows of {path, x, y, status} via git status --porcelain=v2. Args: {path?: string} (repository directory, default cwd), {paths?: [string]} to filter specific files."
}
func (gitStatusCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{"type":"object","properties":{"path":{"type":"string","description":"Repository/worktree directory. Do not pass a file path; use paths for file filters."},"paths":{"type":"array","items":{"type":"string"},"description":"Optional file path filters, relative to the repository/worktree directory or accepted by git as pathspecs."}}}`)
}

type gitStatusArgs struct {
	Path  string   `json:"path"`
	Paths []string `json:"paths"`
}

func (gitStatusCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a gitStatusArgs
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &a); err != nil {
			return nil, fmt.Errorf("git_status: parse args: %w", err)
		}
	}
	args := []string{"status", "--porcelain=v2", "--untracked-files=all"}
	if len(a.Paths) > 0 {
		args = append(args, "--")
		args = append(args, a.Paths...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	dir := a.Path
	if dir == "" {
		dir = call.WorkDir
	}
	if dir != "" {
		if info, statErr := os.Stat(dir); statErr == nil && !info.IsDir() {
			return nil, fmt.Errorf("git_status: path must be a repository/worktree directory, got file %q; pass the repository in path and file filters in paths, for example {\"path\":\"/path/to/repo\",\"paths\":[%q]}", dir, dir)
		}
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git_status: %w", err)
	}
	rows := parseGitStatusV2(string(out))
	res := capabilities.NewRowsResult(rows)
	res.Detail = countLabel(len(rows), "change", "changes")
	return res, nil
}

// parseGitStatusV2 handles the most common record types:
//
//   - "1 XY ..." — changed entries
//   - "2 XY ..." — renamed/copied entries
//   - "? path"  — untracked
//   - "! path"  — ignored (rare unless caller asks)
//
// We keep the parser narrow to common fields {path, x, y, status}.
func parseGitStatusV2(text string) []map[string]any {
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case '1', '2':
			// "1 XY ..." or "2 XY ..." — XY at positions 2-3, path is the last field.
			parts := strings.Split(line, " ")
			if len(parts) < 9 {
				continue
			}
			path := parts[len(parts)-1]
			xy := parts[1]
			rows = append(rows, map[string]any{
				"path":   path,
				"x":      string(xy[0]),
				"y":      string(xy[1]),
				"status": describeXY(xy),
			})
		case '?':
			path := strings.TrimSpace(line[1:])
			rows = append(rows, map[string]any{
				"path":   path,
				"x":      "?",
				"y":      "?",
				"status": "untracked",
			})
		}
	}
	return rows
}

func describeXY(xy string) string {
	// One-letter codes mostly; the most useful summaries.
	switch xy {
	case ".M", " M":
		return "modified (unstaged)"
	case "M.", "M ":
		return "modified (staged)"
	case "A.", "A ":
		return "added (staged)"
	case ".D", " D":
		return "deleted (unstaged)"
	case "D.", "D ":
		return "deleted (staged)"
	case "R.", "R ":
		return "renamed (staged)"
	case "C.", "C ":
		return "copied (staged)"
	case "U.", "U ", ".U", " U", "UU":
		return "unmerged"
	}
	return xy
}

// gitInfoCap reports branch, HEAD, repo root, and upstream/ahead state without
// requiring generic shell access. It exists so agents can do common branch
// safety checks with an R-tier scoped git tool instead of Bash.
type gitInfoCap struct{}

// GitInfo constructs the git_info capability.
func GitInfo() capabilities.Capability { return gitInfoCap{} }

func (gitInfoCap) Name() string            { return "git_info" }
func (gitInfoCap) Tier() capabilities.Tier { return capabilities.TierR }
func (gitInfoCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (gitInfoCap) Description() string {
	return "Report repository branch, HEAD SHA, root, upstream, and ahead/behind counts. Args: {path?: string} (default cwd)."
}
func (gitInfoCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{"type":"object","properties":{"path":{"type":"string"}}}`)
}

type gitInfoArgs struct {
	Path string `json:"path"`
}

func (gitInfoCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a gitInfoArgs
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &a); err != nil {
			return nil, fmt.Errorf("git_info: parse args: %w", err)
		}
	}
	dir := a.Path
	if dir == "" {
		dir = call.WorkDir
	}
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		if dir != "" {
			cmd.Dir = dir
		}
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
	root, err := run("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("git_info: %w", err)
	}
	branch, _ := run("branch", "--show-current")
	head, err := run("rev-parse", "--short", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git_info: %w", err)
	}
	upstream, upstreamErr := run("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	ahead, behind := 0, 0
	if upstreamErr == nil && upstream != "" {
		if counts, err := run("rev-list", "--left-right", "--count", "HEAD...@{u}"); err == nil {
			fields := strings.Fields(counts)
			if len(fields) == 2 {
				ahead, _ = strconv.Atoi(fields[0])
				behind, _ = strconv.Atoi(fields[1])
			}
		}
	}
	payload := map[string]any{
		"root":     root,
		"branch":   branch,
		"head":     head,
		"upstream": upstream,
		"ahead":    ahead,
		"behind":   behind,
	}
	b, _ := json.Marshal(payload)
	res := &capabilities.Result{Type: capabilities.ResultJSON, JSON: b}
	if branch == "" {
		branch = "detached"
	}
	res.Detail = strings.TrimSpace(branch + " " + head)
	return res, nil
}

// gitLogCap reads `git log` and returns commit rows.
type gitLogCap struct{}

// GitLog constructs the git_log capability.
func GitLog() capabilities.Capability { return gitLogCap{} }

func (gitLogCap) Name() string            { return "git_log" }
func (gitLogCap) Tier() capabilities.Tier { return capabilities.TierR }
func (gitLogCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (gitLogCap) Description() string {
	return "Show recent commit history as rows of {sha, author, date, subject}. Args: {path?: string, limit?: int (default 20), since?: string}."
}
func (gitLogCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"properties": {
			"path":  {"type": "string"},
			"limit": {"type": "integer", "minimum": 1, "default": 20},
			"since": {"type": "string", "description": "git --since spec (e.g. '7 days ago')."}
		}
	}`)
}

type gitLogArgs struct {
	Path  string `json:"path"`
	Limit int    `json:"limit"`
	Since string `json:"since"`
}

func (gitLogCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a gitLogArgs
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &a); err != nil {
			return nil, fmt.Errorf("git_log: parse args: %w", err)
		}
	}
	if a.Limit <= 0 {
		a.Limit = 20
	}
	// Use unambiguous delimiters between fields to survive subjects with `|`
	// or whitespace.
	args := []string{"log", "--format=%h\x1f%an\x1f%ad\x1f%s", "--date=short", "-n", strconv.Itoa(a.Limit)}
	if a.Since != "" {
		args = append(args, "--since", a.Since)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	dir := a.Path
	if dir == "" {
		dir = call.WorkDir
	}
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git_log: %w", err)
	}
	var rows []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		fields := strings.SplitN(scanner.Text(), "\x1f", 4)
		if len(fields) != 4 {
			continue
		}
		rows = append(rows, map[string]any{
			"sha":     fields[0],
			"author":  fields[1],
			"date":    fields[2],
			"subject": fields[3],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("git_log: scan: %w", err)
	}
	if len(rows) == 0 {
		return nil, errors.New("git_log: no commits matched")
	}
	res := capabilities.NewRowsResult(rows)
	res.Detail = countLabel(len(rows), "commit", "commits")
	return res, nil
}
