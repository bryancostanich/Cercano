package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/web"
	"cercano/source/server/pkg/config"
)

type researchCap struct{}

// Research constructs the research capability.
func Research() capabilities.Capability { return researchCap{} }

func (researchCap) Name() string            { return "research" }
func (researchCap) Tier() capabilities.Tier { return capabilities.TierR }

// SurfaceAgent only: the MCP surface keeps its legacy hand-registered
// cercano_research handler; registering here too would collide on that name.
func (researchCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }

func (researchCap) Description() string {
	return "Research a question using web search and local AI analysis. Crafts search queries, searches DuckDuckGo, fetches top results, and synthesizes a sourced answer — all locally. Requires the Python venv (run 'cercano setup' once). Args: {query: string, max_results?: int, output_dir?: string}."
}
func (researchCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"properties": {
			"query":       {"type": "string", "description": "The research question to investigate."},
			"max_results": {"type": "integer", "description": "Maximum search results to fetch and analyze (default 5)."},
			"output_dir":  {"type": "string", "description": "Persist a resumable research_state.json into this directory so an interrupted run can resume."}
		},
		"required": ["query"]
	}`)
}

type researchArgs struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
	OutputDir  string `json:"output_dir"`
}

func (researchCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a researchArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("research: parse args: %w", err)
	}
	if a.Query == "" {
		return nil, fmt.Errorf("research: 'query' is required")
	}
	if !venvReady() {
		return nil, errors.New("research: " + venvMissingMessage)
	}

	if a.OutputDir != "" {
		a.OutputDir = resolvePath(call.WorkDir, a.OutputDir)
	}

	// The capability is R-tier (research is read-only), but output_dir makes it
	// write a resumable state file — gate that specific case through the
	// permission callback rather than promoting the whole capability to W.
	if a.OutputDir != "" && call.RequestPermission != nil {
		ok, err := call.RequestPermission(ctx, fmt.Sprintf("research will write a resumable research_state.json into %s", a.OutputDir))
		if err != nil {
			return nil, fmt.Errorf("research: permission check: %w", err)
		}
		if !ok {
			return nil, errors.New("research: permission to write state file denied; re-run without 'output_dir' for an in-memory-only run")
		}
	}

	scriptPath, err := searchScriptPath()
	if err != nil {
		return nil, fmt.Errorf("research: search script: %w", err)
	}
	searcher := web.NewSearcher(config.VenvPython(), scriptPath)
	model := &dispatchModelCaller{call: call, source: "research", tier: config.TierFastLightText}
	pipeline := web.NewResearchPipeline(model, searcher, web.NewFetcher())

	activity := newActivityReporter(call, "research", "research")
	maxResults := a.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}
	activity.Started(fmt.Sprintf("research start: query=%q max_results=%d", a.Query, maxResults))
	activity.Prompt("Query: " + a.Query)
	pipeline.SetProgress(func(phase string) { activity.Progress(phase) })

	result, err := pipeline.RunDurable(ctx, a.Query, a.MaxResults, a.OutputDir)
	if err != nil {
		activity.Failed(err)
		return nil, fmt.Errorf("research: %w", err)
	}
	activity.Done(fmt.Sprintf("research complete: %d sources", len(result.Sources)))

	var b strings.Builder
	b.WriteString(result.Answer)
	if len(result.Sources) > 0 {
		b.WriteString("\n\nSources:\n")
		for _, src := range result.Sources {
			b.WriteString("- " + src + "\n")
		}
	}
	return capabilities.NewTextResult(b.String()), nil
}
