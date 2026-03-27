package research

import (
	"fmt"
	"sort"
	"strings"
)

// CompileReport builds the final markdown research document.
func CompileReport(plan *ResearchPlan, findings []AnnotatedFinding, sections ReportSections) string {
	var out strings.Builder

	// Sort findings: primary first (by relevance desc, then date), then chased
	primaryFindings, chasedFindings := splitFindings(findings)
	sortFindings(primaryFindings)
	sortFindings(chasedFindings)

	// Title
	out.WriteString(fmt.Sprintf("# Deep Research: %s\n\n", plan.Topic))

	// Intent
	out.WriteString("## Research Intent\n")
	out.WriteString(plan.Intent + "\n\n")

	// Date range if specified
	if plan.DateRange != "" {
		out.WriteString(fmt.Sprintf("**Date range:** %s\n\n", plan.DateRange))
	}

	// Executive Summary
	if sections.ExecutiveSummary != "" {
		out.WriteString("## Executive Summary\n")
		out.WriteString(sections.ExecutiveSummary + "\n\n")
	}

	// Source Plan
	out.WriteString("## Source Plan\n")
	out.WriteString("The following sources were searched:\n")
	for i, src := range plan.Sources {
		out.WriteString(fmt.Sprintf("%d. **%s** — %s\n", i+1, src.Name, src.Reason))
	}
	out.WriteString(fmt.Sprintf("\nSearched %d sources, found %d relevant publications", len(plan.Sources), len(findings)))
	if len(chasedFindings) > 0 {
		out.WriteString(fmt.Sprintf(" (%d primary, %d discovered via references)", len(primaryFindings), len(chasedFindings)))
	}
	out.WriteString(".\n\n")

	// Primary Findings
	out.WriteString("---\n\n## Findings\n\n")
	for i, f := range primaryFindings {
		out.WriteString(formatFinding(i+1, f))
	}

	// Chased Findings
	if len(chasedFindings) > 0 {
		out.WriteString("---\n\n## Discovered References\n\n")
		out.WriteString("*These works were cited by primary findings and identified as relevant to your intent.*\n\n")
		for i, f := range chasedFindings {
			out.WriteString(formatFinding(i+1, f))
		}
	}

	// Synthesis
	if sections.Synthesis != "" {
		out.WriteString("---\n\n## Synthesis\n")
		out.WriteString(sections.Synthesis + "\n\n")
	}

	// Contradictions
	if sections.Contradictions != "" {
		out.WriteString("## Contradictions & Open Debates\n")
		out.WriteString(sections.Contradictions + "\n\n")
	}

	// Gap Analysis
	if sections.GapAnalysis != "" {
		out.WriteString("## Gap Analysis\n")
		out.WriteString(sections.GapAnalysis + "\n\n")
	}

	// Reading Order
	if len(sections.ReadingOrder) > 0 {
		out.WriteString("## Recommended Reading Order\n")
		for _, item := range sections.ReadingOrder {
			out.WriteString(item + "\n")
		}
		out.WriteString("\n")
	}

	// Follow-up Queries
	if len(sections.FollowUpQueries) > 0 {
		out.WriteString("## Suggested Follow-Up Research\n")
		for _, q := range sections.FollowUpQueries {
			out.WriteString(q + "\n")
		}
		out.WriteString("\n")
	}

	return out.String()
}

func formatFinding(num int, f AnnotatedFinding) string {
	var out strings.Builder

	stars := strings.Repeat("\u2b50", f.RelevanceScore)
	out.WriteString(fmt.Sprintf("### %d. %s %s\n", num, f.Publication.Title, stars))
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
		out.WriteString(fmt.Sprintf("**Summary:** %s\n\n", f.Summary))
	}
	if f.WhyItMatters != "" {
		out.WriteString(fmt.Sprintf("**Why this matters to your research:** %s\n\n", f.WhyItMatters))
	}
	if f.HowToUse != "" {
		out.WriteString(fmt.Sprintf("**How to use:** %s\n\n", f.HowToUse))
	}
	out.WriteString(fmt.Sprintf("**Potential impact:** %s\n\n", f.ImpactRating))
	out.WriteString("---\n\n")

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
		// Secondary sort: by date descending (newer first)
		return findings[i].Publication.Date > findings[j].Publication.Date
	})
}
