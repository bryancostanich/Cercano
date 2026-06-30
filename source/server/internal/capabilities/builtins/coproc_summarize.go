package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"cercano/source/server/internal/capabilities"
)

type summarizeCap struct{}

// Summarize constructs the summarize capability.
func Summarize() capabilities.Capability { return summarizeCap{} }

func (summarizeCap) Name() string            { return "summarize" }
func (summarizeCap) Tier() capabilities.Tier { return capabilities.TierR }
func (summarizeCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (summarizeCap) WantsProjectContext() bool { return true }
func (summarizeCap) Description() string {
	return "Summarize text or a file using the local co-processor. Args: {text?: string, file_path?: string, max_length?: string}."
}
func (summarizeCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"properties": {
			"text":       {"type": "string", "description": "Text to summarize."},
			"file_path":  {"type": "string", "description": "Path to a file to summarize."},
			"max_length": {"type": "string", "description": "Output length hint: \"brief\" (1-2 sentences), \"detailed\" (multiple paragraphs), or omit for one paragraph."}
		}
	}`)
}

type summarizeArgs struct {
	Text      string `json:"text"`
	FilePath  string `json:"file_path"`
	MaxLength string `json:"max_length"`
}

func (summarizeCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a summarizeArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("summarize: parse args: %w", err)
	}
	if a.Text == "" && a.FilePath == "" {
		return nil, fmt.Errorf("summarize: provide either 'text' or 'file_path'")
	}

	content := a.Text
	if a.FilePath != "" {
		data, err := os.ReadFile(a.FilePath)
		if err != nil {
			return nil, fmt.Errorf("summarize: failed to read file %q: %w", a.FilePath, err)
		}
		content = string(data)
	}

	lengthInstruction := "one paragraph"
	switch a.MaxLength {
	case "brief":
		lengthInstruction = "1-2 sentences"
	case "detailed":
		lengthInstruction = "multiple paragraphs covering all key points"
	}

	prompt := fmt.Sprintf("Summarize the following text in %s. Focus on the most important information. Output only the summary, no preamble.\n\nText to summarize:\n%s", lengthInstruction, content)
	return runCoproc(ctx, call, "summarize", prompt, content)
}
