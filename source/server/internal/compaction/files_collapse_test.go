package compaction

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Recorded summary seq 141 wrote six FILES rows: four distinct observations of
// site_mesh_job.rs and two of mesh_mode.rs. StructuredSummary.Files is keyed by
// path, so same-path rows overwrite. This measures how much of that real model
// output survives parsing.
func TestParseCollapsesDistinctSameFileObservations(t *testing.T) {
	raw := recordedOutput(t, 141)

	var written int
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- ") && strings.Contains(line, ".rs:") {
			written++
		}
	}
	if written != 6 {
		t.Fatalf("fixture drift: expected 6 FILES rows in seq 141, found %d", written)
	}

	parsed := ParseSummary(raw)
	t.Logf("model wrote %d file observations; parser retained %d", written, len(parsed.Files))
	for path, state := range parsed.Files {
		t.Logf("  survivor: ...%s -> %s", path[strings.LastIndex(path, "/")+1:], state)
	}

	if len(parsed.Files) == written {
		t.Fatal("no collapse observed; this test no longer describes the bug")
	}

	// Name what was lost: the grep that located the rebuild entry point.
	if strings.Contains(raw, "build_site_mesh_for_key_with_stats_cancellable") {
		var survives bool
		for _, state := range parsed.Files {
			if strings.Contains(state, "build_site_mesh_for_key_with_stats_cancellable") {
				survives = true
			}
		}
		if !survives {
			t.Logf("LOST: the grep locating build_site_mesh_for_key_with_stats_cancellable")
		}
	}
}

// Collapse must also be shown at the merge seam, since a later, weaker entry can
// overwrite an earlier, more informative one for the same path.
func TestMergeOverwritesEarlierFileObservation(t *testing.T) {
	early := StructuredSummary{Files: map[string]string{
		"job.rs": "[verified] read lines 560-700; JobKey carries grading and terrain_epoch",
	}}
	late := StructuredSummary{Files: map[string]string{
		"job.rs": "[verified] source sha256:3be84034",
	}}

	merged := MergeSummaries([]StructuredSummary{early, late})
	got := merged.Files["job.rs"]
	t.Logf("after merge, job.rs = %q", got)

	if strings.Contains(got, "JobKey carries grading") {
		t.Fatal("no overwrite observed; this test no longer describes the bug")
	}
	if !strings.Contains(got, "sha256") {
		t.Fatalf("unexpected merge result: %q", got)
	}
}

// Findings are the replacement channel for observations, so they must accumulate
// where Files overwrite. This is the contrast that makes the fix direction clear.
func TestFindingsAccumulateWhereFilesOverwrite(t *testing.T) {
	a := StructuredSummary{Findings: []string{"job.rs: JobKey carries grading and terrain_epoch"}}
	b := StructuredSummary{Findings: []string{"job.rs: cancellation returns without publishing"}}

	merged := MergeSummaries([]StructuredSummary{a, b})
	if len(merged.Findings) != 2 {
		t.Fatalf("findings = %d, want both retained: %v", len(merged.Findings), merged.Findings)
	}
}

func recordedOutput(t *testing.T, seq int) string {
	t.Helper()
	data, err := os.ReadFile("testdata/recorded_summaries.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ResponseSeq int    `json:"response_seq"`
		Output      string `json:"recorded_output"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.ResponseSeq == seq {
			return c.Output
		}
	}
	t.Fatalf("recorded summary seq %d not found", seq)
	return ""
}
