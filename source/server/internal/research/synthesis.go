package research

import (
	"context"
	"fmt"
	"strings"

	"cercano/source/server/internal/modelbudget"
	"cercano/source/server/internal/tokens"
)

// GenerateExecutiveSummary produces a 3-4 sentence TL;DR.
func GenerateExecutiveSummary(ctx context.Context, model ModelCaller, findings []AnnotatedFinding, intent string) (string, error) {
	format := func(summaries string) string {
		return fmt.Sprintf(`Write a 3-4 sentence executive summary of these research findings for someone whose intent is: %s

Findings:
%s

Write only the summary, no headers or labels.`, intent, summaries)
	}
	summaries, err := findingSummariesForModel(ctx, model, findings, 10, format(""))
	if err != nil {
		return "", err
	}
	return model.Call(ctx, format(summaries))
}

// Synthesize generates a 2-3 paragraph narrative tying findings together.
func Synthesize(ctx context.Context, model ModelCaller, findings []AnnotatedFinding, intent string) (string, error) {
	format := func(summaries string) string {
		return fmt.Sprintf(`Write a 2-3 paragraph narrative synthesis of these research findings. Connect the key themes, highlight how they relate to each other, and explain their collective significance for someone whose intent is: %s

Findings:
%s

Write flowing paragraphs, not bullet points.`, intent, summaries)
	}
	summaries, err := findingSummariesForModel(ctx, model, findings, 15, format(""))
	if err != nil {
		return "", err
	}
	return model.Call(ctx, format(summaries))
}

// DetectContradictions identifies conflicting findings.
func DetectContradictions(ctx context.Context, model ModelCaller, findings []AnnotatedFinding) (string, error) {
	if len(findings) < 2 {
		return "", nil
	}

	format := func(summaries string) string {
		return fmt.Sprintf(`Review these research findings and identify any contradictions or contested claims — cases where two or more findings reach opposite or conflicting conclusions.

Findings:
%s

If you find contradictions, describe each one briefly (which findings conflict, what the disagreement is about). If there are no contradictions, respond with exactly: NONE`, summaries)
	}
	summaries, err := findingSummariesForModel(ctx, model, findings, 15, format(""))
	if err != nil {
		return "", err
	}
	resp, err := model.Call(ctx, format(summaries))
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(resp) == "NONE" {
		return "", nil
	}
	return resp, nil
}

// AnalyzeGaps identifies what the research didn't find.
func AnalyzeGaps(ctx context.Context, model ModelCaller, findings []AnnotatedFinding, intent string) (string, error) {
	format := func(summaries string) string {
		return fmt.Sprintf(`Given these research findings and the user's intent, identify what the research did NOT find — gaps in evidence, missing perspectives, underrepresented areas, or absent data types.

User's intent: %s

Findings:
%s

List each gap as a bullet point. Focus on gaps that matter for the user's intent.`, intent, summaries)
	}
	summaries, err := findingSummariesForModel(ctx, model, findings, 15, format(""))
	if err != nil {
		return "", err
	}
	return model.Call(ctx, format(summaries))
}

// SuggestFollowUp generates follow-up research queries based on gaps.
func SuggestFollowUp(ctx context.Context, model ModelCaller, gaps, intent string) ([]string, error) {
	prompt := fmt.Sprintf(`Based on these research gaps and the user's intent, suggest 3-5 specific follow-up research queries they should investigate next.

User's intent: %s

Gaps identified:
%s

Format each suggestion as a numbered list:
1. "<specific search query>" — <brief explanation of why>
2. "<specific search query>" — <brief explanation of why>`, intent, gaps)

	resp, err := model.Call(ctx, prompt)
	if err != nil {
		return nil, err
	}

	var queries []string
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Strip leading number + punctuation
		for i, c := range line {
			if c >= '0' && c <= '9' || c == '.' || c == ')' || c == ' ' {
				continue
			}
			line = line[i:]
			break
		}
		line = strings.TrimSpace(line)
		if line != "" {
			queries = append(queries, line)
		}
	}
	return queries, nil
}

// RecommendReadingOrder suggests an ordered reading path.
func RecommendReadingOrder(ctx context.Context, model ModelCaller, findings []AnnotatedFinding, intent string) ([]string, error) {
	format := func(summaries string) string {
		return fmt.Sprintf(`Suggest an optimal reading order for these research findings, given the user's intent. Start with foundational context, then state of the art, then specific findings relevant to their goal.

User's intent: %s

Findings:
%s

Format as a numbered list:
1. "<Title>" — <brief reason to read this first/next>`, intent, summaries)
	}
	summaries, err := findingSummariesForModel(ctx, model, findings, 15, format(""))
	if err != nil {
		return nil, err
	}
	resp, err := model.Call(ctx, format(summaries))
	if err != nil {
		return nil, err
	}

	var order []string
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && (len(line) > 2 && line[0] >= '0' && line[0] <= '9') {
			order = append(order, line)
		}
	}
	return order, nil
}

func findingSummariesForModel(ctx context.Context, model ModelCaller, findings []AnnotatedFinding, max int, emptyPrompt string) (string, error) {
	budgeter, ok := model.(modelbudget.Budgeter)
	if !ok {
		return buildFindingSummaries(findings, max), nil
	}
	budget, err := budgeter.Budget(ctx, modelbudget.DefaultOutputReserve)
	if err != nil {
		return "", fmt.Errorf("deep research synthesis budget: %w", err)
	}
	summaryBudget := budget.InputTokens - tokens.Estimate(emptyPrompt) - 16
	if summaryBudget < 128 {
		return "", fmt.Errorf("deep research synthesis budget too small after prompt overhead: input_budget=%d prompt_overhead=%d summary_budget=%d provider=%s model=%s context_window=%d", budget.InputTokens, tokens.Estimate(emptyPrompt), summaryBudget, budget.Target.Provider, budget.Target.Model, budget.Target.ContextWindow)
	}
	summaries, included, _ := buildFindingSummariesBudgeted(findings, max, summaryBudget)
	if included == 0 {
		return "", fmt.Errorf("deep research synthesis budget could not fit any finding summaries: summary_budget=%d provider=%s model=%s context_window=%d", summaryBudget, budget.Target.Provider, budget.Target.Model, budget.Target.ContextWindow)
	}
	return summaries, nil
}

func buildFindingSummariesBudgeted(findings []AnnotatedFinding, max, summaryBudget int) (summaries string, included int, trimmed int) {
	var sb strings.Builder
	limit := len(findings)
	if limit > max {
		limit = max
	}
	for i, f := range findings[:limit] {
		prefix := fmt.Sprintf("%d. [%s] %s (relevance: %d/5, impact: %s)\n   ", i+1, f.Publication.Source, f.Publication.Title, f.RelevanceScore, f.ImpactRating)
		suffix := "\n\n"
		remaining := summaryBudget - tokens.Estimate(sb.String()) - tokens.Estimate(prefix) - tokens.Estimate(suffix)
		if remaining <= 0 {
			trimmed++
			continue
		}
		summary := strings.TrimSpace(f.Summary)
		if summary == "" {
			summary = "(No summary available.)"
		}
		candidate := summary
		if tokens.Estimate(candidate) > remaining {
			trimmed++
			candidate = truncateTextToTokenBudget(candidate, remaining, " [...truncated to fit local model context]")
		}
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		fmt.Fprintf(&sb, "%s%s%s", prefix, candidate, suffix)
		included++
	}
	return sb.String(), included, trimmed
}

func truncateTextToTokenBudget(text string, budget int, marker string) string {
	if budget <= 0 {
		return ""
	}
	if tokens.Estimate(text) <= budget {
		return text
	}
	usable := budget - tokens.Estimate(marker)
	if usable <= 0 {
		return ""
	}
	chars := usable * 4
	if chars > len(text) {
		chars = len(text)
	}
	candidate := strings.TrimSpace(text[:chars]) + marker
	for tokens.Estimate(candidate) > budget && chars > 0 {
		chars = chars * 9 / 10
		candidate = strings.TrimSpace(text[:chars]) + marker
	}
	if tokens.Estimate(candidate) > budget {
		return ""
	}
	return candidate
}

func buildFindingSummaries(findings []AnnotatedFinding, max int) string {
	var sb strings.Builder
	limit := len(findings)
	if limit > max {
		limit = max
	}
	for i, f := range findings[:limit] {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s (relevance: %d/5, impact: %s)\n   %s\n\n",
			i+1, f.Publication.Source, f.Publication.Title, f.RelevanceScore, f.ImpactRating, f.Summary))
	}
	return sb.String()
}
