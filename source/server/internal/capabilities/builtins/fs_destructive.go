package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"cercano/source/server/internal/capabilities"
)

// rmFileCap removes a single file. Refuses directories outright — for
// directory removal the agent must invoke run_command with rm -r explicitly,
// giving the user an extra confirm checkpoint.
type rmFileCap struct{}

// RmFile constructs the rm_file capability. X-tier (destructive); always
// confirms even under tiered bypass.
func RmFile() capabilities.Capability { return rmFileCap{} }

func (rmFileCap) Name() string                  { return "rm_file" }
func (rmFileCap) Tier() capabilities.Tier        { return capabilities.TierX }
func (rmFileCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (rmFileCap) Description() string {
	return "Delete a single file (NOT a directory; refuses dirs for safety). Args: {path: string}."
}
func (rmFileCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`)
}

func (rmFileCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(call.Args, &a); err != nil {
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
	return capabilities.NewTextResult("deleted " + a.Path), nil
}
