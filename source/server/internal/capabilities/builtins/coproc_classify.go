package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"cercano/source/server/internal/capabilities"
)

type classifyCap struct{}

// Classify constructs the classify capability.
func Classify() capabilities.Capability { return classifyCap{} }

func (classifyCap) Name() string            { return "classify" }
func (classifyCap) Tier() capabilities.Tier { return capabilities.TierR }
func (classifyCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (classifyCap) WantsProjectContext() bool { return true }
func (classifyCap) Description() string {
	return "Classify text or a file into categories using the local co-processor. Args: {text?: string, file_path?: string, categories?: string}."
}
func (classifyCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"properties": {
			"text":       {"type": "string", "description": "Text to classify."},
			"file_path":  {"type": "string", "description": "Path to a file to classify."},
			"categories": {"type": "string", "description": "Optional comma-separated list of categories to choose from."}
		}
	}`)
}

type classifyArgs struct {
	Text       string `json:"text"`
	FilePath   string `json:"file_path"`
	Categories string `json:"categories"`
}

func (classifyCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a classifyArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("classify: parse args: %w", err)
	}
	if a.Text == "" && a.FilePath == "" {
		return nil, fmt.Errorf("classify: provide either 'text' or 'file_path'")
	}

	content := a.Text
	if a.FilePath != "" {
		data, err := os.ReadFile(a.FilePath)
		if err != nil {
			return nil, fmt.Errorf("classify: failed to read file %q: %w", a.FilePath, err)
		}
		content = string(data)
	}

	categoryInstruction := "Determine the most appropriate category."
	if a.Categories != "" {
		categoryInstruction = fmt.Sprintf("Choose from these categories: %s", a.Categories)
	}

	prompt := fmt.Sprintf("Classify the following text. %s\n\nRespond with exactly this format:\nCategory: <category>\nConfidence: <high/medium/low>\nReasoning: <one sentence explanation>\n\nText:\n%s", categoryInstruction, content)
	return runCoproc(ctx, call, "classify", prompt, content)
}
