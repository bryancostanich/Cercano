// Package localruntime — partials.go: partial-download bookkeeping.
//
// Failed downloads deliberately keep their .part file so a retry can
// resume via a Range request (runDownload). Two consequences need
// managing: the total size hides inside a Content-Range header on
// resumed responses, and .part files orphaned by a server kill (no
// clean failure path ran) need an eventual sweep so abandoned attempts
// don't hold gigabytes forever.
package localruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// contentRangeTotal parses the total size out of a Content-Range
// header ("bytes 100-999/1000" -> 1000). Returns 0 when absent or
// malformed (including the "bytes */..." and ".../*" forms).
func contentRangeTotal(header string) int64 {
	idx := strings.LastIndex(header, "/")
	if idx < 0 {
		return 0
	}
	total, err := strconv.ParseInt(strings.TrimSpace(header[idx+1:]), 10, 64)
	if err != nil || total < 0 {
		return 0
	}
	return total
}

// DefaultPartialMaxAge is how long an untouched .part file survives
// before SweepStalePartials removes it. A week comfortably covers
// "retry tomorrow" while bounding how long tens of gigabytes of
// abandoned partials can linger.
const DefaultPartialMaxAge = 7 * 24 * time.Hour

// SweepStalePartials removes .part files in dir whose modification
// time is older than maxAge. Recent partials are kept — they're what
// download resume feeds on. Returns the removed paths. A missing dir
// is not an error (nothing was ever downloaded).
func SweepStalePartials(dir string, maxAge time.Duration) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("sweep partials: %w", err)
	}
	cutoff := time.Now().Add(-maxAge)
	var removed []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".part") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := os.Remove(path); err == nil {
			removed = append(removed, path)
		}
	}
	return removed, nil
}
