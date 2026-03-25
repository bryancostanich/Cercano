package web

import (
	"context"
	"errors"
	"testing"
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
	results := p.SearchAll(context.Background(), []string{"query1", "query2"}, 5)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
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
	results := p.SearchAll(context.Background(), []string{"query1", "query2"}, 5)
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
			"1. ollama list models API\n2. ollama REST API models",                                  // query crafting
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
