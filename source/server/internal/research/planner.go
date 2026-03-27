package research

import (
	"context"
	"fmt"
	"strings"
)

// PlanSources asks the local model to identify relevant sources and generate search queries.
func PlanSources(ctx context.Context, model ModelCaller, topic, intent, depth, dateRange string) (*ResearchPlan, error) {
	prompt := fmt.Sprintf(`You are a research librarian. Given a topic and research intent, identify the most relevant sources to search and generate tailored search queries for each.

Available sources:
%s

Topic: %s
Intent: %s
Depth: %s
%s

Instructions:
- Choose 3-8 sources most relevant to this topic and intent
- For each source, provide 2-3 search queries tailored to that source's strengths
- Format your response EXACTLY as:

SOURCE: <source name>
REASON: <why this source is relevant>
QUERY: <search query 1>
QUERY: <search query 2>

SOURCE: <source name>
REASON: <why this source is relevant>
QUERY: <search query 1>
QUERY: <search query 2>

Only include sources that are genuinely relevant. Do not include all sources.`, SourceNames(), topic, intent, depth, formatDateRangeInstruction(dateRange))

	resp, err := model.Call(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("source planning failed: %w", err)
	}

	plan := &ResearchPlan{
		Topic:     topic,
		Intent:    intent,
		Depth:     depth,
		DateRange: dateRange,
	}

	plan.Sources = parsePlanResponse(resp)

	// Fallback: if model returned nothing useful, use generic sources
	if len(plan.Sources) == 0 {
		plan.Sources = fallbackSources(topic)
	}

	return plan, nil
}

// PlanWithOverride creates a plan using user-specified sources, generating queries via the model.
func PlanWithOverride(ctx context.Context, model ModelCaller, topic, intent, depth, dateRange string, sourceNames []string) (*ResearchPlan, error) {
	prompt := fmt.Sprintf(`Generate 2-3 search queries for each of the following sources, tailored to this research topic.

Topic: %s
Intent: %s
%s

For each source, format as:
SOURCE: <source name>
QUERY: <search query 1>
QUERY: <search query 2>

Sources to search: %s`, topic, intent, formatDateRangeInstruction(dateRange), strings.Join(sourceNames, ", "))

	resp, err := model.Call(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("query generation failed: %w", err)
	}

	plan := &ResearchPlan{
		Topic:     topic,
		Intent:    intent,
		Depth:     depth,
		DateRange: dateRange,
	}

	plan.Sources = parsePlanResponse(resp)

	// Ensure all requested sources are represented
	for _, name := range sourceNames {
		found := false
		for _, s := range plan.Sources {
			if equalFold(s.Name, name) {
				found = true
				break
			}
		}
		if !found {
			plan.Sources = append(plan.Sources, Source{
				Name:    name,
				Queries: []string{topic},
				Reason:  "User-requested source",
			})
		}
	}

	// Set type and site from registry
	for i := range plan.Sources {
		if entry := FindSource(plan.Sources[i].Name); entry != nil {
			plan.Sources[i].Type = entry.Type
			plan.Sources[i].Site = entry.Site
		} else {
			plan.Sources[i].Type = "web"
		}
	}

	return plan, nil
}

// parsePlanResponse parses the structured model response into Sources.
func parsePlanResponse(resp string) []Source {
	var sources []Source
	var current *Source

	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "SOURCE:") {
			if current != nil && len(current.Queries) > 0 {
				sources = append(sources, *current)
			}
			name := strings.TrimSpace(strings.TrimPrefix(line, "SOURCE:"))
			s := Source{Name: name}
			if entry := FindSource(name); entry != nil {
				s.Type = entry.Type
				s.Site = entry.Site
			} else {
				s.Type = "web"
			}
			current = &s
		} else if strings.HasPrefix(line, "REASON:") && current != nil {
			current.Reason = strings.TrimSpace(strings.TrimPrefix(line, "REASON:"))
		} else if strings.HasPrefix(line, "QUERY:") && current != nil {
			q := strings.TrimSpace(strings.TrimPrefix(line, "QUERY:"))
			if q != "" {
				current.Queries = append(current.Queries, q)
			}
		}
	}

	if current != nil && len(current.Queries) > 0 {
		sources = append(sources, *current)
	}

	return sources
}

// fallbackSources returns a generic set of sources when the model fails.
func fallbackSources(topic string) []Source {
	return []Source{
		{Name: "Google Scholar", Type: "web", Site: "scholar.google.com", Queries: []string{topic}, Reason: "Broad academic search"},
		{Name: "Wikipedia", Type: "web", Site: "wikipedia.org", Queries: []string{topic}, Reason: "Background context"},
	}
}

func formatDateRangeInstruction(dateRange string) string {
	if dateRange == "" {
		return ""
	}
	return fmt.Sprintf("Date range: Only include results from %s. Incorporate date filters into your search queries where possible.", dateRange)
}
