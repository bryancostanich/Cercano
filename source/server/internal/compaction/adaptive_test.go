package compaction

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestAdaptive_PinsHeadAndTailVerbatim(t *testing.T) {
	raw := []llm.Message{
		bUser("PINNED-ASK fix the flaky retry loop"),
		bAssistant("PINNED-PLAN start with the backoff"),
	}
	for i := 0; i < 6; i++ {
		raw = append(raw, bUser(fmt.Sprintf("middle message number %d here", i)))
	}
	raw = append(raw, bAssistant("RECENT-A"), bUser("RECENT-B"))

	rec := &recordingSummarizer{}
	res, err := AdaptiveCompactor{KeepFirst: 2}.Compact(context.Background(), raw, rec.fn,
		Budget{VerbatimRecent: 2, SegmentTokens: 6})
	if err != nil {
		t.Fatal(err)
	}

	flat := flattenText(res.SendView)
	for _, want := range []string{"PINNED-ASK", "PINNED-PLAN", "RECENT-A", "RECENT-B"} {
		if !strings.Contains(flat, want) {
			t.Errorf("send-view lost verbatim marker %q:\n%s", want, flat)
		}
	}
	// The pinned head must never reach the summarizer.
	for i, in := range rec.inputs {
		if strings.Contains(in, "PINNED-ASK") {
			t.Errorf("summarize call %d received pinned head content:\n%s", i, in)
		}
	}
	if rec.n == 0 {
		t.Error("middle span was never summarized")
	}
	if !llm.IsValidPairing(res.SendView) {
		t.Error("send-view must be pairing-valid")
	}
}

func TestSplitPinnedHead_ExtendsPastToolResult(t *testing.T) {
	msgs := []llm.Message{
		bUser("ask"),
		bToolCall("t1", "read", `{"path":"a.go"}`),
		bToolResult("t1", "contents of a.go"),
		bAssistant("done"),
	}
	head, rest := splitPinnedHead(msgs, 2)
	if len(head) != 3 {
		t.Fatalf("pin boundary should extend past the tool_result: head=%d", len(head))
	}
	if len(rest) != 1 || !llm.IsValidPairing(head) {
		t.Errorf("bad split: head=%d rest=%d pairingValid=%v", len(head), len(rest), llm.IsValidPairing(head))
	}
}

func TestBuildAdaptivePrompt_Invariants(t *testing.T) {
	prompt := BuildAdaptivePrompt([]llm.Message{bUser("please rename models.Resolve")})
	for _, want := range []string{
		"NEVER invent",
		"omit the section entirely",
		"PROPOSALS:",
		"DECISIONS:",
		"models.Resolve",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("adaptive prompt missing %q", want)
		}
	}
}
