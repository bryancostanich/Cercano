package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// rmFileTool removes a file. Refuses directories outright — for directory
// removal the agent must invoke `run_command rm -r` explicitly, which gives
// the user another confirm checkpoint to think twice.
type rmFileTool struct{}

// RmFile constructs the rm_file tool. X-tier (destructive); always confirms
// even under tiered bypass.
func RmFile() Tool { return rmFileTool{} }

func (rmFileTool) Name() string             { return "rm_file" }
func (rmFileTool) Permission() Permission   { return PermX }
func (rmFileTool) Description() string {
	return "Delete a single file (NOT a directory; refuses dirs for safety). Args: {path: string}."
}
func (rmFileTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`)
}

func (rmFileTool) Execute(ctx context.Context, raw json.RawMessage) (*Result, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("rm_file: parse args: %w", err)
	}
	if a.Path == "" {
		return nil, errors.New("rm_file: path is required")
	}
	info, err := os.Stat(a.Path)
	if err != nil {
		return nil, fmt.Errorf("rm_file: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("rm_file: %s is a directory — use run_command if you really mean to delete recursively", a.Path)
	}
	if err := os.Remove(a.Path); err != nil {
		return nil, fmt.Errorf("rm_file: %w", err)
	}
	return &Result{Type: ResultText, Text: "deleted " + a.Path}, nil
}
