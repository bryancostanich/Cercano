package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/tokens"
	"cercano/source/server/pkg/config"
)

type localCap struct{}

// Local constructs the local capability — a one-shot prompt against the
// local model, for offloading work that doesn't need the main (possibly
// cloud) model.
func Local() capabilities.Capability { return localCap{} }

func (localCap) Name() string            { return "local" }
func (localCap) Tier() capabilities.Tier { return capabilities.TierR }

// SurfaceAgent only: the MCP surface keeps its legacy hand-registered
// cercano_local handler; registering here too would collide on that name.
func (localCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }

func (localCap) WantsProjectContext() bool { return true }

func (localCap) Description() string {
	return "Run a prompt against Cercano's local AI model (Ollama / llama-server). Use this to offload self-contained work to local inference — private and at zero cloud cost. Args: {prompt: string, context?: string, model?: string}."
}
func (localCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"properties": {
			"prompt":  {"type": "string", "description": "The prompt to run locally."},
			"context": {"type": "string", "description": "Extra context appended to the prompt."},
			"model":   {"type": "string", "description": "Advisory local model override; empty for the configured default."}
		},
		"required": ["prompt"]
	}`)
}

type localArgs struct {
	Prompt  string `json:"prompt"`
	Context string `json:"context"`
	Model   string `json:"model"`
}

func (localCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a localArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("local: parse args: %w", err)
	}
	if a.Prompt == "" {
		return nil, fmt.Errorf("local: 'prompt' is required")
	}
	if call.Svc.Dispatch == nil {
		return nil, errors.New("local: dispatch engine not available")
	}

	input := a.Prompt
	if a.Context != "" {
		input = fmt.Sprintf("%s\n\nContext:\n%s", a.Prompt, a.Context)
	}

	res, err := call.Svc.Dispatch(ctx, dispatch.Spec{
		Mode:                 dispatch.OneShot,
		Role:                 dispatch.RoleCoproc,
		Tier:                 config.TierEveryday,
		Prompt:               input,
		WorkDir:              call.WorkDir,
		WantsProjectContext:  true,
		ModelOverride:        a.Model,
		Source:               "local",
		ContentTokensAvoided: tokens.Estimate(input),
		RecordUsage:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("local: %w", err)
	}
	out := capabilities.NewTextResult(res.Text)
	if res.Model != "" {
		out.Note = "model: " + res.Model
	}
	return out, nil
}
