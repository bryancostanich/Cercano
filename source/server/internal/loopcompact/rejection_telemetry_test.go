package loopcompact

import (
	"errors"
	"strings"
	"testing"

	"cercano/source/server/internal/compaction"
)

// A gate rejection must be distinguishable from a provider failure in the pass
// log. Both previously collapsed to "summarizer_error", which made a live run
// undiagnosable.
func TestRejectionReasonIsDistinctFromProviderFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"meta objective", &compaction.RejectionError{Cause: compaction.RejectMetaObjective}, "summary_rejected_meta_objective"},
		{"idle state", &compaction.RejectionError{Cause: compaction.RejectIdleState}, "summary_rejected_idle_state"},
		{"idle open thread", &compaction.RejectionError{Cause: compaction.RejectIdleOpenThread}, "summary_rejected_idle_open_thread"},
		{"no findings", &compaction.RejectionError{Cause: compaction.RejectNoFindings}, "summary_rejected_no_findings"},
		{"provider failure", errors.New("connection reset"), "summarizer_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyFailure(tc.err); got != tc.want {
				t.Fatalf("classifyFailure = %q, want %q", got, tc.want)
			}
		})
	}
}

// Guard exhaustion must be visible as its own state, not as another rejection:
// they call for different responses.
func TestSuspensionHasItsOwnReason(t *testing.T) {
	err := errors.Join(compaction.ErrUnhelpfulSummary, compaction.ErrSummarySuspended)
	if got := classifyFailure(err); got != "summary_rejection_budget_exhausted" {
		t.Fatalf("classifyFailure = %q, want the exhaustion code", got)
	}
}

// The rejected summary text must never reach the shared pass log, which is
// explicitly content-free: provider and tool output must not leak there.
func TestPassLineOmitsRejectedSummaryText(t *testing.T) {
	secret := "SiteMeshJobKey carries grading and terrain_epoch"
	ev := PassEvent{
		ConversationID: "c", Iteration: 11, Enabled: true, Outcome: "failed",
		Reason: classifyFailure(&compaction.RejectionError{
			Cause:    compaction.RejectNoFindings,
			Evidence: "GOAL: x\nFINDING: " + secret,
		}),
	}
	line := FormatPassEvent(ev)
	if strings.Contains(line, secret) {
		t.Fatalf("pass log leaked summary content: %q", line)
	}
	if !strings.Contains(line, "summary_rejected_no_findings") {
		t.Fatalf("pass log lost the rejection cause: %q", line)
	}
}
