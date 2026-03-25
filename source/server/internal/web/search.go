package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// SearchResult represents a single search result from DuckDuckGo.
type SearchResult struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// Searcher executes DuckDuckGo searches via a Python subprocess.
type Searcher struct {
	pythonPath string
	scriptPath string
}

// NewSearcher creates a Searcher with explicit paths to the venv python and search script.
func NewSearcher(pythonPath, scriptPath string) *Searcher {
	return &Searcher{
		pythonPath: pythonPath,
		scriptPath: scriptPath,
	}
}

// Search executes a DuckDuckGo search and returns parsed results.
// maxResults <= 0 defaults to 5.
func (s *Searcher) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}
	if maxResults <= 0 {
		maxResults = 5
	}

	cmd := exec.CommandContext(ctx, s.pythonPath, s.scriptPath,
		"--query", query,
		"--max-results", strconv.Itoa(maxResults),
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return nil, fmt.Errorf("ddg search failed: %s", errMsg)
		}
		return nil, fmt.Errorf("ddg search failed: %w", err)
	}

	var results []SearchResult
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		return nil, fmt.Errorf("failed to parse search results: %w (output: %s)", err, stdout.String())
	}

	return results, nil
}
