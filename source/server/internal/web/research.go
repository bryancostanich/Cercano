package web

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"cercano/source/server/internal/modelbudget"
	"cercano/source/server/internal/tokens"
	"time"
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

// ResearchProgressFunc receives a coarse phase update as the research pipeline
// advances (e.g. "crafting search queries…", "fetching 4 pages…").
type ResearchProgressFunc func(phase string)

// ResearchPipeline orchestrates the full research flow: query crafting,
// search, fetch, and synthesis via the local model.
type ResearchPipeline struct {
	model    ModelCaller
	searcher SearchProvider
	fetcher  URLFetcher
	progress ResearchProgressFunc
}

// SetProgress registers a callback invoked at each pipeline phase. Optional;
// nil disables progress reporting.
func (p *ResearchPipeline) SetProgress(fn ResearchProgressFunc) { p.progress = fn }

func (p *ResearchPipeline) report(phase string) {
	if p.progress != nil {
		p.progress(phase)
	}
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

// SearchAll runs searches for all queries concurrently and returns combined
// results plus the errors from failed queries. A partial failure is fine —
// the surviving queries still feed the pipeline — but callers must be able
// to tell "every search broke" apart from "the web had nothing to say".
func (p *ResearchPipeline) SearchAll(ctx context.Context, queries []string, maxPerQuery int) ([]SearchResult, []error) {
	var mu sync.Mutex
	var all []SearchResult
	var errs []error
	var wg sync.WaitGroup

	for _, q := range queries {
		wg.Add(1)
		go func(query string) {
			defer wg.Done()
			results, err := p.searcher.Search(ctx, query, maxPerQuery)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			all = append(all, results...)
		}(q)
	}
	wg.Wait()
	return all, errs
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
	prompt, err := p.synthesisPrompt(ctx, question, pages)
	if err != nil {
		return "", err
	}

	resp, err := p.model.Call(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("synthesis failed: %w", err)
	}
	return resp, nil
}

func (p *ResearchPipeline) synthesisPrompt(ctx context.Context, question string, pages []FetchedPage) (string, error) {
	corpus, err := p.sourceCorpus(ctx, question, pages)
	if err != nil {
		return "", err
	}
	return formatSynthesisPrompt(question, corpus), nil
}

func (p *ResearchPipeline) sourceCorpus(ctx context.Context, question string, pages []FetchedPage) (string, error) {
	budgeter, ok := p.model.(modelbudget.Budgeter)
	if !ok {
		return legacySourceCorpus(pages), nil
	}
	budget, err := budgeter.Budget(ctx, modelbudget.DefaultOutputReserve)
	if err != nil {
		return "", fmt.Errorf("research synthesis budget: %w", err)
	}
	emptyPromptTokens := tokens.Estimate(formatSynthesisPrompt(question, ""))
	corpusBudget := budget.InputTokens - emptyPromptTokens - 16 // tokenizer/formatting safety margin
	if corpusBudget < 128 {
		return "", fmt.Errorf("research synthesis budget too small after prompt overhead: input_budget=%d prompt_overhead=%d corpus_budget=%d provider=%s model=%s context_window=%d", budget.InputTokens, emptyPromptTokens, corpusBudget, budget.Target.Provider, budget.Target.Model, budget.Target.ContextWindow)
	}
	corpus, included, _ := budgetedSourceCorpus(pages, corpusBudget)
	if included == 0 {
		return "", fmt.Errorf("research synthesis budget could not fit any source content: corpus_budget=%d provider=%s model=%s context_window=%d", corpusBudget, budget.Target.Provider, budget.Target.Model, budget.Target.ContextWindow)
	}
	return corpus, nil
}

func formatSynthesisPrompt(question, corpus string) string {
	return fmt.Sprintf(`You are a research assistant. Based on the web sources below, provide a clear, accurate answer to the question. Cite sources by URL where relevant. If the sources don't contain enough information, say so.

Question: %s

%s

Provide your answer now. Include source URLs as citations.`, question, corpus)
}

func legacySourceCorpus(pages []FetchedPage) string {
	var sb strings.Builder
	for i, page := range pages {
		content := page.Content
		if len(content) > 8000 {
			content = content[:8000] + "\n[...truncated]"
		}
		fmt.Fprintf(&sb, "--- Source %d: %s (%s) ---\n%s\n\n", i+1, page.Title, page.URL, content)
	}
	return sb.String()
}

func budgetedSourceCorpus(pages []FetchedPage, corpusBudget int) (corpus string, included int, trimmed int) {
	var sb strings.Builder
	for i, page := range pages {
		header := fmt.Sprintf("--- Source %d: %s (%s) ---\n", i+1, page.Title, page.URL)
		footer := "\n\n"
		remaining := corpusBudget - tokens.Estimate(sb.String()) - tokens.Estimate(header) - tokens.Estimate(footer)
		if remaining <= 0 {
			trimmed++
			continue
		}
		content := strings.TrimSpace(page.Content)
		if content == "" {
			content = "(No readable text content found.)"
		}
		candidate := content
		if tokens.Estimate(candidate) > remaining {
			trimmed++
			candidate = truncateToTokenBudget(candidate, remaining, "\n[...truncated to fit local model context]")
		}
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		fmt.Fprintf(&sb, "%s%s%s", header, candidate, footer)
		included++
	}
	return sb.String(), included, trimmed
}

func truncateToTokenBudget(text string, budget int, marker string) string {
	if budget <= 0 {
		return ""
	}
	if tokens.Estimate(text) <= budget {
		return text
	}
	markerTokens := tokens.Estimate(marker)
	usable := budget - markerTokens
	if usable <= 0 {
		return ""
	}
	chars := usable * 4
	if chars > len(text) {
		chars = len(text)
	}
	candidate := strings.TrimSpace(text[:chars]) + marker
	for tokens.Estimate(candidate) > budget && chars > 0 {
		chars = chars * 9 / 10
		candidate = strings.TrimSpace(text[:chars]) + marker
	}
	if tokens.Estimate(candidate) > budget {
		return ""
	}
	return candidate
}

// Run executes the full research pipeline: craft queries → search → deduplicate
// → fetch → synthesize. Returns a distilled answer with source citations. It
// keeps no on-disk state — a crash mid-run loses all work. Callers that want a
// resumable run should use RunDurable with an output directory.
func (p *ResearchPipeline) Run(ctx context.Context, question string, maxResults int) (*ResearchResult, error) {
	return p.RunDurable(ctx, question, maxResults, "")
}

// RunDurable executes the research pipeline while persisting a sidecar
// (research_state.json) into outputDir after every phase. If a sidecar for an
// interrupted run already exists there, the run resumes from the last completed
// phase instead of starting over — crafted queries, search results, and fetched
// pages survive a crash. When outputDir is empty the sidecar is disabled and
// this behaves exactly like the classic in-memory Run.
func (p *ResearchPipeline) RunDurable(ctx context.Context, question string, maxResults int, outputDir string) (*ResearchResult, error) {
	if maxResults <= 0 {
		maxResults = 5
	}

	sidecar := newResearchSidecar(outputDir)
	state := p.loadOrInitState(sidecar, question, maxResults)

	// Step 1: Craft search queries (skip if a resumed run already has them).
	if !state.phaseReached(phaseQueries) {
		p.report("crafting search queries…")
		queries, err := p.CraftQueries(ctx, question)
		if err != nil {
			return nil, err
		}
		state.Queries = queries
		state.Phase = phaseQueries
		if err := sidecar.save(state); err != nil {
			return nil, fmt.Errorf("persist research state: %w", err)
		}
	}

	// Step 2 + 3: Search all queries in parallel, then deduplicate.
	if !state.phaseReached(phaseSearch) {
		p.report(fmt.Sprintf("searching %d queries…", len(state.Queries)))
		allResults, searchErrs := p.SearchAll(ctx, state.Queries, maxResults)

		deduped := DeduplicateResults(allResults)
		p.report(fmt.Sprintf("found %d unique results", len(deduped)))
		if len(deduped) == 0 {
			// When every query errored the cause is the search layer, not the
			// query content — surface it instead of a misleading "no results".
			if len(searchErrs) > 0 && len(searchErrs) == len(state.Queries) {
				return nil, fmt.Errorf("all %d searches failed: %w", len(state.Queries), searchErrs[0])
			}
			return nil, fmt.Errorf("no search results found for: %s", question)
		}
		state.Results = deduped
		state.Phase = phaseSearch
		if err := sidecar.save(state); err != nil {
			return nil, fmt.Errorf("persist research state: %w", err)
		}
	}

	// Step 4: Fetch top pages in parallel.
	if !state.phaseReached(phaseFetch) {
		p.report(fmt.Sprintf("fetching up to %d pages…", maxResults))
		pages := p.FetchAll(ctx, state.Results, maxResults)
		if len(pages) == 0 {
			// Fall back: synthesize from snippets only.
			for _, r := range state.Results {
				pages = append(pages, FetchedPage{
					URL:     r.URL,
					Title:   r.Title,
					Content: r.Snippet,
				})
			}
		}
		state.Pages = pages
		state.Phase = phaseFetch
		if err := sidecar.save(state); err != nil {
			return nil, fmt.Errorf("persist research state: %w", err)
		}
	}

	// Step 5: Synthesize answer.
	if !state.phaseReached(phaseSynthesis) {
		p.report(fmt.Sprintf("synthesizing answer from %d sources…", len(state.Pages)))
		answer, err := p.Synthesize(ctx, question, state.Pages)
		if err != nil {
			return nil, err
		}
		state.Answer = answer
		state.Sources = sourceURLs(state.Pages)
		state.Phase = phaseSynthesis
		if err := sidecar.save(state); err != nil {
			return nil, fmt.Errorf("persist research state: %w", err)
		}
	}

	state.Phase = phaseComplete
	if err := sidecar.save(state); err != nil {
		return nil, fmt.Errorf("persist research state: %w", err)
	}

	return &ResearchResult{
		Answer:  state.Answer,
		Sources: state.Sources,
	}, nil
}

// loadOrInitState resumes an interrupted run from the sidecar when one exists
// and matches the current question and format; otherwise it returns a fresh
// state. A completed or mismatched sidecar is treated as a new run so a repeat
// question is not silently answered from stale cache.
func (p *ResearchPipeline) loadOrInitState(sidecar *researchSidecar, question string, maxResults int) *researchState {
	if sidecar.exists() {
		if loaded, err := sidecar.load(); err == nil &&
			loaded.Version == currentResearchStateVersion &&
			loaded.Question == question &&
			loaded.isInProgress() {
			p.report(fmt.Sprintf("resuming research from phase %q…", loaded.Phase))
			return loaded
		}
	}
	now := time.Now()
	return &researchState{
		Version:    currentResearchStateVersion,
		Question:   question,
		MaxResults: maxResults,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

// sourceURLs extracts the page URLs in order for the result's source list.
func sourceURLs(pages []FetchedPage) []string {
	var sources []string
	for _, page := range pages {
		sources = append(sources, page.URL)
	}
	return sources
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
