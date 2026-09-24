package compaction

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

// TestAuditGateAgainstRecordedSummaries replays the ten real summarizer outputs
// from dispatch a4bfafd8578002fc6b9ab73c through the current gate and prints the
// verdict, the rule that fired, and the full summary text for each.
//
// This is the check that should have preceded the live reruns: it answers
// "is the gate's judgment correct?" without a provider, cost, or latency.
// Run with: go test ./internal/compaction -run TestAuditGate -v
func TestAuditGateAgainstRecordedSummaries(t *testing.T) {
	data, err := os.ReadFile("testdata/recorded_summaries.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ResponseSeq     int    `json:"response_seq"`
		Iteration       int    `json:"iteration"`
		Output          string `json:"recorded_output"`
		TranscriptChars int    `json:"transcript_chars"`
		HasCode         bool   `json:"has_code"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}

	task := "Investigate grading-only aperture reuse and implement it."
	var rejected, accepted int

	for _, c := range cases {
		parsed := ParseSummary(c.Output)
		span := codeBearingSpan()
		if !c.HasCode {
			span = proseSpan()
		}
		err := ValidateWorkingMemory(span, parsed, task)

		verdict, cause := "ACCEPT", ""
		if err != nil {
			verdict = "REJECT"
			var rej *RejectionError
			if errors.As(err, &rej) {
				cause = string(rej.Cause)
			}
			rejected++
		} else {
			accepted++
		}

		t.Logf("\n=== seq %d (iter %d) | %s %s | span %d chars, code=%v ===\n"+
			"  parsed: goal=%q findings=%d files=%d open=%d state=%q\n"+
			"  ---- full recorded output ----\n%s\n  ------------------------------",
			c.ResponseSeq, c.Iteration, verdict, cause,
			c.TranscriptChars, c.HasCode,
			parsed.Goal, len(parsed.Findings), len(parsed.Files), len(parsed.OpenThreads), parsed.State,
			indent(c.Output))
	}
	t.Logf("\nTOTAL: %d rejected, %d accepted, of %d recorded summaries", rejected, accepted, len(cases))
}

func indent(s string) string {
	return "    " + strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n    ")
}

func codeBearingSpan() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "r", ToolName: "Read", ToolInput: []byte(`{"path":"site_mesh_job.rs"}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "r", Content: strings.Repeat("pub struct SiteMeshJobKey { grading: GradingSnapshot, terrain_epoch: TerrainEpoch }\npub fn build_site_mesh_for_key_with_stats_cancellable() {}\n", 4)}}},
	}
}

func proseSpan() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Let's look at the renderer next."}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Sure, I'll start with the job runner."}}},
	}
}
