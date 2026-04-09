package research

import (
	"context"
	"fmt"
	"strings"
)

// PlanSources asks the local model to identify relevant sources and generate search queries.
// It first decomposes the topic into sub-questions, then generates targeted queries for each.
func PlanSources(ctx context.Context, model ModelCaller, topic, intent, depth, dateRange string) (*ResearchPlan, error) {
	prompt := fmt.Sprintf(`You are a research librarian. Given a topic and research intent, plan a systematic search strategy.

STEP 1: DECOMPOSE THE TOPIC
First, break the research topic into 3-5 specific sub-questions that need answering. Think about how a human researcher would approach this — what are the distinct facets or entities to investigate individually?

For example, if the topic is "AI coding tool plugin systems", the sub-questions might be:
- What plugin/extension system does Claude Code use?
- What plugin/extension system does GitHub Copilot use?
- What plugin/extension system does Cursor use?
- What is MCP and how does it relate to these plugin systems?

STEP 2: GENERATE TARGETED QUERIES
For each sub-question, choose the best source and generate 1-2 highly specific search queries.

Available sources:
%s

Topic: %s
Intent: %s
Depth: %s
%s

Instructions:
- Choose 3-5 sources most relevant to this SPECIFIC topic and intent
- Generate queries that target INDIVIDUAL sub-questions, not the whole topic at once
- Each query should be specific enough to return directly relevant results

BAD queries (too broad, will return irrelevant results):
QUERY: AI coding tool plugins
QUERY: developer tools extension systems
QUERY: MCP protocol overview

GOOD queries (targeted at specific sub-questions):
QUERY: "Claude Code" plugin marketplace how to build extension
QUERY: "GitHub Copilot" extension API third party tools
QUERY: "Gemini CLI" extension developer guide 2025
QUERY: Model Context Protocol vs native IDE extensions comparison

Format your response EXACTLY as:

SUB_QUESTION: <the specific sub-question being addressed>

SOURCE: <source name>
REASON: <why this source is relevant to this sub-question>
QUERY: <specific search query 1>
QUERY: <specific search query 2>

SUB_QUESTION: <next sub-question>

SOURCE: <source name>
REASON: <why this source is relevant>
QUERY: <specific search query 1>
QUERY: <specific search query 2>

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
	prompt := fmt.Sprintf(`Generate targeted search queries for each of the following sources.

First, decompose the research topic into 3-5 specific sub-questions that need answering. Then for each source, generate queries that target these sub-questions — not the whole topic at once.

Topic: %s
Intent: %s
%s
Sources to search: %s

For each source, format as:
SOURCE: <source name>
QUERY: <search query targeting a specific sub-question>
QUERY: <search query targeting another sub-question>`, topic, intent, formatDateRangeInstruction(dateRange), strings.Join(sourceNames, ", "))

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

// PlanExpansion asks the model to select NEW sources that complement existing ones.
// It returns only the new sources to add (not the existing ones).
func PlanExpansion(ctx context.Context, model ModelCaller, topic, intent, depth, dateRange string, existingSources []Source, maxSources int) ([]Source, error) {
	var existingList strings.Builder
	for i, s := range existingSources {
		existingList.WriteString(fmt.Sprintf("%d. %s — %s\n", i+1, s.Name, s.Reason))
		for _, q := range s.Queries {
			existingList.WriteString(fmt.Sprintf("   QUERY: %s\n", q))
		}
	}

	prompt := fmt.Sprintf(`You are a research librarian expanding a research plan. The user is deepening their research and needs COMPLEMENTARY sources that were NOT already searched.

Available sources:
%s

Topic: %s
Intent: %s
Depth: %s
%s

Sources ALREADY searched (do NOT repeat these):
%s

Instructions:
- Choose 2-%d NEW sources that complement the existing research
- Pick sources that cover DIFFERENT angles, methodologies, or perspectives
- For each source, provide 2-3 HIGHLY SPECIFIC search queries
- Do NOT repeat any source already listed above

Format your response EXACTLY as:

SOURCE: <source name>
REASON: <why this source adds new perspective>
QUERY: <specific search query 1>
QUERY: <specific search query 2>`, SourceNames(), topic, intent, depth, formatDateRangeInstruction(dateRange), existingList.String(), maxSources)

	resp, err := model.Call(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("plan expansion failed: %w", err)
	}

	candidates := parsePlanResponse(resp)

	// Filter out any sources that match existing ones
	existingNames := make(map[string]bool, len(existingSources))
	for _, s := range existingSources {
		existingNames[strings.ToLower(s.Name)] = true
	}

	var newSources []Source
	for _, c := range candidates {
		if !existingNames[strings.ToLower(c.Name)] {
			newSources = append(newSources, c)
		}
	}

	return newSources, nil
}

func formatDateRangeInstruction(dateRange string) string {
	if dateRange == "" {
		return ""
	}
	return fmt.Sprintf("Date range: Only include results from %s. Incorporate date filters into your search queries where possible.", dateRange)
}
