package compaction

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestReduce_SingleIsMerge_NoModel(t *testing.T) {
	calls := 0
	fake := func(context.Context, []llm.Message) (StructuredSummary, error) {
		calls++
		return StructuredSummary{}, nil
	}
	out, err := Reduce(context.Background(), []StructuredSummary{{Goal: "only"}}, fake)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Errorf("single part should not call the model, got %d calls", calls)
	}
	if out.Goal != "only" {
		t.Errorf("single part should pass through, got %q", out.Goal)
	}
}

func TestReduce_MultiCallsModelWithRenderedParts(t *testing.T) {
	var seen string
	fake := func(_ context.Context, m []llm.Message) (StructuredSummary, error) {
		for _, msg := range m {
			for _, b := range msg.Blocks {
				seen += b.Text
			}
		}
		return StructuredSummary{Goal: "reduced"}, nil
	}
	out, err := Reduce(context.Background(),
		[]StructuredSummary{{Goal: "g1"}, {Goal: "g2"}}, fake)
	if err != nil {
		t.Fatal(err)
	}
	if out.Goal != "reduced" {
		t.Errorf("multi part should use the model reduce, got %q", out.Goal)
	}
	if !strings.Contains(seen, "g1") || !strings.Contains(seen, "g2") {
		t.Errorf("reduce input should contain both rendered parts, saw: %s", seen)
	}
}
