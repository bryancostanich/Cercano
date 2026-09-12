package llm

import (
	"context"
	"errors"
	"testing"
)

func TestTokenUsageCumulativePresence(t *testing.T) {
	u := TokenUsage{Input: ReportedTokens(11), Output: ReportedTokens(7), CacheRead: ReportedTokens(3)}
	if got := u.Merge(u); got != u {
		t.Fatal("repeated snapshot was added")
	}
	next := u.Merge(TokenUsage{Output: ReportedTokens(0)})
	if next.Input.Value != 11 || next.Output.Value != 0 || !next.Output.Known || next.CacheRead.Value != 3 {
		t.Fatalf("presence merge: %+v", next)
	}
	if ReportedTokens(-1).Known {
		t.Fatal("negative count accepted")
	}
	if (TokenUsage{}).TotalsKnown() {
		t.Fatal("unknown totals treated as zero")
	}
}

type accountingCollectReader struct {
	index   int
	failure error
}

func (r *accountingCollectReader) Next() (StreamEvent, bool, error) {
	r.index++
	if r.index == 1 {
		return StreamEvent{Type: EventMessageStart, Usage: TokenUsage{Input: ReportedTokens(11)}}, true, nil
	}
	return StreamEvent{Usage: TokenUsage{Output: ReportedTokens(7)}}, false, r.failure
}
func (*accountingCollectReader) Close() error { return nil }

func TestCollectStreamRetainsUsageOnErrorEvent(t *testing.T) {
	failure := errors.New("failed after usage")
	out, err := CollectStream(context.Background(), &accountingCollectReader{failure: failure}, nil, nil)
	if !errors.Is(err, failure) {
		t.Fatalf("error=%v", err)
	}
	if out.Usage.Input != ReportedTokens(11) || out.Usage.Output != ReportedTokens(7) {
		t.Fatalf("lost partial usage: %+v", out.Usage)
	}
}
