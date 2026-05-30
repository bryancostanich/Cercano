package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/web"
)

// WebFetch fetches a URL and returns extracted text (reuses internal/web's HTML stripper).
type WebFetch struct {
	fetcher *web.Fetcher
}

func NewWebFetch() *WebFetch { return &WebFetch{fetcher: web.NewFetcher()} }

func (t *WebFetch) Name() string { return "web_fetch" }

func (t *WebFetch) Schema() dispatch.ToolSchema {
	return dispatch.ToolSchema{
		Name:        "web_fetch",
		Description: "Fetch a URL and return readable text (HTML stripped). Errors on network failure or non-2xx responses.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"url": map[string]interface{}{"type": "string", "description": "URL to fetch."},
			},
			"required": []string{"url"},
		},
	}
}

type webFetchArgs struct {
	URL string `json:"url"`
}

func (t *WebFetch) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var a webFetchArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	res, err := t.fetcher.Fetch(a.URL)
	if err != nil {
		return "", err
	}
	return res.Content, nil
}
