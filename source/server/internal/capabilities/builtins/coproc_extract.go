package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"cercano/source/server/internal/capabilities"
)

type extractCap struct{}

// Extract constructs the extract capability.
func Extract() capabilities.Capability { return extractCap{} }

func (extractCap) Name() string            { return "extract" }
func (extractCap) Tier() capabilities.Tier { return capabilities.TierR }
func (extractCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (extractCap) WantsProjectContext() bool { return true }
func (extractCap) Description() string {
	return "Extract specific information from text or a file using the local co-processor. Args: {query: string, text?: string, file_path?: string}."
}
func (extractCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["query"],
		"properties": {
			"query":     {"type": "string", "description": "What to extract from the text."},
			"text":      {"type": "string", "description": "Text to extract from."},
			"file_path": {"type": "string", "description": "Path to a file to extract from."}
		}
	}`)
}

type extractArgs struct {
	Query    string `json:"query"`
	Text     string `json:"text"`
	FilePath string `json:"file_path"`
}

func (extractCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a extractArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("extract: parse args: %w", err)
	}
	if a.Text == "" && a.FilePath == "" {
		return nil, fmt.Errorf("extract: provide either 'text' or 'file_path'")
	}
	if a.Query == "" {
		return nil, fmt.Errorf("extract: 'query' is required")
	}

	content := a.Text
	if a.FilePath != "" {
		a.FilePath = resolvePath(call.WorkDir, a.FilePath)
		data, err := os.ReadFile(a.FilePath)
		if err != nil {
			return nil, fmt.Errorf("extract: failed to read file %q: %w", a.FilePath, err)
		}
		content = string(data)
	}

	prompt := fmt.Sprintf("Extract the following from the text below: %s\n\nRules:\n- Output ONLY the extracted content, no commentary\n- Preserve the original formatting of extracted sections\n- If nothing matches, respond with \"No matching content found.\"\n\nText:\n%s", a.Query, content)
	return runCoproc(ctx, call, "extract", prompt, content)
}
