package agenttools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// gitStatusTool reports the working tree's status using `git status
// --porcelain=v2` and parses each row into structured fields.
type gitStatusTool struct{}

// GitStatus constructs the git_status tool.
func GitStatus() Tool { return gitStatusTool{} }

func (gitStatusTool) Name() string             { return "git_status" }
func (gitStatusTool) Permission() Permission   { return PermR }
func (gitStatusTool) Description() string {
	return "Show working-tree status as rows of {path, x, y, status} via git status --porcelain=v2. Args: {path?: string} (default cwd)."
}
func (gitStatusTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
}

type pathArgs struct {
	Path string `json:"path"`
}

func (gitStatusTool) Execute(ctx context.Context, raw json.RawMessage) (*Result, error) {
	var a pathArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("git_status: parse args: %w", err)
		}
	}
	args := []string{"status", "--porcelain=v2", "--untracked-files=all"}
	cmd := exec.CommandContext(ctx, "git", args...)
	if a.Path != "" {
		cmd.Dir = a.Path
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git_status: %w", err)
	}
	rows := parseGitStatusV2(string(out))
	return NewRowsResult(rows), nil
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

// gitLogTool reads `git log --oneline -n N` and returns commit rows.
type gitLogTool struct{}

// GitLog constructs the git_log tool.
func GitLog() Tool { return gitLogTool{} }

func (gitLogTool) Name() string             { return "git_log" }
func (gitLogTool) Permission() Permission   { return PermR }
func (gitLogTool) Description() string {
	return "Show recent commit history as rows of {sha, author, date, subject}. Args: {path?: string, limit?: int (default 20), since?: string}."
}
func (gitLogTool) Schema() json.RawMessage {
	return json.RawMessage(`{
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

func (gitLogTool) Execute(ctx context.Context, raw json.RawMessage) (*Result, error) {
	var a gitLogArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
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
	if a.Path != "" {
		cmd.Dir = a.Path
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
	return NewRowsResult(rows), nil
}
