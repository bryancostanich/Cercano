package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/pkg/config"
)

type reviewCap struct{}

// Review constructs the review capability.
func Review() capabilities.Capability { return reviewCap{} }

func (reviewCap) Name() string            { return "review" }
func (reviewCap) Tier() capabilities.Tier { return capabilities.TierR }
func (reviewCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (reviewCap) Description() string {
	return "Adversarially review a claim: a model tries to REFUTE it and returns a verdict (holds / refuted) with reasoning. Optionally grant read-only tools so the reviewer can inspect files."
}
func (reviewCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["claim"],
		"properties": {
			"claim":   {"type": "string", "description": "The claim to adversarially review."},
			"context": {"type": "string", "description": "Optional background context the reviewer may need."},
			"tools":   {"type": "array", "items": {"type": "string"}, "description": "Tool names to grant the reviewer (enables file inspection). Default: none (OneShot)."}
		}
	}`)
}

type reviewArgs struct {
	Claim   string   `json:"claim"`
	Context string   `json:"context"`
	Tools   []string `json:"tools"`
}

func (reviewCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a reviewArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("review: parse args: %w", err)
	}
	if strings.TrimSpace(a.Claim) == "" {
		return nil, errors.New("review: 'claim' is required and must not be empty")
	}
	if call.Svc.Dispatch == nil {
		return nil, errors.New("review: dispatch engine not available")
	}

	prompt := buildRefutePrompt(a.Claim, a.Context)

	var spec dispatch.Spec
	if len(a.Tools) == 0 {
		spec = dispatch.Spec{
			Mode:    dispatch.OneShot,
			Role:    dispatch.RoleMain,
			Tier:    config.TierEveryday,
			Prompt:  prompt,
			WorkDir: call.WorkDir,
		}
	} else {
		spec = dispatch.Spec{
			Mode:    dispatch.Agentic,
			Role:    dispatch.RoleMain,
			Tier:    config.TierEveryday,
			Task:    prompt,
			Tools:   a.Tools,
			WorkDir: call.WorkDir,
		}
	}

	res, err := call.Svc.Dispatch(ctx, spec)
	if err != nil {
		return nil, err
	}
	v := parseVerdict(res.Text)
	b, _ := json.Marshal(v)
	return &capabilities.Result{Type: capabilities.ResultJSON, JSON: b}, nil
}

func buildRefutePrompt(claim, context string) string {
	var b strings.Builder
	b.WriteString("You are an adversarial reviewer. Try to REFUTE the following claim. Look for the strongest reason it could be wrong. If after genuine effort you cannot refute it, say it holds.\n\n")
	b.WriteString("Respond in this format:\n")
	b.WriteString("VERDICT: <HOLDS | REFUTED>\n")
	b.WriteString("REASONING: <one or two sentences>\n\n")
	b.WriteString("CLAIM:\n")
	b.WriteString(claim)
	if strings.TrimSpace(context) != "" {
		b.WriteString("\n\nCONTEXT:\n")
		b.WriteString(context)
	}
	return b.String()
}
