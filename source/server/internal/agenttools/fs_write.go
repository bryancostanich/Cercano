package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeFileTool creates or overwrites a file. Atomic via temp-rename so a
// partial write never leaves a half-baked file at the target path.
type writeFileTool struct{}

// WriteFile constructs the Write tool.
func WriteFile() Tool { return writeFileTool{} }

func (writeFileTool) Name() string             { return "Write" }
func (writeFileTool) Permission() Permission   { return PermW }
func (writeFileTool) Description() string {
	return "Create or overwrite a file with the given content. Atomic — writes via temp file + rename. Args: {path: string, content: string, mkdir?: bool (create parent dirs, default true)}."
}
func (writeFileTool) Schema() json.RawMessage {
	return json.RawMessage(`{
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

func (writeFileTool) Execute(ctx context.Context, raw json.RawMessage) (*Result, error) {
	var a writeFileArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("Write: parse args: %w", err)
	}
	if a.Path == "" {
		return nil, errors.New("Write: path is required")
	}
	mkdir := true
	if a.Mkdir != nil {
		mkdir = *a.Mkdir
	}
	dir := filepath.Dir(a.Path)
	if mkdir {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("Write: mkdir %s: %w", dir, err)
		}
	}
	// Atomic write: temp in same dir → rename. Same-dir is important so
	// rename stays a metadata op rather than a cross-device copy.
	tmp, err := os.CreateTemp(dir, ".cercano-write-*")
	if err != nil {
		return nil, fmt.Errorf("Write: temp create: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(a.Content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return nil, fmt.Errorf("Write: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("Write: close: %w", err)
	}
	if err := os.Rename(tmpPath, a.Path); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("Write: rename: %w", err)
	}
	return &Result{
		Type:   ResultText,
		Text:   fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path),
		Detail: countLabel(lineCount(a.Content), "line", "lines"),
	}, nil
}

// editFileTool does an exact-match string replacement. Refuses on
// zero-matches or multiple-matches so the agent can't accidentally do an
// open-ended replace that hits unintended call sites.
type editFileTool struct{}

// EditFile constructs the Edit tool.
func EditFile() Tool { return editFileTool{} }

func (editFileTool) Name() string             { return "Edit" }
func (editFileTool) Permission() Permission   { return PermW }
func (editFileTool) Description() string {
	return "Replace an exact occurrence of old_string with new_string in a file. The old_string must match exactly once — refuses on zero matches (typo) or multiple matches (ambiguous). Args: {path, old_string, new_string}."
}
func (editFileTool) Schema() json.RawMessage {
	return json.RawMessage(`{
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

func (editFileTool) Execute(ctx context.Context, raw json.RawMessage) (*Result, error) {
	var a editFileArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("Edit: parse args: %w", err)
	}
	if a.Path == "" {
		return nil, errors.New("Edit: path is required")
	}
	if a.OldString == "" {
		return nil, errors.New("Edit: old_string is required")
	}
	if a.OldString == a.NewString {
		return nil, errors.New("Edit: old_string == new_string (no-op)")
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return nil, fmt.Errorf("Edit: %w", err)
	}
	content := string(data)
	count := strings.Count(content, a.OldString)
	if count == 0 {
		return nil, fmt.Errorf("Edit: old_string not found in %s — check exact text including whitespace", a.Path)
	}
	if count > 1 {
		return nil, fmt.Errorf("Edit: old_string matches %d times in %s — make it more specific to uniquely identify the target", count, a.Path)
	}
	updated := strings.Replace(content, a.OldString, a.NewString, 1)
	// Re-use the atomic-write helper so partial writes can't happen.
	res, err := writeFileTool{}.Execute(ctx, mustMarshal(map[string]any{
		"path":    a.Path,
		"content": updated,
		"mkdir":   false,
	}))
	if err != nil {
		return nil, err
	}
	// Override Write's whole-file line count with the edit delta.
	res.Detail = editDetail(a.OldString, a.NewString)
	return res, nil
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
