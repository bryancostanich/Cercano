package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"cercano/source/server/internal/dispatch"
)

// WriteFile writes content to a file, optionally creating parent directories.
type WriteFile struct{}

func NewWriteFile() *WriteFile { return &WriteFile{} }

func (t *WriteFile) Name() string { return "write_file" }

func (t *WriteFile) Schema() dispatch.ToolSchema {
	return dispatch.ToolSchema{
		Name:        "write_file",
		Description: "Write (or overwrite) a text file. Set create_dirs=true to auto-create missing parent directories.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path":        map[string]interface{}{"type": "string", "description": "File path to write."},
				"content":     map[string]interface{}{"type": "string", "description": "Text content to write to the file."},
				"create_dirs": map[string]interface{}{"type": "boolean", "description": "If true, create missing parent directories. Default false."},
			},
			"required": []string{"path", "content"},
		},
	}
}

type writeFileArgs struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	CreateDirs bool   `json:"create_dirs"`
}

func (t *WriteFile) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var a writeFileArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if a.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(a.Path), 0755); err != nil {
			return "", fmt.Errorf("failed to create parent dirs: %w", err)
		}
	}
	if err := os.WriteFile(a.Path, []byte(a.Content), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path), nil
}
