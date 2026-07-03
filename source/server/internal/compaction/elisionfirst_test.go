package compaction

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

func TestElisionFirst_DeterministicAndLossy(t *testing.T) {
	raw := []llm.Message{bUser("fix the parser bug in scan.go")}
	for i := 1; i <= 8; i++ {
		id := fmt.Sprintf("t%d", i)
		raw = append(raw, bToolCall(id, "read", fmt.Sprintf(`{"path":"f%d.go"}`, i)))
		raw = append(raw, bToolResult(id, fmt.Sprintf("UNIQUE-CONTENT-%d for file %d", i, i)))
	}
	raw = append(raw, bAssistant("done, the parser handles EOF now"))

	mustNotCall := func(context.Context, []llm.Message) (StructuredSummary, error) {
		t.Fatal("elision-first must make zero model calls")
		return StructuredSummary{}, nil
	}

	res, err := ElisionFirstCompactor{KeepLast: 2}.Compact(context.Background(), raw,
		mustNotCall, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	flat := flattenText(res.SendView)
	// The last 2 tool results survive; earlier ones are stubbed away.
	for _, want := range []string{"UNIQUE-CONTENT-7", "UNIQUE-CONTENT-8", "fix the parser bug", "handles EOF"} {
		if !strings.Contains(flat, want) {
			t.Errorf("send-view lost %q", want)
		}
	}
	for i := 1; i <= 6; i++ {
		if strings.Contains(flat, fmt.Sprintf("UNIQUE-CONTENT-%d ", i)) {
			t.Errorf("tool result %d should have been stubbed", i)
		}
	}
	if !llm.IsValidPairing(res.SendView) {
		t.Error("send-view must be pairing-valid")
	}
}

func TestElisionFirst_TruncatesToTarget(t *testing.T) {
	raw := []llm.Message{bUser("the goal line")}
	for i := 0; i < 20; i++ {
		raw = append(raw, bAssistant(fmt.Sprintf("filler message number %d with some extra words", i)))
	}
	tok := contextmeter.Default()
	rawTok := TotalTokens(tok, raw)
	target := rawTok / 3

	res, err := ElisionFirstCompactor{}.Compact(context.Background(), raw, nil,
		Budget{TargetTokens: target})
	if err != nil {
		t.Fatal(err)
	}
	sent := TotalTokens(tok, res.SendView)
	if sent >= rawTok {
		t.Errorf("no truncation happened: sent=%d raw=%d", sent, rawTok)
	}
	// The protected head (the goal) must survive truncation.
	if !strings.Contains(flattenText(res.SendView), "the goal line") {
		t.Error("protected head was truncated away")
	}
}
