// Package builtin provides the built-in tools for cercano_dispatch.
package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"cercano/source/server/internal/dispatch"
)

const binaryDetectWindow = 8 * 1024

// ReadFile reads a UTF-8 text file from disk.
type ReadFile struct{}

func NewReadFile() *ReadFile { return &ReadFile{} }

func (t *ReadFile) Name() string { return "read_file" }

func (t *ReadFile) Schema() dispatch.ToolSchema {
	return dispatch.ToolSchema{
		Name:        "read_file",
		Description: "Read the contents of a text file from disk. Returns the full file as a UTF-8 string. Errors on binary files or missing/unreadable paths.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Absolute or working-directory-relative file path to read.",
				},
			},
			"required": []string{"path"},
		},
	}
}

type readFileArgs struct {
	Path string `json:"path"`
}

func (t *ReadFile) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var a readFileArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return "", err
	}
	window := data
	if len(window) > binaryDetectWindow {
		window = window[:binaryDetectWindow]
	}
	if bytes.IndexByte(window, 0) >= 0 {
		return "", fmt.Errorf("file appears to be binary (NUL byte in first %d bytes): %s", binaryDetectWindow, a.Path)
	}
	return string(data), nil
}
