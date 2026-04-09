package research

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// SearchDispatcher routes searches to the right backend based on source type.
type SearchDispatcher struct {
	ddgSearcher SearchProvider
	httpClient  *http.Client
}

// NewSearchDispatcher creates a dispatcher with a DDG searcher for web-scoped searches.
func NewSearchDispatcher(ddgSearcher SearchProvider) *SearchDispatcher {
	return &SearchDispatcher{
		ddgSearcher: ddgSearcher,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
}

// SearchSource executes all queries for a source and returns deduplicated results.
func (d *SearchDispatcher) SearchSource(ctx context.Context, source Source, maxResults int) []Publication {
	var allPubs []Publication
	var mu sync.Mutex

	for _, query := range source.Queries {
		var pubs []Publication
		var err error

		switch source.Name {
		case "PubMed":
			pubs, err = d.searchPubMed(ctx, query, maxResults)
		case "arXiv":
			pubs, err = d.searchArXiv(ctx, query, maxResults)
		default:
			pubs, err = d.searchWeb(ctx, source, query, maxResults)
		}

		if err != nil {
			continue // graceful degradation
		}

		mu.Lock()
		allPubs = append(allPubs, pubs...)
		mu.Unlock()
	}

	result := deduplicatePubs(allPubs)
	if len(result) > maxResults {
		result = result[:maxResults]
	}
	return result
}

// SearchAllSources searches all sources in a plan concurrently.
func (d *SearchDispatcher) SearchAllSources(ctx context.Context, plan *ResearchPlan, maxPerSource int) []Publication {
	var allPubs []Publication
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, source := range plan.Sources {
		wg.Add(1)
		go func(src Source) {
			defer wg.Done()
			pubs := d.SearchSource(ctx, src, maxPerSource)
			mu.Lock()
			allPubs = append(allPubs, pubs...)
			mu.Unlock()
		}(source)
	}

	wg.Wait()
	topicIntent := plan.Topic + " " + plan.Intent
	deduped := deduplicatePubs(allPubs)
	return FilterByKeywordOverlap(deduped, topicIntent)
}

// SearchAndPrefetch searches all sources concurrently AND starts fetching content
// as results come in. Returns publications and a prefetched content map.
func (d *SearchDispatcher) SearchAndPrefetch(ctx context.Context, plan *ResearchPlan, maxPerSource int, fetcher URLFetcher) ([]Publication, map[string]string) {
	var allPubs []Publication
	var pubsMu sync.Mutex

	content := make(map[string]string)
	var contentMu sync.Mutex
	var fetchWg sync.WaitGroup

	var searchWg sync.WaitGroup

	for _, source := range plan.Sources {
		searchWg.Add(1)
		go func(src Source) {
			defer searchWg.Done()
			pubs := d.SearchSource(ctx, src, maxPerSource)

			pubsMu.Lock()
			allPubs = append(allPubs, pubs...)
			pubsMu.Unlock()

			// Start fetching these results immediately
			for _, pub := range pubs {
				if pub.Abstract != "" {
					contentMu.Lock()
					content[pub.URL] = pub.Abstract
					contentMu.Unlock()
					continue
				}
				if pub.URL == "" {
					continue
				}

				fetchWg.Add(1)
				go func(url string) {
					defer fetchWg.Done()
					if result, err := fetcher.FetchURL(url); err == nil && result.Content != "" {
						contentMu.Lock()
						content[url] = result.Content
						contentMu.Unlock()
					}
				}(pub.URL)
			}
		}(source)
	}

	searchWg.Wait()
	fetchWg.Wait()

	topicIntent := plan.Topic + " " + plan.Intent
	deduped := deduplicatePubs(allPubs)
	filtered := FilterByKeywordOverlap(deduped, topicIntent)
	return filtered, content
}

// searchWeb uses DDG with site-scoping for web sources.
func (d *SearchDispatcher) searchWeb(ctx context.Context, source Source, query string, maxResults int) ([]Publication, error) {
	searchQuery := query
	if source.Site != "" {
		searchQuery = fmt.Sprintf("site:%s %s", source.Site, query)
	}

	results, err := d.ddgSearcher.Search(ctx, searchQuery, maxResults)
	if err != nil {
		return nil, err
	}

	var pubs []Publication
	for _, r := range results {
		pubs = append(pubs, Publication{
			Title:    r.Title,
			URL:      r.URL,
			Abstract: r.Snippet,
			Source:   source.Name,
		})
	}
	return pubs, nil
}

// PubMed types
type pubmedSearchResult struct {
	ESearchResult struct {
		IDList []string `json:"idlist"`
	} `json:"esearchresult"`
}

type pubmedSummaryResult struct {
	Result map[string]json.RawMessage `json:"result"`
}

type pubmedArticle struct {
	UID       string `json:"uid"`
	Title     string `json:"title"`
	Source    string `json:"source"`
	PubDate  string `json:"pubdate"`
	AuthorList []struct {
		Name string `json:"name"`
	} `json:"authors"`
	DOI string `json:"elocationid"`
}

func (d *SearchDispatcher) searchPubMed(ctx context.Context, query string, maxResults int) ([]Publication, error) {
	// Step 1: Search for IDs
	searchURL := fmt.Sprintf("https://eutils.ncbi.nlm.nih.gov/entrez/eutils/esearch.fcgi?db=pubmed&term=%s&retmax=%d&retmode=json",
		url.QueryEscape(query), maxResults)

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var searchResp pubmedSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, err
	}

	ids := searchResp.ESearchResult.IDList
	if len(ids) == 0 {
		return nil, nil
	}

	// Rate limit: brief pause before summary request
	time.Sleep(350 * time.Millisecond)

	// Step 2: Fetch summaries
	summaryURL := fmt.Sprintf("https://eutils.ncbi.nlm.nih.gov/entrez/eutils/esummary.fcgi?db=pubmed&id=%s&retmode=json",
		strings.Join(ids, ","))

	req, err = http.NewRequestWithContext(ctx, "GET", summaryURL, nil)
	if err != nil {
		return nil, err
	}
	resp2, err := d.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp2.Body.Close()

	var summaryResp pubmedSummaryResult
	if err := json.NewDecoder(resp2.Body).Decode(&summaryResp); err != nil {
		return nil, err
	}

	var pubs []Publication
	for _, id := range ids {
		raw, ok := summaryResp.Result[id]
		if !ok {
			continue
		}
		var article pubmedArticle
		if err := json.Unmarshal(raw, &article); err != nil {
			continue
		}

		var authors []string
		for _, a := range article.AuthorList {
			authors = append(authors, a.Name)
		}

		pubs = append(pubs, Publication{
			Title:   article.Title,
			Authors: strings.Join(authors, ", "),
			Source:  "PubMed",
			URL:     fmt.Sprintf("https://pubmed.ncbi.nlm.nih.gov/%s/", id),
			Date:    article.PubDate,
			DOI:     article.DOI,
		})
	}

	return pubs, nil
}

// arXiv types
type arxivFeed struct {
	XMLName xml.Name     `xml:"feed"`
	Entries []arxivEntry `xml:"entry"`
}

type arxivEntry struct {
	Title     string       `xml:"title"`
	Summary   string       `xml:"summary"`
	Published string       `xml:"published"`
	ID        string       `xml:"id"`
	Authors   []arxivAuthor `xml:"author"`
}

type arxivAuthor struct {
	Name string `xml:"name"`
}

func (d *SearchDispatcher) searchArXiv(ctx context.Context, query string, maxResults int) ([]Publication, error) {
	searchURL := fmt.Sprintf("https://export.arxiv.org/api/query?search_query=all:%s&max_results=%d",
		url.QueryEscape(query), maxResults)

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var feed arxivFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, err
	}

	var pubs []Publication
	for _, e := range feed.Entries {
		var authors []string
		for _, a := range e.Authors {
			authors = append(authors, a.Name)
		}

		pubs = append(pubs, Publication{
			Title:    strings.TrimSpace(e.Title),
			Authors:  strings.Join(authors, ", "),
			Source:   "arXiv",
			URL:      e.ID,
			Date:     e.Published,
			Abstract: strings.TrimSpace(e.Summary),
		})
	}

	return pubs, nil
}

// FilterByKeywordOverlap removes publications whose title has zero keyword overlap
// with the given topic/intent text. Keywords shorter than 4 chars are ignored (stop words).
func FilterByKeywordOverlap(pubs []Publication, topicIntent string) []Publication {
	keywords := make(map[string]bool)
	for _, word := range strings.Fields(strings.ToLower(topicIntent)) {
		if len(word) >= 4 {
			keywords[word] = true
		}
	}
	if len(keywords) == 0 {
		return pubs
	}

	var result []Publication
	for _, p := range pubs {
		titleWords := strings.Fields(strings.ToLower(p.Title))
		overlap := false
		for _, w := range titleWords {
			// Strip leading/trailing punctuation before matching
			w = strings.Trim(w, ".,;:!?\"'()")
			if keywords[w] {
				overlap = true
				break
			}
		}
		if overlap {
			result = append(result, p)
		}
	}

	// If filtering removed everything, return originals
	if len(result) == 0 {
		return pubs
	}
	return result
}

// deduplicatePubs removes duplicates by URL.
func deduplicatePubs(pubs []Publication) []Publication {
	seen := make(map[string]bool)
	var result []Publication
	for _, p := range pubs {
		if p.URL == "" || seen[p.URL] {
			continue
		}
		seen[p.URL] = true
		result = append(result, p)
	}
	return result
}
