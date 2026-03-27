package research

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// AnalyzeFinding asks the local model to analyze a publication relative to the user's intent.
func AnalyzeFinding(ctx context.Context, model ModelCaller, pub Publication, content, intent string) (*AnnotatedFinding, error) {
	prompt := fmt.Sprintf(`Analyze this research finding relative to the user's research intent.

User's intent: %s

Finding:
Title: %s
Source: %s
Authors: %s
Date: %s
Content:
%s

Respond in EXACTLY this format:
SUMMARY: <2-3 sentence summary>
WHY_IT_MATTERS: <why this is relevant to the user's intent>
HOW_TO_USE: <how the user could apply this in their work>
RELEVANCE: <1-5, where 5 is directly relevant>
IMPACT: <low, medium, or high>
CITED_REF: <title of a cited work that's relevant> | <why it's relevant> | <suggested source to find it>
CITED_REF: <another cited work> | <why> | <source>

Only include CITED_REF lines for works that are directly relevant to the user's intent. Include 0-5 references.`, intent, pub.Title, pub.Source, pub.Authors, pub.Date, truncateContent(content, 6000))

	resp, err := model.Call(ctx, prompt)
	if err != nil {
		return nil, err
	}

	finding := &AnnotatedFinding{
		Publication:    pub,
		RelevanceScore: 3, // default
		ImpactRating:   "medium",
	}

	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SUMMARY:") {
			finding.Summary = strings.TrimSpace(strings.TrimPrefix(line, "SUMMARY:"))
		} else if strings.HasPrefix(line, "WHY_IT_MATTERS:") {
			finding.WhyItMatters = strings.TrimSpace(strings.TrimPrefix(line, "WHY_IT_MATTERS:"))
		} else if strings.HasPrefix(line, "HOW_TO_USE:") {
			finding.HowToUse = strings.TrimSpace(strings.TrimPrefix(line, "HOW_TO_USE:"))
		} else if strings.HasPrefix(line, "RELEVANCE:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "RELEVANCE:"))
			if n, err := strconv.Atoi(val); err == nil && n >= 1 && n <= 5 {
				finding.RelevanceScore = n
			}
		} else if strings.HasPrefix(line, "IMPACT:") {
			val := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(line, "IMPACT:")))
			if val == "low" || val == "medium" || val == "high" {
				finding.ImpactRating = val
			}
		} else if strings.HasPrefix(line, "CITED_REF:") {
			ref := parseCitedRef(strings.TrimPrefix(line, "CITED_REF:"))
			if ref != nil {
				finding.CitedRefs = append(finding.CitedRefs, *ref)
			}
		}
	}

	// Fallback if model didn't produce a summary
	if finding.Summary == "" {
		finding.Summary = pub.Abstract
		if finding.Summary == "" {
			finding.Summary = "(No summary available)"
		}
	}

	return finding, nil
}

// AnalyzeAll processes all publications sequentially, returning annotated findings.
func AnalyzeAll(ctx context.Context, model ModelCaller, fetcher URLFetcher, pubs []Publication, intent string, cfg DeepResearchConfig) []AnnotatedFinding {
	var findings []AnnotatedFinding

	for _, pub := range pubs {
		// Get content: prefer abstract from API, fall back to fetching the URL
		content := pub.Abstract
		if content == "" && pub.URL != "" {
			if result, err := fetcher.FetchURL(pub.URL); err == nil {
				content = result.Content
			}
		}

		if content == "" {
			continue // skip if we can't get any content
		}

		finding, err := AnalyzeFinding(ctx, model, pub, content, intent)
		if err != nil {
			continue // graceful degradation
		}
		findings = append(findings, *finding)
	}

	return findings
}

// ChaseReferences takes cited references from analyzed findings, searches for them,
// fetches content, and analyzes them. Returns additional findings.
func ChaseReferences(ctx context.Context, model ModelCaller, dispatcher *SearchDispatcher, fetcher URLFetcher, findings []AnnotatedFinding, intent string, cfg DeepResearchConfig) []AnnotatedFinding {
	// Collect all cited refs into a chase queue
	type chaseItem struct {
		ref       CitedReference
		parentTitle string
	}

	var queue []chaseItem
	existingURLs := make(map[string]bool)
	existingTitles := make(map[string]bool)

	// Build set of existing findings for dedup
	for _, f := range findings {
		if f.Publication.URL != "" {
			existingURLs[f.Publication.URL] = true
		}
		existingTitles[strings.ToLower(f.Publication.Title)] = true
	}

	// Collect refs from findings
	for _, f := range findings {
		count := 0
		for _, ref := range f.CitedRefs {
			if count >= cfg.MaxChasedPerFinding {
				break
			}
			if existingTitles[strings.ToLower(ref.Title)] {
				continue // already have this
			}
			queue = append(queue, chaseItem{ref: ref, parentTitle: f.Publication.Title})
			existingTitles[strings.ToLower(ref.Title)] = true
			count++
		}
	}

	// Limit total
	if len(queue) > cfg.MaxChasedTotal {
		queue = queue[:cfg.MaxChasedTotal]
	}

	var chased []AnnotatedFinding

	for _, item := range queue {
		// Search for the reference by title
		source := Source{
			Name:    "Google Scholar",
			Type:    "web",
			Site:    "scholar.google.com",
			Queries: []string{fmt.Sprintf(`"%s"`, item.ref.Title)},
		}

		// Try the suggested source first if it's a known web source
		if entry := FindSource(item.ref.Source); entry != nil && entry.Type == "web" {
			source.Name = entry.Name
			source.Site = entry.Site
		}

		pubs := dispatcher.SearchSource(ctx, source, 3)
		if len(pubs) == 0 {
			continue
		}

		// Take the best match (first result)
		pub := pubs[0]
		if existingURLs[pub.URL] {
			continue
		}
		existingURLs[pub.URL] = true

		// Fetch and analyze
		content := pub.Abstract
		if content == "" && pub.URL != "" {
			if result, err := fetcher.FetchURL(pub.URL); err == nil {
				content = result.Content
			}
		}
		if content == "" {
			continue
		}

		finding, err := AnalyzeFinding(ctx, model, pub, content, intent)
		if err != nil {
			continue
		}
		finding.DiscoveredVia = item.parentTitle
		finding.CitedRefs = nil // don't chase further (1-hop limit)
		chased = append(chased, *finding)
	}

	return chased
}

func parseCitedRef(s string) *CitedReference {
	s = strings.TrimSpace(s)
	parts := strings.SplitN(s, "|", 3)
	if len(parts) < 2 {
		return nil
	}
	title := strings.TrimSpace(parts[0])
	if title == "" {
		return nil
	}
	ref := &CitedReference{
		Title: title,
		Why:   strings.TrimSpace(parts[1]),
	}
	if len(parts) >= 3 {
		ref.Source = strings.TrimSpace(parts[2])
	}
	return ref
}

func truncateContent(content string, maxChars int) string {
	if len(content) <= maxChars {
		return content
	}
	return content[:maxChars] + "\n... (truncated)"
}
