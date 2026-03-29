package research

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ProgressWriter writes status updates to stderr and a status file.
type ProgressWriter struct {
	statusPath string
	startTime  time.Time
}

// NewProgressWriter creates a progress writer. If outputDir is set, writes status.md there.
func NewProgressWriter(outputDir string) *ProgressWriter {
	pw := &ProgressWriter{startTime: time.Now()}
	if outputDir != "" {
		os.MkdirAll(outputDir, 0755)
		pw.statusPath = filepath.Join(outputDir, "status.md")
	}
	return pw
}

// Update writes a progress message to stderr and the status file.
func (pw *ProgressWriter) Update(phase, detail string) {
	elapsed := time.Since(pw.startTime).Round(time.Second)
	msg := fmt.Sprintf("[%s] %s: %s", elapsed, phase, detail)
	fmt.Fprintf(os.Stderr, "%s\n", msg)

	if pw.statusPath != "" {
		status := fmt.Sprintf("# Research Status\n\n**Phase:** %s\n**Detail:** %s\n**Elapsed:** %s\n", phase, detail, elapsed)
		os.WriteFile(pw.statusPath, []byte(status), 0644)
	}
}

// Done writes the final status.
func (pw *ProgressWriter) Done(findingsCount, sourcesCount int) {
	elapsed := time.Since(pw.startTime).Round(time.Second)
	msg := fmt.Sprintf("[%s] Complete: %d findings from %d sources", elapsed, findingsCount, sourcesCount)
	fmt.Fprintf(os.Stderr, "%s\n", msg)

	if pw.statusPath != "" {
		os.Remove(pw.statusPath) // clean up status file on completion
	}
}
