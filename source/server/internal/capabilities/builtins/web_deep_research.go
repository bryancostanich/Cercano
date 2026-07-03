package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/research"
	"cercano/source/server/internal/web"
	"cercano/source/server/pkg/config"
)

type deepResearchCap struct{}

// DeepResearch constructs the deep_research capability.
func DeepResearch() capabilities.Capability { return deepResearchCap{} }

func (deepResearchCap) Name() string            { return "deep_research" }
func (deepResearchCap) Tier() capabilities.Tier { return capabilities.TierR }

// SurfaceAgent only: the MCP surface keeps its legacy hand-registered
// cercano_deep_research handler; registering here too would collide on that name.
func (deepResearchCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }

func (deepResearchCap) Description() string {
	return "Deep multi-source research. Takes a topic and intent, identifies authoritative sources, systematically searches each, analyzes and ranks findings, chases cited references, and compiles a structured report — all locally. Requires the Python venv (run 'cercano setup' once). Args: {topic: string, intent: string, depth?: \"survey\"|\"standard\"|\"deep\", date_range?: string, sources?: [string], output_dir?: string, phase?: string, model?: string}."
}
func (deepResearchCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"properties": {
			"topic":      {"type": "string", "description": "The research topic."},
			"intent":     {"type": "string", "description": "What the research is for — shapes source selection and analysis."},
			"depth":      {"type": "string", "description": "Research depth: \"survey\", \"standard\" (default), or \"deep\"."},
			"date_range": {"type": "string", "description": "Restrict findings to a date range, e.g. \"2024-2026\"."},
			"sources":    {"type": "array", "items": {"type": "string"}, "description": "Source categories to search; empty for automatic selection."},
			"output_dir": {"type": "string", "description": "Write the report and findings as files into this directory."},
			"phase":      {"type": "string", "description": "Run a single phase: \"plan\", \"search\", \"analyze\", or \"synthesize\"; empty runs all."},
			"model":      {"type": "string", "description": "Advisory local model override for the analysis calls."}
		},
		"required": ["topic", "intent"]
	}`)
}

type deepResearchArgs struct {
	Topic     string   `json:"topic"`
	Intent    string   `json:"intent"`
	Depth     string   `json:"depth"`
	DateRange string   `json:"date_range"`
	Sources   []string `json:"sources"`
	OutputDir string   `json:"output_dir"`
	Phase     string   `json:"phase"`
	Model     string   `json:"model"`
}

func (deepResearchCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a deepResearchArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("deep_research: parse args: %w", err)
	}
	if a.Topic == "" {
		return nil, fmt.Errorf("deep_research: 'topic' is required")
	}
	if a.Intent == "" {
		return nil, fmt.Errorf("deep_research: 'intent' is required")
	}
	if !venvReady() {
		return nil, errors.New("deep_research: " + venvMissingMessage)
	}

	// The capability is R-tier (research is read-only), but output_dir makes
	// it write report files — gate that specific case through the permission
	// callback rather than promoting the whole capability to W.
	if a.OutputDir != "" && call.RequestPermission != nil {
		ok, err := call.RequestPermission(ctx, fmt.Sprintf("deep_research will write report files into %s", a.OutputDir))
		if err != nil {
			return nil, fmt.Errorf("deep_research: permission check: %w", err)
		}
		if !ok {
			return nil, errors.New("deep_research: permission to write report files denied; re-run without 'output_dir' for an in-conversation report")
		}
	}

	// Note: the MCP surface pre-checks for code-only local models via gRPC
	// routing metadata; natively the dispatch engine owns model selection, so
	// that probe is skipped here.
	model := &dispatchModelCaller{call: call, source: "deep_research", model: a.Model}
	searcher := web.NewSearcher(config.VenvPython(), ddgScriptPath())
	dispatcher := research.NewSearchDispatcher(&researchSearchAdapter{searcher: searcher})
	pipeline := research.NewPipeline(model, dispatcher, &researchFetchAdapter{fetcher: web.NewFetcher()})

	if call.Emit != nil {
		call.Emit("starting deep research…")
	}
	phaseResult, err := pipeline.Run(ctx, research.RunConfig{
		Topic:      a.Topic,
		Intent:     a.Intent,
		Depth:      a.Depth,
		DateRange:  a.DateRange,
		Sources:    a.Sources,
		OutputDir:  a.OutputDir,
		ProjectDir: call.WorkDir,
		Phase:      a.Phase,
	})
	if err != nil {
		return nil, fmt.Errorf("deep_research: %w", err)
	}

	out := capabilities.NewTextResult(phaseResult.Summary)
	if phaseResult.SuggestedNext != nil {
		if hint, err := json.Marshal(phaseResult.SuggestedNext); err == nil {
			out.Note = "suggested_next: " + string(hint)
		}
	}
	return out, nil
}
