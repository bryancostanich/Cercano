package compaction

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

// recordingSummarizer returns a uniquely-marked summary per call and records the
// flattened input it received, so tests can assert how an algorithm fed it.
type recordingSummarizer struct {
	n      int
	inputs []string
}

func (r *recordingSummarizer) fn(_ context.Context, msgs []llm.Message) (StructuredSummary, error) {
	var b strings.Builder
	for _, m := range msgs {
		for _, blk := range m.Blocks {
			b.WriteString(blk.Text)
			b.WriteString(blk.Content)
			b.WriteByte('\n')
		}
	}
	r.inputs = append(r.inputs, b.String())
	id := r.n
	r.n++
	return StructuredSummary{Goal: fmt.Sprintf("SUM%d", id), State: fmt.Sprintf("SUM%d", id)}, nil
}

func TestRolling_ThreadsPriorSummaryAndKeepsRecent(t *testing.T) {
	// 6 older text messages + 2 recent; tiny segment budget forces >1 segment.
	var raw []llm.Message
	for i := 0; i < 6; i++ {
		raw = append(raw, textMsg(llm.RoleUser, fmt.Sprintf("older message number %d here", i)))
	}
	raw = append(raw, textMsg(llm.RoleAssistant, "RECENT-A"), textMsg(llm.RoleUser, "RECENT-B"))

	rec := &recordingSummarizer{}
	res, err := RollingCompactor{}.Compact(context.Background(), raw, rec.fn,
		Budget{VerbatimRecent: 2, SegmentTokens: 6})
	if err != nil {
		t.Fatal(err)
	}
	if rec.n < 2 {
		t.Fatalf("expected multiple sequential summarize calls, got %d", rec.n)
	}
	// The 2nd+ call must contain the prior summary marker (threading).
	if !strings.Contains(rec.inputs[1], "SUM0") {
		t.Errorf("rolling did not thread prior summary into call 2:\n%s", rec.inputs[1])
	}
	// Recent window kept verbatim in the send-view.
	flat := flattenText(res.SendView)
	if !strings.Contains(flat, "RECENT-A") || !strings.Contains(flat, "RECENT-B") {
		t.Errorf("recent window not kept verbatim:\n%s", flat)
	}
	if !llm.IsValidPairing(res.SendView) {
		t.Error("send-view must be pairing-valid")
	}
}
