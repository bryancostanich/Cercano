//go:build darwin

package sysram

import "testing"

// realVMStatOutput is verbatim output captured on the 128 GiB Apple
// Silicon machine this guard was written for, with one GLM-4.5-Air
// llama-server live. Derived figures were cross-checked against `top`:
// wired 74.98 GiB vs top's "75G wired", compressor 10.41 GiB vs top's
// "10G compressor". See efforts/llama-server-memory-guard/evidence-vmstat.md.
const realVMStatOutput = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                   101177.
Pages active:                                1305470.
Pages inactive:                              1298924.
Pages speculative:                              5386.
Pages throttled:                                   0.
Pages wired down:                            4913635.
Pages purgeable:                               22890.
"Translation faults":                       63680459.
Pages copy-on-write:                         1284081.
Pages zero filled:                          52550573.
Pages reactivated:                           2213071.
Pages purged:                                 294860.
File-backed pages:                            761229.
Anonymous pages:                             1848551.
Pages stored in compressor:                  1726986.
Pages occupied by compressor:                 682369.
Decompressions:                              1267514.
Compressions:                                3711029.
Pageins:                                     9275107.
Pageouts:                                       4965.
Swapins:                                           0.
Swapouts:                                          0.
`

func TestParseVMStatNonEvictable_MatchesCapturedMachine(t *testing.T) {
	got, ok := parseVMStatNonEvictable(realVMStatOutput)
	if !ok {
		t.Fatal("parse failed on real vm_stat output")
	}
	// (4913635 wired + 682369 compressor-occupied) * 16384
	const want = int64(4913635+682369) * 16384
	if got != want {
		t.Fatalf("NonEvictable = %d, want %d", got, want)
	}
	// Sanity: ~85.39 GiB on a 128 GiB machine.
	const gib = 1 << 30
	if got < 80*gib || got > 90*gib {
		t.Errorf("got %.2f GiB, expected ~85.4 GiB", float64(got)/gib)
	}
}

// The compressor has two counters and picking the wrong one over-counts
// by ~16 GiB, which would cause spurious refusals. This pins the choice.
func TestParseVMStatNonEvictable_UsesOccupiedNotStored(t *testing.T) {
	got, ok := parseVMStatNonEvictable(realVMStatOutput)
	if !ok {
		t.Fatal("parse failed")
	}
	stored := int64(4913635+1726986) * 16384
	if got == stored {
		t.Fatal("used 'Pages stored in compressor'; must use 'Pages occupied by compressor' (physical RAM held)")
	}
}

// Page size differs between Intel (4096) and Apple Silicon (16384); a
// hardcoded multiplier would be a 4x error in a safety check.
func TestParseVMStatNonEvictable_HonorsReportedPageSize(t *testing.T) {
	intelStyle := `Mach Virtual Memory Statistics: (page size of 4096 bytes)
Pages wired down:                                 100.
Pages occupied by compressor:                      50.
`
	got, ok := parseVMStatNonEvictable(intelStyle)
	if !ok {
		t.Fatal("parse failed")
	}
	if want := int64(150 * 4096); got != want {
		t.Fatalf("got %d, want %d (must use the reported 4096 page size)", got, want)
	}
}

func TestParseVMStatNonEvictable_SoftFailsOnGarbage(t *testing.T) {
	cases := map[string]string{
		"empty":              "",
		"no header":          "Pages wired down: 100.\nPages occupied by compressor: 50.\n",
		"missing wired":      "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages occupied by compressor: 50.\n",
		"missing compressor": "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages wired down: 100.\n",
		"unparsable counter": "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages wired down: lots.\nPages occupied by compressor: 50.\n",
		"zero page size":     "Mach Virtual Memory Statistics: (page size of 0 bytes)\nPages wired down: 100.\nPages occupied by compressor: 50.\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := parseVMStatNonEvictable(in)
			if ok {
				t.Fatalf("expected soft-fail, got ok with %d bytes", got)
			}
			if got != 0 {
				t.Errorf("failed probe returned %d, want 0", got)
			}
		})
	}
}

// The live probe must agree with Total(): non-evictable memory is a
// subset of physical RAM, and a probe reporting more would mean the
// parse or the page-size multiply is wrong.
func TestNonEvictable_LiveProbeIsPlausible(t *testing.T) {
	got, ok := NonEvictable()
	if !ok {
		t.Skip("vm_stat unavailable in this environment")
	}
	if got <= 0 {
		t.Fatalf("NonEvictable = %d, want positive", got)
	}
	total := Total()
	if total <= 0 {
		t.Skip("Total() unavailable")
	}
	if got >= total {
		t.Fatalf("NonEvictable %d >= Total %d; probe is wrong", got, total)
	}
}
