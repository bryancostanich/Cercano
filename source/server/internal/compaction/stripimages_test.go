package compaction

import (
	"strings"
	"testing"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

func bigImage(n int) llm.Block {
	return llm.Block{Type: llm.BlockImage, MediaType: "image/png", ImageData: strings.Repeat("A", n)}
}

func TestStripImagesPreservesMessageCountAndOrder(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: "first"}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{bigImage(1000)}}, // image-only
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "third"}}},
	}
	out, n := StripImagesForSummary(in)
	if len(out) != len(in) {
		t.Fatalf("message count changed: got %d want %d", len(out), len(in))
	}
	if n != 1 {
		t.Fatalf("stripped count: got %d want 1", n)
	}
	if out[0].Blocks[0].Text != "first" || out[2].Blocks[0].Text != "third" {
		t.Fatal("order not preserved")
	}
	// The image-only message must survive as a real message, not vanish.
	if len(out[1].Blocks) != 1 || out[1].Blocks[0].Type != llm.BlockText {
		t.Fatalf("image-only message not preserved as text: %+v", out[1].Blocks)
	}
	if out[1].Role != llm.RoleUser {
		t.Fatal("role not preserved")
	}
}

func TestStripImagesDoesNotMutateInput(t *testing.T) {
	in := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{bigImage(64)}}}
	_, _ = StripImagesForSummary(in)
	if in[0].Blocks[0].Type != llm.BlockImage || in[0].Blocks[0].ImageData == "" {
		t.Fatal("input was mutated")
	}
}

func TestStripImagesCollapsesTokenCost(t *testing.T) {
	tok := contextmeter.Default()
	// 7.4 MB base64, the size observed on real screenshot turns.
	in := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{bigImage(7_400_000)}}}
	before := TotalTokens(tok, in)
	out, _ := StripImagesForSummary(in)
	after := TotalTokens(tok, out)
	if before < 1_000_000 {
		t.Fatalf("precondition: expected huge pre-strip charge, got %d", before)
	}
	if after > 100 {
		t.Fatalf("post-strip charge still large: %d", after)
	}
}

// An image block alone always fits the summary prompt (BuildSummaryPrompt
// renders it as "[image]"), so splitOversizedMessageForSummary already peels
// it off into its own part rather than deferring. Stripping does not rescue an
// otherwise-stuck split — it removes the payload from every measurement before
// that point. Pin both halves of that so the rationale can't silently rot.
func TestImageBlockAloneAlwaysFitsSummaryPrompt(t *testing.T) {
	alone := llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{bigImage(7_400_000)}}
	b := EstimateSummaryBudget(BuildSummaryPrompt([]llm.Message{alone}), 1024, 8192)
	if !b.Fits {
		t.Fatalf("image alone should fit the summary prompt, got %d tokens", b.PromptTokens)
	}
}

func TestStrippedMessageStillSplitsAndDropsPayload(t *testing.T) {
	msg := llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockToolResult, Content: strings.Repeat("lorem ipsum dolor sit amet ", 4000)},
		bigImage(20000),
	}}
	out, n := StripImagesForSummary([]llm.Message{msg})
	if n != 1 {
		t.Fatalf("stripped count: got %d want 1", n)
	}
	parts, err := splitOversizedMessageForSummary(out[0], 8192, 1024)
	if err != nil {
		t.Fatalf("stripped message unsplittable: %v", err)
	}
	if len(parts) < 2 {
		t.Fatalf("expected split into multiple parts, got %d", len(parts))
	}
	for _, p := range parts {
		for _, b := range p.Blocks {
			if b.Type == llm.BlockImage || b.ImageData != "" {
				t.Fatal("image payload survived into a split part")
			}
		}
	}
}

// Stripping must not change the prompt, since BuildSummaryPrompt already
// rendered images as the same placeholder.
func TestStripIsPromptNeutral(t *testing.T) {
	in := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
		{Type: llm.BlockText, Text: "look:"},
		bigImage(500),
	}}}
	out, _ := StripImagesForSummary(in)
	if BuildSummaryPrompt(in) != BuildSummaryPrompt(out) {
		t.Fatal("stripping changed the summary prompt")
	}
}
