package builtins

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/web"
)

type fetchCap struct{}

// Fetch constructs the fetch capability.
func Fetch() capabilities.Capability { return fetchCap{} }

func (fetchCap) Name() string            { return "fetch" }
func (fetchCap) Tier() capabilities.Tier { return capabilities.TierR }

// SurfaceAgent only: the MCP surface keeps its legacy hand-registered
// cercano_fetch handler; registering here too would collide on that name.
func (fetchCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }

func (fetchCap) Description() string {
	return "Fetch a URL and extract readable text content. Returns extracted plain text (HTML stripped), not a summary. Args: {url: string}."
}
func (fetchCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"properties": {
			"url": {"type": "string", "description": "The URL to fetch and extract text from."}
		},
		"required": ["url"]
	}`)
}

type fetchArgs struct {
	URL string `json:"url"`
}

func (fetchCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a fetchArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("fetch: parse args: %w", err)
	}
	if a.URL == "" {
		return nil, fmt.Errorf("fetch: 'url' is required")
	}

	result, err := web.NewFetcher().Fetch(a.URL)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	if result.Content == "" {
		return capabilities.NewTextResult("(No readable text content found at this URL)"), nil
	}
	return capabilities.NewTextResult(result.Content), nil
}
