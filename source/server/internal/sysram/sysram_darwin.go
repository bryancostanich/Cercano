//go:build darwin

// Package sysram reports the machine's total physical memory. On Apple
// Silicon this is the unified memory pool — the same budget the GPU
// draws from, which is what makes it the right denominator for "will
// this model fit?" verdicts.
package sysram

import (
	"bufio"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Total returns total physical memory in bytes, or 0 if the probe
// fails (callers render "unknown" rather than a wrong verdict).
func Total() int64 {
	v, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return int64(v)
}

// vmStatTimeout bounds the probe. vm_stat measures at ~2 ms; anything
// approaching this bound means the machine is already in trouble, and a
// memory probe must never be the thing that hangs a spawn.
const vmStatTimeout = 2 * time.Second

// NonEvictable returns the bytes of physical memory that cannot be
// reclaimed under pressure — wired pages plus the pages physically
// occupied by the compressor — and whether the probe succeeded.
//
// This, not "free" memory, is the quantity that predicts a hard lockup.
// On Apple Silicon a llama-server run with GPU offload holds its weights
// in Metal buffers in unified memory, and those pages are wired. When
// wired memory approaches physical RAM the kernel has nothing left to
// reclaim and the machine freezes rather than paging. Free memory is a
// useless signal here: macOS deliberately spends nearly all of it on
// evictable cache, so a free-memory check would refuse almost every
// legitimate spawn.
//
// Reads /usr/bin/vm_stat rather than calling host_statistics64, which
// would require cgo — the repository is deliberately pure-Go (see
// modernc.org/sqlite). The probe runs once per model spawn, an operation
// that takes tens of seconds, so a ~2 ms subprocess is irrelevant.
//
// ok is false on any failure. Callers must fall back rather than treat
// an unknown reading as zero, which would look like a completely idle
// machine and permit every spawn.
func NonEvictable() (bytes int64, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), vmStatTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/vm_stat").Output()
	if err != nil {
		return 0, false
	}
	return parseVMStatNonEvictable(string(out))
}

// parseVMStatNonEvictable extracts wired + compressor-occupied bytes from
// vm_stat output. Split out from NonEvictable so the parser is testable
// against captured fixtures without running the subprocess.
//
// Parsing is by line label rather than line position, so added or
// reordered counters (they vary across macOS releases) do not break it.
func parseVMStatNonEvictable(out string) (int64, bool) {
	pageSize, ok := parseVMStatPageSize(out)
	if !ok {
		return 0, false
	}

	var wired, compressor int64
	haveWired, haveCompressor := false, false

	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		label, value, found := splitVMStatLine(sc.Text())
		if !found {
			continue
		}
		switch label {
		case "Pages wired down":
			wired, haveWired = value, true
		// "occupied by" is the physical RAM the compressor holds.
		// Deliberately NOT "Pages stored in compressor", which is the
		// larger uncompressed volume of the data held there — on the
		// machine this was developed against, 26.35 GiB stored versus
		// 10.41 GiB actually occupied. Using "stored" would over-count
		// by ~16 GiB and cause spurious refusals.
		case "Pages occupied by compressor":
			compressor, haveCompressor = value, true
		}
	}
	if !haveWired || !haveCompressor {
		return 0, false
	}
	// Purgeable pages are excluded: reclaimable by definition, so they
	// are not part of a non-evictable total.
	return (wired + compressor) * pageSize, true
}

// parseVMStatPageSize reads the page size from vm_stat's header line:
//
//	Mach Virtual Memory Statistics: (page size of 16384 bytes)
//
// The page size is not assumed — it differs between Intel (4096) and
// Apple Silicon (16384), and a wrong multiplier would be a 4x error in
// a safety check.
func parseVMStatPageSize(out string) (int64, bool) {
	const marker = "page size of "
	i := strings.Index(out, marker)
	if i < 0 {
		return 0, false
	}
	rest := out[i+len(marker):]
	j := strings.Index(rest, " ")
	if j < 0 {
		return 0, false
	}
	size, err := strconv.ParseInt(rest[:j], 10, 64)
	if err != nil || size <= 0 {
		return 0, false
	}
	return size, true
}

// splitVMStatLine parses one "Label:   12345." counter line into its
// label and value, trimming the trailing period vm_stat appends.
func splitVMStatLine(line string) (label string, value int64, ok bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", 0, false
	}
	label = strings.TrimSpace(line[:idx])
	raw := strings.TrimSpace(line[idx+1:])
	raw = strings.TrimSuffix(raw, ".")
	if raw == "" {
		return "", 0, false
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return "", 0, false
	}
	return label, v, true
}
