package compaction

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

// Real summarizer outputs from dispatch a4bfafd8578002fc6b9ab73c, whose worker
// reread source it had already inspected after compaction replaced the
// declarations with file/hash receipts. Only the model's own output text is
// stored; no transcript source, reasoning, or credentials.
type recordedSummary struct {
	ResponseSeq int    `json:"response_seq"`
	Iteration   int    `json:"iteration"`
	Output      string `json:"recorded_output"`
	HasCode     bool   `json:"has_code"`
}

func loadRecorded(t *testing.T) []recordedSummary {
	t.Helper()
	data, err := os.ReadFile("testdata/recorded_summaries.json")
	if err != nil {
		t.Fatal(err)
	}
	var out []recordedSummary
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 10 {
		t.Fatalf("expected the 10 recorded nonempty summaries, got %d", len(out))
	}
	return out
}

func codeInspection() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "c", ToolName: "Read", ToolInput: []byte(`{"path":"site_mesh_job.rs"}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "c", Content: strings.Repeat("pub struct SiteMeshJobKey { grading: GradingSnapshot, terrain_epoch: TerrainEpoch }\npub fn build_site_mesh_for_key_with_stats_cancellable() {}\n", 4)}}},
	}
}

func TestRecordedReceiptSummariesAreRejectedForCodeSpans(t *testing.T) {
	task := "Investigate grading-only aperture reuse and implement it."
	rejected := 0
	for _, rec := range loadRecorded(t) {
		parsed := ParseSummary(rec.Output)
		if parsed.IsEmpty() {
			t.Fatalf("seq %d: recorded output no longer parses as a summary", rec.ResponseSeq)
		}
		err := ValidateWorkingMemory(codeInspection(), parsed, task)
		if !rec.HasCode {
			continue
		}
		if err == nil {
			t.Fatalf("seq %d (iteration %d) still accepted: %q", rec.ResponseSeq, rec.Iteration, rec.Output)
		}
		rejected++
	}
	if rejected != 8 {
		t.Fatalf("expected the 8 code-span summaries rejected, got %d", rejected)
	}
}

func TestRecordedFailuresProducedNoFindingsAndInventedIdleState(t *testing.T) {
	meta, idle := 0, 0
	for _, rec := range loadRecorded(t) {
		parsed := ParseSummary(rec.Output)
		if len(parsed.Findings) != 0 {
			t.Fatalf("seq %d unexpectedly had findings", rec.ResponseSeq)
		}
		if metaObjective(parsed.Goal) {
			meta++
		}
		if idleClaim(parsed.State) {
			idle++
		}
	}
	if meta != 9 {
		t.Fatalf("meta goals=%d, want the 9 recorded summarization objectives", meta)
	}
	if idle == 0 {
		t.Fatal("expected at least one recorded idle/meta state claim")
	}
}

// The replacement contract must accept a substantive summary for the same span.
func TestWorkingMemorySummaryForSameSpanIsAccepted(t *testing.T) {
	good := ParseSummary(strings.Join([]string{
		"GOAL: Implement grading-only aperture reuse",
		"FINDINGS:",
		"- site_mesh_job.rs: SiteMeshJobKey carries grading and terrain_epoch; non-grading fields must still invalidate reuse (source sha256:abc)",
		"- build_site_mesh_for_key_with_stats_cancellable is the rebuild entry point reached per job (source sha256:def)",
		"FILES:",
		"- site_mesh_job.rs: [unverified] no modifications yet",
		"OPEN:",
		"- Reuse path not implemented; cancellation behavior must be preserved",
		"STATE: Investigation complete; implementation pending",
	}, "\n"))
	if err := ValidateWorkingMemory(codeInspection(), good, "Implement grading-only aperture reuse"); err != nil {
		t.Fatal(err)
	}
	if len(good.Findings) != 2 || good.State == "" {
		t.Fatalf("summary parse lost content: %+v", good)
	}
}
