package compaction

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestElisionCompactor_DedupesNoModel(t *testing.T) {
	raw := []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{toolUse("u1", "read", `{"path":"a"}`)}},
		{Role: llm.RoleUser, Blocks: []llm.Block{toolResult("u1", "OLD")}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{toolUse("u2", "read", `{"path":"a"}`)}},
		{Role: llm.RoleUser, Blocks: []llm.Block{toolResult("u2", "NEW")}},
	}
	modelCalls := 0
	fake := func(ctx context.Context, _ []llm.Message) (StructuredSummary, error) {
		modelCalls++
		return StructuredSummary{}, nil
	}
	res, err := ElisionCompactor{}.Compact(context.Background(), raw, fake, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if modelCalls != 0 {
		t.Errorf("elision baseline must make no model calls, made %d", modelCalls)
	}
	if !llm.IsValidPairing(res.SendView) {
		t.Error("send-view must be pairing-valid")
	}
	flat := ""
	for _, m := range res.SendView {
		for _, b := range m.Blocks {
			flat += b.Content
		}
	}
	if strings.Contains(flat, "OLD") || !strings.Contains(flat, "NEW") {
		t.Errorf("expected superseded OLD stubbed, NEW kept:\n%s", flat)
	}
}
