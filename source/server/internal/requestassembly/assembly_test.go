package requestassembly

import (
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
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

func TestEstimateFullRequestAddsMessagesSystemToolsAndReserve(t *testing.T) {
	base := Accounting{FinalTokens: 10}
	acct := EstimateFullRequest(base, RequestEstimateInput{
		Messages:      []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "ignored because FinalTokens is set"}}}},
		System:        "system",
		Tools:         []llm.Tool{{Name: "Read", Description: "read files", Schema: []byte(`{"type":"object"}`)}},
		OutputReserve: 7,
	}, byteTokenizer{})

	if acct.MessageTokens != 10 {
		t.Fatalf("message tokens = %d, want final token count 10", acct.MessageTokens)
	}
	if acct.SystemTokens != len("system") || acct.ToolSchemaTokens == 0 || acct.OutputReserveTokens != 7 {
		t.Fatalf("unexpected decomposed request accounting: %+v", acct)
	}
	want := acct.MessageTokens + acct.SystemTokens + acct.ToolSchemaTokens + acct.OutputReserveTokens
	if acct.EstimatedRequestTokens != want {
		t.Fatalf("estimated total = %d, want %d (%+v)", acct.EstimatedRequestTokens, want, acct)
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
