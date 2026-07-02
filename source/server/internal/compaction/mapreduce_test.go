package compaction

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func mapReduceRaw() []llm.Message {
	var raw []llm.Message
	for i := 0; i < 6; i++ {
		raw = append(raw, textMsg(llm.RoleUser, fmt.Sprintf("older message number %d here", i)))
	}
	return append(raw, textMsg(llm.RoleAssistant, "RECENT-A"), textMsg(llm.RoleUser, "RECENT-B"))
}

func TestMapReduce_MapCallsRawOnly(t *testing.T) {
	rec := &recordingSummarizer{}
	res, err := MapReduceCompactor{}.Compact(
		context.Background(), mapReduceRaw(), rec.fn, Budget{VerbatimRecent: 2, SegmentTokens: 6})
	if err != nil {
		t.Fatal(err)
	}
	// Each map call sees only a raw segment — never a prior SUM marker. The
	// reduce step is deterministic (MergeSummaries) and makes no model call.
	for i, in := range rec.inputs {
		if strings.Contains(in, "SUM") {
			t.Errorf("map call %d saw a prior summary (should be raw only):\n%s", i, in)
		}
	}
	if !llm.IsValidPairing(res.SendView) {
		t.Error("send-view must be pairing-valid")
	}
}
