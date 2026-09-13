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
	final   bool
}

func (r *accountingCollectReader) Next() (StreamEvent, bool, error) {
	r.index++
	if r.index == 1 {
		return StreamEvent{Type: EventMessageStart, Usage: TokenUsage{Input: ReportedTokens(11)}}, true, nil
	}
	return StreamEvent{Usage: TokenUsage{Final: r.final, Output: ReportedTokens(7)}}, false, r.failure
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

func TestUsageFinalityIsIndependentOfKnownTotals(t *testing.T) {
	u := TokenUsage{Input: ReportedTokens(11), Output: ReportedTokens(0)}
	if u.Complete() {
		t.Fatal("initial usage was final")
	}
	final := u.Merge(TokenUsage{Final: true, Output: ReportedTokens(0)})
	if !final.Complete() || final.Input != u.Input || final.Output != u.Output {
		t.Fatalf("final=%+v", final)
	}
	if final.Merge(TokenUsage{}) != final {
		t.Fatal("empty snapshot erased evidence")
	}
	if (TokenUsage{Final: true, Input: ReportedTokens(11)}).Complete() {
		t.Fatal("final boundary fabricated missing output")
	}
}

func TestCollectStreamPreservesFinalUsageDespiteLaterError(t *testing.T) {
	failure := errors.New("transport failed after final usage")
	out, err := CollectStream(t.Context(), &accountingCollectReader{failure: failure, final: true}, nil, nil)
	if !errors.Is(err, failure) || !out.Usage.Complete() {
		t.Fatalf("usage=%+v err=%v", out.Usage, err)
	}
}
