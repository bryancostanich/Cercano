package research

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// AnalyzeFinding asks the local model to analyze a publication relative to the user's intent.
func AnalyzeFinding(ctx context.Context, model ModelCaller, pub Publication, content, intent string) (*AnnotatedFinding, error) {
	prompt := fmt.Sprintf(`Analyze this research finding relative to the user's research intent. Extract concrete, substantive information — not vague descriptions.

User's intent: %s

Finding:
Title: %s
Source: %s
Authors: %s
Date: %s
Content:
%s

Respond in EXACTLY this format:

SUMMARY: Write a detailed 4-6 sentence summary that includes KEY FACTS: specific numbers, methods, results, conclusions, names of technologies/tools, performance metrics, sample sizes, dates, or other concrete data points. Do NOT write vague descriptions like "this paper presents a novel approach" — instead write what the approach IS, what it FOUND, and what the NUMBERS were.

KEY_FINDINGS: List 3-5 bullet points of the most important specific facts, data points, or conclusions. Each bullet should contain concrete information someone could cite or act on.

WHY_IT_MATTERS: Explain specifically how this connects to the user's intent. Reference specific aspects of both the finding and the intent.

HOW_TO_USE: Give specific, actionable suggestions for how the user could apply this. Not generic advice — concrete next steps.

RELEVANCE: <1-5, where 5 is directly relevant>
IMPACT: <low, medium, or high>
CITED_REF: <title of a cited work that's relevant> | <why it's relevant> | <suggested source to find it>
CITED_REF: <another cited work> | <why> | <source>

Only include CITED_REF lines for works that are directly relevant to the user's intent. Include 0-5 references.`, intent, pub.Title, pub.Source, pub.Authors, pub.Date, truncateContent(content, 8000))

	resp, err := model.Call(ctx, prompt)
	if err != nil {
		return nil, err
	}

	finding := &AnnotatedFinding{
		Publication:    pub,
		RelevanceScore: 3, // default
		ImpactRating:   "medium",
	}

	// Parse with section-aware logic to handle multi-line values
	currentSection := ""
	var sectionLines []string

	flushSection := func() {
		text := strings.TrimSpace(strings.Join(sectionLines, "\n"))
		switch currentSection {
		case "SUMMARY":
			finding.Summary = text
		case "KEY_FINDINGS":
			for _, line := range sectionLines {
				line = strings.TrimSpace(line)
				line = strings.TrimPrefix(line, "- ")
				line = strings.TrimPrefix(line, "* ")
				line = strings.TrimPrefix(line, "• ")
				// Strip leading number + punctuation (1. or 1) )
				for i, c := range line {
					if c >= '0' && c <= '9' || c == '.' || c == ')' || c == ' ' {
						continue
					}
					line = line[i:]
					break
				}
				line = strings.TrimSpace(line)
				if line != "" {
					finding.KeyFindings = append(finding.KeyFindings, line)
				}
			}
		case "WHY_IT_MATTERS":
			finding.WhyItMatters = text
		case "HOW_TO_USE":
			finding.HowToUse = text
		}
		sectionLines = nil
	}

	for _, line := range strings.Split(resp, "\n") {
		trimmed := strings.TrimSpace(line)

		// Check for section headers
		if strings.HasPrefix(trimmed, "SUMMARY:") {
			flushSection()
			currentSection = "SUMMARY"
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "SUMMARY:"))
			if rest != "" {
				sectionLines = append(sectionLines, rest)
			}
		} else if strings.HasPrefix(trimmed, "KEY_FINDINGS:") {
			flushSection()
			currentSection = "KEY_FINDINGS"
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "KEY_FINDINGS:"))
			if rest != "" {
				sectionLines = append(sectionLines, rest)
			}
		} else if strings.HasPrefix(trimmed, "WHY_IT_MATTERS:") {
			flushSection()
			currentSection = "WHY_IT_MATTERS"
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "WHY_IT_MATTERS:"))
			if rest != "" {
				sectionLines = append(sectionLines, rest)
			}
		} else if strings.HasPrefix(trimmed, "HOW_TO_USE:") {
			flushSection()
			currentSection = "HOW_TO_USE"
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "HOW_TO_USE:"))
			if rest != "" {
				sectionLines = append(sectionLines, rest)
			}
		} else if strings.HasPrefix(trimmed, "RELEVANCE:") {
			flushSection()
			currentSection = ""
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "RELEVANCE:"))
			if n, err := strconv.Atoi(val); err == nil && n >= 1 && n <= 5 {
				finding.RelevanceScore = n
			}
		} else if strings.HasPrefix(trimmed, "IMPACT:") {
			flushSection()
			currentSection = ""
			val := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(trimmed, "IMPACT:")))
			if val == "low" || val == "medium" || val == "high" {
				finding.ImpactRating = val
			}
		} else if strings.HasPrefix(trimmed, "CITED_REF:") {
			flushSection()
			currentSection = ""
			ref := parseCitedRef(strings.TrimPrefix(trimmed, "CITED_REF:"))
			if ref != nil {
				finding.CitedRefs = append(finding.CitedRefs, *ref)
			}
		} else if currentSection != "" && trimmed != "" {
			sectionLines = append(sectionLines, trimmed)
		}
	}
	flushSection()

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
