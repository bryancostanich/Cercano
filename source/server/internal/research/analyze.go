package research

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// --- Pass 1: Fact Extraction ---

// ExtractFacts pulls concrete facts, numbers, methods, and conclusions from content.
func ExtractFacts(ctx context.Context, model ModelCaller, pub Publication, content string) ([]string, error) {
	prompt := fmt.Sprintf(`Extract every concrete fact from this content. Return ONLY a bullet list of specific facts.

Each bullet must contain a SPECIFIC piece of information: a number, a method name, a performance metric, a date, a conclusion, or a named technology. Do NOT include vague statements.

BAD bullet: "The tool uses a novel approach to inference."
GOOD bullet: "Uses 4-bit GPTQ quantization, achieving 15 tokens/sec on M2 MacBook Air with 8GB RAM."
BAD bullet: "The framework supports multiple platforms."
GOOD bullet: "Supports 12 hardware backends: Apple, Qualcomm, ARM, MediaTek, Intel, Vulkan, CUDA, and 5 others."

Title: %s
Source: %s
Content:
%s

Return ONLY bullet points starting with "- ". Nothing else.`, pub.Title, pub.Source, truncateContent(content, 12000))

	resp, err := model.Call(ctx, prompt)
	if err != nil {
		return nil, err
	}

	return parseBullets(resp), nil
}

// --- Pass 2: Relevance Analysis ---

// RelevanceResult holds the output of relevance analysis.
type RelevanceResult struct {
	WhyItMatters   string
	HowToUse       string
	RelevanceScore int
	ImpactRating   string
	CrossRefs      string // connections to other findings
	CitedRefs      []CitedReference
}

// AnalyzeRelevance assesses how a finding relates to the user's intent.
func AnalyzeRelevance(ctx context.Context, model ModelCaller, facts []string, title, intent, crossContext string) (*RelevanceResult, error) {
	crossSection := ""
	if crossContext != "" {
		crossSection = fmt.Sprintf(`
Previously analyzed findings (draw connections where relevant):
%s

If this finding contradicts, corroborates, or extends any prior finding, say so specifically.
`, crossContext)
	}

	factsText := ""
	for _, f := range facts {
		factsText += "- " + f + "\n"
	}

	prompt := fmt.Sprintf(`Given these extracted facts about "%s" and the user's research intent, analyze the relevance.

User's intent: %s

Facts:
%s
%s
Respond in this format:

WHY_IT_MATTERS: Explain the SPECIFIC connection between these facts and the user's intent. Don't say "this is relevant to the competitive landscape" — say HOW and WHY, referencing specific facts.
BAD: "This directly addresses the user's need to understand the competitive landscape."
GOOD: "ExecuTorch's 50KB footprint is 100x smaller than typical local inference tools, setting a benchmark Cercano would need to match for embedded deployment. Its 12-backend support also shows the market expects broad hardware coverage."

HOW_TO_USE: Give 2-3 SPECIFIC, actionable next steps. Not "consider evaluating this" — say exactly what to do.
BAD: "The user should consider this tool's approach."
GOOD: "1) Benchmark Cercano's binary size against ExecuTorch's 50KB. 2) Test if Cercano's Ollama dependency can run on the 12 platforms ExecuTorch supports. 3) Evaluate AOT compilation as an alternative to Cercano's current JIT approach."

CROSS_REFS: How does this relate to previously analyzed findings? (skip if no prior findings)

RELEVANCE: 1-5 (be discriminating — not everything is a 5. Use the full range.)
1 = tangentially related at best
2 = related topic but doesn't help with the specific intent
3 = useful context but not directly actionable
4 = directly relevant with actionable information
5 = essential — core finding that changes how you think about the intent

IMPACT: low, medium, or high

CITED_REF: <title> | <why relevant to intent> | <source to search>
(include 0-5, only if directly relevant)`, title, intent, factsText, crossSection)

	resp, err := model.Call(ctx, prompt)
	if err != nil {
		return nil, err
	}

	result := &RelevanceResult{
		RelevanceScore: 3,
		ImpactRating:   "medium",
	}

	for _, line := range strings.Split(resp, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "WHY_IT_MATTERS:") {
			result.WhyItMatters = strings.TrimSpace(strings.TrimPrefix(trimmed, "WHY_IT_MATTERS:"))
		} else if strings.HasPrefix(trimmed, "HOW_TO_USE:") {
			result.HowToUse = strings.TrimSpace(strings.TrimPrefix(trimmed, "HOW_TO_USE:"))
		} else if strings.HasPrefix(trimmed, "CROSS_REFS:") {
			result.CrossRefs = strings.TrimSpace(strings.TrimPrefix(trimmed, "CROSS_REFS:"))
		} else if strings.HasPrefix(trimmed, "RELEVANCE:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "RELEVANCE:"))
			// Handle "4 —" or "4/5" or just "4"
			val = strings.Split(val, " ")[0]
			val = strings.Split(val, "/")[0]
			if n, err := strconv.Atoi(val); err == nil && n >= 1 && n <= 5 {
				result.RelevanceScore = n
			}
		} else if strings.HasPrefix(trimmed, "IMPACT:") {
			val := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(trimmed, "IMPACT:")))
			val = strings.Split(val, " ")[0] // handle "high — because..."
			if val == "low" || val == "medium" || val == "high" {
				result.ImpactRating = val
			}
		} else if strings.HasPrefix(trimmed, "CITED_REF:") {
			ref := parseCitedRef(strings.TrimPrefix(trimmed, "CITED_REF:"))
			if ref != nil {
				result.CitedRefs = append(result.CitedRefs, *ref)
			}
		}
	}

	return result, nil
}

// --- Pass 3: Quality Gate ---

// ScoreQuality checks if a summary and facts are specific enough to be useful.
// Returns pass/fail and a critique if it fails.
func ScoreQuality(ctx context.Context, model ModelCaller, summary string, keyFindings []string) (bool, string, error) {
	factsText := ""
	for _, f := range keyFindings {
		factsText += "- " + f + "\n"
	}

	prompt := fmt.Sprintf(`Review this research summary and key findings for QUALITY. Is it specific enough to be useful?

Summary: %s

Key Findings:
%s

Check:
1. Does the summary contain SPECIFIC facts (numbers, named tools, metrics, dates)?
2. Are the key findings concrete enough that someone could CITE them?
3. Or is it vague filler like "presents a novel approach" or "is relevant to the field"?

Respond with EXACTLY one line:
PASS — if it contains specific, citable information
FAIL: <what's vague and needs to be more specific>`, summary, factsText)

	resp, err := model.Call(ctx, prompt)
	if err != nil {
		return true, "", err // default to pass on error
	}

	resp = strings.TrimSpace(resp)
	if strings.HasPrefix(resp, "PASS") {
		return true, "", nil
	}
	critique := strings.TrimPrefix(resp, "FAIL:")
	critique = strings.TrimSpace(critique)
	return false, critique, nil
}

// --- Cross-Finding Context ---

// BuildCrossContext creates a 1-line-per-finding context string for cross-referencing.
func BuildCrossContext(findings []AnnotatedFinding) string {
	var sb strings.Builder
	limit := len(findings)
	if limit > 15 {
		limit = 15
	}
	for i, f := range findings[:limit] {
		// Build a 1-liner from the top key finding or summary
		oneLiner := f.Summary
		if len(f.KeyFindings) > 0 {
			oneLiner = f.KeyFindings[0]
		}
		// Truncate to keep context compact
		if len(oneLiner) > 120 {
			oneLiner = oneLiner[:120] + "..."
		}
		sb.WriteString(fmt.Sprintf("%d. [%s] %s — %s\n", i+1, f.Publication.Source, f.Publication.Title, oneLiner))
	}
	return sb.String()
}

// --- Orchestrated Analysis ---

// AnalyzeFinding runs the multi-pass analysis pipeline on a single publication.
func AnalyzeFinding(ctx context.Context, model ModelCaller, pub Publication, content, intent, crossContext string) (*AnnotatedFinding, error) {
	// Pass 1: Extract facts
	facts, err := ExtractFacts(ctx, model, pub, content)
	if err != nil || len(facts) == 0 {
		// Fallback: use abstract or content snippet as a single "fact"
		if pub.Abstract != "" {
			facts = []string{pub.Abstract}
		} else if len(content) > 200 {
			facts = []string{content[:200]}
		} else {
			facts = []string{content}
		}
	}

	// Pass 2: Analyze relevance
	relevance, err := AnalyzeRelevance(ctx, model, facts, pub.Title, intent, crossContext)
	if err != nil {
		relevance = &RelevanceResult{RelevanceScore: 3, ImpactRating: "medium"}
	}

	// Build summary from facts
	summary := buildSummaryFromFacts(pub.Title, facts)

	// Pass 3: Quality gate
	passed, critique, _ := ScoreQuality(ctx, model, summary, facts)
	if !passed && critique != "" {
		// Retry fact extraction with critique
		retryFacts, err := retryExtractFacts(ctx, model, pub, content, critique)
		if err == nil && len(retryFacts) > 0 {
			facts = retryFacts
			summary = buildSummaryFromFacts(pub.Title, facts)
		}
	}

	finding := &AnnotatedFinding{
		Publication:    pub,
		Summary:        summary,
		KeyFindings:    facts,
		WhyItMatters:   relevance.WhyItMatters,
		HowToUse:       relevance.HowToUse,
		RelevanceScore: relevance.RelevanceScore,
		ImpactRating:   relevance.ImpactRating,
		CitedRefs:      relevance.CitedRefs,
	}

	// Add cross-reference notes if present
	if relevance.CrossRefs != "" {
		finding.WhyItMatters += "\n\n**Connections to other findings:** " + relevance.CrossRefs
	}

	return finding, nil
}

// PrefetchContent fetches all publication URLs concurrently and returns a map of URL → content.
func PrefetchContent(ctx context.Context, fetcher URLFetcher, pubs []Publication, progress *ProgressWriter) map[string]string {
	content := make(map[string]string)
	var mu sync.Mutex
	var wg sync.WaitGroup

	progress.Update("Fetching", fmt.Sprintf("Prefetching %d pages concurrently...", len(pubs)))

	for _, pub := range pubs {
		if pub.Abstract != "" {
			// Already have content from API
			mu.Lock()
			content[pub.URL] = pub.Abstract
			mu.Unlock()
			continue
		}
		if pub.URL == "" {
			continue
		}

		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			if result, err := fetcher.FetchURL(url); err == nil && result.Content != "" {
				mu.Lock()
				content[url] = result.Content
				mu.Unlock()
			}
		}(pub.URL)
	}

	wg.Wait()
	progress.Update("Fetching", fmt.Sprintf("Prefetched %d pages", len(content)))
	return content
}

// AnalyzeAllWithPrefetch uses a pre-populated content map (from search+fetch overlap).
// Falls back to fetching individual URLs if content is missing.
func AnalyzeAllWithPrefetch(ctx context.Context, model ModelCaller, fetcher URLFetcher, pubs []Publication, prefetched map[string]string, intent string, cfg DeepResearchConfig, progress *ProgressWriter) []AnnotatedFinding {
	// If no prefetched content, do a bulk prefetch now
	if len(prefetched) == 0 {
		prefetched = PrefetchContent(ctx, fetcher, pubs, progress)
	}

	var findings []AnnotatedFinding

	for i, pub := range pubs {
		progress.Update("Analyzing", fmt.Sprintf("Finding %d of %d: %s", i+1, len(pubs), truncateTitle(pub.Title, 50)))

		content := prefetched[pub.URL]
		if content == "" && pub.Abstract != "" {
			content = pub.Abstract
		}
		// Last resort: fetch individually
		if content == "" && pub.URL != "" {
			if result, err := fetcher.FetchURL(pub.URL); err == nil {
				content = result.Content
			}
		}
		if content == "" {
			continue
		}

		crossCtx := BuildCrossContext(findings)

		finding, err := AnalyzeFinding(ctx, model, pub, content, intent, crossCtx)
		if err != nil {
			continue
		}
		findings = append(findings, *finding)
	}

	return findings
}

// AnalyzeAllWithProgress processes all publications with progress updates.
// Content is prefetched concurrently before analysis begins.
func AnalyzeAllWithProgress(ctx context.Context, model ModelCaller, fetcher URLFetcher, pubs []Publication, intent string, cfg DeepResearchConfig, progress *ProgressWriter) []AnnotatedFinding {
	prefetched := PrefetchContent(ctx, fetcher, pubs, progress)
	return AnalyzeAllWithPrefetch(ctx, model, fetcher, pubs, prefetched, intent, cfg, progress)
}

// AnalyzeAll processes all publications sequentially with accumulating cross-context (no progress).
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
			continue
		}

		// Build cross-context from previously analyzed findings
		crossCtx := BuildCrossContext(findings)

		finding, err := AnalyzeFinding(ctx, model, pub, content, intent, crossCtx)
		if err != nil {
			continue
		}
		findings = append(findings, *finding)
	}

	return findings
}

// ChaseReferences takes cited references from analyzed findings, searches for them,
// fetches content, and analyzes them. Returns additional findings.
func ChaseReferences(ctx context.Context, model ModelCaller, dispatcher *SearchDispatcher, fetcher URLFetcher, findings []AnnotatedFinding, intent string, cfg DeepResearchConfig) []AnnotatedFinding {
	type chaseItem struct {
		ref         CitedReference
		parentTitle string
	}

	var queue []chaseItem
	existingURLs := make(map[string]bool)
	existingTitles := make(map[string]bool)

	for _, f := range findings {
		if f.Publication.URL != "" {
			existingURLs[f.Publication.URL] = true
		}
		existingTitles[strings.ToLower(f.Publication.Title)] = true
	}

	for _, f := range findings {
		count := 0
		for _, ref := range f.CitedRefs {
			if count >= cfg.MaxChasedPerFinding {
				break
			}
			if existingTitles[strings.ToLower(ref.Title)] {
				continue
			}
			queue = append(queue, chaseItem{ref: ref, parentTitle: f.Publication.Title})
			existingTitles[strings.ToLower(ref.Title)] = true
			count++
		}
	}

	if len(queue) > cfg.MaxChasedTotal {
		queue = queue[:cfg.MaxChasedTotal]
	}

	// Build cross-context from all primary findings
	crossCtx := BuildCrossContext(findings)
	var chased []AnnotatedFinding

	for _, item := range queue {
		source := Source{
			Name:    "Google Scholar",
			Type:    "web",
			Site:    "scholar.google.com",
			Queries: []string{fmt.Sprintf(`"%s"`, item.ref.Title)},
		}

		if entry := FindSource(item.ref.Source); entry != nil && entry.Type == "web" {
			source.Name = entry.Name
			source.Site = entry.Site
		}

		pubs := dispatcher.SearchSource(ctx, source, 3)
		if len(pubs) == 0 {
			continue
		}

		pub := pubs[0]
		if existingURLs[pub.URL] {
			continue
		}
		existingURLs[pub.URL] = true

		content := pub.Abstract
		if content == "" && pub.URL != "" {
			if result, err := fetcher.FetchURL(pub.URL); err == nil {
				content = result.Content
			}
		}
		if content == "" {
			continue
		}

		finding, err := AnalyzeFinding(ctx, model, pub, content, intent, crossCtx)
		if err != nil {
			continue
		}
		finding.DiscoveredVia = item.parentTitle
		finding.CitedRefs = nil // 1-hop limit
		chased = append(chased, *finding)
	}

	return chased
}

// --- Helpers ---

func buildSummaryFromFacts(title string, facts []string) string {
	if len(facts) == 0 {
		return "(No summary available)"
	}
	// Join top facts into a narrative summary
	limit := len(facts)
	if limit > 5 {
		limit = 5
	}
	return strings.Join(facts[:limit], ". ") + "."
}

func retryExtractFacts(ctx context.Context, model ModelCaller, pub Publication, content, critique string) ([]string, error) {
	prompt := fmt.Sprintf(`The previous fact extraction was too vague. %s

Try again — extract SPECIFIC facts from this content. Each bullet must have a concrete number, metric, method name, or conclusion.

Title: %s
Content:
%s

Return ONLY bullet points starting with "- ". Be MORE SPECIFIC this time.`, critique, pub.Title, truncateContent(content, 12000))

	resp, err := model.Call(ctx, prompt)
	if err != nil {
		return nil, err
	}
	return parseBullets(resp), nil
}

func parseBullets(text string) []string {
	var bullets []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			bullet := strings.TrimPrefix(line, "- ")
			bullet = strings.TrimSpace(bullet)
			if bullet != "" {
				bullets = append(bullets, bullet)
			}
		} else if strings.HasPrefix(line, "* ") {
			bullet := strings.TrimPrefix(line, "* ")
			bullet = strings.TrimSpace(bullet)
			if bullet != "" {
				bullets = append(bullets, bullet)
			}
		} else if strings.HasPrefix(line, "• ") {
			bullet := strings.TrimPrefix(line, "• ")
			bullet = strings.TrimSpace(bullet)
			if bullet != "" {
				bullets = append(bullets, bullet)
			}
		}
	}
	return bullets
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

func truncateTitle(title string, maxLen int) string {
	if len(title) <= maxLen {
		return title
	}
	return title[:maxLen] + "..."
}

func truncateContent(content string, maxChars int) string {
	if len(content) <= maxChars {
		return content
	}
	return content[:maxChars] + "\n... (truncated)"
}
