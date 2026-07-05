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
	return "Research a question using web search and local AI analysis. Crafts search queries, searches DuckDuckGo, fetches top results, and synthesizes a sourced answer — all locally. Requires the Python venv (run 'cercano setup' once). Args: {query: string, max_results?: int}."
}
func (researchCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"properties": {
			"query":       {"type": "string", "description": "The research question to investigate."},
			"max_results": {"type": "integer", "description": "Maximum search results to fetch and analyze (default 5)."}
		},
		"required": ["query"]
	}`)
}

type researchArgs struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
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

	scriptPath, err := searchScriptPath()
	if err != nil {
		return nil, fmt.Errorf("research: search script: %w", err)
	}
	searcher := web.NewSearcher(config.VenvPython(), scriptPath)
	model := &dispatchModelCaller{call: call, source: "research"}
	pipeline := web.NewResearchPipeline(model, searcher, web.NewFetcher())

	if call.Emit != nil {
		call.Emit("researching locally…")
	}
	result, err := pipeline.Run(ctx, a.Query, a.MaxResults)
	if err != nil {
		return nil, fmt.Errorf("research: %w", err)
	}

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
