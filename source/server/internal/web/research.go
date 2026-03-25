package web

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// ModelCaller abstracts calling the local model. Implemented by the MCP server
// using the gRPC client; mockable for tests.
type ModelCaller interface {
	Call(ctx context.Context, prompt string) (string, error)
}

// SearchProvider abstracts the search backend. Implemented by Searcher (DDG);
// mockable for tests.
type SearchProvider interface {
	Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error)
}

// URLFetcher abstracts URL fetching. Implemented by Fetcher; mockable for tests.
type URLFetcher interface {
	FetchURL(url string) (*FetchResult, error)
}

// FetchedPage holds a fetched page's content alongside its search metadata.
type FetchedPage struct {
	URL     string
	Title   string
	Content string
}

// ResearchResult is the final output of the research pipeline.
type ResearchResult struct {
	Answer  string
	Sources []string
}

// ResearchPipeline orchestrates the full research flow: query crafting,
// search, fetch, and synthesis via the local model.
type ResearchPipeline struct {
	model    ModelCaller
	searcher SearchProvider
	fetcher  URLFetcher
}

// NewResearchPipeline creates a pipeline with the given dependencies.
func NewResearchPipeline(model ModelCaller, searcher SearchProvider, fetcher URLFetcher) *ResearchPipeline {
	return &ResearchPipeline{
		model:    model,
		searcher: searcher,
		fetcher:  fetcher,
	}
}

// CraftQueries asks the local model to generate 2-3 search queries for the
// user's research question. Falls back to the original query if parsing fails.
func (p *ResearchPipeline) CraftQueries(ctx context.Context, question string) ([]string, error) {
	prompt := fmt.Sprintf(`Generate 2-3 concise web search queries to answer this question. Output ONLY the queries, one per line, numbered (1. 2. 3.). No explanations.

Question: %s`, question)

	resp, err := p.model.Call(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("query crafting failed: %w", err)
	}

	queries := parseNumberedList(resp)
	if len(queries) == 0 {
		// Fallback: use the original question as the search query
		return []string{question}, nil
	}
	return queries, nil
}

// SearchAll runs searches for all queries concurrently and returns combined results.
func (p *ResearchPipeline) SearchAll(ctx context.Context, queries []string, maxPerQuery int) []SearchResult {
	var mu sync.Mutex
	var all []SearchResult
	var wg sync.WaitGroup

	for _, q := range queries {
		wg.Add(1)
		go func(query string) {
			defer wg.Done()
			results, err := p.searcher.Search(ctx, query, maxPerQuery)
			if err != nil {
				return // graceful degradation — skip failed queries
			}
			mu.Lock()
			all = append(all, results...)
			mu.Unlock()
		}(q)
	}
	wg.Wait()
	return all
}

// DeduplicateResults removes duplicate URLs, preserving first occurrence order.
func DeduplicateResults(results []SearchResult) []SearchResult {
	seen := make(map[string]bool)
	var deduped []SearchResult
	for _, r := range results {
		if !seen[r.URL] {
			seen[r.URL] = true
			deduped = append(deduped, r)
		}
	}
	return deduped
}

// FetchAll fetches up to maxResults URLs concurrently and returns their content.
func (p *ResearchPipeline) FetchAll(ctx context.Context, results []SearchResult, maxResults int) []FetchedPage {
	if maxResults <= 0 {
		maxResults = 5
	}
	if len(results) > maxResults {
		results = results[:maxResults]
	}

	var mu sync.Mutex
	var pages []FetchedPage
	var wg sync.WaitGroup

	for _, r := range results {
		wg.Add(1)
		go func(sr SearchResult) {
			defer wg.Done()
			fr, err := p.fetcher.FetchURL(sr.URL)
			if err != nil {
				return // graceful degradation — skip failed fetches
			}
			mu.Lock()
			pages = append(pages, FetchedPage{
				URL:     sr.URL,
				Title:   sr.Title,
				Content: fr.Content,
			})
			mu.Unlock()
		}(r)
	}
	wg.Wait()
	return pages
}

// Synthesize asks the local model to analyze fetched content and produce a
// sourced answer to the original question.
func (p *ResearchPipeline) Synthesize(ctx context.Context, question string, pages []FetchedPage) (string, error) {
	var sb strings.Builder
	for i, page := range pages {
		// Truncate very long pages to keep prompt reasonable
		content := page.Content
		if len(content) > 8000 {
			content = content[:8000] + "\n[...truncated]"
		}
		fmt.Fprintf(&sb, "--- Source %d: %s (%s) ---\n%s\n\n", i+1, page.Title, page.URL, content)
	}

	prompt := fmt.Sprintf(`You are a research assistant. Based on the web sources below, provide a clear, accurate answer to the question. Cite sources by URL where relevant. If the sources don't contain enough information, say so.

Question: %s

%s

Provide your answer now. Include source URLs as citations.`, question, sb.String())

	resp, err := p.model.Call(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("synthesis failed: %w", err)
	}
	return resp, nil
}

// Run executes the full research pipeline: craft queries → search → deduplicate
// → fetch → synthesize. Returns a distilled answer with source citations.
func (p *ResearchPipeline) Run(ctx context.Context, question string, maxResults int) (*ResearchResult, error) {
	if maxResults <= 0 {
		maxResults = 5
	}

	// Step 1: Craft search queries
	queries, err := p.CraftQueries(ctx, question)
	if err != nil {
		return nil, err
	}

	// Step 2: Search all queries in parallel
	allResults := p.SearchAll(ctx, queries, maxResults)

	// Step 3: Deduplicate
	deduped := DeduplicateResults(allResults)
	if len(deduped) == 0 {
		return nil, fmt.Errorf("no search results found for: %s", question)
	}

	// Step 4: Fetch top pages in parallel
	pages := p.FetchAll(ctx, deduped, maxResults)
	if len(pages) == 0 {
		// Fall back: synthesize from snippets only
		for _, r := range deduped {
			pages = append(pages, FetchedPage{
				URL:     r.URL,
				Title:   r.Title,
				Content: r.Snippet,
			})
		}
	}

	// Step 5: Synthesize answer
	answer, err := p.Synthesize(ctx, question, pages)
	if err != nil {
		return nil, err
	}

	// Collect source URLs
	var sources []string
	for _, page := range pages {
		sources = append(sources, page.URL)
	}

	return &ResearchResult{
		Answer:  answer,
		Sources: sources,
	}, nil
}

// FetchURL implements URLFetcher for the existing Fetcher type.
func (f *Fetcher) FetchURL(url string) (*FetchResult, error) {
	return f.Fetch(url)
}

// parseNumberedList extracts items from a numbered list (1. item\n2. item\n...).
func parseNumberedList(text string) []string {
	var items []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Strip leading number and punctuation: "1. ", "2) ", "1: ", etc.
		for i, c := range line {
			if c >= '0' && c <= '9' {
				continue
			}
			if c == '.' || c == ')' || c == ':' {
				item := strings.TrimSpace(line[i+1:])
				if item != "" {
					items = append(items, item)
				}
				break
			}
			// Not a numbered line — skip
			break
		}
	}
	return items
}
