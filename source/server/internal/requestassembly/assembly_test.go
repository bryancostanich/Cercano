package requestassembly

import (
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/pkg/config"
)

type byteTokenizer struct{}

func (byteTokenizer) Count(s string) int { return len(s) }

func turn(id, role, content string, at int64) conversation.Turn {
	return conversation.Turn{ID: id, Role: role, Content: content, CreatedAt: time.Unix(at, 0)}
}

func TestEstimateRawTokensUsesLargerStoredBody(t *testing.T) {
	turns := []conversation.Turn{
		{Content: strings.Repeat("c", 8), BlocksJSON: strings.Repeat("b", 20)},
		{Content: strings.Repeat("c", 12), BlocksJSON: ""},
	}
	// Larger bodies total 20 + 12 = 32, len/4 = 8.
	if got := EstimateRawTokens(turns); got != 8 {
		t.Fatalf("EstimateRawTokens = %d, want 8", got)
	}
}

func TestWindowForPrefersConcreteRuntimeWindow(t *testing.T) {
	if got := WindowFor(Target{Model: "claude-opus-5", ContextWindow: 32_768, ContextWindowKnown: true}); got != 32_768 {
		t.Fatalf("WindowFor explicit runtime window = %d", got)
	}
	if got := WindowFor(Target{Model: "claude-opus-5"}); got != 200_000 {
		t.Fatalf("WindowFor model lookup = %d, want 200000", got)
	}
}

func TestWindowForTargetReportsCertainty(t *testing.T) {
	if got, known := WindowForTarget(Target{Model: "claude-opus-5"}); got != 200_000 || !known {
		t.Fatalf("WindowForTarget claude = %d/%v, want 200000/true", got, known)
	}
	if got, known := WindowForTarget(Target{Model: "gpt-5.5"}); got != 128_000 || known {
		t.Fatalf("WindowForTarget unknown openai = %d/%v, want 128000/false", got, known)
	}
	if got, known := WindowForTarget(Target{Model: "unknown", ContextWindow: 65_536, ContextWindowKnown: true}); got != 65_536 || !known {
		t.Fatalf("WindowForTarget explicit = %d/%v, want 65536/true", got, known)
	}
}

// TestAssembleReportsMessagesOnlyEstimate pins the honest contract of
// assembly-time accounting: it covers the messages it assembled and nothing
// else. The system prompt and tool schemas are not knowable here, so the
// decomposed fields stay zero rather than carrying a fabricated value. Callers
// wanting the true request size read the request.budget routing event.
func TestAssembleReportsMessagesOnlyEstimate(t *testing.T) {
	turns := []conversation.Turn{
		{Role: "user", Content: "hello there"},
		{Role: "assistant", Content: "general kenobi"},
	}
	res := Assemble(turns, conversation.Compaction{}, config.CompactionConfig{}, 0,
		Target{Model: "claude-opus-5"}, byteTokenizer{})
	acct := res.Accounting

	if acct.MessageTokens == 0 {
		t.Fatal("message tokens should be counted")
	}
	if acct.EstimatedRequestTokens != acct.MessageTokens {
		t.Fatalf("estimate = %d, want it to equal message tokens %d",
			acct.EstimatedRequestTokens, acct.MessageTokens)
	}
	if acct.SystemTokens != 0 || acct.ToolSchemaTokens != 0 || acct.OutputReserveTokens != 0 {
		t.Fatalf("assembly cannot know system/tool/reserve costs; want zeros, got %+v", acct)
	}
}

func TestAssembleHardLimitUsesConcreteTargetWindow(t *testing.T) {
	turns := []conversation.Turn{
		turn("t1", "user", strings.Repeat("a", 40), 1),
		turn("t2", "assistant", strings.Repeat("b", 40), 2),
		turn("t3", "user", strings.Repeat("c", 40), 3),
	}
	res := Assemble(turns, conversation.Compaction{}, config.CompactionConfig{
		Enabled:         true,
		HardOverridePct: 0.5,
	}, 0, Target{Model: "claude-opus-5", ContextWindow: 100}, byteTokenizer{})

	if res.Accounting.Window != 100 || res.Accounting.HardLimit != 50 {
		t.Fatalf("window/hard = %d/%d, want 100/50", res.Accounting.Window, res.Accounting.HardLimit)
	}
	if !res.Accounting.Scheduled || !res.Accounting.Truncated {
		t.Fatalf("expected hard override truncation, accounting=%+v", res.Accounting)
	}
	if res.Accounting.FinalTokens > res.Accounting.HardLimit {
		t.Fatalf("final tokens %d exceed hard limit %d", res.Accounting.FinalTokens, res.Accounting.HardLimit)
	}
	if len(res.Messages) == len(turns) {
		t.Fatalf("expected at least one old message to be dropped")
	}
}
