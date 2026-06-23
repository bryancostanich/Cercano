package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// readFileTool reads a UTF-8 text file from disk. Refuses binaries (anything
// whose first 8 KiB contain NUL bytes) and applies the 32 KiB truncation
// policy on large files.
type readFileTool struct{}

// ReadFile constructs the Read tool.
func ReadFile() Tool { return readFileTool{} }

func (readFileTool) Name() string       { return "Read" }
func (readFileTool) Permission() Permission { return PermR }
func (readFileTool) Description() string {
	return "Read a UTF-8 text file from disk. Returns the file contents, capped at 32 KiB. Refuses binary files. Args: {path: string, start?: int, end?: int}."
}
func (readFileTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path":  {"type": "string", "description": "Absolute or relative file path."},
			"start": {"type": "integer", "minimum": 1, "description": "Optional 1-indexed first line."},
			"end":   {"type": "integer", "minimum": 1, "description": "Optional 1-indexed last line, inclusive."}
		}
	}`)
}

type readFileArgs struct {
	Path  string `json:"path"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

func (readFileTool) Execute(ctx context.Context, raw json.RawMessage) (*Result, error) {
	var a readFileArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("Read: parse args: %w", err)
	}
	if a.Path == "" {
		return nil, errors.New("Read: path is required")
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return nil, fmt.Errorf("Read: %w", err)
	}
	if looksBinary(data) {
		return nil, fmt.Errorf("Read: %s appears to be binary; refusing to read", a.Path)
	}
	text := string(data)
	if a.Start > 0 || a.End > 0 {
		text = selectLines(text, a.Start, a.End)
	}
	res := NewTextResult(text)
	res.Detail = countLabel(lineCount(text), "line", "lines")
	return res, nil
}

// looksBinary heuristic: NUL byte in the first 8 KiB. Catches most binaries
// without trying to enumerate every extension.
func looksBinary(b []byte) bool {
	n := len(b)
	if n > 8192 {
		n = 8192
	}
	for i := 0; i < n; i++ {
		if b[i] == 0 {
			return true
		}
	}
	return false
}

func selectLines(text string, start, end int) string {
	lines := strings.Split(text, "\n")
	if start < 1 {
		start = 1
	}
	if end < 1 || end > len(lines) {
		end = len(lines)
	}
	if start > end || start > len(lines) {
		return ""
	}
	return strings.Join(lines[start-1:end], "\n")
}

// listDirTool returns the entries of a directory as rows with name/type/size.
type listDirTool struct{}

// ListDir constructs the LS tool.
func ListDir() Tool { return listDirTool{} }

func (listDirTool) Name() string             { return "LS" }
func (listDirTool) Permission() Permission   { return PermR }
func (listDirTool) Description() string {
	return "List entries of a directory with name, type, and size. Args: {path: string, hidden?: bool}. Default skips dotfiles."
}
func (listDirTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path":   {"type": "string"},
			"hidden": {"type": "boolean", "default": false}
		}
	}`)
}

type listDirArgs struct {
	Path   string `json:"path"`
	Hidden bool   `json:"hidden"`
}

func (listDirTool) Execute(ctx context.Context, raw json.RawMessage) (*Result, error) {
	var a listDirArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("LS: parse args: %w", err)
	}
	if a.Path == "" {
		return nil, errors.New("LS: path is required")
	}
	entries, err := os.ReadDir(a.Path)
	if err != nil {
		return nil, fmt.Errorf("LS: %w", err)
	}
	rows := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !a.Hidden && strings.HasPrefix(name, ".") {
			continue
		}
		info, err := e.Info()
		kind := "file"
		if e.IsDir() {
			kind = "dir"
		} else if isSymlink(e.Type()) {
			kind = "symlink"
		}
		row := map[string]any{
			"name": name,
			"type": kind,
		}
		if err == nil {
			row["size"] = info.Size()
		}
		rows = append(rows, row)
	}
	// Stable order: dirs first, then files, alphabetical within each.
	sort.Slice(rows, func(i, j int) bool {
		ti, tj := rows[i]["type"].(string), rows[j]["type"].(string)
		if ti != tj {
			return ti == "dir" // dirs come first
		}
		return rows[i]["name"].(string) < rows[j]["name"].(string)
	})
	res := NewRowsResult(rows)
	res.Detail = countLabel(len(rows), "entry", "entries")
	return res, nil
}

func isSymlink(m fs.FileMode) bool { return m&fs.ModeSymlink != 0 }

// statFileTool reports presence + size + mtime + type. Lighter than list_dir
// for the common "does X exist" agent need.
type statFileTool struct{}

// StatFile constructs the stat_file tool.
func StatFile() Tool { return statFileTool{} }

func (statFileTool) Name() string             { return "stat_file" }
func (statFileTool) Permission() Permission   { return PermR }
func (statFileTool) Description() string {
	return "Report whether a path exists and its type/size/mtime. Args: {path: string}."
}
func (statFileTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`)
}

type statFileArgs struct {
	Path string `json:"path"`
}

func (statFileTool) Execute(ctx context.Context, raw json.RawMessage) (*Result, error) {
	var a statFileArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("stat_file: parse args: %w", err)
	}
	if a.Path == "" {
		return nil, errors.New("stat_file: path is required")
	}
	info, err := os.Stat(a.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Result{Type: ResultRows, Rows: []map[string]any{{
			"path":   a.Path,
			"exists": false,
		}}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat_file: %w", err)
	}
	kind := "file"
	if info.IsDir() {
		kind = "dir"
	} else if isSymlink(info.Mode()) {
		kind = "symlink"
	}
	abs, _ := filepath.Abs(a.Path)
	return &Result{Type: ResultRows, Rows: []map[string]any{{
		"path":   abs,
		"exists": true,
		"type":   kind,
		"size":   info.Size(),
		"mtime":  info.ModTime().UTC().Format(time.RFC3339),
	}}}, nil
}

// globTool matches paths against a glob pattern via filepath.Glob.
type globTool struct{}

// Glob constructs the Glob tool.
func Glob() Tool { return globTool{} }

func (globTool) Name() string             { return "Glob" }
func (globTool) Permission() Permission   { return PermR }
func (globTool) Description() string {
	// V1 limitation: uses Go stdlib filepath.Glob, which does NOT support
	// `**` recursive descent. Patterns are evaluated relative to `path`
	// (defaults to cwd).
	return "Match paths against a glob pattern. Returns matching paths one per line, sorted. Does NOT support ** recursive globbing in V1. Args: {pattern: string, path?: string}."
}
func (globTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["pattern"],
		"properties": {
			"pattern": {"type": "string", "description": "Glob pattern, e.g. 'README*' or '*.go'. Does NOT support ** recursive globbing in V1."},
			"path":    {"type": "string", "description": "Optional directory to glob within. Defaults to current working directory."}
		}
	}`)
}

type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

func (globTool) Execute(ctx context.Context, raw json.RawMessage) (*Result, error) {
	var a globArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("Glob: parse args: %w", err)
	}
	if a.Pattern == "" {
		return nil, errors.New("Glob: pattern is required")
	}
	pat := a.Pattern
	if a.Path != "" {
		pat = filepath.Join(a.Path, a.Pattern)
	}
	matches, err := filepath.Glob(pat)
	if err != nil {
		return nil, fmt.Errorf("Glob: %w", err)
	}
	if len(matches) == 0 {
		return &Result{Type: ResultText, Text: "(no matches)", Note: "0 matches", Detail: "0 matches"}, nil
	}
	sort.Strings(matches)
	res := NewTextResult(strings.Join(matches, "\n") + "\n")
	res.Detail = countLabel(len(matches), "match", "matches")
	return res, nil
}
