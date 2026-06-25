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

func TestMapReduce_Mechanical_NoPriorThreadingNoExtraCall(t *testing.T) {
	rec := &recordingSummarizer{}
	res, err := MapReduceCompactor{ModelReduce: false}.Compact(
		context.Background(), mapReduceRaw(), rec.fn, Budget{VerbatimRecent: 2, SegmentTokens: 6})
	if err != nil {
		t.Fatal(err)
	}
	// Each map call sees only a raw segment — never a prior SUM marker.
	for i, in := range rec.inputs {
		if strings.Contains(in, "SUM") {
			t.Errorf("map call %d saw a prior summary (should be raw only):\n%s", i, in)
		}
	}
	if !llm.IsValidPairing(res.SendView) {
		t.Error("send-view must be pairing-valid")
	}
}

func TestMapReduce_Model_AddsReduceCall(t *testing.T) {
	recMech := &recordingSummarizer{}
	_, _ = MapReduceCompactor{ModelReduce: false}.Compact(
		context.Background(), mapReduceRaw(), recMech.fn, Budget{VerbatimRecent: 2, SegmentTokens: 6})

	recModel := &recordingSummarizer{}
	_, err := MapReduceCompactor{ModelReduce: true}.Compact(
		context.Background(), mapReduceRaw(), recModel.fn, Budget{VerbatimRecent: 2, SegmentTokens: 6})
	if err != nil {
		t.Fatal(err)
	}
	// The model-reduce variant makes exactly one MORE call (the reduce pass).
	if recModel.n != recMech.n+1 {
		t.Errorf("model reduce should add one call: mechanical=%d model=%d", recMech.n, recModel.n)
	}
	// The final (reduce) call's input contains the prior segment summary markers.
	last := recModel.inputs[len(recModel.inputs)-1]
	if !strings.Contains(last, "SUM") {
		t.Errorf("reduce call should receive segment summaries:\n%s", last)
	}
}
