package research

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ProgressTracker tracks phase/step progress, calculates ETAs, and writes status.md.
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
	itemTimes        []time.Duration // rolling last-10 item durations for ETA
	lastItemStart    time.Time
}

// NewProgressTracker creates a ProgressTracker. If outputDir is set, writes status.md there.
func NewProgressTracker(outputDir string) *ProgressTracker {
	pt := &ProgressTracker{
		runStartedAt: time.Now(),
	}
	if outputDir != "" {
		os.MkdirAll(outputDir, 0755)
		pt.outputDir = outputDir
		pt.statusPath = filepath.Join(outputDir, "status.md")
	}
	return pt
}

// StartPhase begins tracking a new phase with a known total item count.
func (pt *ProgressTracker) StartPhase(phase string, total int) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.phase = phase
	pt.total = total
	pt.current = 0
	pt.phaseStartedAt = time.Now()
	pt.lastItemStart = time.Now()
	pt.itemTimes = pt.itemTimes[:0]
	pt.writeStatus()
}

// SetStep updates the current sub-step label within a phase.
func (pt *ProgressTracker) SetStep(step string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.step = step
	pt.writeStatus()
}

// CompleteItem marks one item done and records its duration for ETA calculation.
func (pt *ProgressTracker) CompleteItem() {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	dur := time.Since(pt.lastItemStart)
	pt.itemTimes = append(pt.itemTimes, dur)
	if len(pt.itemTimes) > 10 {
		pt.itemTimes = pt.itemTimes[len(pt.itemTimes)-10:]
	}
	pt.current++
	pt.lastItemStart = time.Now()
	pt.writeStatus()
}

// IncrementFindings bumps the accepted findings counter by one.
func (pt *ProgressTracker) IncrementFindings() {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.findingsAccepted++
	pt.writeStatus()
}

// EstRemainingSeconds returns an ETA in seconds based on the rolling average of the last 10 item durations.
func (pt *ProgressTracker) EstRemainingSeconds() int {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return pt.estRemainingSeconds()
}

// estRemainingSeconds is the internal (non-locking) ETA calculation.
func (pt *ProgressTracker) estRemainingSeconds() int {
	if len(pt.itemTimes) == 0 || pt.total <= pt.current {
		return 0
	}
	var sum time.Duration
	for _, d := range pt.itemTimes {
		sum += d
	}
	avg := sum / time.Duration(len(pt.itemTimes))
	remaining := pt.total - pt.current
	return int(avg.Seconds() * float64(remaining))
}

// State returns a serializable snapshot of the current progress.
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

// Update is a backward-compatible method: writes a phase/detail message to stderr and status.md.
func (pt *ProgressTracker) Update(phase, detail string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.phase = phase
	pt.step = detail
	elapsed := time.Since(pt.runStartedAt).Round(time.Second)
	fmt.Fprintf(os.Stderr, "[%s] %s: %s\n", elapsed, phase, detail)
	pt.writeStatus()
}

// Done is a backward-compatible final method: prints summary and deletes status.md.
func (pt *ProgressTracker) Done(findingsCount, sourcesCount int) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	elapsed := time.Since(pt.runStartedAt).Round(time.Second)
	fmt.Fprintf(os.Stderr, "[%s] Complete: %d findings from %d sources\n", elapsed, findingsCount, sourcesCount)
	if pt.statusPath != "" {
		os.Remove(pt.statusPath)
	}
}

// writeStatus writes status.md and logs to stderr. Must be called with mu held.
func (pt *ProgressTracker) writeStatus() {
	elapsed := time.Since(pt.runStartedAt).Round(time.Second)
	estSec := pt.estRemainingSeconds()

	// Build phase label
	phaseLabel := pt.phase
	if pt.total > 0 {
		phaseLabel = fmt.Sprintf("%s (%d/%d)", pt.phase, pt.current, pt.total)
	}

	// Build ETA string
	var etaStr string
	if estSec > 0 {
		if estSec < 60 {
			etaStr = fmt.Sprintf("~%d sec", estSec)
		} else {
			etaStr = fmt.Sprintf("~%d min", (estSec+30)/60)
		}
	}

	// Elapsed formatted
	elapsedStr := formatDuration(elapsed)

	// Stderr log
	stderrMsg := fmt.Sprintf("[%s] %s", elapsedStr, phaseLabel)
	if pt.step != "" {
		stderrMsg += ": " + pt.step
	}
	details := fmt.Sprintf("(%d accepted", pt.findingsAccepted)
	if etaStr != "" {
		details += ", " + etaStr + " remaining"
	}
	details += ")"
	stderrMsg += " " + details
	fmt.Fprintf(os.Stderr, "%s\n", stderrMsg)

	// Write status.md
	if pt.statusPath == "" {
		return
	}

	var content string
	if etaStr != "" {
		content = fmt.Sprintf("# Research Progress\n**Phase:** %s\n**Current step:** %s\n**Elapsed:** %s | **Est. remaining:** %s\n**Findings accepted:** %d\n",
			phaseLabel, pt.step, elapsedStr, etaStr, pt.findingsAccepted)
	} else {
		content = fmt.Sprintf("# Research Progress\n**Phase:** %s\n**Current step:** %s\n**Elapsed:** %s\n**Findings accepted:** %d\n",
			phaseLabel, pt.step, elapsedStr, pt.findingsAccepted)
	}
	os.WriteFile(pt.statusPath, []byte(content), 0644)
}

// formatDuration formats a duration as "Xm Ys" or "Xs".
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm %ds", m, s)
}
