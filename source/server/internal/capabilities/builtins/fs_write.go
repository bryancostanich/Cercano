package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cercano/source/server/internal/capabilities"
)

// writeFileCap creates or overwrites a file. Atomic via temp-rename so a
// partial write never leaves a half-baked file at the target path.
type writeFileCap struct{}

// WriteFile constructs the write_file capability (display name "Write").
func WriteFile() capabilities.Capability { return writeFileCap{} }

func (writeFileCap) Name() string            { return "write_file" }
func (writeFileCap) Tier() capabilities.Tier { return capabilities.TierW }
func (writeFileCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (writeFileCap) Description() string {
	return "Create or overwrite a file with the given content. Atomic — writes via temp file + rename. Args: {path: string, content: string, mkdir?: bool (create parent dirs, default true)}."
}
func (writeFileCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["path", "content"],
		"properties": {
			"path":    {"type": "string"},
			"content": {"type": "string"},
			"mkdir":   {"type": "boolean", "default": true}
		}
	}`)
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mkdir   *bool  `json:"mkdir"`
}

func (writeFileCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a writeFileArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("write_file: parse args: %w", err)
	}
	if a.Path == "" {
		return nil, errors.New("write_file: path is required")
	}
	a.Path = resolvePath(call.WorkDir, a.Path)
	mkdir := true
	if a.Mkdir != nil {
		mkdir = *a.Mkdir
	}
	dir := filepath.Dir(a.Path)
	if mkdir {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("write_file: mkdir %s: %w", dir, err)
		}
	}
	// Atomic write: temp in same dir → rename. Same-dir ensures rename stays
	// a metadata op rather than a cross-device copy.
	//
	// Rename replaces the destination inode, so the temp file's mode becomes
	// the destination's mode. CreateTemp uses 0600 — carry over the existing
	// file's permissions (an overwritten launcher script must stay executable),
	// or 0644 for a new file.
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(a.Path); statErr == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".cercano-write-*")
	if err != nil {
		return nil, fmt.Errorf("write_file: temp create: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return nil, fmt.Errorf("write_file: chmod: %w", err)
	}
	if _, err := tmp.WriteString(a.Content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return nil, fmt.Errorf("write_file: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("write_file: close: %w", err)
	}
	if err := os.Rename(tmpPath, a.Path); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("write_file: rename: %w", err)
	}
	res := capabilities.NewTextResult(fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path))
	res.Detail = countLabel(lineCount(a.Content), "line", "lines")
	res.StartLine = 1
	return res, nil
}

// editFileCap does an exact-match string replacement. Refuses on zero-matches
// or multiple-matches so the agent can't accidentally do an open-ended replace
// that hits unintended call sites.
type editFileCap struct{}

// EditFile constructs the edit_file capability (display name "Edit").
func EditFile() capabilities.Capability { return editFileCap{} }

func (editFileCap) Name() string            { return "edit_file" }
func (editFileCap) Tier() capabilities.Tier { return capabilities.TierW }
func (editFileCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (editFileCap) Description() string {
	return "Replace an exact occurrence of old_string with new_string in a file. The old_string must match exactly once — refuses on zero matches (typo) or multiple matches (ambiguous). Args: {path, old_string, new_string}."
}
func (editFileCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["path", "old_string", "new_string"],
		"properties": {
			"path":       {"type": "string"},
			"old_string": {"type": "string"},
			"new_string": {"type": "string"}
		}
	}`)
}

type editFileArgs struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

func (editFileCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a editFileArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("edit_file: parse args: %w", err)
	}
	if a.Path == "" {
		return nil, errors.New("edit_file: path is required")
	}
	a.Path = resolvePath(call.WorkDir, a.Path)
	if a.OldString == "" {
		return nil, errors.New("edit_file: old_string is required")
	}
	if a.OldString == a.NewString {
		return nil, errors.New("edit_file: old_string == new_string (no-op)")
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return nil, fmt.Errorf("edit_file: %w", err)
	}
	content := string(data)
	count := strings.Count(content, a.OldString)
	if count == 0 {
		return nil, fmt.Errorf("edit_file: old_string not found in %s — check exact text including whitespace", a.Path)
	}
	if count > 1 {
		return nil, fmt.Errorf("edit_file: old_string matches %d times in %s — make it more specific to uniquely identify the target", count, a.Path)
	}
	// The unique match's 1-based start line in the pre-edit file, recorded so
	// clients can number the diff they render from old_string/new_string.
	startLine := 1 + strings.Count(content[:strings.Index(content, a.OldString)], "\n")
	updated := strings.Replace(content, a.OldString, a.NewString, 1)
	// Re-use the atomic-write path so partial writes can't happen.
	writeArgs := fsWriteMustMarshal(map[string]any{
		"path":    a.Path,
		"content": updated,
		"mkdir":   false,
	})
	res, err := writeFileCap{}.Execute(ctx, &capabilities.Call{Args: writeArgs})
	if err != nil {
		return nil, err
	}
	// Override write_file's whole-file line count with the edit delta.
	res.Detail = editDetail(a.OldString, a.NewString)
	res.StartLine = startLine
	return res, nil
}

// fsWriteMustMarshal marshals v to JSON and panics on error. Only used with
// known-safe literal maps, so a panic here is a programmer error.
func fsWriteMustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
