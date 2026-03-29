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
}

// RunResult holds the output of a research run.
type RunResult struct {
	Report               string // the full markdown report (single-file version)
	OutputDir            string // directory where report files were written
	FindingsCount        int
	ChasedCount          int
	SourcesSearched      int
	ContentTokensAvoided int // for telemetry
}

// Run executes the full deep research pipeline with checkpoint support.
func (p *Pipeline) Run(ctx context.Context, cfg RunConfig) (*RunResult, error) {
	depth := cfg.Depth
	if depth == "" {
		depth = "thorough"
	}

	rcfg := DefaultConfig(depth)

	// Determine output directory early for progress writing
	outputDir := cfg.OutputDir
	if outputDir == "" {
		base := cfg.ProjectDir
		if base == "" {
			base, _ = os.Getwd()
		}
		outputDir = filepath.Join(base, "scratch", "research", slugifyTopic(cfg.Topic))
	}

	progress := NewProgressWriter(outputDir)

	// Determine base dir for checkpoints
	baseDir := cfg.ProjectDir
	if baseDir == "" {
		baseDir, _ = os.Getwd()
	}
	cp := NewCheckpoint(baseDir, cfg.Topic, cfg.Intent, depth)

	var totalContentSize int

	// Phase 1: Plan sources
	progress.Update("Planning", "Identifying relevant sources...")
	var plan *ResearchPlan
	if cp.HasPhase("plan.json") {
		plan, _ = cp.LoadPlan()
	}
	if plan == nil {
		var err error
		if len(cfg.Sources) > 0 {
			plan, err = PlanWithOverride(ctx, p.model, cfg.Topic, cfg.Intent, depth, cfg.DateRange, cfg.Sources)
		} else {
			plan, err = PlanSources(ctx, p.model, cfg.Topic, cfg.Intent, depth, cfg.DateRange)
		}
		if err != nil {
			return nil, fmt.Errorf("source planning: %w", err)
		}
		cp.SavePlan(plan)
	}
	progress.Update("Planning", fmt.Sprintf("Selected %d sources", len(plan.Sources)))

	// Phase 2-3: Search all sources
	progress.Update("Searching", fmt.Sprintf("Querying %d sources...", len(plan.Sources)))
	var pubs []Publication
	if cp.HasPhase("search_results.json") {
		pubs, _ = cp.LoadSearchResults()
	}
	if len(pubs) == 0 {
		pubs = p.dispatcher.SearchAllSources(ctx, plan, rcfg.MaxPrimaryResults)
		cp.SaveSearchResults(pubs)
	}
	progress.Update("Searching", fmt.Sprintf("Found %d results", len(pubs)))

	if len(pubs) == 0 {
		return &RunResult{Report: fmt.Sprintf("# Deep Research: %s\n\nNo results found across %d sources.", cfg.Topic, len(plan.Sources))}, nil
	}

	// Track content sizes for token savings
	for _, pub := range pubs {
		totalContentSize += len(pub.Abstract)
	}

	// Phase 4: Analyze findings
	var findings []AnnotatedFinding
	if cp.HasPhase("findings.json") {
		findings, _ = cp.LoadFindings()
	}
	if len(findings) == 0 {
		progress.Update("Analyzing", fmt.Sprintf("Processing %d results (3 passes each)...", len(pubs)))
		findings = AnalyzeAllWithProgress(ctx, p.model, p.fetcher, pubs, cfg.Intent, rcfg, progress)

		// Phase 4b: Chase references
		if len(findings) > 0 {
			refCount := countCitedRefs(findings, rcfg)
			if refCount > 0 {
				progress.Update("Chasing", fmt.Sprintf("Following %d cited references...", refCount))
				chased := ChaseReferences(ctx, p.model, p.dispatcher, p.fetcher, findings, cfg.Intent, rcfg)
				findings = append(findings, chased...)
				progress.Update("Chasing", fmt.Sprintf("Discovered %d additional findings", len(chased)))
			}
		}

		cp.SaveFindings(findings)
	}

	// Count content fetched during analysis
	for _, f := range findings {
		if f.Publication.Abstract != "" {
			totalContentSize += len(f.Publication.Abstract)
		}
	}

	// Phase 5: Synthesis
	progress.Update("Synthesizing", "Generating executive summary...")
	var sections ReportSections
	if cp.HasPhase("sections.json") {
		loaded, _ := cp.LoadSections()
		if loaded != nil {
			sections = *loaded
		}
	}

	if sections.ExecutiveSummary == "" {
		sections.ExecutiveSummary, _ = GenerateExecutiveSummary(ctx, p.model, findings, cfg.Intent)
	}
	progress.Update("Synthesizing", "Generating narrative synthesis...")
	if sections.Synthesis == "" {
		sections.Synthesis, _ = Synthesize(ctx, p.model, findings, cfg.Intent)
	}
	progress.Update("Synthesizing", "Detecting contradictions...")
	if sections.Contradictions == "" {
		sections.Contradictions, _ = DetectContradictions(ctx, p.model, findings)
	}
	progress.Update("Synthesizing", "Analyzing gaps...")
	if sections.GapAnalysis == "" {
		sections.GapAnalysis, _ = AnalyzeGaps(ctx, p.model, findings, cfg.Intent)
	}
	progress.Update("Synthesizing", "Generating reading order and follow-ups...")
	if len(sections.ReadingOrder) == 0 {
		sections.ReadingOrder, _ = RecommendReadingOrder(ctx, p.model, findings, cfg.Intent)
	}
	if len(sections.FollowUpQueries) == 0 && sections.GapAnalysis != "" {
		sections.FollowUpQueries, _ = SuggestFollowUp(ctx, p.model, sections.GapAnalysis, cfg.Intent)
	}

	cp.SaveSections(&sections)

	// Phase 6: Compile report
	progress.Update("Compiling", "Writing report files...")
	primaryCount, chasedCount := countFindings(findings)
	report := CompileReport(plan, findings, sections)

	if err := WriteReport(outputDir, plan, findings, sections); err != nil {
		return nil, fmt.Errorf("failed to write report: %w", err)
	}

	// Always clean up process checkpoints
	cp.Cleanup()

	progress.Done(primaryCount+chasedCount, len(plan.Sources))

	return &RunResult{
		Report:               report,
		OutputDir:            outputDir,
		FindingsCount:        primaryCount + chasedCount,
		ChasedCount:          chasedCount,
		SourcesSearched:      len(plan.Sources),
		ContentTokensAvoided: totalContentSize / 4,
	}, nil
}

// Summary returns a short summary of the result for the MCP response.
func (r *RunResult) Summary(topic string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Deep research complete: %s\n", topic))
	sb.WriteString(fmt.Sprintf("  Sources searched: %d\n", r.SourcesSearched))
	sb.WriteString(fmt.Sprintf("  Findings: %d", r.FindingsCount))
	if r.ChasedCount > 0 {
		sb.WriteString(fmt.Sprintf(" (%d primary, %d discovered via references)", r.FindingsCount-r.ChasedCount, r.ChasedCount))
	}
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("  Report written to: %s\n", r.OutputDir))
	return sb.String()
}

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
