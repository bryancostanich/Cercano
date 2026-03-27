// Package research implements deep multi-source research with ranked, annotated findings.
package research

import (
	"context"
	"time"
)

// ModelCaller abstracts calling the local model for inference.
type ModelCaller interface {
	Call(ctx context.Context, prompt string) (string, error)
}

// SearchProvider abstracts searching a source for publications.
type SearchProvider interface {
	Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error)
}

// URLFetcher abstracts fetching URL content.
type URLFetcher interface {
	FetchURL(url string) (*FetchResult, error)
}

// SearchResult is a single search hit.
type SearchResult struct {
	URL     string
	Title   string
	Snippet string
}

// FetchResult is the content fetched from a URL.
type FetchResult struct {
	URL     string
	Title   string
	Content string
}

// Publication represents a discovered work from any source.
type Publication struct {
	Title    string
	Authors  string
	Source   string // e.g. "PubMed", "arXiv", "Wired"
	URL      string
	Date     string
	Abstract string
	DOI      string
	Metadata map[string]string // source-specific fields
}

// AnnotatedFinding is a publication with analysis relative to the user's intent.
type AnnotatedFinding struct {
	Publication    Publication
	Summary        string
	WhyItMatters   string
	HowToUse       string
	RelevanceScore int    // 1-5
	ImpactRating   string // "low", "medium", "high"
	DiscoveredVia  string // parent finding title if chased, empty for primary
	CitedRefs      []CitedReference
}

// CitedReference is a reference extracted from a finding's content.
type CitedReference struct {
	Title  string
	Why    string // why it's relevant to the intent
	Source string // suggested source to search (e.g. "PubMed")
}

// Source represents a research source to search.
type Source struct {
	Name    string   // e.g. "PubMed", "Wired"
	Type    string   // "api" or "web"
	Site    string   // site domain for web-scoped DDG search (e.g. "wired.com")
	Queries []string // tailored search queries
	Reason  string   // why this source is relevant
}

// ResearchPlan is the output of source planning.
type ResearchPlan struct {
	Topic     string
	Intent    string
	Depth     string // "survey" or "thorough"
	DateRange string
	Sources   []Source
}

// ResearchProgress tracks the state of a running research job.
type ResearchProgress struct {
	Phase        string
	Step         string
	Current      int
	Total        int
	StartedAt    time.Time
}

// ReportSections holds all generated sections for the final report.
type ReportSections struct {
	ExecutiveSummary  string
	Synthesis         string
	Contradictions    string
	GapAnalysis       string
	ReadingOrder      []string
	FollowUpQueries   []string
}

// DeepResearchConfig holds configuration for a research run.
type DeepResearchConfig struct {
	MaxPrimaryResults   int // max results per source search
	MaxChasedTotal      int // max total chased references
	MaxChasedPerFinding int // max chased references per finding
	PageTruncateChars   int // max chars per fetched page
}

// DefaultConfig returns config for the given depth.
func DefaultConfig(depth string) DeepResearchConfig {
	if depth == "survey" {
		return DeepResearchConfig{
			MaxPrimaryResults:   5,
			MaxChasedTotal:      10,
			MaxChasedPerFinding: 3,
			PageTruncateChars:   6000,
		}
	}
	return DeepResearchConfig{
		MaxPrimaryResults:   10,
		MaxChasedTotal:      50,
		MaxChasedPerFinding: 5,
		PageTruncateChars:   8000,
	}
}
