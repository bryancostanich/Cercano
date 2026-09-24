package compaction

import (
	"errors"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

// Each rejection site must be individually identifiable. A single opaque error
// made a live run undiagnosable: the log could not distinguish "the gate
// rejected this summary" from "the provider failed".
func TestRejectionCarriesDistinctCause(t *testing.T) {
	for _, tc := range []struct {
		name    string
		summary StructuredSummary
		intent  string
		want    RejectionCause
	}{
		{
			name:    "meta objective",
			summary: StructuredSummary{Goal: "Summarize the conversation span for later reference", State: "Summary prepared."},
			want:    RejectMetaObjective,
		},
		{
			name:    "invented idle state",
			summary: StructuredSummary{Goal: "Implement aperture reuse", State: "awaiting further instruction"},
			want:    RejectIdleState,
		},
		{
			name:    "idle open thread",
			summary: StructuredSummary{Goal: "Implement aperture reuse", OpenThreads: []string{"No pending actions remain"}, State: "Implementation pending"},
			want:    RejectIdleOpenThread,
		},
		{
			name:    "receipt only",
			summary: StructuredSummary{Goal: "Implement aperture reuse", Files: map[string]string{"job.rs": "[verified] source sha256:8fb51aa3"}, State: "Implementation pending"},
			want:    RejectNoFindings,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateWorkingMemory(inspectedSpan(), tc.summary, tc.intent)
			if !errors.Is(err, ErrUnhelpfulSummary) {
				t.Fatalf("err = %v, want a quality rejection", err)
			}
			var rej *RejectionError
			if !errors.As(err, &rej) {
				t.Fatalf("err = %v, want a *RejectionError carrying a cause", err)
			}
			if rej.Cause != tc.want {
				t.Fatalf("cause = %q, want %q", rej.Cause, tc.want)
			}
			if rej.Cause.String() == "" {
				t.Fatal("cause has no stable log code")
			}
		})
	}
}

// The rejected summary must be recoverable for diagnosis, without dumping the
// transcript or unbounded model text into logs.
func TestRejectionCarriesBoundedEvidence(t *testing.T) {
	long := "GOAL: Summarize the conversation span\nSTATE: " + strings.Repeat("filler ", 400)
	err := ValidateWorkingMemory(inspectedSpan(), ParseSummary(long), "")
	var rej *RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("err = %v, want *RejectionError", err)
	}
	if rej.Evidence == "" {
		t.Fatal("rejection dropped the summary text; the gate stays unfalsifiable")
	}
	if len(rej.Evidence) > maxRejectionEvidence {
		t.Fatalf("evidence = %d bytes, want <= %d", len(rej.Evidence), maxRejectionEvidence)
	}
	if !strings.Contains(rej.Error(), string(RejectMetaObjective)) {
		t.Fatalf("error text %q omits the cause", rej.Error())
	}
}

// An accepted summary must not allocate a rejection.
func TestAcceptedSummaryHasNoRejection(t *testing.T) {
	good := StructuredSummary{
		Goal:     "Implement grading-only aperture reuse",
		Findings: []string{"job.rs: SiteMeshJobKey carries grading and terrain_epoch; non-grading inputs must invalidate reuse."},
		State:    "Implementation pending",
	}
	if err := ValidateWorkingMemory(inspectedSpan(), good, ""); err != nil {
		t.Fatalf("substantive summary rejected: %v", err)
	}
}

func inspectedSpan() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ToolUseID: "r", ToolName: "Read", ToolInput: []byte(`{"path":"job.rs"}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "r", Content: strings.Repeat("pub struct SiteMeshJobKey { grading: GradingSnapshot }\npub fn build_site_mesh_for_key() {}\n", 6)}}},
	}
}
