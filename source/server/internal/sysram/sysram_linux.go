//go:build linux

package sysram

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Total returns total physical memory in bytes parsed from
// /proc/meminfo, or 0 if the probe fails.
func Total() int64 {
	if kb, ok := meminfoField("MemTotal:"); ok {
		return kb * 1024
	}
	return 0
}

// NonEvictable returns bytes of physical memory that cannot be reclaimed
// under pressure, and whether the probe succeeded.
//
// Linux has no single counter equivalent to Darwin's wired pages, so this
// approximates it as MemTotal - MemAvailable. MemAvailable is the
// kernel's own estimate of what could be handed to a new allocation
// without swapping, so the difference is memory genuinely committed. That
// is a coarser signal than the Darwin path, but it errs toward
// over-counting (treating some reclaimable memory as committed), which
// makes the guard more conservative rather than less — the right
// direction for a check whose false-permit outcome is a machine lockup.
//
// Older kernels without MemAvailable return ok=false so callers fall back
// rather than acting on a wrong number.
func NonEvictable() (int64, bool) {
	total, okTotal := meminfoField("MemTotal:")
	available, okAvail := meminfoField("MemAvailable:")
	if !okTotal || !okAvail || total <= 0 || available > total {
		return 0, false
	}
	return (total - available) * 1024, true
}

// meminfoField returns the kilobyte value of a /proc/meminfo line by its
// prefix (e.g. "MemTotal:"), and whether it was found and parsed.
func meminfoField(prefix string) (int64, bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return kb, true
	}
	return 0, false
}
