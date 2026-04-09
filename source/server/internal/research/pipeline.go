package research

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Pipeline orchestrates the full deep research flow.
type Pipeline struct {
	model      ModelCaller
	dispatcher *SearchDispatcher
	fetcher    URLFetcher
}

// NewPipeline creates a new deep research pipeline.
func NewPipeline(model ModelCaller, dispatcher *SearchDispatcher, fetcher URLFetcher) *Pipeline {
	return &Pipeline{
		model:      model,
		dispatcher: dispatcher,
		fetcher:    fetcher,
	}
}

// RunConfig holds the parameters for a research run.
type RunConfig struct {
	Topic      string
	Intent     string
	Depth      string   // "survey", "standard", or "deep"
	DateRange  string
	Sources    []string // user override, empty for auto
	OutputDir  string   // write report to this directory if set
	ProjectDir string
	Phase      string // "plan", "search", "analyze", "synthesize", or "" for all
}

// SuggestedNext holds structured metadata for the host agent to auto-invoke deeper research.
type SuggestedNext struct {
	Action string            `json:"action"`
	Tool   string            `json:"tool"`
	Params map[string]string `json:"params"`
	Reason string            `json:"reason"`
}

// PhaseResult holds the output of a single phase.
type PhaseResult struct {
	Phase                string // which phase just completed
	NextPhase            string // what to run next ("" if done)
	Summary              string // human-readable summary for the host
	OutputDir            string
	FindingsCount        int
	ChasedCount          int
	SourcesSearched      int
	ContentTokensAvoided int
	Report               string         // full report, only set on final phase
	SuggestedNext        *SuggestedNext // structured hint for host to deepen research
}

// Run executes the pipeline — either a single phase or all phases.
func (p *Pipeline) Run(ctx context.Context, cfg RunConfig) (*PhaseResult, error) {
	depth := cfg.Depth
	if depth == "" {
		depth = "standard"
	}
	rcfg := DefaultConfig(depth)

	// Determine output directory
	outputDir := cfg.OutputDir
	if outputDir == "" {
		base := cfg.ProjectDir
		if base == "" {
			base, _ = os.Getwd()
		}
		outputDir = filepath.Join(base, "scratch", "research", slugifyTopic(cfg.Topic))
	}

	progress := NewProgressTracker(outputDir)
	sidecar := NewSidecar(outputDir)

	// Check for existing state
	var state *ResearchState
	if sidecar.Exists() {
		loaded, err := sidecar.Load()
		if err == nil && loaded.Version == CurrentStateVersion {
			if loaded.IsInProgress() {
				state = loaded // resume from crash
			} else if DepthOrder(depth) > DepthOrder(loaded.Depth) {
				state = loaded // incremental deepening
			} else {
				return &PhaseResult{
					Phase:   "all",
					Summary: fmt.Sprintf("Research already at depth '%s' (requested '%s'). To re-run, delete research_state.json in %s.", loaded.Depth, depth, outputDir),
				}, nil
			}
		}
	}

	if state == nil {
		state = NewState(cfg.Topic, cfg.Intent, depth, cfg.DateRange)
	} else {
		state.Depth = depth // upgrade depth level
	}

	phase := cfg.Phase
	if phase == "" {
		phase = "all"
	}

	switch phase {
	case "plan":
		return p.runPlan(ctx, cfg, state, sidecar, rcfg, progress, outputDir)
	case "search":
		return p.runSearch(ctx, cfg, state, sidecar, rcfg, progress, outputDir)
	case "analyze":
		return p.runAnalyze(ctx, cfg, state, sidecar, rcfg, progress, outputDir)
	case "synthesize":
		return p.runSynthesize(ctx, cfg, state, sidecar, rcfg, progress, outputDir)
	case "all":
		return p.runAll(ctx, cfg, state, sidecar, rcfg, progress, outputDir)
	default:
		return nil, fmt.Errorf("unknown phase: %s (use plan, search, analyze, synthesize, or omit for all)", phase)
	}
}

// --- Phase: Plan ---

func (p *Pipeline) runPlan(ctx context.Context, cfg RunConfig, state *ResearchState, sidecar *Sidecar, rcfg DeepResearchConfig, progress *ProgressTracker, outputDir string) (*PhaseResult, error) {
	progress.Update("Planning", "Identifying relevant sources...")

	var plan *ResearchPlan
	var err error

	if state.Plan != nil && len(state.Plan.Sources) > 0 {
		// Incremental deepening: expand existing plan with complementary sources
		newSources, expErr := PlanExpansion(ctx, p.model, cfg.Topic, cfg.Intent, state.Depth, cfg.DateRange, state.Plan.Sources, rcfg.MaxSources)
		if expErr != nil {
			return nil, fmt.Errorf("plan expansion: %w", expErr)
		}
		plan = state.Plan
		plan.Depth = state.Depth
		plan.Sources = append(plan.Sources, newSources...)
	} else if len(cfg.Sources) > 0 {
		plan, err = PlanWithOverride(ctx, p.model, cfg.Topic, cfg.Intent, state.Depth, cfg.DateRange, cfg.Sources)
	} else {
		plan, err = PlanSources(ctx, p.model, cfg.Topic, cfg.Intent, state.Depth, cfg.DateRange)
	}
	if err != nil {
		return nil, fmt.Errorf("source planning: %w", err)
	}

	state.Plan = plan
	state.Progress.Phase = "plan"
	state.Progress.PhaseStartedAt = time.Now()
	sidecar.Save(state)

	// Build summary
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Source plan for:** %s\n\n", cfg.Topic))
	sb.WriteString(fmt.Sprintf("Selected %d sources:\n", len(plan.Sources)))
	for i, src := range plan.Sources {
		sb.WriteString(fmt.Sprintf("%d. **%s** — %s\n", i+1, src.Name, src.Reason))
		for _, q := range src.Queries {
			sb.WriteString(fmt.Sprintf("   - `%s`\n", q))
		}
	}
	sb.WriteString(fmt.Sprintf("\nNext step: run with `phase: \"search\"` to search these sources."))
	sb.WriteString(fmt.Sprintf("\nTo adjust sources, re-run with `sources: [\"Source1\", \"Source2\"]`"))

	return &PhaseResult{
		Phase:           "plan",
		NextPhase:       "search",
		Summary:         sb.String(),
		OutputDir:       outputDir,
		SourcesSearched: len(plan.Sources),
	}, nil
}

// --- Phase: Search ---

func (p *Pipeline) runSearch(ctx context.Context, cfg RunConfig, state *ResearchState, sidecar *Sidecar, rcfg DeepResearchConfig, progress *ProgressTracker, outputDir string) (*PhaseResult, error) {
	plan := state.Plan
	if plan == nil || len(plan.Sources) == 0 {
		return nil, fmt.Errorf("no research plan found — run with phase: \"plan\" first")
	}

	progress.Update("Searching", fmt.Sprintf("Searching %d sources and prefetching content concurrently...", len(plan.Sources)))

	// Search + prefetch content in parallel
	pubs, contentMap := p.dispatcher.SearchAndPrefetch(ctx, plan, rcfg.MaxPrimaryResults, p.fetcher)

	// If deepening, skip publications already in state
	if len(state.SearchResults) > 0 {
		existingURLs := make(map[string]bool, len(state.SearchResults))
		for _, existing := range state.SearchResults {
			existingURLs[existing.URL] = true
		}
		var newPubs []Publication
		for _, pub := range pubs {
			if !existingURLs[pub.URL] {
				newPubs = append(newPubs, pub)
			}
		}
		state.SearchResults = append(state.SearchResults, newPubs...)
		// Merge content caches
		if state.ContentCache == nil {
			state.ContentCache = make(map[string]string)
		}
		for k, v := range contentMap {
			state.ContentCache[k] = v
		}
		pubs = state.SearchResults // for summary counts
	} else {
		state.SearchResults = pubs
		state.ContentCache = contentMap
	}

	state.Progress.Phase = "search"
	state.Progress.PhaseStartedAt = time.Now()
	sidecar.Save(state)

	// Build summary
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Search results for:** %s\n\n", cfg.Topic))
	sb.WriteString(fmt.Sprintf("Found **%d results** across %d sources:\n\n", len(pubs), len(plan.Sources)))

	// Group by source
	bySource := map[string]int{}
	for _, pub := range pubs {
		bySource[pub.Source]++
	}
	for src, count := range bySource {
		sb.WriteString(fmt.Sprintf("- **%s:** %d results\n", src, count))
	}
	sb.WriteString("\nTop results:\n")
	limit := 10
	if limit > len(pubs) {
		limit = len(pubs)
	}
	for i, pub := range pubs[:limit] {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, pub.Source, pub.Title))
	}
	if len(pubs) > limit {
		sb.WriteString(fmt.Sprintf("   ... and %d more\n", len(pubs)-limit))
	}
	sb.WriteString(fmt.Sprintf("\nNext step: run with `phase: \"analyze\"` to analyze these findings."))

	return &PhaseResult{
		Phase:           "search",
		NextPhase:       "analyze",
		Summary:         sb.String(),
		OutputDir:       outputDir,
		SourcesSearched: len(plan.Sources),
		FindingsCount:   len(pubs),
	}, nil
}

// --- Phase: Analyze ---

func (p *Pipeline) runAnalyze(ctx context.Context, cfg RunConfig, state *ResearchState, sidecar *Sidecar, rcfg DeepResearchConfig, progress *ProgressTracker, outputDir string) (*PhaseResult, error) {
	pubs := state.SearchResults
	if len(pubs) == 0 {
		return nil, fmt.Errorf("no search results found — run with phase: \"search\" first")
	}

	prefetched := state.ContentCache

	// If deepening, only analyze pubs not already in findings
	var pubsToAnalyze []Publication
	if len(state.Findings) > 0 {
		analyzedURLs := make(map[string]bool, len(state.Findings))
		for _, f := range state.Findings {
			analyzedURLs[f.Publication.URL] = true
		}
		for _, pub := range pubs {
			if !analyzedURLs[pub.URL] {
				pubsToAnalyze = append(pubsToAnalyze, pub)
			}
		}
	} else {
		pubsToAnalyze = pubs
	}

	progress.Update("Analyzing", fmt.Sprintf("Processing %d results (3 passes each)...", len(pubsToAnalyze)))
	newFindings := AnalyzeAllWithPrefetch(ctx, p.model, p.fetcher, pubsToAnalyze, prefetched, cfg.Intent, rcfg, progress)

	// Chase references on new findings
	if len(newFindings) > 0 {
		refCount := countCitedRefs(newFindings, rcfg)
		if refCount > 0 {
			progress.Update("Chasing", fmt.Sprintf("Following %d cited references...", refCount))
			chased := ChaseReferences(ctx, p.model, p.dispatcher, p.fetcher, newFindings, cfg.Intent, rcfg)
			newFindings = append(newFindings, chased...)
		}
	}

	// Merge new findings with existing
	allFindings := append(state.Findings, newFindings...)

	// If deepening and we have existing findings, re-analyze middle-scored ones
	if len(state.Findings) > 0 && len(newFindings) > 0 {
		progress.Update("Re-analyzing", "Re-scoring middle-range findings with new context...")
		allFindings = ReAnalyzeMiddleFindings(ctx, p.model, allFindings, cfg.Intent)
	}

	state.Findings = allFindings
	state.Progress.Phase = "analyze"
	state.Progress.PhaseStartedAt = time.Now()
	sidecar.Save(state)

	primaryCount, chasedCount := countFindings(allFindings)

	// Build summary
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Analysis complete for:** %s\n\n", cfg.Topic))
	sb.WriteString(fmt.Sprintf("Analyzed **%d findings** (%d primary, %d discovered via references)\n\n", primaryCount+chasedCount, primaryCount, chasedCount))

	sb.WriteString("Top findings by relevance:\n")
	sorted := make([]AnnotatedFinding, len(allFindings))
	copy(sorted, allFindings)
	sortFindings(sorted)
	limit := 10
	if limit > len(sorted) {
		limit = len(sorted)
	}
	for i, f := range sorted[:limit] {
		stars := strings.Repeat("\u2b50", f.RelevanceScore)
		sb.WriteString(fmt.Sprintf("%d. %s [%s] %s\n", i+1, stars, f.Publication.Source, f.Publication.Title))
		if f.Summary != "" {
			summary := f.Summary
			if len(summary) > 150 {
				summary = summary[:150] + "..."
			}
			sb.WriteString(fmt.Sprintf("   %s\n", summary))
		}
	}
	sb.WriteString(fmt.Sprintf("\nNext step: run with `phase: \"synthesize\"` to generate the final report."))

	// Track content for telemetry
	totalContent := 0
	for _, f := range allFindings {
		totalContent += len(f.Publication.Abstract)
	}

	return &PhaseResult{
		Phase:                "analyze",
		NextPhase:            "synthesize",
		Summary:              sb.String(),
		OutputDir:            outputDir,
		FindingsCount:        primaryCount + chasedCount,
		ChasedCount:          chasedCount,
		ContentTokensAvoided: totalContent / 4,
	}, nil
}

// --- Phase: Synthesize ---

func (p *Pipeline) runSynthesize(ctx context.Context, cfg RunConfig, state *ResearchState, sidecar *Sidecar, rcfg DeepResearchConfig, progress *ProgressTracker, outputDir string) (*PhaseResult, error) {
	findings := state.Findings
	if len(findings) == 0 {
		return nil, fmt.Errorf("no findings found — run with phase: \"analyze\" first")
	}

	plan := state.Plan
	if plan == nil {
		plan = &ResearchPlan{Topic: cfg.Topic, Intent: cfg.Intent}
	}

	progress.Update("Synthesizing", "Generating executive summary...")
	var sections ReportSections
	sections.ExecutiveSummary, _ = GenerateExecutiveSummary(ctx, p.model, findings, cfg.Intent)

	progress.Update("Synthesizing", "Generating narrative synthesis...")
	sections.Synthesis, _ = Synthesize(ctx, p.model, findings, cfg.Intent)

	progress.Update("Synthesizing", "Detecting contradictions...")
	sections.Contradictions, _ = DetectContradictions(ctx, p.model, findings)

	progress.Update("Synthesizing", "Analyzing gaps...")
	sections.GapAnalysis, _ = AnalyzeGaps(ctx, p.model, findings, cfg.Intent)

	progress.Update("Synthesizing", "Generating reading order and follow-ups...")
	sections.ReadingOrder, _ = RecommendReadingOrder(ctx, p.model, findings, cfg.Intent)
	if sections.GapAnalysis != "" {
		sections.FollowUpQueries, _ = SuggestFollowUp(ctx, p.model, sections.GapAnalysis, cfg.Intent)
	}

	state.Sections = &sections
	state.Progress = ProgressState{
		Phase:       "complete",
		CompletedAt: time.Now(),
	}
	sidecar.Save(state)

	// Compile and write report
	progress.Update("Compiling", "Writing report files...")
	primaryCount, chasedCount := countFindings(findings)
	report := CompileReport(plan, findings, sections)

	if err := WriteReport(outputDir, plan, findings, sections); err != nil {
		return nil, fmt.Errorf("failed to write report: %w", err)
	}

	progress.Done(primaryCount+chasedCount, len(plan.Sources))

	// Build summary
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Research complete:** %s\n\n", cfg.Topic))
	sb.WriteString(fmt.Sprintf("Report written to: `%s`\n\n", outputDir))
	sb.WriteString(fmt.Sprintf("- **Sources:** %d\n", len(plan.Sources)))
	sb.WriteString(fmt.Sprintf("- **Findings:** %d (%d primary, %d references)\n", primaryCount+chasedCount, primaryCount, chasedCount))
	sb.WriteString("\nFiles:\n")
	sb.WriteString("- `README.md` — index with table of contents\n")
	sb.WriteString("- `findings/` — individual finding files\n")
	sb.WriteString("- `references/` — discovered references\n")
	sb.WriteString("- `synthesis.md` — synthesis, gaps, follow-ups\n")
	sb.WriteString("- `source_plan.md` — sources and queries used\n")

	if sections.ExecutiveSummary != "" {
		sb.WriteString(fmt.Sprintf("\n**Executive Summary:**\n%s", sections.ExecutiveSummary))
	}

	totalContent := 0
	for _, f := range findings {
		totalContent += len(f.Publication.Abstract)
	}

	var suggested *SuggestedNext
	currentDepth := state.Depth
	if currentDepth == "survey" || currentDepth == "standard" {
		nextDepth := "standard"
		if currentDepth == "standard" {
			nextDepth = "deep"
		}
		suggested = &SuggestedNext{
			Action: "deepen",
			Tool:   "cercano_deep_research",
			Params: map[string]string{
				"topic":      cfg.Topic,
				"intent":     cfg.Intent,
				"depth":      nextDepth,
				"output_dir": outputDir,
			},
			Reason: fmt.Sprintf("%s found %d findings across %d sources. %s depth adds broader coverage and reference chasing.",
				currentDepth, primaryCount+chasedCount, len(plan.Sources), nextDepth),
		}
	}

	return &PhaseResult{
		Phase:                "synthesize",
		NextPhase:            "",
		Summary:              sb.String(),
		OutputDir:            outputDir,
		FindingsCount:        primaryCount + chasedCount,
		ChasedCount:          chasedCount,
		SourcesSearched:      len(plan.Sources),
		ContentTokensAvoided: totalContent / 4,
		Report:               report,
		SuggestedNext:        suggested,
	}, nil
}

// --- Run All (backward compat) ---

func (p *Pipeline) runAll(ctx context.Context, cfg RunConfig, state *ResearchState, sidecar *Sidecar, rcfg DeepResearchConfig, progress *ProgressTracker, outputDir string) (*PhaseResult, error) {
	// Plan
	planResult, err := p.runPlan(ctx, cfg, state, sidecar, rcfg, progress, outputDir)
	if err != nil {
		return nil, err
	}

	// Search
	searchResult, err := p.runSearch(ctx, cfg, state, sidecar, rcfg, progress, outputDir)
	if err != nil {
		return nil, err
	}
	if searchResult.FindingsCount == 0 {
		return &PhaseResult{
			Phase:   "all",
			Summary: fmt.Sprintf("No results found across %d sources.", planResult.SourcesSearched),
		}, nil
	}

	// Analyze
	_, err = p.runAnalyze(ctx, cfg, state, sidecar, rcfg, progress, outputDir)
	if err != nil {
		return nil, err
	}

	// Synthesize
	return p.runSynthesize(ctx, cfg, state, sidecar, rcfg, progress, outputDir)
}

// --- Helpers ---

func countFindings(findings []AnnotatedFinding) (primary, chased int) {
	for _, f := range findings {
		if f.DiscoveredVia != "" {
			chased++
		} else {
			primary++
		}
	}
	return
}

func countCitedRefs(findings []AnnotatedFinding, cfg DeepResearchConfig) int {
	count := 0
	for _, f := range findings {
		c := len(f.CitedRefs)
		if c > cfg.MaxChasedPerFinding {
			c = cfg.MaxChasedPerFinding
		}
		count += c
	}
	if count > cfg.MaxChasedTotal {
		count = cfg.MaxChasedTotal
	}
	return count
}

var nonAlphaNumPipeline = regexp.MustCompile(`[^a-z0-9]+`)

func slugifyTopic(topic string) string {
	s := strings.ToLower(topic)
	s = nonAlphaNumPipeline.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 50 {
		s = s[:50]
		if i := strings.LastIndex(s, "-"); i > 20 {
			s = s[:i]
		}
	}
	return s
}
