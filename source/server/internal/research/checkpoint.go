package research

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Checkpoint manages saving and loading intermediate research state as JSON.
// These are ephemeral process files that get cleaned up after the run completes.
type Checkpoint struct {
	workDir string
}

// NewCheckpoint creates a checkpoint manager for a research run.
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

func (c *Checkpoint) SavePlan(plan *ResearchPlan) error {
	return c.saveJSON("plan.json", plan)
}

func (c *Checkpoint) LoadPlan() (*ResearchPlan, error) {
	var plan ResearchPlan
	if err := c.loadJSON("plan.json", &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (c *Checkpoint) SaveSearchResults(pubs []Publication) error {
	return c.saveJSON("search_results.json", pubs)
}

func (c *Checkpoint) LoadSearchResults() ([]Publication, error) {
	var pubs []Publication
	if err := c.loadJSON("search_results.json", &pubs); err != nil {
		return nil, err
	}
	return pubs, nil
}

func (c *Checkpoint) SaveFindings(findings []AnnotatedFinding) error {
	return c.saveJSON("findings.json", findings)
}

func (c *Checkpoint) LoadFindings() ([]AnnotatedFinding, error) {
	var findings []AnnotatedFinding
	if err := c.loadJSON("findings.json", &findings); err != nil {
		return nil, err
	}
	return findings, nil
}

func (c *Checkpoint) SaveSections(sections *ReportSections) error {
	return c.saveJSON("sections.json", sections)
}

func (c *Checkpoint) LoadSections() (*ReportSections, error) {
	var sections ReportSections
	if err := c.loadJSON("sections.json", &sections); err != nil {
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

func (c *Checkpoint) saveJSON(filename string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(c.workDir, filename), data, 0644)
}

func (c *Checkpoint) loadJSON(filename string, v interface{}) error {
	data, err := os.ReadFile(filepath.Join(c.workDir, filename))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
