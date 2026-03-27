package research

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// WriteReport writes the research to a directory structure with multiple files.
func WriteReport(outputDir string, plan *ResearchPlan, findings []AnnotatedFinding, sections ReportSections) error {
	if err := os.MkdirAll(filepath.Join(outputDir, "findings"), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(outputDir, "references"), 0755); err != nil {
		return err
	}

	primaryFindings, chasedFindings := splitFindings(findings)
	sortFindings(primaryFindings)
	sortFindings(chasedFindings)

	// Write individual finding files
	for i, f := range primaryFindings {
		filename := fmt.Sprintf("%02d_%s.md", i+1, slugify(f.Publication.Title))
		path := filepath.Join(outputDir, "findings", filename)
		os.WriteFile(path, []byte(formatFindingFull(i+1, f)), 0644)
	}

	for i, f := range chasedFindings {
		filename := fmt.Sprintf("%02d_%s.md", i+1, slugify(f.Publication.Title))
		path := filepath.Join(outputDir, "references", filename)
		os.WriteFile(path, []byte(formatFindingFull(i+1, f)), 0644)
	}

	// Write source_plan.md
	os.WriteFile(filepath.Join(outputDir, "source_plan.md"), []byte(formatSourcePlan(plan, len(primaryFindings), len(chasedFindings))), 0644)

	// Write synthesis.md
	os.WriteFile(filepath.Join(outputDir, "synthesis.md"), []byte(formatSynthesis(sections)), 0644)

	// Write README.md (index)
	os.WriteFile(filepath.Join(outputDir, "README.md"), []byte(formatReadme(plan, primaryFindings, chasedFindings, sections)), 0644)

	return nil
}

// CompileReport builds a single markdown document (used when no output_dir is set).
func CompileReport(plan *ResearchPlan, findings []AnnotatedFinding, sections ReportSections) string {
	var out strings.Builder

	primaryFindings, chasedFindings := splitFindings(findings)
	sortFindings(primaryFindings)
	sortFindings(chasedFindings)

	out.WriteString(fmt.Sprintf("# Deep Research: %s\n\n", plan.Topic))
	out.WriteString("## Research Intent\n")
	out.WriteString(plan.Intent + "\n\n")

	if plan.DateRange != "" {
		out.WriteString(fmt.Sprintf("**Date range:** %s\n\n", plan.DateRange))
	}

	if sections.ExecutiveSummary != "" {
		out.WriteString("## Executive Summary\n")
		out.WriteString(sections.ExecutiveSummary + "\n\n")
	}

	out.WriteString("## Source Plan\n")
	out.WriteString("The following sources were searched:\n")
	for i, src := range plan.Sources {
		out.WriteString(fmt.Sprintf("%d. **%s** — %s\n", i+1, src.Name, src.Reason))
	}
	out.WriteString(fmt.Sprintf("\nSearched %d sources, found %d relevant publications", len(plan.Sources), len(primaryFindings)+len(chasedFindings)))
	if len(chasedFindings) > 0 {
		out.WriteString(fmt.Sprintf(" (%d primary, %d discovered via references)", len(primaryFindings), len(chasedFindings)))
	}
	out.WriteString(".\n\n---\n\n## Findings\n\n")

	for i, f := range primaryFindings {
		out.WriteString(formatFindingFull(i+1, f))
	}

	if len(chasedFindings) > 0 {
		out.WriteString("---\n\n## Discovered References\n\n")
		out.WriteString("*These works were cited by primary findings and identified as relevant to your intent.*\n\n")
		for i, f := range chasedFindings {
			out.WriteString(formatFindingFull(i+1, f))
		}
	}

	out.WriteString(formatSynthesis(sections))

	return out.String()
}

func formatReadme(plan *ResearchPlan, primary, chased []AnnotatedFinding, sections ReportSections) string {
	var out strings.Builder

	out.WriteString(fmt.Sprintf("# Deep Research: %s\n\n", plan.Topic))
	out.WriteString("## Research Intent\n")
	out.WriteString(plan.Intent + "\n\n")

	if plan.DateRange != "" {
		out.WriteString(fmt.Sprintf("**Date range:** %s\n\n", plan.DateRange))
	}

	if sections.ExecutiveSummary != "" {
		out.WriteString("## Executive Summary\n")
		out.WriteString(sections.ExecutiveSummary + "\n\n")
	}

	out.WriteString(fmt.Sprintf("**Sources searched:** %d | **Findings:** %d primary, %d references\n\n", len(plan.Sources), len(primary), len(chased)))

	// Table of contents
	out.WriteString("## Findings\n\n")
	out.WriteString("| # | Title | Source | Relevance | Impact |\n")
	out.WriteString("|---|-------|--------|-----------|--------|\n")
	for i, f := range primary {
		stars := strings.Repeat("\u2b50", f.RelevanceScore)
		out.WriteString(fmt.Sprintf("| %d | [%s](findings/%02d_%s.md) | %s | %s | %s |\n",
			i+1, f.Publication.Title, i+1, slugify(f.Publication.Title), f.Publication.Source, stars, f.ImpactRating))
	}
	out.WriteString("\n")

	if len(chased) > 0 {
		out.WriteString("## Discovered References\n\n")
		out.WriteString("| # | Title | Source | Relevance | Discovered Via |\n")
		out.WriteString("|---|-------|--------|-----------|----------------|\n")
		for i, f := range chased {
			stars := strings.Repeat("\u2b50", f.RelevanceScore)
			out.WriteString(fmt.Sprintf("| %d | [%s](references/%02d_%s.md) | %s | %s | %s |\n",
				i+1, f.Publication.Title, i+1, slugify(f.Publication.Title), f.Publication.Source, stars, f.DiscoveredVia))
		}
		out.WriteString("\n")
	}

	out.WriteString("## Other Sections\n\n")
	out.WriteString("- [Source Plan](source_plan.md)\n")
	out.WriteString("- [Synthesis, Gaps & Follow-Up](synthesis.md)\n")

	return out.String()
}

func formatSourcePlan(plan *ResearchPlan, primaryCount, chasedCount int) string {
	var out strings.Builder
	out.WriteString(fmt.Sprintf("# Source Plan: %s\n\n", plan.Topic))
	for i, src := range plan.Sources {
		out.WriteString(fmt.Sprintf("## %d. %s\n", i+1, src.Name))
		out.WriteString(fmt.Sprintf("**Why:** %s\n\n", src.Reason))
		out.WriteString("**Queries:**\n")
		for _, q := range src.Queries {
			out.WriteString(fmt.Sprintf("- %s\n", q))
		}
		out.WriteString("\n")
	}
	out.WriteString(fmt.Sprintf("---\n\n**Total:** %d primary findings, %d discovered references\n", primaryCount, chasedCount))
	return out.String()
}

func formatSynthesis(sections ReportSections) string {
	var out strings.Builder

	if sections.Synthesis != "" {
		out.WriteString("## Synthesis\n")
		out.WriteString(sections.Synthesis + "\n\n")
	}
	if sections.Contradictions != "" {
		out.WriteString("## Contradictions & Open Debates\n")
		out.WriteString(sections.Contradictions + "\n\n")
	}
	if sections.GapAnalysis != "" {
		out.WriteString("## Gap Analysis\n")
		out.WriteString(sections.GapAnalysis + "\n\n")
	}
	if len(sections.ReadingOrder) > 0 {
		out.WriteString("## Recommended Reading Order\n")
		for _, item := range sections.ReadingOrder {
			out.WriteString(item + "\n")
		}
		out.WriteString("\n")
	}
	if len(sections.FollowUpQueries) > 0 {
		out.WriteString("## Suggested Follow-Up Research\n")
		for _, q := range sections.FollowUpQueries {
			out.WriteString(q + "\n")
		}
		out.WriteString("\n")
	}
	return out.String()
}

func formatFindingFull(num int, f AnnotatedFinding) string {
	var out strings.Builder

	stars := strings.Repeat("\u2b50", f.RelevanceScore)
	out.WriteString(fmt.Sprintf("# %s %s\n\n", f.Publication.Title, stars))
	out.WriteString(fmt.Sprintf("**Source:** %s", f.Publication.Source))
	if f.Publication.Date != "" {
		out.WriteString(fmt.Sprintf(" | **Published:** %s", f.Publication.Date))
	}
	if f.Publication.Authors != "" {
		out.WriteString(fmt.Sprintf(" | **Authors:** %s", f.Publication.Authors))
	}
	out.WriteString("\n")
	if f.Publication.URL != "" {
		out.WriteString(fmt.Sprintf("**URL:** %s\n", f.Publication.URL))
	}
	if f.DiscoveredVia != "" {
		out.WriteString(fmt.Sprintf("*Discovered via: %s*\n", f.DiscoveredVia))
	}
	out.WriteString("\n")

	if f.Summary != "" {
		out.WriteString(fmt.Sprintf("## Summary\n%s\n\n", f.Summary))
	}

	if len(f.KeyFindings) > 0 {
		out.WriteString("## Key Findings\n")
		for _, kf := range f.KeyFindings {
			out.WriteString(fmt.Sprintf("- %s\n", kf))
		}
		out.WriteString("\n")
	}

	if f.WhyItMatters != "" {
		out.WriteString(fmt.Sprintf("## Why This Matters\n%s\n\n", f.WhyItMatters))
	}
	if f.HowToUse != "" {
		out.WriteString(fmt.Sprintf("## How to Use\n%s\n\n", f.HowToUse))
	}
	out.WriteString(fmt.Sprintf("**Relevance:** %d/5 | **Impact:** %s\n\n---\n\n", f.RelevanceScore, f.ImpactRating))

	return out.String()
}

func splitFindings(findings []AnnotatedFinding) (primary, chased []AnnotatedFinding) {
	for _, f := range findings {
		if f.DiscoveredVia != "" {
			chased = append(chased, f)
		} else {
			primary = append(primary, f)
		}
	}
	return
}

func sortFindings(findings []AnnotatedFinding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].RelevanceScore != findings[j].RelevanceScore {
			return findings[i].RelevanceScore > findings[j].RelevanceScore
		}
		return findings[i].Publication.Date > findings[j].Publication.Date
	})
}

var nonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(title string) string {
	s := strings.ToLower(title)
	s = nonAlphaNum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = s[:60]
		// Don't end mid-word
		if i := strings.LastIndex(s, "-"); i > 30 {
			s = s[:i]
		}
	}
	return s
}
