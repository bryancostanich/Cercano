package agenttools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// grepTool searches across a directory for a pattern. Prefers ripgrep (rg)
// for speed; falls back to POSIX grep -RIn. Output is structured rows of
// {path, line, content} — the model gets parsed matches rather than the
// raw stdout of either backend.
type grepTool struct{}

// Grep constructs the Grep tool.
func Grep() Tool { return grepTool{} }

func (grepTool) Name() string             { return "Grep" }
func (grepTool) Permission() Permission   { return PermR }
func (grepTool) Description() string {
	return "Search files under a directory for a pattern. Returns rows of {path, line, content}. Prefers ripgrep when present; falls back to grep. Args: {pattern: string, path?: string, case_insensitive?: bool, glob?: string}."
}
func (grepTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["pattern"],
		"properties": {
			"pattern":          {"type": "string", "description": "Regex pattern (rg/grep dialect)."},
			"path":             {"type": "string", "description": "Directory or file to search. Defaults to cwd."},
			"case_insensitive": {"type": "boolean", "default": false},
			"glob":             {"type": "string", "description": "Glob filter (rg --glob, grep --include). Optional."}
		}
	}`)
}

type grepArgs struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path"`
	CaseInsensitive bool   `json:"case_insensitive"`
	Glob            string `json:"glob"`
}

func (grepTool) Execute(ctx context.Context, raw json.RawMessage) (*Result, error) {
	var a grepArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("Grep: parse args: %w", err)
	}
	if a.Pattern == "" {
		return nil, errors.New("Grep: pattern is required")
	}
	if a.Path == "" {
		a.Path = "."
	}

	if _, err := exec.LookPath("rg"); err == nil {
		return runRipgrep(ctx, a)
	}
	return runGrep(ctx, a)
}

func runRipgrep(ctx context.Context, a grepArgs) (*Result, error) {
	args := []string{"--vimgrep", "--no-heading", "--color", "never"}
	if a.CaseInsensitive {
		args = append(args, "-i")
	}
	if a.Glob != "" {
		args = append(args, "--glob", a.Glob)
	}
	args = append(args, "--", a.Pattern, a.Path)

	cmd := exec.CommandContext(ctx, "rg", args...)
	out, err := cmd.Output()
	if err != nil {
		// rg returns exit code 1 when there are zero matches. That's not an
		// error condition for our purposes — treat as empty result.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			res := NewRowsResult(nil)
			res.Detail = "0 matches"
			return res, nil
		}
		return nil, fmt.Errorf("grep (rg): %w: %s", err, string(bytes.TrimSpace(out)))
	}
	rows := parseVimgrep(string(out))
	r := NewRowsResult(rows)
	r.Detail = countLabel(len(rows), "match", "matches")
	if r.Note == "" {
		r.Note = "via rg"
	} else {
		r.Note += "; via rg"
	}
	return r, nil
}

func runGrep(ctx context.Context, a grepArgs) (*Result, error) {
	args := []string{"-R", "-I", "-n", "-H"}
	if a.CaseInsensitive {
		args = append(args, "-i")
	}
	if a.Glob != "" {
		args = append(args, "--include", a.Glob)
	}
	args = append(args, "-e", a.Pattern, a.Path)

	cmd := exec.CommandContext(ctx, "grep", args...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			res := NewRowsResult(nil)
			res.Detail = "0 matches"
			return res, nil
		}
		return nil, fmt.Errorf("grep: %w: %s", err, string(bytes.TrimSpace(out)))
	}
	rows := parseGrepNH(string(out))
	r := NewRowsResult(rows)
	r.Detail = countLabel(len(rows), "match", "matches")
	if r.Note == "" {
		r.Note = "via grep (rg not found)"
	} else {
		r.Note += "; via grep (rg not found)"
	}
	return r, nil
}

// parseVimgrep parses rg --vimgrep output: path:line:col:content.
func parseVimgrep(text string) []map[string]any {
	var rows []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		// path:line:col:content
		parts := strings.SplitN(line, ":", 4)
		if len(parts) < 4 {
			continue
		}
		row := map[string]any{
			"path":    parts[0],
			"content": parts[3],
		}
		if n, err := strconv.Atoi(parts[1]); err == nil {
			row["line"] = n
		}
		if n, err := strconv.Atoi(parts[2]); err == nil {
			row["col"] = n
		}
		rows = append(rows, row)
	}
	return rows
}

// parseGrepNH parses grep -nH output: path:line:content.
func parseGrepNH(text string) []map[string]any {
	var rows []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		row := map[string]any{
			"path":    parts[0],
			"content": parts[2],
		}
		if n, err := strconv.Atoi(parts[1]); err == nil {
			row["line"] = n
		}
		rows = append(rows, row)
	}
	return rows
}
