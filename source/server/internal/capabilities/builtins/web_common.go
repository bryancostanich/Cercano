package builtins

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/research"
	"cercano/source/server/internal/tokens"
	"cercano/source/server/internal/web"
	"cercano/source/server/pkg/config"
)

// venvMissingMessage mirrors the MCP surface's message for a missing Python venv.
const venvMissingMessage = "Web research requires a Python virtual environment that is not set up. Run `cercano setup` to create it automatically."

// venvReady reports whether the Python venv for web search exists.
func venvReady() bool {
	_, err := os.Stat(config.VenvPython())
	return err == nil
}

// ddgScriptPath resolves the DuckDuckGo search script relative to the running
// binary, matching the MCP surface's resolution (<bin dir>/../scripts/ddg_search.py).
func ddgScriptPath() string {
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}
	p := filepath.Join(filepath.Dir(exePath), "..", "scripts", "ddg_search.py")
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// dispatchModelCaller implements web.ModelCaller (and the research package's
// equivalent) on top of the one-shot dispatch engine, so pipeline model calls
// route through the same provider selection and usage recording as the
// co-processor capabilities. The MCP surface's equivalent round-trips over
// gRPC instead.
type dispatchModelCaller struct {
	call   *capabilities.Call
	source string
	model  string // advisory model override; empty for the configured default
}

func (m *dispatchModelCaller) Call(ctx context.Context, prompt string) (string, error) {
	if m.call.Svc.Dispatch == nil {
		return "", errors.New(m.source + ": dispatch engine not available")
	}
	res, err := m.call.Svc.Dispatch(ctx, dispatch.Spec{
		Mode:                 dispatch.OneShot,
		Role:                 dispatch.RoleCoproc,
		Prompt:               prompt,
		WorkDir:              m.call.WorkDir,
		ModelOverride:        m.model,
		Source:               m.source,
		ContentTokensAvoided: tokens.Estimate(prompt),
		RecordUsage:          true,
	})
	if err != nil {
		return "", err
	}
	return res.Text, nil
}

// researchSearchAdapter adapts web.Searcher to research.SearchProvider.
type researchSearchAdapter struct {
	searcher *web.Searcher
}

func (a *researchSearchAdapter) Search(ctx context.Context, query string, maxResults int) ([]research.SearchResult, error) {
	results, err := a.searcher.Search(ctx, query, maxResults)
	if err != nil {
		return nil, err
	}
	var out []research.SearchResult
	for _, r := range results {
		out = append(out, research.SearchResult{
			URL:     r.URL,
			Title:   r.Title,
			Snippet: r.Snippet,
		})
	}
	return out, nil
}

// researchFetchAdapter adapts web.Fetcher to research.URLFetcher.
type researchFetchAdapter struct {
	fetcher *web.Fetcher
}

func (a *researchFetchAdapter) FetchURL(url string) (*research.FetchResult, error) {
	result, err := a.fetcher.Fetch(url)
	if err != nil {
		return nil, err
	}
	return &research.FetchResult{
		URL:     result.URL,
		Title:   result.Title,
		Content: result.Content,
	}, nil
}
