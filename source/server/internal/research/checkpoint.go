package research

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Checkpoint manages saving and loading intermediate research state.
// Checkpoints are stored as markdown files with embedded JSON in code blocks
// for human readability while maintaining parseability.
type Checkpoint struct {
	workDir string
}

// NewCheckpoint creates a checkpoint manager for a research run.
// The work directory is derived from a hash of the topic + intent + depth.
func NewCheckpoint(baseDir, topic, intent, depth string) *Checkpoint {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(topic+"|"+intent+"|"+depth)))[:12]
	workDir := filepath.Join(baseDir, ".cercano", "research", hash)
	os.MkdirAll(workDir, 0755)
	return &Checkpoint{workDir: workDir}
}

// WorkDir returns the checkpoint directory path.
func (c *Checkpoint) WorkDir() string {
	return c.workDir
}

// SavePlan saves the research plan.
func (c *Checkpoint) SavePlan(plan *ResearchPlan) error {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Research Plan: %s\n\n", plan.Topic))
	sb.WriteString(fmt.Sprintf("**Intent:** %s\n\n", plan.Intent))
	sb.WriteString(fmt.Sprintf("**Depth:** %s\n\n", plan.Depth))
	if plan.DateRange != "" {
		sb.WriteString(fmt.Sprintf("**Date Range:** %s\n\n", plan.DateRange))
	}
	sb.WriteString("## Sources\n\n")
	for i, src := range plan.Sources {
		sb.WriteString(fmt.Sprintf("### %d. %s\n", i+1, src.Name))
		sb.WriteString(fmt.Sprintf("- **Type:** %s\n", src.Type))
		if src.Site != "" {
			sb.WriteString(fmt.Sprintf("- **Site:** %s\n", src.Site))
		}
		sb.WriteString(fmt.Sprintf("- **Reason:** %s\n", src.Reason))
		sb.WriteString("- **Queries:**\n")
		for _, q := range src.Queries {
			sb.WriteString(fmt.Sprintf("  - %s\n", q))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("<!--json\n")
	data, _ := json.Marshal(plan)
	sb.WriteString(string(data))
	sb.WriteString("\n-->\n")
	return os.WriteFile(filepath.Join(c.workDir, "plan.md"), []byte(sb.String()), 0644)
}

// LoadPlan loads a previously saved plan.
func (c *Checkpoint) LoadPlan() (*ResearchPlan, error) {
	var plan ResearchPlan
	if err := c.loadEmbeddedJSON("plan.md", &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// SaveSearchResults saves the search results.
func (c *Checkpoint) SaveSearchResults(pubs []Publication) error {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Search Results (%d found)\n\n", len(pubs)))
	for i, pub := range pubs {
		sb.WriteString(fmt.Sprintf("## %d. %s\n", i+1, pub.Title))
		sb.WriteString(fmt.Sprintf("- **Source:** %s\n", pub.Source))
		if pub.URL != "" {
			sb.WriteString(fmt.Sprintf("- **URL:** %s\n", pub.URL))
		}
		if pub.Authors != "" {
			sb.WriteString(fmt.Sprintf("- **Authors:** %s\n", pub.Authors))
		}
		if pub.Date != "" {
			sb.WriteString(fmt.Sprintf("- **Date:** %s\n", pub.Date))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("<!--json\n")
	data, _ := json.Marshal(pubs)
	sb.WriteString(string(data))
	sb.WriteString("\n-->\n")
	return os.WriteFile(filepath.Join(c.workDir, "search_results.md"), []byte(sb.String()), 0644)
}

// LoadSearchResults loads previously saved search results.
func (c *Checkpoint) LoadSearchResults() ([]Publication, error) {
	var pubs []Publication
	if err := c.loadEmbeddedJSON("search_results.md", &pubs); err != nil {
		return nil, err
	}
	return pubs, nil
}

// SaveFindings saves analyzed findings.
func (c *Checkpoint) SaveFindings(findings []AnnotatedFinding) error {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Analyzed Findings (%d total)\n\n", len(findings)))
	for i, f := range findings {
		stars := strings.Repeat("\u2b50", f.RelevanceScore)
		sb.WriteString(fmt.Sprintf("## %d. %s %s\n", i+1, f.Publication.Title, stars))
		sb.WriteString(fmt.Sprintf("- **Source:** %s | **Impact:** %s\n", f.Publication.Source, f.ImpactRating))
		if f.DiscoveredVia != "" {
			sb.WriteString(fmt.Sprintf("- **Discovered via:** %s\n", f.DiscoveredVia))
		}
		sb.WriteString(fmt.Sprintf("- **Summary:** %s\n", f.Summary))
		sb.WriteString("\n")
	}
	sb.WriteString("<!--json\n")
	data, _ := json.Marshal(findings)
	sb.WriteString(string(data))
	sb.WriteString("\n-->\n")
	return os.WriteFile(filepath.Join(c.workDir, "findings.md"), []byte(sb.String()), 0644)
}

// LoadFindings loads previously saved findings.
func (c *Checkpoint) LoadFindings() ([]AnnotatedFinding, error) {
	var findings []AnnotatedFinding
	if err := c.loadEmbeddedJSON("findings.md", &findings); err != nil {
		return nil, err
	}
	return findings, nil
}

// SaveSections saves the report sections.
func (c *Checkpoint) SaveSections(sections *ReportSections) error {
	var sb strings.Builder
	sb.WriteString("# Report Sections\n\n")
	if sections.ExecutiveSummary != "" {
		sb.WriteString("## Executive Summary\n")
		sb.WriteString(sections.ExecutiveSummary + "\n\n")
	}
	if sections.Synthesis != "" {
		sb.WriteString("## Synthesis\n")
		sb.WriteString(sections.Synthesis + "\n\n")
	}
	if sections.Contradictions != "" {
		sb.WriteString("## Contradictions\n")
		sb.WriteString(sections.Contradictions + "\n\n")
	}
	if sections.GapAnalysis != "" {
		sb.WriteString("## Gap Analysis\n")
		sb.WriteString(sections.GapAnalysis + "\n\n")
	}
	if len(sections.ReadingOrder) > 0 {
		sb.WriteString("## Reading Order\n")
		for _, r := range sections.ReadingOrder {
			sb.WriteString(r + "\n")
		}
		sb.WriteString("\n")
	}
	if len(sections.FollowUpQueries) > 0 {
		sb.WriteString("## Follow-Up Queries\n")
		for _, q := range sections.FollowUpQueries {
			sb.WriteString(q + "\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("<!--json\n")
	data, _ := json.Marshal(sections)
	sb.WriteString(string(data))
	sb.WriteString("\n-->\n")
	return os.WriteFile(filepath.Join(c.workDir, "sections.md"), []byte(sb.String()), 0644)
}

// LoadSections loads previously saved sections.
func (c *Checkpoint) LoadSections() (*ReportSections, error) {
	var sections ReportSections
	if err := c.loadEmbeddedJSON("sections.md", &sections); err != nil {
		return nil, err
	}
	return &sections, nil
}

// HasPhase returns true if a checkpoint file for the given phase exists.
func (c *Checkpoint) HasPhase(filename string) bool {
	_, err := os.Stat(filepath.Join(c.workDir, filename))
	return err == nil
}

// Cleanup removes the work directory.
func (c *Checkpoint) Cleanup() {
	os.RemoveAll(c.workDir)
}

// loadEmbeddedJSON extracts JSON from an HTML comment in a markdown file.
func (c *Checkpoint) loadEmbeddedJSON(filename string, v interface{}) error {
	data, err := os.ReadFile(filepath.Join(c.workDir, filename))
	if err != nil {
		return err
	}
	content := string(data)
	start := strings.Index(content, "<!--json\n")
	if start < 0 {
		return fmt.Errorf("no embedded JSON found in %s", filename)
	}
	start += len("<!--json\n")
	end := strings.Index(content[start:], "\n-->")
	if end < 0 {
		return fmt.Errorf("unterminated JSON block in %s", filename)
	}
	return json.Unmarshal([]byte(content[start:start+end]), v)
}
