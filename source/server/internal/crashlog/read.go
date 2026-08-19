// Package crashlog — read.go: parsing existing crash entries so the CLI
// (or an operator's grep pipeline) can inspect what previously killed
// the agent.
package crashlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// TailEntries returns the last n entries from the crash log at path,
// most-recent first. If the file doesn't exist, returns nil (no
// crashes yet is a valid state, not an error). Malformed lines are
// silently skipped — better to return the parseable subset than nothing.
//
// Called by the SDK's reconnect flow to attach a short summary to the
// disconnect event (so the CLI can say "agent crashed: nil pointer
// deref in compaction/reducer.go:47" instead of just "agent
// disconnected"), and by the `cercano logs --crashes` subcommand.
func TailEntries(path string, n int) ([]Entry, error) {
	if n <= 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("crashlog: open %s: %w", path, err)
	}
	defer f.Close()

	var all []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), 1<<20) // stack traces can be big
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip malformed lines rather than failing the whole read
		}
		all = append(all, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("crashlog: scan %s: %w", path, err)
	}

	// Take the last n, reverse so most-recent comes first.
	if len(all) > n {
		all = all[len(all)-n:]
	}
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	return all, nil
}

// IsCrash reports whether a kind represents an actual crash, as opposed
// to a routine operational record such as KindRuntimeEvent. Used to keep
// lifecycle noise out of crash-facing surfaces now that both share one
// file.
func IsCrash(k Kind) bool {
	switch k {
	case KindPanic, KindGoroutinePanic, KindSignal:
		return true
	default:
		return false
	}
}

// summaryScanWindow is how many trailing entries LatestSummary inspects
// looking for a crash. Runtime events are far more frequent than
// crashes, so the most recent entry is usually a spawn or stop; we scan
// back far enough to find the real crash behind that noise, but not so
// far that we resurrect an ancient one.
const summaryScanWindow = 200

// LatestSummary returns a compact one-line description of the most
// recent crash for surfacing in the CLI's disconnect message. Empty
// string if the log is missing, empty, or contains no crash within the
// recent window. Never returns an error — summary-attach is
// best-effort; if the log can't be read we just don't have a summary.
//
// Non-crash records (KindRuntimeEvent) are skipped: reporting "agent
// crashed: runtime_event" on every reconnect would be actively
// misleading.
func LatestSummary(path string) string {
	entries, err := TailEntries(path, summaryScanWindow)
	if err != nil {
		return ""
	}
	for _, e := range entries { // most-recent first
		if !IsCrash(e.Kind) {
			continue
		}
		label := string(e.Kind)
		switch e.Kind {
		case KindPanic, KindGoroutinePanic:
			if e.Reason != "" {
				return label + ": " + firstLine(e.Reason)
			}
		case KindSignal:
			if e.Signal != "" {
				return label + ": " + e.Signal
			}
		}
		return label
	}
	return ""
}

// firstLine returns the first newline-terminated substring of s so a
// multi-line panic reason (like a formatted error chain) doesn't
// clobber the CLI status bar. Trimmed to 120 chars as a hard cap.
func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			s = s[:i]
			break
		}
	}
	const cap = 120
	if len(s) > cap {
		s = s[:cap] + "…"
	}
	return s
}
