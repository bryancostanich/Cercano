package builtins

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

	"cercano/source/server/internal/capabilities"
)

// readFileCap reads a UTF-8 text file from disk. Refuses binaries (anything
// whose first 8 KiB contain NUL bytes) and applies the 32 KiB truncation
// policy on large files.
type readFileCap struct{}

// ReadFile constructs the read_file capability (display name "Read").
func ReadFile() capabilities.Capability { return readFileCap{} }

func (readFileCap) Name() string                  { return "read_file" }
func (readFileCap) Tier() capabilities.Tier        { return capabilities.TierR }
func (readFileCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (readFileCap) Description() string {
	return "Read a UTF-8 text file from disk. Returns the file contents, capped at 32 KiB. Refuses binary files. Args: {path: string, start?: int, end?: int}."
}
func (readFileCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
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

func (readFileCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a readFileArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("read_file: parse args: %w", err)
	}
	if a.Path == "" {
		return nil, errors.New("read_file: path is required")
	}
	a.Path = resolvePath(call.WorkDir, a.Path)
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return nil, fmt.Errorf("read_file: %w", err)
	}
	if looksBinary(data) {
		return nil, fmt.Errorf("read_file: %s appears to be binary; refusing to read", a.Path)
	}
	text := string(data)
	if a.Start > 0 || a.End > 0 {
		text = selectLines(text, a.Start, a.End)
	}
	res := capabilities.NewTextResult(text)
	res.Detail = countLabel(lineCount(text), "line", "lines")
	return res, nil
}

// listDirCap returns the entries of a directory as rows with name/type/size.
type listDirCap struct{}

// ListDir constructs the list_dir capability (display name "LS").
func ListDir() capabilities.Capability { return listDirCap{} }

func (listDirCap) Name() string                  { return "list_dir" }
func (listDirCap) Tier() capabilities.Tier        { return capabilities.TierR }
func (listDirCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (listDirCap) Description() string {
	return "List entries of a directory with name, type, and size. Args: {path: string, hidden?: bool}. Default skips dotfiles."
}
func (listDirCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
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

func (listDirCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a listDirArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("list_dir: parse args: %w", err)
	}
	if a.Path == "" {
		return nil, errors.New("list_dir: path is required")
	}
	a.Path = resolvePath(call.WorkDir, a.Path)
	entries, err := os.ReadDir(a.Path)
	if err != nil {
		return nil, fmt.Errorf("list_dir: %w", err)
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
			// Full path (relative to the same base the caller used) so the model
			// can address the entry directly instead of re-deriving the parent —
			// re-deriving is what makes it fumble nested paths.
			"path": filepath.Join(a.Path, name),
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
	res := capabilities.NewRowsResult(rows)
	res.Detail = countLabel(len(rows), "entry", "entries")
	return res, nil
}

// statFileCap reports presence + size + mtime + type. Lighter than list_dir
// for the common "does X exist" agent need.
type statFileCap struct{}

// StatFile constructs the stat_file capability.
func StatFile() capabilities.Capability { return statFileCap{} }

func (statFileCap) Name() string                  { return "stat_file" }
func (statFileCap) Tier() capabilities.Tier        { return capabilities.TierR }
func (statFileCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (statFileCap) Description() string {
	return "Report whether a path exists and its type/size/mtime. Args: {path: string}."
}
func (statFileCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`)
}

type statFileArgs struct {
	Path string `json:"path"`
}

func (statFileCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a statFileArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("stat_file: parse args: %w", err)
	}
	if a.Path == "" {
		return nil, errors.New("stat_file: path is required")
	}
	a.Path = resolvePath(call.WorkDir, a.Path)
	info, err := os.Stat(a.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return &capabilities.Result{Type: capabilities.ResultRows, Rows: []map[string]any{{
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
	return &capabilities.Result{Type: capabilities.ResultRows, Rows: []map[string]any{{
		"path":   abs,
		"exists": true,
		"type":   kind,
		"size":   info.Size(),
		"mtime":  info.ModTime().UTC().Format(time.RFC3339),
	}}}, nil
}

// globCap matches paths against a glob pattern via filepath.Glob.
type globCap struct{}

// Glob constructs the glob capability (display name "Glob").
func Glob() capabilities.Capability { return globCap{} }

func (globCap) Name() string                  { return "glob" }
func (globCap) Tier() capabilities.Tier        { return capabilities.TierR }
func (globCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (globCap) Description() string {
	// V1 limitation: uses Go stdlib filepath.Glob, which does NOT support
	// `**` recursive descent. Patterns are evaluated relative to `path`
	// (defaults to cwd).
	return "Match paths against a glob pattern. Returns matching paths one per line, sorted. Does NOT support ** recursive globbing in V1. Args: {pattern: string, path?: string}."
}
func (globCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
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

func (globCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a globArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("glob: parse args: %w", err)
	}
	if a.Pattern == "" {
		return nil, errors.New("glob: pattern is required")
	}
	base := a.Path
	if base == "" {
		base = call.WorkDir
	} else {
		base = resolvePath(call.WorkDir, base)
	}
	pat := a.Pattern
	if base != "" {
		pat = filepath.Join(base, a.Pattern)
	}
	matches, err := filepath.Glob(pat)
	if err != nil {
		return nil, fmt.Errorf("glob: %w", err)
	}
	if len(matches) == 0 {
		return &capabilities.Result{Type: capabilities.ResultText, Text: "(no matches)", Note: "0 matches", Detail: "0 matches"}, nil
	}
	sort.Strings(matches)
	res := capabilities.NewTextResult(strings.Join(matches, "\n") + "\n")
	res.Detail = countLabel(len(matches), "match", "matches")
	return res, nil
}
