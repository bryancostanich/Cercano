package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"cercano/source/server/internal/capabilities"
)

type explainCap struct{}

// Explain constructs the explain capability.
func Explain() capabilities.Capability { return explainCap{} }

func (explainCap) Name() string            { return "explain" }
func (explainCap) Tier() capabilities.Tier { return capabilities.TierR }
func (explainCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (explainCap) WantsProjectContext() bool { return true }
func (explainCap) Description() string {
	return "Explain code or text using the local co-processor. Describes what it does, key components, and how they interact. Args: {text?: string, file_path?: string}."
}
func (explainCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"properties": {
			"text":      {"type": "string", "description": "Code or text to explain."},
			"file_path": {"type": "string", "description": "Path to a file to explain."}
		}
	}`)
}

type explainArgs struct {
	Text     string `json:"text"`
	FilePath string `json:"file_path"`
}

func (explainCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a explainArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("explain: parse args: %w", err)
	}
	if a.Text == "" && a.FilePath == "" {
		return nil, fmt.Errorf("explain: provide either 'text' or 'file_path'")
	}

	content := a.Text
	if a.FilePath != "" {
		data, err := os.ReadFile(a.FilePath)
		if err != nil {
			return nil, fmt.Errorf("explain: failed to read file %q: %w", a.FilePath, err)
		}
		content = string(data)
	}

	prompt := fmt.Sprintf("Explain the following code or text. Describe what it does, its key components, and how they interact. Be concise and focus on what a developer needs to understand to work with this code.\n\nCode:\n%s", content)
	return runCoproc(ctx, call, "explain", prompt, content)
}
