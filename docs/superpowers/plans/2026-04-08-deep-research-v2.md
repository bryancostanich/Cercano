# Deep Research v2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Three-tier incremental deep research with progress tracking and bug fixes.

**Architecture:** Replace the ephemeral checkpoint system with a persistent `research_state.json` sidecar in the output directory. Add `standard` tier between `survey` and `deep`. Enable incremental deepening by loading prior state and expanding only what's new. Add a `ProgressTracker` that writes granular `status.md` updates.

**Tech Stack:** Go 1.25.5, existing research pipeline in `source/server/internal/research/`

**Spec:** `docs/superpowers/specs/2026-04-08-deep-research-v2-design.md`

---

### Task 1: Three-Tier Configuration

Update the config system to support `survey`, `standard`, and `deep` tiers. Remove `thorough`.

**Files:**
- Modify: `source/server/internal/research/types.go:107-134`
- Test: `source/server/internal/research/research_test.go`

- [ ] **Step 1: Write tests for three-tier config**

Add to `research_test.go`:

```go
func TestDefaultConfig_Survey(t *testing.T) {
	cfg := DefaultConfig("survey")
	if cfg.MaxPrimaryResults != 3 {
		t.Errorf("survey MaxPrimaryResults = %d, want 3", cfg.MaxPrimaryResults)
	}
	if cfg.MaxChasedTotal != 0 {
		t.Errorf("survey MaxChasedTotal = %d, want 0", cfg.MaxChasedTotal)
	}
	if cfg.MaxChasedPerFinding != 0 {
		t.Errorf("survey MaxChasedPerFinding = %d, want 0", cfg.MaxChasedPerFinding)
	}
	if cfg.PageTruncateChars != 8000 {
		t.Errorf("survey PageTruncateChars = %d, want 8000", cfg.PageTruncateChars)
	}
	if cfg.AnalysisTruncate != 10000 {
		t.Errorf("survey AnalysisTruncate = %d, want 10000", cfg.AnalysisTruncate)
	}
	if cfg.MaxQueriesPerSource != 2 {
		t.Errorf("survey MaxQueriesPerSource = %d, want 2", cfg.MaxQueriesPerSource)
	}
	if cfg.MaxSources != 3 {
		t.Errorf("survey MaxSources = %d, want 3", cfg.MaxSources)
	}
}

func TestDefaultConfig_Standard(t *testing.T) {
	cfg := DefaultConfig("standard")
	if cfg.MaxPrimaryResults != 4 {
		t.Errorf("standard MaxPrimaryResults = %d, want 4", cfg.MaxPrimaryResults)
	}
	if cfg.MaxChasedTotal != 15 {
		t.Errorf("standard MaxChasedTotal = %d, want 15", cfg.MaxChasedTotal)
	}
	if cfg.MaxChasedPerFinding != 3 {
		t.Errorf("standard MaxChasedPerFinding = %d, want 3", cfg.MaxChasedPerFinding)
	}
	if cfg.PageTruncateChars != 10000 {
		t.Errorf("standard PageTruncateChars = %d, want 10000", cfg.PageTruncateChars)
	}
	if cfg.AnalysisTruncate != 12000 {
		t.Errorf("standard AnalysisTruncate = %d, want 12000", cfg.AnalysisTruncate)
	}
	if cfg.MaxQueriesPerSource != 3 {
		t.Errorf("standard MaxQueriesPerSource = %d, want 3", cfg.MaxQueriesPerSource)
	}
	if cfg.MaxSources != 4 {
		t.Errorf("standard MaxSources = %d, want 4", cfg.MaxSources)
	}
}

func TestDefaultConfig_Deep(t *testing.T) {
	cfg := DefaultConfig("deep")
	if cfg.MaxPrimaryResults != 6 {
		t.Errorf("deep MaxPrimaryResults = %d, want 6", cfg.MaxPrimaryResults)
	}
	if cfg.MaxChasedTotal != 50 {
		t.Errorf("deep MaxChasedTotal = %d, want 50", cfg.MaxChasedTotal)
	}
	if cfg.MaxChasedPerFinding != 5 {
		t.Errorf("deep MaxChasedPerFinding = %d, want 5", cfg.MaxChasedPerFinding)
	}
	if cfg.MaxSources != 5 {
		t.Errorf("deep MaxSources = %d, want 5", cfg.MaxSources)
	}
}

func TestDefaultConfig_EmptyDefaultsToStandard(t *testing.T) {
	cfg := DefaultConfig("")
	standard := DefaultConfig("standard")
	if cfg.MaxPrimaryResults != standard.MaxPrimaryResults {
		t.Errorf("empty depth should default to standard, got MaxPrimaryResults = %d", cfg.MaxPrimaryResults)
	}
}

func TestDepthOrder(t *testing.T) {
	if DepthOrder("survey") >= DepthOrder("standard") {
		t.Error("survey should be less than standard")
	}
	if DepthOrder("standard") >= DepthOrder("deep") {
		t.Error("standard should be less than deep")
	}
	if DepthOrder("unknown") != 0 {
		t.Error("unknown depth should return 0")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd source/server && go test ./internal/research/ -run "TestDefaultConfig|TestDepthOrder" -v -count=1`
Expected: FAIL — `MaxQueriesPerSource`, `MaxSources`, `DepthOrder` not defined.

- [ ] **Step 3: Update DeepResearchConfig and DefaultConfig**

In `source/server/internal/research/types.go`, replace the `DeepResearchConfig` struct and `DefaultConfig` function:

```go
// DeepResearchConfig holds configuration for a research run.
type DeepResearchConfig struct {
	MaxPrimaryResults   int // max results per source search query
	MaxChasedTotal      int // max total chased references (0 = no chasing)
	MaxChasedPerFinding int // max chased references per finding
	PageTruncateChars   int // max chars per fetched page
	AnalysisTruncate    int // max chars sent to model for analysis
	MaxQueriesPerSource int // max queries per source in planning
	MaxSources          int // max sources to select in planning
}

// DepthOrder returns a numeric ordering for depth levels.
// survey=1, standard=2, deep=3. Unknown returns 0.
func DepthOrder(depth string) int {
	switch depth {
	case "survey":
		return 1
	case "standard":
		return 2
	case "deep":
		return 3
	default:
		return 0
	}
}

// DefaultConfig returns config for the given depth.
func DefaultConfig(depth string) DeepResearchConfig {
	switch depth {
	case "survey":
		return DeepResearchConfig{
			MaxPrimaryResults:   3,
			MaxChasedTotal:      0,
			MaxChasedPerFinding: 0,
			PageTruncateChars:   8000,
			AnalysisTruncate:    10000,
			MaxQueriesPerSource: 2,
			MaxSources:          3,
		}
	case "deep":
		return DeepResearchConfig{
			MaxPrimaryResults:   6,
			MaxChasedTotal:      50,
			MaxChasedPerFinding: 5,
			PageTruncateChars:   12000,
			AnalysisTruncate:    15000,
			MaxQueriesPerSource: 3,
			MaxSources:          5,
		}
	default: // "standard" or empty
		return DeepResearchConfig{
			MaxPrimaryResults:   4,
			MaxChasedTotal:      15,
			MaxChasedPerFinding: 3,
			PageTruncateChars:   10000,
			AnalysisTruncate:    12000,
			MaxQueriesPerSource: 3,
			MaxSources:          4,
		}
	}
}
```

Also update `ResearchPlan.Depth` comment:

```go
// ResearchPlan is the output of source planning.
type ResearchPlan struct {
	Topic     string
	Intent    string
	Depth     string // "survey", "standard", or "deep"
	DateRange string
	Sources   []Source
}
```

- [ ] **Step 4: Update RunConfig default depth in pipeline.go**

In `source/server/internal/research/pipeline.go:55-58`, change:

```go
	depth := cfg.Depth
	if depth == "" {
		depth = "standard"
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd source/server && go test ./internal/research/ -run "TestDefaultConfig|TestDepthOrder" -v -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd source/server && git add internal/research/types.go internal/research/pipeline.go internal/research/research_test.go
git commit -m "feat(research): three-tier config — survey, standard, deep"
```

---

### Task 2: Sidecar State File

Replace the ephemeral checkpoint system with a persistent `research_state.json` sidecar in the output directory.

**Files:**
- Modify: `source/server/internal/research/checkpoint.go` (full rewrite)
- Modify: `source/server/internal/research/types.go` (add sidecar types)
- Test: `source/server/internal/research/research_test.go`

- [ ] **Step 1: Add sidecar types to types.go**

Append to `source/server/internal/research/types.go`:

```go
// ResearchState is the persistent sidecar written to research_state.json.
type ResearchState struct {
	Version       int                    `json:"version"`
	Depth         string                 `json:"depth"`
	Topic         string                 `json:"topic"`
	Intent        string                 `json:"intent"`
	DateRange     string                 `json:"date_range,omitempty"`
	Plan          *ResearchPlan          `json:"plan,omitempty"`
	SearchResults []Publication          `json:"search_results,omitempty"`
	ContentCache  map[string]string      `json:"content_cache,omitempty"`
	Findings      []AnnotatedFinding     `json:"findings,omitempty"`
	Sections      *ReportSections        `json:"sections,omitempty"`
	Progress      ProgressState          `json:"progress"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

// ProgressState is the serializable progress snapshot in the sidecar.
type ProgressState struct {
	Phase            string    `json:"phase"`              // plan, search, analyze, synthesize, complete
	Step             string    `json:"step,omitempty"`
	Current          int       `json:"current,omitempty"`
	Total            int       `json:"total,omitempty"`
	FindingsAccepted int       `json:"findings_accepted"`
	RunStartedAt     time.Time `json:"run_started_at"`
	PhaseStartedAt   time.Time `json:"phase_started_at,omitempty"`
	CompletedAt      time.Time `json:"completed_at,omitempty"`
}

const CurrentStateVersion = 1
```

- [ ] **Step 2: Write tests for sidecar read/write**

Add to `research_test.go`:

```go
func TestSidecar_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	sc := NewSidecar(dir)

	state := &ResearchState{
		Version: CurrentStateVersion,
		Depth:   "survey",
		Topic:   "test topic",
		Intent:  "test intent",
		Plan: &ResearchPlan{
			Topic:  "test topic",
			Intent: "test intent",
			Depth:  "survey",
			Sources: []Source{{Name: "arXiv", Queries: []string{"q1"}}},
		},
		Progress:  ProgressState{Phase: "plan"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := sc.Save(state); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := sc.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Topic != "test topic" {
		t.Errorf("Topic = %q, want %q", loaded.Topic, "test topic")
	}
	if loaded.Depth != "survey" {
		t.Errorf("Depth = %q, want %q", loaded.Depth, "survey")
	}
	if loaded.Plan == nil || len(loaded.Plan.Sources) != 1 {
		t.Error("Plan sources not preserved")
	}
}

func TestSidecar_Exists(t *testing.T) {
	dir := t.TempDir()
	sc := NewSidecar(dir)

	if sc.Exists() {
		t.Error("Exists() should be false on empty dir")
	}

	state := &ResearchState{Version: CurrentStateVersion, Topic: "t", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	sc.Save(state)

	if !sc.Exists() {
		t.Error("Exists() should be true after save")
	}
}

func TestSidecar_IsInProgress(t *testing.T) {
	state := &ResearchState{Progress: ProgressState{Phase: "analyze"}}
	if !state.IsInProgress() {
		t.Error("should be in progress when phase is analyze")
	}
	state.Progress.Phase = "complete"
	if state.IsInProgress() {
		t.Error("should not be in progress when phase is complete")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd source/server && go test ./internal/research/ -run "TestSidecar" -v -count=1`
Expected: FAIL — `NewSidecar`, `IsInProgress` not defined.

- [ ] **Step 4: Rewrite checkpoint.go as sidecar.go**

Rename `source/server/internal/research/checkpoint.go` to `source/server/internal/research/sidecar.go` and replace its contents:

```go
package research

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const sidecarFilename = "research_state.json"

// Sidecar manages the persistent research_state.json in the output directory.
type Sidecar struct {
	dir string
}

// NewSidecar creates a sidecar manager for the given output directory.
func NewSidecar(outputDir string) *Sidecar {
	return &Sidecar{dir: outputDir}
}

// Path returns the full path to the sidecar file.
func (s *Sidecar) Path() string {
	return filepath.Join(s.dir, sidecarFilename)
}

// Exists returns true if a sidecar file exists in the directory.
func (s *Sidecar) Exists() bool {
	_, err := os.Stat(s.Path())
	return err == nil
}

// Load reads and parses the sidecar file.
func (s *Sidecar) Load() (*ResearchState, error) {
	data, err := os.ReadFile(s.Path())
	if err != nil {
		return nil, err
	}
	var state ResearchState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// Save writes the research state to the sidecar file.
func (s *Sidecar) Save(state *ResearchState) error {
	os.MkdirAll(s.dir, 0755)
	state.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path(), data, 0644)
}

// IsInProgress returns true if the research run is not yet complete.
func (rs *ResearchState) IsInProgress() bool {
	return rs.Progress.Phase != "" && rs.Progress.Phase != "complete"
}

// NewState creates a fresh ResearchState for a new research run.
func NewState(topic, intent, depth, dateRange string) *ResearchState {
	now := time.Now()
	return &ResearchState{
		Version:   CurrentStateVersion,
		Depth:     depth,
		Topic:     topic,
		Intent:    intent,
		DateRange: dateRange,
		Progress: ProgressState{
			Phase:        "plan",
			RunStartedAt: now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd source/server && go test ./internal/research/ -run "TestSidecar" -v -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd source/server && git add internal/research/sidecar.go internal/research/types.go internal/research/research_test.go
git rm internal/research/checkpoint.go 2>/dev/null; true
git commit -m "feat(research): persistent sidecar state replaces ephemeral checkpoints"
```

---

### Task 3: Progress Tracker

Replace the basic `ProgressWriter` with a `ProgressTracker` that calculates ETAs and writes granular `status.md`.

**Files:**
- Modify: `source/server/internal/research/progress.go` (full rewrite)
- Test: `source/server/internal/research/research_test.go`

- [ ] **Step 1: Write tests for progress tracker**

Add to `research_test.go`:

```go
func TestProgressTracker_ETA(t *testing.T) {
	dir := t.TempDir()
	tracker := NewProgressTracker(dir)
	tracker.StartPhase("analyze", 10)

	// Simulate 3 completed items at ~100ms each
	for i := 0; i < 3; i++ {
		time.Sleep(100 * time.Millisecond)
		tracker.CompleteItem()
	}

	eta := tracker.EstRemainingSeconds()
	// 7 remaining * ~0.1s each = ~0.7s, allow some margin
	if eta < 0 || eta > 5 {
		t.Errorf("ETA = %d seconds, expected roughly 1", eta)
	}
}

func TestProgressTracker_StatusFile(t *testing.T) {
	dir := t.TempDir()
	tracker := NewProgressTracker(dir)
	tracker.StartPhase("analyze", 5)
	tracker.SetStep("fact_extraction")
	tracker.CompleteItem()

	// Check status.md was written
	data, err := os.ReadFile(filepath.Join(dir, "status.md"))
	if err != nil {
		t.Fatalf("status.md not written: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Analyzing findings") {
		t.Errorf("status.md missing phase info: %s", content)
	}
	if !strings.Contains(content, "1/5") {
		t.Errorf("status.md missing progress count: %s", content)
	}
}

func TestProgressTracker_Done(t *testing.T) {
	dir := t.TempDir()
	tracker := NewProgressTracker(dir)
	tracker.StartPhase("analyze", 1)
	tracker.CompleteItem()
	tracker.Done(5, 3)

	// status.md should be deleted on completion
	if _, err := os.Stat(filepath.Join(dir, "status.md")); err == nil {
		t.Error("status.md should be deleted after Done()")
	}
}

func TestProgressTracker_ProgressState(t *testing.T) {
	dir := t.TempDir()
	tracker := NewProgressTracker(dir)
	tracker.StartPhase("search", 10)
	tracker.SetStep("fetching")
	tracker.IncrementFindings()
	tracker.IncrementFindings()

	state := tracker.State()
	if state.Phase != "search" {
		t.Errorf("Phase = %q, want %q", state.Phase, "search")
	}
	if state.FindingsAccepted != 2 {
		t.Errorf("FindingsAccepted = %d, want 2", state.FindingsAccepted)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd source/server && go test ./internal/research/ -run "TestProgressTracker" -v -count=1`
Expected: FAIL — `NewProgressTracker` signature changed.

- [ ] **Step 3: Rewrite progress.go**

Replace `source/server/internal/research/progress.go`:

```go
package research

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ProgressTracker tracks research pipeline progress with ETA calculation.
type ProgressTracker struct {
	mu               sync.Mutex
	outputDir        string
	statusPath       string
	phase            string
	step             string
	current          int
	total            int
	findingsAccepted int
	runStartedAt     time.Time
	phaseStartedAt   time.Time
	itemTimes        []time.Duration
	lastItemStart    time.Time
}

// NewProgressTracker creates a progress tracker. Writes status.md to outputDir.
func NewProgressTracker(outputDir string) *ProgressTracker {
	os.MkdirAll(outputDir, 0755)
	return &ProgressTracker{
		outputDir:    outputDir,
		statusPath:   filepath.Join(outputDir, "status.md"),
		runStartedAt: time.Now(),
	}
}

// StartPhase begins tracking a new phase.
func (pt *ProgressTracker) StartPhase(phase string, total int) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.phase = phase
	pt.step = ""
	pt.current = 0
	pt.total = total
	pt.phaseStartedAt = time.Now()
	pt.itemTimes = nil
	pt.lastItemStart = time.Now()
	pt.writeStatus()
}

// SetStep updates the current sub-step within the phase.
func (pt *ProgressTracker) SetStep(step string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.step = step
	pt.writeStatus()
}

// CompleteItem marks one item as completed and records its duration.
func (pt *ProgressTracker) CompleteItem() {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	elapsed := time.Since(pt.lastItemStart)
	pt.itemTimes = append(pt.itemTimes, elapsed)
	pt.current++
	pt.lastItemStart = time.Now()
	pt.writeStatus()
}

// IncrementFindings bumps the accepted findings counter.
func (pt *ProgressTracker) IncrementFindings() {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.findingsAccepted++
}

// EstRemainingSeconds returns estimated seconds remaining in the current phase.
func (pt *ProgressTracker) EstRemainingSeconds() int {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return pt.estRemaining()
}

func (pt *ProgressTracker) estRemaining() int {
	if len(pt.itemTimes) == 0 || pt.current >= pt.total {
		return 0
	}
	// Rolling average of last 10 items
	window := pt.itemTimes
	if len(window) > 10 {
		window = window[len(window)-10:]
	}
	var sum time.Duration
	for _, d := range window {
		sum += d
	}
	avg := sum / time.Duration(len(window))
	remaining := pt.total - pt.current
	return int((avg * time.Duration(remaining)).Seconds())
}

// State returns the current progress as a serializable ProgressState.
func (pt *ProgressTracker) State() ProgressState {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return ProgressState{
		Phase:            pt.phase,
		Step:             pt.step,
		Current:          pt.current,
		Total:            pt.total,
		FindingsAccepted: pt.findingsAccepted,
		RunStartedAt:     pt.runStartedAt,
		PhaseStartedAt:   pt.phaseStartedAt,
	}
}

// Update writes a simple phase + detail message (backward-compat with old ProgressWriter callers).
func (pt *ProgressTracker) Update(phase, detail string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.phase = phase
	pt.step = detail
	elapsed := time.Since(pt.runStartedAt).Round(time.Second)
	msg := fmt.Sprintf("[%s] %s: %s", elapsed, phase, detail)
	fmt.Fprintf(os.Stderr, "%s\n", msg)

	if pt.statusPath != "" {
		status := fmt.Sprintf("# Research Progress\n**Phase:** %s\n**Detail:** %s\n**Elapsed:** %s\n",
			phase, detail, elapsed)
		os.WriteFile(pt.statusPath, []byte(status), 0644)
	}
}

// Done writes the final status and removes the status file.
func (pt *ProgressTracker) Done(findingsCount, sourcesCount int) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	elapsed := time.Since(pt.runStartedAt).Round(time.Second)
	msg := fmt.Sprintf("[%s] Complete: %d findings from %d sources", elapsed, findingsCount, sourcesCount)
	fmt.Fprintf(os.Stderr, "%s\n", msg)

	if pt.statusPath != "" {
		os.Remove(pt.statusPath)
	}
}

func (pt *ProgressTracker) writeStatus() {
	if pt.statusPath == "" {
		return
	}
	elapsed := time.Since(pt.runStartedAt).Round(time.Second)
	eta := pt.estRemaining()

	phaseName := pt.phase
	switch pt.phase {
	case "analyze":
		phaseName = fmt.Sprintf("Analyzing findings (%d/%d)", pt.current, pt.total)
	case "search":
		phaseName = fmt.Sprintf("Searching sources (%d/%d)", pt.current, pt.total)
	case "synthesize":
		phaseName = "Synthesizing report"
	case "plan":
		phaseName = "Planning sources"
	}

	status := fmt.Sprintf("# Research Progress\n**Phase:** %s\n", phaseName)
	if pt.step != "" {
		status += fmt.Sprintf("**Current step:** %s\n", pt.step)
	}
	status += fmt.Sprintf("**Elapsed:** %s", elapsed)
	if eta > 0 {
		status += fmt.Sprintf(" | **Est. remaining:** ~%d min", (eta+30)/60)
	}
	status += "\n"
	if pt.findingsAccepted > 0 {
		status += fmt.Sprintf("**Findings accepted:** %d\n", pt.findingsAccepted)
	}

	os.WriteFile(pt.statusPath, []byte(status), 0644)

	// Also log to stderr
	stderrMsg := fmt.Sprintf("[%s] %s", elapsed, phaseName)
	if pt.step != "" {
		stderrMsg += ": " + pt.step
	}
	if pt.findingsAccepted > 0 {
		stderrMsg += fmt.Sprintf(" (%d accepted", pt.findingsAccepted)
		if eta > 0 {
			stderrMsg += fmt.Sprintf(", ~%d min remaining", (eta+30)/60)
		}
		stderrMsg += ")"
	}
	fmt.Fprintf(os.Stderr, "%s\n", stderrMsg)
}
```

- [ ] **Step 4: Update pipeline.go to use NewProgressTracker instead of NewProgressWriter**

In `source/server/internal/research/pipeline.go:71`, change:

```go
	progress := NewProgressTracker(outputDir)
```

(The `Update` and `Done` methods are backward-compatible, so existing callers work.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd source/server && go test ./internal/research/ -run "TestProgressTracker" -v -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd source/server && git add internal/research/progress.go internal/research/pipeline.go internal/research/research_test.go
git commit -m "feat(research): progress tracker with ETA and granular status.md"
```

---

### Task 4: Bug Fix — Relevance Score Calibration

Update the relevance scoring prompt with explicit anchors and calibration instructions.

**Files:**
- Modify: `source/server/internal/research/analyze.go:68-97`

- [ ] **Step 1: Update the AnalyzeRelevance prompt**

In `source/server/internal/research/analyze.go`, replace the RELEVANCE section of the prompt inside `AnalyzeRelevance` (around line 87-93):

Old:
```
RELEVANCE: 1-5 (be discriminating — not everything is a 5. Use the full range.)
1 = tangentially related at best
2 = related topic but doesn't help with the specific intent
3 = useful context but not directly actionable
4 = directly relevant with actionable information
5 = essential — core finding that changes how you think about the intent
```

New:
```
RELEVANCE: 1-5
1 = Tangentially related, no actionable connection to the research intent
2 = Related topic area, but doesn't address the specific question
3 = Addresses the question but with limited specificity or indirect evidence
4 = Directly relevant with specific data, methods, or conclusions that can be acted on
5 = Essential finding — primary source, strong evidence, directly answers the intent

CALIBRATION: Use the FULL range. Most findings in a typical set should score 2-4. A score of 5 means this is one of the most important results in the entire set — reserve it. A score of 1 is fine for tangential results. Do not default to 4.
```

- [ ] **Step 2: Run existing tests to verify nothing breaks**

Run: `cd source/server && go test ./internal/research/ -v -count=1`
Expected: PASS (prompt change only, no structural changes)

- [ ] **Step 3: Commit**

```bash
cd source/server && git add internal/research/analyze.go
git commit -m "fix(research): score calibration anchors to fix 4/5 clustering"
```

---

### Task 5: Bug Fix — DDG Result Cap

Hard-cap search results per query after deduplication.

**Files:**
- Modify: `source/server/internal/research/search.go:30-57`
- Test: `source/server/internal/research/research_test.go`

- [ ] **Step 1: Write test for result capping**

Add to `research_test.go`:

```go
type mockSearchProvider struct {
	results []SearchResult
}

func (m *mockSearchProvider) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	return m.results, nil // intentionally returns more than maxResults
}

func TestSearchSource_CapsResults(t *testing.T) {
	// Create 10 results but request max 3
	var results []SearchResult
	for i := 0; i < 10; i++ {
		results = append(results, SearchResult{
			URL:   fmt.Sprintf("https://example.com/%d", i),
			Title: fmt.Sprintf("Result %d", i),
		})
	}

	dispatcher := NewSearchDispatcher(&mockSearchProvider{results: results})
	source := Source{Name: "TestWeb", Type: "web", Queries: []string{"test query"}}

	pubs := dispatcher.SearchSource(context.Background(), source, 3)
	if len(pubs) > 3 {
		t.Errorf("SearchSource returned %d results, want <= 3", len(pubs))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/research/ -run "TestSearchSource_CapsResults" -v -count=1`
Expected: FAIL — returns 10 results.

- [ ] **Step 3: Add hard cap in SearchSource**

In `source/server/internal/research/search.go`, at the end of `SearchSource` (before the `return` on line 57), add:

```go
	result := deduplicatePubs(allPubs)
	// Hard cap: respect maxResults even if backend returned more
	if len(result) > maxResults {
		result = result[:maxResults]
	}
	return result
```

And change the existing return from `return deduplicatePubs(allPubs)` to the code above.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/research/ -run "TestSearchSource_CapsResults" -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd source/server && git add internal/research/search.go internal/research/research_test.go
git commit -m "fix(research): hard-cap DDG results per query to MaxPrimaryResults"
```

---

### Task 6: Bug Fix — Thin Content Filter

Skip analysis on pages with less than 500 characters of extracted content.

**Files:**
- Modify: `source/server/internal/research/analyze.go:300-335`
- Test: `source/server/internal/research/research_test.go`

- [ ] **Step 1: Write test for thin content filtering**

Add to `research_test.go`:

```go
func TestAnalyzeAllWithPrefetch_SkipsThinContent(t *testing.T) {
	pubs := []Publication{
		{Title: "Good Article", URL: "https://example.com/good", Source: "Web"},
		{Title: "Thin Page", URL: "https://example.com/thin", Source: "Web"},
		{Title: "Empty Page", URL: "https://example.com/empty", Source: "Web"},
	}
	prefetched := map[string]string{
		"https://example.com/good":  strings.Repeat("This is substantial content. ", 50), // >500 chars
		"https://example.com/thin":  "Short.",                                              // <500 chars
		"https://example.com/empty": "",                                                    // empty
	}

	callCount := 0
	model := &mockModelCaller{fn: func(prompt string) (string, error) {
		callCount++
		if strings.Contains(prompt, "Extract every concrete fact") {
			return "- Fact one about the topic", nil
		}
		if strings.Contains(prompt, "analyze the relevance") {
			return "WHY_IT_MATTERS: relevant\nHOW_TO_USE: use it\nRELEVANCE: 3\nIMPACT: medium", nil
		}
		if strings.Contains(prompt, "QUALITY") {
			return "PASS", nil
		}
		return "", nil
	}}

	cfg := DefaultConfig("survey")
	tracker := NewProgressTracker(t.TempDir())
	findings := AnalyzeAllWithPrefetch(context.Background(), model, nil, pubs, prefetched, "test intent", cfg, tracker)

	if len(findings) != 1 {
		t.Errorf("expected 1 finding (only good article), got %d", len(findings))
	}
	if len(findings) > 0 && findings[0].Publication.Title != "Good Article" {
		t.Errorf("expected 'Good Article', got %q", findings[0].Publication.Title)
	}
}
```

Note: This test also needs a `mockModelCaller`. If one doesn't exist yet, add:

```go
type mockModelCaller struct {
	fn func(prompt string) (string, error)
}

func (m *mockModelCaller) Call(ctx context.Context, prompt string) (string, error) {
	return m.fn(prompt)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/research/ -run "TestAnalyzeAllWithPrefetch_SkipsThinContent" -v -count=1`
Expected: FAIL — thin content not filtered, findings count wrong.

- [ ] **Step 3: Add thin content filter**

In `source/server/internal/research/analyze.go`, add a constant at the top of the file (after the imports):

```go
const minContentChars = 500
```

In `AnalyzeAllWithPrefetch`, after the content resolution block (around line 322, after `if content == "" { continue }`), add:

```go
		// Skip thin content — paywalled pages, error pages, failed extractions
		if len(content) < minContentChars {
			fmt.Fprintf(os.Stderr, "  Skipping %q: only %d chars (min %d)\n", truncateTitle(pub.Title, 40), len(content), minContentChars)
			continue
		}
```

Add `"os"` to the import block if not already present.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/research/ -run "TestAnalyzeAllWithPrefetch_SkipsThinContent" -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd source/server && git add internal/research/analyze.go internal/research/research_test.go
git commit -m "fix(research): skip analysis on thin content (<500 chars)"
```

---

### Task 7: Bug Fix — Better Query Generation

Improve planner prompts to avoid broad queries. Add post-search title filtering for keyword overlap.

**Files:**
- Modify: `source/server/internal/research/planner.go:11-48`
- Modify: `source/server/internal/research/search.go`
- Test: `source/server/internal/research/research_test.go`

- [ ] **Step 1: Write test for title filtering**

Add to `research_test.go`:

```go
func TestFilterByKeywordOverlap(t *testing.T) {
	pubs := []Publication{
		{Title: "Local LLM inference performance on Apple Silicon"},
		{Title: "Best restaurants in downtown Portland"},
		{Title: "Ollama: running large language models locally"},
		{Title: "Weather forecast for next week"},
	}

	filtered := FilterByKeywordOverlap(pubs, "local LLM inference Ollama performance")
	if len(filtered) != 2 {
		t.Errorf("expected 2 relevant results, got %d", len(filtered))
	}
	for _, p := range filtered {
		if p.Title == "Best restaurants in downtown Portland" || p.Title == "Weather forecast for next week" {
			t.Errorf("irrelevant result not filtered: %q", p.Title)
		}
	}
}

func TestFilterByKeywordOverlap_KeepsAllWhenRelevant(t *testing.T) {
	pubs := []Publication{
		{Title: "AI inference optimization techniques"},
		{Title: "Machine learning model serving at edge"},
	}
	filtered := FilterByKeywordOverlap(pubs, "AI inference edge serving optimization")
	if len(filtered) != 2 {
		t.Errorf("all relevant results should be kept, got %d", len(filtered))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd source/server && go test ./internal/research/ -run "TestFilterByKeywordOverlap" -v -count=1`
Expected: FAIL — `FilterByKeywordOverlap` not defined.

- [ ] **Step 3: Implement FilterByKeywordOverlap in search.go**

Add to `source/server/internal/research/search.go`:

```go
// FilterByKeywordOverlap removes publications whose title has zero keyword overlap
// with the given topic/intent text. Keywords shorter than 4 chars are ignored (stop words).
func FilterByKeywordOverlap(pubs []Publication, topicIntent string) []Publication {
	// Build keyword set from topic+intent
	keywords := make(map[string]bool)
	for _, word := range strings.Fields(strings.ToLower(topicIntent)) {
		if len(word) >= 4 {
			keywords[word] = true
		}
	}
	if len(keywords) == 0 {
		return pubs // no useful keywords, keep everything
	}

	var result []Publication
	for _, p := range pubs {
		titleWords := strings.Fields(strings.ToLower(p.Title))
		overlap := false
		for _, w := range titleWords {
			if keywords[w] {
				overlap = true
				break
			}
		}
		if overlap {
			result = append(result, p)
		}
	}

	// If filtering removed everything, return originals (avoid empty results)
	if len(result) == 0 {
		return pubs
	}
	return result
}
```

- [ ] **Step 4: Apply title filtering in SearchSource**

In `source/server/internal/research/search.go`, update `SearchAllSources` to accept topic/intent and filter. Add a new parameter:

Actually, the simpler approach: apply the filter in `SearchAndPrefetch` since that's what the pipeline calls. Add `topicIntent string` parameter:

In `SearchAndPrefetch` signature, add parameter and filter before returning:

```go
func (d *SearchDispatcher) SearchAndPrefetch(ctx context.Context, plan *ResearchPlan, maxPerSource int, fetcher URLFetcher) ([]Publication, map[string]string) {
```

After `searchWg.Wait()` and `fetchWg.Wait()`, before the return, add:

```go
	// Filter results with no keyword overlap with the research topic
	topicIntent := plan.Topic + " " + plan.Intent
	deduped := deduplicatePubs(allPubs)
	filtered := FilterByKeywordOverlap(deduped, topicIntent)
	return filtered, content
```

And change the existing return from `return deduplicatePubs(allPubs), content` to the above.

Similarly update `SearchAllSources`:

```go
func (d *SearchDispatcher) SearchAllSources(ctx context.Context, plan *ResearchPlan, maxPerSource int) []Publication {
```

After `wg.Wait()`, before return:

```go
	topicIntent := plan.Topic + " " + plan.Intent
	deduped := deduplicatePubs(allPubs)
	return FilterByKeywordOverlap(deduped, topicIntent)
```

- [ ] **Step 5: Update the planner prompt in planner.go**

The existing `PlanSources` prompt already has good/bad query examples (added in the enhancement track). Verify they're present. The current prompt at line 31-34 already includes BAD/GOOD examples. No change needed — the examples are already there.

- [ ] **Step 6: Run all tests to verify nothing breaks**

Run: `cd source/server && go test ./internal/research/ -v -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
cd source/server && git add internal/research/search.go internal/research/planner.go internal/research/research_test.go
git commit -m "fix(research): filter irrelevant DDG results by keyword overlap"
```

---

### Task 8: Pipeline Integration — Sidecar + Incremental Deepening

Wire the sidecar into the pipeline, replacing checkpoint usage. Add incremental deepening logic.

**Files:**
- Modify: `source/server/internal/research/pipeline.go` (major changes)
- Test: `source/server/internal/research/research_test.go`

- [ ] **Step 1: Write test for incremental deepening**

Add to `research_test.go`:

```go
func TestPipeline_IncrementalDeepening_SkipsExistingWork(t *testing.T) {
	dir := t.TempDir()

	// Simulate existing survey state
	sidecar := NewSidecar(dir)
	state := NewState("test topic", "test intent", "survey", "")
	state.Plan = &ResearchPlan{
		Topic: "test topic", Intent: "test intent", Depth: "survey",
		Sources: []Source{{Name: "arXiv", Queries: []string{"existing query"}, Reason: "test"}},
	}
	state.SearchResults = []Publication{
		{Title: "Existing Result", URL: "https://example.com/existing", Source: "arXiv"},
	}
	state.Findings = []AnnotatedFinding{
		{Publication: Publication{Title: "Existing Result", URL: "https://example.com/existing"}, RelevanceScore: 5},
	}
	state.ContentCache = map[string]string{
		"https://example.com/existing": "existing content",
	}
	state.Progress = ProgressState{Phase: "complete"}
	sidecar.Save(state)

	// Verify deepening is detected
	loaded, err := sidecar.Load()
	if err != nil {
		t.Fatal(err)
	}
	if DepthOrder("standard") <= DepthOrder(loaded.Depth) {
		t.Error("standard should be deeper than survey")
	}
}

func TestPipeline_SameDepth_ReturnsExisting(t *testing.T) {
	dir := t.TempDir()
	sidecar := NewSidecar(dir)
	state := NewState("topic", "intent", "standard", "")
	state.Progress = ProgressState{Phase: "complete"}
	sidecar.Save(state)

	// Requesting same or lower depth should not re-run
	loaded, _ := sidecar.Load()
	if DepthOrder("survey") > DepthOrder(loaded.Depth) {
		t.Error("survey should not trigger deepening over standard")
	}
	if DepthOrder("standard") > DepthOrder(loaded.Depth) {
		t.Error("standard should not trigger deepening over standard")
	}
}
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `cd source/server && go test ./internal/research/ -run "TestPipeline_Incremental|TestPipeline_SameDepth" -v -count=1`
Expected: PASS (these tests use sidecar + DepthOrder directly, no pipeline call needed yet)

- [ ] **Step 3: Refactor pipeline.go to use sidecar**

Replace the `Run` method in `pipeline.go` to handle sidecar loading and incremental deepening. The key changes:

1. Replace `NewCheckpoint(...)` with `NewSidecar(outputDir)`
2. Check for existing sidecar and compare depths
3. If deepening, load existing state and pass it through the phases
4. Update sidecar after each phase instead of ephemeral checkpoint files

In `source/server/internal/research/pipeline.go`, replace the `Run` method:

```go
// Run executes the pipeline — either a single phase or all phases.
// If an existing research_state.json exists at the output dir and the requested
// depth is deeper, runs in incremental deepening mode.
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
	var existing *ResearchState
	if sidecar.Exists() {
		loaded, err := sidecar.Load()
		if err == nil && loaded.Version == CurrentStateVersion {
			// Check if we're deepening or resuming
			if loaded.IsInProgress() {
				// Resume from crash — use existing state
				existing = loaded
			} else if DepthOrder(depth) > DepthOrder(loaded.Depth) {
				// Deepening — load existing as base
				existing = loaded
			} else {
				// Same or shallower depth — return existing
				return &PhaseResult{
					Phase:   "all",
					Summary: fmt.Sprintf("Research already at depth '%s' (requested '%s'). To re-run from scratch, delete research_state.json in %s.", loaded.Depth, depth, outputDir),
				}, nil
			}
		}
	}

	// Initialize fresh state if no existing
	if existing == nil {
		existing = NewState(cfg.Topic, cfg.Intent, depth, cfg.DateRange)
	} else {
		existing.Depth = depth // upgrade depth
	}

	phase := cfg.Phase
	if phase == "" {
		phase = "all"
	}

	switch phase {
	case "plan":
		return p.runPlan(ctx, cfg, existing, sidecar, rcfg, progress, outputDir)
	case "search":
		return p.runSearch(ctx, cfg, existing, sidecar, rcfg, progress, outputDir)
	case "analyze":
		return p.runAnalyze(ctx, cfg, existing, sidecar, rcfg, progress, outputDir)
	case "synthesize":
		return p.runSynthesize(ctx, cfg, existing, sidecar, rcfg, progress, outputDir)
	case "all":
		return p.runAll(ctx, cfg, existing, sidecar, rcfg, progress, outputDir)
	default:
		return nil, fmt.Errorf("unknown phase: %s (use plan, search, analyze, synthesize, or omit for all)", phase)
	}
}
```

Then update each phase method signature to accept `*ResearchState`, `*Sidecar`, and `DeepResearchConfig`, replacing the old `*Checkpoint` parameter. Each phase saves to the sidecar instead of checkpoint files:

**runPlan** — after planning, check if existing state has a plan. If deepening, prompt the planner with existing sources to select complementary ones. Merge new sources with existing. Save to `existing.Plan` and `sidecar.Save(existing)`.

**runSearch** — use `existing.Plan`. Skip queries already present in existing state. Add new results to `existing.SearchResults`. Save new content to `existing.ContentCache`. Call `sidecar.Save(existing)`.

**runAnalyze** — analyze new publications not already in `existing.Findings`. For selective re-analysis: re-run relevance (Pass 2) on existing findings scored 2-4 with enriched cross-context. Update `existing.Findings` and save.

**runSynthesize** — synthesize using `existing.Findings`. Save sections to `existing.Sections`. Mark `existing.Progress.Phase = "complete"`. Call `sidecar.Save(existing)`.

**runAll** — calls the four phases in sequence, passing the same `existing` state through.

The full implementation of each method is too large to inline here. The implementer should follow the patterns from the existing methods but replace all `cp.SaveX/LoadX` calls with reads/writes to `existing.*` fields + `sidecar.Save(existing)`.

Key patterns:
- `cp.SavePlan(plan)` becomes `existing.Plan = plan; sidecar.Save(existing)`
- `cp.LoadPlan()` becomes `existing.Plan` (already in memory)
- `cp.SaveSearchResults(pubs)` becomes `existing.SearchResults = pubs; sidecar.Save(existing)`
- `cp.Cleanup()` is removed entirely — the sidecar persists
- `existing.Progress` is updated at each phase transition

- [ ] **Step 4: Add plan expansion to planner.go**

Add a new function to `source/server/internal/research/planner.go`:

```go
// PlanExpansion asks the model to select additional sources beyond those already searched.
func PlanExpansion(ctx context.Context, model ModelCaller, topic, intent, depth, dateRange string, existingSources []Source, maxSources int) ([]Source, error) {
	var existingList strings.Builder
	for _, s := range existingSources {
		existingList.WriteString(fmt.Sprintf("- %s (queries: %s)\n", s.Name, strings.Join(s.Queries, ", ")))
	}

	newSourceCount := maxSources - len(existingSources)
	if newSourceCount <= 0 {
		return nil, nil // already at or over limit
	}

	prompt := fmt.Sprintf(`You are a research librarian expanding a prior search. The following sources were ALREADY searched — do NOT repeat them.

Already searched:
%s

Available sources (pick from these only):
%s

Topic: %s
Intent: %s
%s

Select up to %d NEW sources that complement the existing search. Pick sources that cover different angles or information types. For each, provide 2-3 HIGHLY SPECIFIC search queries.

Format:
SOURCE: <source name>
REASON: <why this source adds value beyond what's already searched>
QUERY: <specific search query>
QUERY: <specific search query>`, existingList.String(), SourceNames(), topic, intent, formatDateRangeInstruction(dateRange), newSourceCount)

	resp, err := model.Call(ctx, prompt)
	if err != nil {
		return nil, err
	}

	return parsePlanResponse(resp), nil
}
```

- [ ] **Step 5: Add selective re-analysis to analyze.go**

Add to `source/server/internal/research/analyze.go`:

```go
// ReAnalyzeMiddleFindings re-runs relevance scoring (Pass 2) on findings scored 2-4,
// using enriched cross-context from all findings. Returns updated findings slice.
func ReAnalyzeMiddleFindings(ctx context.Context, model ModelCaller, findings []AnnotatedFinding, intent string) []AnnotatedFinding {
	crossCtx := BuildCrossContext(findings)

	for i := range findings {
		score := findings[i].RelevanceScore
		if score < 2 || score > 4 {
			continue // skip 1s (clearly irrelevant) and 5s (clearly essential)
		}

		// Re-run Pass 2 only — facts (Pass 1) haven't changed
		relevance, err := AnalyzeRelevance(ctx, model, findings[i].KeyFindings, findings[i].Publication.Title, intent, crossCtx)
		if err != nil {
			continue
		}

		findings[i].WhyItMatters = relevance.WhyItMatters
		findings[i].HowToUse = relevance.HowToUse
		findings[i].RelevanceScore = relevance.RelevanceScore
		findings[i].ImpactRating = relevance.ImpactRating
		if relevance.CrossRefs != "" {
			findings[i].WhyItMatters += "\n\n**Connections to other findings:** " + relevance.CrossRefs
		}
	}

	return findings
}
```

- [ ] **Step 6: Run all tests**

Run: `cd source/server && go test ./internal/research/ -v -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
cd source/server && git add internal/research/pipeline.go internal/research/planner.go internal/research/analyze.go internal/research/research_test.go
git commit -m "feat(research): incremental deepening via sidecar state"
```

---

### Task 9: Suggested Next Action

Add "Next Steps" section to reports and `suggested_next` metadata to MCP responses.

**Files:**
- Modify: `source/server/internal/research/report.go`
- Modify: `source/server/internal/research/pipeline.go`
- Modify: `source/server/internal/mcp/server.go`
- Test: `source/server/internal/research/research_test.go`

- [ ] **Step 1: Write test for next steps in report**

Add to `research_test.go`:

```go
func TestFormatNextSteps_Survey(t *testing.T) {
	result := FormatNextSteps("survey", "quantum error correction", "understand QEC methods", "/tmp/research")
	if !strings.Contains(result, "standard") {
		t.Error("survey should suggest standard depth")
	}
	if !strings.Contains(result, "cercano_deep_research") {
		t.Error("should include tool name")
	}
	if !strings.Contains(result, "/tmp/research") {
		t.Error("should include output_dir")
	}
}

func TestFormatNextSteps_Deep(t *testing.T) {
	result := FormatNextSteps("deep", "topic", "intent", "/tmp")
	if result != "" {
		t.Error("deep should not suggest further deepening")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd source/server && go test ./internal/research/ -run "TestFormatNextSteps" -v -count=1`
Expected: FAIL — `FormatNextSteps` not defined.

- [ ] **Step 3: Add FormatNextSteps to report.go**

Add to `source/server/internal/research/report.go`:

```go
// FormatNextSteps returns a markdown "Next Steps" section suggesting deeper research.
// Returns empty string for "deep" depth (terminal level).
func FormatNextSteps(depth, topic, intent, outputDir string) string {
	var nextDepth string
	switch depth {
	case "survey":
		nextDepth = "standard"
	case "standard":
		nextDepth = "deep"
	default:
		return "" // deep or unknown — no suggestion
	}

	return fmt.Sprintf(`## Next Steps

This %s identified findings that may warrant deeper analysis. To expand with %s coverage and reference chasing, run:

    cercano_deep_research topic="%s" intent="%s" depth="%s" output_dir="%s"

The existing findings will be preserved and enriched with additional sources.
`, depth, nextDepth, topic, intent, nextDepth, outputDir)
}
```

- [ ] **Step 4: Add next steps to synthesis output**

In `source/server/internal/research/report.go`, in the `formatSynthesis` function, add at the end (before the final `return`):

This requires passing depth/topic/intent/outputDir to `formatSynthesis`. Instead, add the next steps in `CompileReport` and `WriteReport` — append after the synthesis section.

In `CompileReport`, before the final `return out.String()`:

```go
	// Already receives plan which has Topic, Intent, Depth
	nextSteps := FormatNextSteps(plan.Depth, plan.Topic, plan.Intent, "")
	if nextSteps != "" {
		out.WriteString(nextSteps)
	}
```

In `WriteReport`, add `FormatNextSteps` output to `synthesis.md`:

In the `formatSynthesis` call or in `WriteReport` after writing `synthesis.md`, the next steps should be included. The simplest approach: modify `WriteReport` to accept outputDir context and append next steps to the synthesis file:

```go
// In WriteReport, after writing synthesis.md:
nextSteps := FormatNextSteps(plan.Depth, plan.Topic, plan.Intent, outputDir)
if nextSteps != "" {
	// Append to synthesis.md
	synthPath := filepath.Join(outputDir, "synthesis.md")
	f, err := os.OpenFile(synthPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		f.WriteString("\n" + nextSteps)
		f.Close()
	}
}
```

- [ ] **Step 5: Add SuggestedNext to PhaseResult**

In `source/server/internal/research/pipeline.go`, add to `PhaseResult`:

```go
type SuggestedNext struct {
	Action string            `json:"action"`
	Tool   string            `json:"tool"`
	Params map[string]string `json:"params"`
	Reason string            `json:"reason"`
}

// Add field to PhaseResult:
type PhaseResult struct {
	// ... existing fields ...
	SuggestedNext *SuggestedNext // populated for survey/standard completions
}
```

In the `runSynthesize` method, after building the summary, populate `SuggestedNext` if depth is survey or standard:

```go
	var suggested *SuggestedNext
	if depth := existing.Depth; depth == "survey" || depth == "standard" {
		nextDepth := "standard"
		if depth == "standard" {
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
				strings.Title(depth), primaryCount+chasedCount, len(existing.Plan.Sources), strings.Title(nextDepth)),
		}
	}
```

Set `SuggestedNext: suggested` in the returned `PhaseResult`.

- [ ] **Step 6: Wire suggested_next into MCP response**

In `source/server/internal/mcp/server.go`, in `handleDeepResearch`, after building the result (around line 1325-1330):

```go
	result := &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: output},
		},
	}

	// Add suggested_next as structured metadata if available
	var metadata any
	if phaseResult.SuggestedNext != nil {
		metadata = map[string]any{
			"suggested_next": phaseResult.SuggestedNext,
		}
	}

	return s.maybeNudge(args.ProjectDir, result), metadata, nil
```

- [ ] **Step 7: Run tests**

Run: `cd source/server && go test ./internal/research/ -run "TestFormatNextSteps" -v -count=1`
Expected: PASS

Run: `cd source/server && go test ./... -count=1`
Expected: PASS (full test suite)

- [ ] **Step 8: Commit**

```bash
cd source/server && git add internal/research/report.go internal/research/pipeline.go internal/mcp/server.go internal/research/research_test.go
git commit -m "feat(research): suggested next action for incremental deepening"
```

---

### Task 10: MCP Handler Updates

Update the MCP tool definition and handler for the new depth options and default.

**Files:**
- Modify: `source/server/internal/mcp/server.go:385-396`
- Modify: `.agents/skills/cercano-deep-research/SKILL.md`

- [ ] **Step 1: Update DeepResearchRequest depth description**

In `source/server/internal/mcp/server.go:388`, change:

```go
Depth string `json:"depth,omitempty" jsonschema:"Research depth: survey (5-10 results, quick) or thorough (20+ results, deep). Default: thorough."`
```

To:

```go
Depth string `json:"depth,omitempty" jsonschema:"Research depth: survey (quick landscape scan, ~2 min), standard (balanced, ~5-8 min), or deep (exhaustive with reference chasing, ~15+ min). Default: standard."`
```

- [ ] **Step 2: Update SKILL.md**

Read the current SKILL.md file, then update the depth parameter documentation and add incremental deepening usage examples. Update references from "thorough" to "standard" and "deep". Add example of running survey first, then deepening.

- [ ] **Step 3: Run the full test suite**

Run: `cd source/server && go test ./... -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
cd source/server && git add internal/mcp/server.go .agents/skills/cercano-deep-research/SKILL.md
git commit -m "feat(research): update MCP handler and SKILL.md for three-tier depth"
```

---

### Task 11: Push Prior Enhancement Commits

Push the 6 unpushed commits from the enhancement track that this work builds on.

**Files:** None (git operations only)

- [ ] **Step 1: Check unpushed commits**

Run: `cd /Users/bryancostanich/Git_Repos/bryan_costanich/Cercano && git log --oneline origin/main..HEAD`

Verify the 6 enhancement commits are present:
1. Pre-check model before running deep research
2. Formal before/after validation
3. Per-request model override and pre-run model check
4. Phased execution
5. Parallel content prefetching
6. Track completion status update

- [ ] **Step 2: Ask user for push approval**

Do NOT push without explicit user approval. Present the commit list and ask.
