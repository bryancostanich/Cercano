package research

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	Depth      string   // "survey" or "thorough"
	DateRange  string
	Sources    []string // user override, empty for auto
	OutputDir  string   // write report to this directory if set
	ProjectDir string
	Phase      string   // "plan", "search", "analyze", "synthesize", or "" for all
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
	Report               string // full report, only set on final phase
}

// Run executes the pipeline — either a single phase or all phases.
func (p *Pipeline) Run(ctx context.Context, cfg RunConfig) (*PhaseResult, error) {
	depth := cfg.Depth
	if depth == "" {
		depth = "thorough"
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

	progress := NewProgressWriter(outputDir)

	// Checkpoints
	baseDir := cfg.ProjectDir
	if baseDir == "" {
		baseDir, _ = os.Getwd()
	}
	cp := NewCheckpoint(baseDir, cfg.Topic, cfg.Intent, depth)

	phase := cfg.Phase
	if phase == "" {
		phase = "all"
	}

	switch phase {
	case "plan":
		return p.runPlan(ctx, cfg, cp, progress, outputDir)
	case "search":
		return p.runSearch(ctx, cfg, cp, rcfg, progress, outputDir)
	case "analyze":
		return p.runAnalyze(ctx, cfg, cp, rcfg, progress, outputDir)
	case "synthesize":
		return p.runSynthesize(ctx, cfg, cp, rcfg, progress, outputDir)
	case "all":
		return p.runAll(ctx, cfg, cp, rcfg, progress, outputDir)
	default:
		return nil, fmt.Errorf("unknown phase: %s (use plan, search, analyze, synthesize, or omit for all)", phase)
	}
}

// --- Phase: Plan ---

func (p *Pipeline) runPlan(ctx context.Context, cfg RunConfig, cp *Checkpoint, progress *ProgressWriter, outputDir string) (*PhaseResult, error) {
	progress.Update("Planning", "Identifying relevant sources...")

	var plan *ResearchPlan
	var err error
	if len(cfg.Sources) > 0 {
		plan, err = PlanWithOverride(ctx, p.model, cfg.Topic, cfg.Intent, cfg.Depth, cfg.DateRange, cfg.Sources)
	} else {
		plan, err = PlanSources(ctx, p.model, cfg.Topic, cfg.Intent, cfg.Depth, cfg.DateRange)
	}
	if err != nil {
		return nil, fmt.Errorf("source planning: %w", err)
	}
	cp.SavePlan(plan)

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

func (p *Pipeline) runSearch(ctx context.Context, cfg RunConfig, cp *Checkpoint, rcfg DeepResearchConfig, progress *ProgressWriter, outputDir string) (*PhaseResult, error) {
	// Load plan from checkpoint
	plan, err := cp.LoadPlan()
	if err != nil {
		return nil, fmt.Errorf("no research plan found — run with phase: \"plan\" first")
	}

	progress.Update("Searching", fmt.Sprintf("Querying %d sources...", len(plan.Sources)))

	pubs := p.dispatcher.SearchAllSources(ctx, plan, rcfg.MaxPrimaryResults)
	cp.SaveSearchResults(pubs)

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

func (p *Pipeline) runAnalyze(ctx context.Context, cfg RunConfig, cp *Checkpoint, rcfg DeepResearchConfig, progress *ProgressWriter, outputDir string) (*PhaseResult, error) {
	pubs, err := cp.LoadSearchResults()
	if err != nil || len(pubs) == 0 {
		return nil, fmt.Errorf("no search results found — run with phase: \"search\" first")
	}

	progress.Update("Analyzing", fmt.Sprintf("Processing %d results (3 passes each)...", len(pubs)))
	findings := AnalyzeAllWithProgress(ctx, p.model, p.fetcher, pubs, cfg.Intent, rcfg, progress)

	// Chase references
	if len(findings) > 0 {
		refCount := countCitedRefs(findings, rcfg)
		if refCount > 0 {
			progress.Update("Chasing", fmt.Sprintf("Following %d cited references...", refCount))
			chased := ChaseReferences(ctx, p.model, p.dispatcher, p.fetcher, findings, cfg.Intent, rcfg)
			findings = append(findings, chased...)
		}
	}
	cp.SaveFindings(findings)

	primaryCount, chasedCount := countFindings(findings)

	// Build summary
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Analysis complete for:** %s\n\n", cfg.Topic))
	sb.WriteString(fmt.Sprintf("Analyzed **%d findings** (%d primary, %d discovered via references)\n\n", primaryCount+chasedCount, primaryCount, chasedCount))

	sb.WriteString("Top findings by relevance:\n")
	sorted := make([]AnnotatedFinding, len(findings))
	copy(sorted, findings)
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
	for _, f := range findings {
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

func (p *Pipeline) runSynthesize(ctx context.Context, cfg RunConfig, cp *Checkpoint, rcfg DeepResearchConfig, progress *ProgressWriter, outputDir string) (*PhaseResult, error) {
	findings, err := cp.LoadFindings()
	if err != nil || len(findings) == 0 {
		return nil, fmt.Errorf("no findings found — run with phase: \"analyze\" first")
	}

	plan, _ := cp.LoadPlan()
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
	cp.SaveSections(&sections)

	// Compile and write report
	progress.Update("Compiling", "Writing report files...")
	primaryCount, chasedCount := countFindings(findings)
	report := CompileReport(plan, findings, sections)

	if err := WriteReport(outputDir, plan, findings, sections); err != nil {
		return nil, fmt.Errorf("failed to write report: %w", err)
	}

	// Clean up checkpoints
	cp.Cleanup()
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
	}, nil
}

// --- Run All (backward compat) ---

func (p *Pipeline) runAll(ctx context.Context, cfg RunConfig, cp *Checkpoint, rcfg DeepResearchConfig, progress *ProgressWriter, outputDir string) (*PhaseResult, error) {
	// Plan
	planResult, err := p.runPlan(ctx, cfg, cp, progress, outputDir)
	if err != nil {
		return nil, err
	}

	// Search
	searchResult, err := p.runSearch(ctx, cfg, cp, rcfg, progress, outputDir)
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
	_, err = p.runAnalyze(ctx, cfg, cp, rcfg, progress, outputDir)
	if err != nil {
		return nil, err
	}

	// Synthesize
	return p.runSynthesize(ctx, cfg, cp, rcfg, progress, outputDir)
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
