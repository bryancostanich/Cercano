package compaction

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestGroundedBullets(t *testing.T) {
	source := "user: we agreed to use models.Resolve(tier, preferredProvider)\n" +
		"assistant: the config lives under models.tiers in config.yaml\n"
	sum := StructuredSummary{
		Decisions: []string{
			"we agreed to use models.Resolve(tier, preferredProvider)",   // exact
			"the config lives   under models.tiers",                      // whitespace-differing, still grounded
			`"we agreed to use models.Resolve(tier, preferredProvider)"`, // model quote-wrapped, still grounded
			`the config lives\nunder models.tiers`,                       // JSON-escaped newline, still grounded
		},
		Proposals:   []string{"switch the CLI to protobuf-over-websocket"}, // fabricated
		OpenThreads: []string{"config.yaml"},                               // exact
		Files:       map[string]string{"config.yaml": "not in source at all"},
	}
	grounded, total := GroundedBullets(sum, source)
	if total != 7 {
		t.Fatalf("total = %d, want 7", total)
	}
	if grounded != 5 {
		t.Errorf("grounded = %d, want 5", grounded)
	}
}

func TestExtractive_KeepsRecentAndPairing(t *testing.T) {
	var raw []llm.Message
	for i := 0; i < 6; i++ {
		raw = append(raw, bUser(fmt.Sprintf("older message number %d here", i)))
	}
	raw = append(raw, bAssistant("RECENT-A"), bUser("RECENT-B"))

	rec := &recordingSummarizer{}
	res, err := ExtractiveCompactor{}.Compact(context.Background(), raw, rec.fn,
		Budget{VerbatimRecent: 2, SegmentTokens: 6})
	if err != nil {
		t.Fatal(err)
	}
	flat := flattenText(res.SendView)
	if !strings.Contains(flat, "RECENT-A") || !strings.Contains(flat, "RECENT-B") {
		t.Errorf("recent window not kept verbatim:\n%s", flat)
	}
	if rec.n == 0 {
		t.Error("older span was never summarized")
	}
	if !llm.IsValidPairing(res.SendView) {
		t.Error("send-view must be pairing-valid")
	}
}

func TestBuildExtractivePrompt_Invariants(t *testing.T) {
	prompt := BuildExtractivePrompt([]llm.Message{bUser("keep KeepLastNToolResults intact")})
	for _, want := range []string{
		"character-for-character",
		"Do not paraphrase",
		"Never invent a quote",
		"DECISIONS:",
		"PROPOSALS:",
		"keep KeepLastNToolResults intact",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("extractive prompt missing %q", want)
		}
	}
}
