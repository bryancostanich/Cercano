package web

import (
	"context"
	"errors"
	"strings"
	"testing"

	"cercano/source/server/internal/modelbudget"
	"cercano/source/server/internal/tokens"
)

// mockModelCaller is a test double for the local model.
type mockModelCaller struct {
	responses []string // returned in order; cycles back to last if exhausted
	callCount int
	err       error
}

func (m *mockModelCaller) Call(ctx context.Context, prompt string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	idx := m.callCount
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	m.callCount++
	return m.responses[idx], nil
}

// mockSearcher is a test double for the DDG searcher.
type mockSearcher struct {
	results map[string][]SearchResult // keyed by query
	err     error
}

func (m *mockSearcher) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results[query], nil
}

// mockFetcher is a test double for the URL fetcher.
type mockFetcher struct {
	pages map[string]string // URL -> content
	err   error
}

func (m *mockFetcher) FetchURL(url string) (*FetchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	content, ok := m.pages[url]
	if !ok {
		return nil, errors.New("not found")
	}
	return &FetchResult{URL: url, Content: content, StatusCode: 200}, nil
}

func TestCraftQueries(t *testing.T) {
	model := &mockModelCaller{
		responses: []string{"1. how to list ollama models\n2. ollama api list models endpoint\n3. ollama REST API documentation"},
	}
	p := NewResearchPipeline(model, nil, nil)
	queries, err := p.CraftQueries(context.Background(), "How do I list models in Ollama?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(queries) < 2 || len(queries) > 3 {
		t.Fatalf("got %d queries, want 2-3", len(queries))
	}
}

func TestCraftQueriesModelError(t *testing.T) {
	model := &mockModelCaller{err: errors.New("model unavailable")}
	p := NewResearchPipeline(model, nil, nil)
	_, err := p.CraftQueries(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCraftQueriesFallback(t *testing.T) {
	// If the model returns garbage, CraftQueries should fall back to the original query
	model := &mockModelCaller{responses: []string{"I don't understand the question."}}
	p := NewResearchPipeline(model, nil, nil)
	queries, err := p.CraftQueries(context.Background(), "test query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(queries) != 1 || queries[0] != "test query" {
		t.Errorf("got %v, want [test query]", queries)
	}
}

func TestDeduplicateResults(t *testing.T) {
	results := []SearchResult{
		{URL: "https://a.com", Title: "A", Snippet: "first"},
		{URL: "https://b.com", Title: "B", Snippet: "second"},
		{URL: "https://a.com", Title: "A dup", Snippet: "duplicate"},
		{URL: "https://c.com", Title: "C", Snippet: "third"},
	}
	deduped := DeduplicateResults(results)
	if len(deduped) != 3 {
		t.Fatalf("got %d results, want 3", len(deduped))
	}
	// First occurrence should be preserved
	if deduped[0].Snippet != "first" {
		t.Errorf("deduped[0].Snippet = %q, want 'first'", deduped[0].Snippet)
	}
}

func TestDeduplicateResultsEmpty(t *testing.T) {
	deduped := DeduplicateResults(nil)
	if len(deduped) != 0 {
		t.Fatalf("got %d results, want 0", len(deduped))
	}
}

func TestSearchAll(t *testing.T) {
	searcher := &mockSearcher{
		results: map[string][]SearchResult{
			"query1": {{URL: "https://a.com", Title: "A", Snippet: "a"}},
			"query2": {{URL: "https://b.com", Title: "B", Snippet: "b"}},
		},
	}
	p := NewResearchPipeline(nil, searcher, nil)
	results, errs := p.SearchAll(context.Background(), []string{"query1", "query2"}, 5)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if len(errs) != 0 {
		t.Fatalf("got %d errors, want 0", len(errs))
	}
}

func TestSearchAllPartialFailure(t *testing.T) {
	// One query works, one fails — should return what we got
	searcher := &mockSearcher{
		results: map[string][]SearchResult{
			"query1": {{URL: "https://a.com", Title: "A", Snippet: "a"}},
			// "query2" not in map → will return nil (no results)
		},
	}
	p := NewResearchPipeline(nil, searcher, nil)
	results, _ := p.SearchAll(context.Background(), []string{"query1", "query2"}, 5)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

func TestFetchAll(t *testing.T) {
	fetcher := &mockFetcher{
		pages: map[string]string{
			"https://a.com": "Page A content",
			"https://b.com": "Page B content",
		},
	}
	p := NewResearchPipeline(nil, nil, fetcher)
	results := []SearchResult{
		{URL: "https://a.com", Title: "A"},
		{URL: "https://b.com", Title: "B"},
	}
	fetched := p.FetchAll(context.Background(), results, 5)
	if len(fetched) != 2 {
		t.Fatalf("got %d fetched, want 2", len(fetched))
	}
}

func TestFetchAllPartialFailure(t *testing.T) {
	fetcher := &mockFetcher{
		pages: map[string]string{
			"https://a.com": "Page A content",
			// b.com not present → fetch fails
		},
	}
	p := NewResearchPipeline(nil, nil, fetcher)
	results := []SearchResult{
		{URL: "https://a.com", Title: "A"},
		{URL: "https://b.com", Title: "B"},
	}
	fetched := p.FetchAll(context.Background(), results, 5)
	if len(fetched) != 1 {
		t.Fatalf("got %d fetched, want 1", len(fetched))
	}
}

func TestFetchAllRespectsMaxResults(t *testing.T) {
	fetcher := &mockFetcher{
		pages: map[string]string{
			"https://a.com": "A",
			"https://b.com": "B",
			"https://c.com": "C",
		},
	}
	p := NewResearchPipeline(nil, nil, fetcher)
	results := []SearchResult{
		{URL: "https://a.com", Title: "A"},
		{URL: "https://b.com", Title: "B"},
		{URL: "https://c.com", Title: "C"},
	}
	fetched := p.FetchAll(context.Background(), results, 2)
	if len(fetched) != 2 {
		t.Fatalf("got %d fetched, want 2", len(fetched))
	}
}

func TestSynthesize(t *testing.T) {
	model := &mockModelCaller{
		responses: []string{"Ollama provides a REST API at localhost:11434. [Source: https://a.com]"},
	}
	p := NewResearchPipeline(model, nil, nil)
	fetched := []FetchedPage{
		{URL: "https://a.com", Title: "Ollama Docs", Content: "REST API documentation..."},
	}
	answer, err := p.Synthesize(context.Background(), "How does Ollama API work?", fetched)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if answer == "" {
		t.Fatal("expected non-empty answer")
	}
}

func TestRunFullPipeline(t *testing.T) {
	model := &mockModelCaller{
		responses: []string{
			"1. ollama list models API\n2. ollama REST API models",                                 // query crafting
			"Ollama lists models via GET /api/tags.\n\nSources:\n- https://a.com\n- https://b.com", // synthesis
		},
	}
	searcher := &mockSearcher{
		results: map[string][]SearchResult{
			"ollama list models API": {
				{URL: "https://a.com", Title: "Ollama Docs", Snippet: "API docs"},
			},
			"ollama REST API models": {
				{URL: "https://b.com", Title: "Ollama GitHub", Snippet: "REST API"},
				{URL: "https://a.com", Title: "Ollama Docs", Snippet: "duplicate"},
			},
		},
	}
	fetcher := &mockFetcher{
		pages: map[string]string{
			"https://a.com": "Full documentation about listing models...",
			"https://b.com": "GitHub README with API details...",
		},
	}

	p := NewResearchPipeline(model, searcher, fetcher)
	result, err := p.Run(context.Background(), "How do I list models in Ollama?", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Answer == "" {
		t.Fatal("expected non-empty answer")
	}
	if len(result.Sources) == 0 {
		t.Fatal("expected at least one source")
	}
}

func TestRunReportsPhaseProgress(t *testing.T) {
	model := &mockModelCaller{
		responses: []string{
			"1. ollama list models API\n2. ollama REST API models",
			"Answer.\n\nSources:\n- https://a.com",
		},
	}
	searcher := &mockSearcher{
		results: map[string][]SearchResult{
			"ollama list models API": {{URL: "https://a.com", Title: "Ollama Docs", Snippet: "API docs"}},
			"ollama REST API models": {{URL: "https://a.com", Title: "Ollama Docs", Snippet: "dup"}},
		},
	}
	fetcher := &mockFetcher{pages: map[string]string{"https://a.com": "Full docs about listing models..."}}

	p := NewResearchPipeline(model, searcher, fetcher)
	var phases []string
	p.SetProgress(func(phase string) { phases = append(phases, phase) })

	if _, err := p.Run(context.Background(), "How do I list models in Ollama?", 5); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	joined := strings.Join(phases, "\n")
	for _, want := range []string{"crafting search queries", "searching", "found", "fetching", "synthesizing"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("progress phases missing %q; got:\n%s", want, joined)
		}
	}
}

func TestRunNoSearchResults(t *testing.T) {
	model := &mockModelCaller{
		responses: []string{
			"1. nonexistent topic search\n2. another bad query",
		},
	}
	searcher := &mockSearcher{results: map[string][]SearchResult{}}
	p := NewResearchPipeline(model, searcher, nil)
	_, err := p.Run(context.Background(), "something with no results", 5)
	if err == nil {
		t.Fatal("expected error for no search results")
	}
}

func TestRunAllSearchesFailedSurfacesCause(t *testing.T) {
	model := &mockModelCaller{responses: []string{"1. query one\n2. query two"}}
	searcher := &mockSearcher{err: errors.New("ddg search failed: can't open file")}
	p := NewResearchPipeline(model, searcher, nil)
	_, err := p.Run(context.Background(), "anything", 3)
	if err == nil {
		t.Fatal("want error when every search fails, got nil")
	}
	if !strings.Contains(err.Error(), "searches failed") || !strings.Contains(err.Error(), "can't open file") {
		t.Fatalf("error should surface the underlying cause, got: %v", err)
	}
}

func TestRunDurableWritesCompleteSidecar(t *testing.T) {
	model := &mockModelCaller{
		responses: []string{
			"1. ollama list models API",
			"Answer.\n\nSources:\n- https://a.com",
		},
	}
	searcher := &mockSearcher{
		results: map[string][]SearchResult{
			"ollama list models API": {{URL: "https://a.com", Title: "Ollama Docs", Snippet: "API docs"}},
		},
	}
	fetcher := &mockFetcher{pages: map[string]string{"https://a.com": "Full docs..."}}

	dir := t.TempDir()
	p := NewResearchPipeline(model, searcher, fetcher)
	result, err := p.RunDurable(context.Background(), "How do I list models in Ollama?", 5, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Answer == "" {
		t.Fatal("expected non-empty answer")
	}

	// Sidecar must exist and record a completed run with the answer preserved.
	sc := newResearchSidecar(dir)
	if !sc.exists() {
		t.Fatalf("expected sidecar at %s", sc.path())
	}
	st, err := sc.load()
	if err != nil {
		t.Fatalf("load sidecar: %v", err)
	}
	if st.Phase != phaseComplete {
		t.Fatalf("expected phase %q, got %q", phaseComplete, st.Phase)
	}
	if st.isInProgress() {
		t.Fatal("completed run should not be in progress")
	}
	if st.Answer == "" {
		t.Fatal("sidecar should persist the synthesized answer")
	}
}

func TestRunDurableResumesFromFetchPhase(t *testing.T) {
	// Pre-seed a sidecar interrupted right after the fetch phase: queries,
	// results, and pages are all present, but no answer yet. A resumed run
	// must skip straight to synthesis — so a model with ONLY a synthesis
	// response (no query-crafting response) must still succeed.
	dir := t.TempDir()
	seed := &researchState{
		Version:    currentResearchStateVersion,
		Question:   "How do I list models in Ollama?",
		MaxResults: 5,
		Phase:      phaseFetch,
		Queries:    []string{"ollama list models API"},
		Results:    []SearchResult{{URL: "https://a.com", Title: "Ollama Docs", Snippet: "API docs"}},
		Pages:      []FetchedPage{{URL: "https://a.com", Title: "Ollama Docs", Content: "Full docs..."}},
	}
	sc := newResearchSidecar(dir)
	if err := sc.save(seed); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}

	// Model returns a single synthesis answer. If the pipeline wrongly
	// re-crafted queries it would consume this as the query response and the
	// answer would be wrong (or crafting would drive a real search).
	model := &mockModelCaller{responses: []string{"Resumed answer.\n\nSources:\n- https://a.com"}}
	// Searcher/fetcher must NOT be called on resume; give them error doubles so
	// any accidental use fails loudly.
	searcher := &mockSearcher{err: errors.New("searcher must not run on resume")}
	fetcher := &mockFetcher{err: errors.New("fetcher must not run on resume")}

	p := NewResearchPipeline(model, searcher, fetcher)
	result, err := p.RunDurable(context.Background(), "How do I list models in Ollama?", 5, dir)
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if !strings.Contains(result.Answer, "Resumed answer") {
		t.Fatalf("expected resumed synthesis answer, got: %q", result.Answer)
	}
	if model.callCount != 1 {
		t.Fatalf("expected exactly 1 model call (synthesis only), got %d", model.callCount)
	}
}

func TestRunWithoutOutputDirWritesNoSidecar(t *testing.T) {
	// The classic Run path must remain sidecar-free. Verify by running in a
	// temp cwd and confirming no research_state.json appears anywhere we pass.
	model := &mockModelCaller{responses: []string{"1. q", "Answer.\n\nSources:\n- https://a.com"}}
	searcher := &mockSearcher{results: map[string][]SearchResult{"q": {{URL: "https://a.com", Title: "T", Snippet: "s"}}}}
	fetcher := &mockFetcher{pages: map[string]string{"https://a.com": "content"}}

	p := NewResearchPipeline(model, searcher, fetcher)
	if _, err := p.Run(context.Background(), "q?", 5); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// An empty-dir sidecar is disabled and reports no file.
	if newResearchSidecar("").exists() {
		t.Fatal("disabled sidecar should never report existing")
	}
}

type budgetedMockModelCaller struct {
	mockModelCaller
	budget     modelbudget.Budget
	lastPrompt string
}

func (m *budgetedMockModelCaller) Budget(ctx context.Context, outputReserve int) (modelbudget.Budget, error) {
	return m.budget, nil
}

func (m *budgetedMockModelCaller) Call(ctx context.Context, prompt string) (string, error) {
	m.lastPrompt = prompt
	return m.mockModelCaller.Call(ctx, prompt)
}

func TestSynthesize_BudgetsFetchedSourcesBeforeModelCall(t *testing.T) {
	budget := modelbudget.Budget{
		Target:      modelbudget.Target{Provider: "llama_server", Model: "tiny", ContextWindow: 1800, ContextWindowKnown: true},
		InputTokens: 320,
	}
	model := &budgetedMockModelCaller{
		mockModelCaller: mockModelCaller{responses: []string{"budgeted answer"}},
		budget:          budget,
	}
	p := NewResearchPipeline(model, nil, nil)
	pages := []FetchedPage{
		{URL: "https://a.com", Title: "A", Content: strings.Repeat("alpha beta gamma delta ", 400)},
		{URL: "https://b.com", Title: "B", Content: strings.Repeat("epsilon zeta eta theta ", 400)},
	}

	answer, err := p.Synthesize(context.Background(), "what happened?", pages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if answer != "budgeted answer" {
		t.Fatalf("answer = %q", answer)
	}
	if got := tokens.Estimate(model.lastPrompt); got > budget.InputTokens {
		t.Fatalf("prompt estimate = %d, want <= %d\nprompt:\n%s", got, budget.InputTokens, model.lastPrompt)
	}
	if !strings.Contains(model.lastPrompt, "https://a.com") {
		t.Fatalf("budgeted prompt lost source URL: %s", model.lastPrompt)
	}
	if !strings.Contains(model.lastPrompt, "truncated to fit local model context") {
		t.Fatalf("budgeted prompt did not mark truncation: %s", model.lastPrompt)
	}
}

func TestSynthesize_BudgetErrorBeforeModelCall(t *testing.T) {
	model := &budgetedMockModelCaller{
		mockModelCaller: mockModelCaller{responses: []string{"should not be called"}},
		budget:          modelbudget.Budget{Target: modelbudget.Target{Provider: "llama_server", Model: "tiny", ContextWindow: 900, ContextWindowKnown: true}, InputTokens: 80},
	}
	p := NewResearchPipeline(model, nil, nil)

	_, err := p.Synthesize(context.Background(), "what happened?", []FetchedPage{{URL: "https://a.com", Title: "A", Content: "alpha"}})
	if err == nil {
		t.Fatal("expected budget error")
	}
	if model.callCount != 0 {
		t.Fatalf("model was called %d times despite budget error", model.callCount)
	}
	if !strings.Contains(err.Error(), "research synthesis budget too small") {
		t.Fatalf("unexpected error: %v", err)
	}
}
