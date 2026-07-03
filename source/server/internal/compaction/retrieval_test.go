package compaction

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestRetrieval_AppendsIndexAfterSummary(t *testing.T) {
	var raw []llm.Message
	for i := 0; i < 6; i++ {
		raw = append(raw, bUser(fmt.Sprintf("older message number %d here", i)))
	}
	raw = append(raw, bAssistant("RECENT-A"), bUser("RECENT-B"))

	rec := &recordingSummarizer{}
	res, err := RetrievalCompactor{}.Compact(context.Background(), raw, rec.fn,
		Budget{VerbatimRecent: 2, SegmentTokens: 6})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SendView) < 2 {
		t.Fatalf("send-view too short: %d messages", len(res.SendView))
	}
	// Preamble first, index second.
	if !strings.Contains(flattenText(res.SendView[:1]), "SUM") {
		t.Errorf("first message should be the summary preamble:\n%s", flattenText(res.SendView[:1]))
	}
	idx := flattenText(res.SendView[1:2])
	if !strings.Contains(idx, "[recall index") {
		t.Fatalf("second message should be the recall index:\n%s", idx)
	}
	// Every summarized-away message gets an ordinal line with its gist.
	for i := 0; i < 6; i++ {
		want := fmt.Sprintf("%d user: older message number %d here", i, i)
		if !strings.Contains(idx, want) {
			t.Errorf("recall index missing line %q:\n%s", want, idx)
		}
	}
	// The verbatim tail is not indexed.
	if strings.Contains(idx, "RECENT-A") {
		t.Error("recall index should not cover the verbatim tail")
	}
	if !llm.IsValidPairing(res.SendView) {
		t.Error("send-view must be pairing-valid")
	}
}

func TestRetrieval_NameAndAllRecentPassthrough(t *testing.T) {
	if got := (RetrievalCompactor{}).Name(); got != "retrieval(rolling)" {
		t.Errorf("Name() = %q", got)
	}
	// When everything fits in the verbatim window there is nothing to index.
	raw := []llm.Message{bUser("only"), bAssistant("two")}
	res, err := RetrievalCompactor{}.Compact(context.Background(), raw,
		(&recordingSummarizer{}).fn, Budget{VerbatimRecent: 4})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(flattenText(res.SendView), "[recall index") {
		t.Error("no index expected when nothing was summarized away")
	}
}

func TestGist_TruncatesAndFlattens(t *testing.T) {
	long := strings.Repeat("wordy ", 40)
	m := bUser(long)
	g := gist(m)
	if len(g) > gistLen+len("…") {
		t.Errorf("gist too long: %d", len(g))
	}
	if strings.Contains(g, "\n") {
		t.Error("gist must be one line")
	}
}
