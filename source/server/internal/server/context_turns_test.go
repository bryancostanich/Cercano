package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"cercano/source/server/internal/compaction"
	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/proto"
)

func TestGetConversationTurns_SummariesAndSideEffectFree(t *testing.T) {
	srv, store := newServerWithStore(t)
	ctx := context.Background()
	convID := "conv-view"
	if err := store.EnsureConversation(ctx, convID, "", "test-model"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	useJSON, _ := json.Marshal([]llm.Block{{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "LS", ToolInput: json.RawMessage(`{"path":"."}`)}})
	resJSON, _ := json.Marshal([]llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "u1", Content: "a.go\nb.go"}})
	for _, tn := range []conversation.Turn{
		{ConversationID: convID, Role: "user", Content: "list the files please"},
		{ConversationID: convID, Role: "assistant", BlocksJSON: string(useJSON)},
		{ConversationID: convID, Role: "user", BlocksJSON: string(resJSON)},
	} {
		if err := store.Append(ctx, tn); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	resp, err := srv.GetConversationTurns(ctx, &proto.GetConversationTurnsRequest{ConversationId: convID})
	if err != nil {
		t.Fatalf("GetConversationTurns: %v", err)
	}
	if len(resp.Turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(resp.Turns))
	}
	if resp.Turns[0].Role != "user" || resp.Turns[0].Kind != "text" || resp.Turns[0].Preview == "" || resp.Turns[0].EstTokens <= 0 {
		t.Errorf("turn0 = %+v", resp.Turns[0])
	}
	if resp.Turns[1].Kind != "tool_use" || resp.Turns[1].Preview == "" {
		t.Errorf("turn1 kind/preview = %+v", resp.Turns[1])
	}
	if resp.Turns[2].Kind != "tool_result" {
		t.Errorf("turn2 kind = %q", resp.Turns[2].Kind)
	}

	// Side-effect-free: usage must be unchanged (still zero — no turn was run).
	used, _ := srv.agent.GetContextUsage(ctx, convID)
	if used != 0 {
		t.Errorf("GetConversationTurns mutated the meter: used = %d, want 0", used)
	}
}

func TestGetContextUsage_CompactedMeterCountsLiveImagesAsReferences(t *testing.T) {
	srv, store := newServerWithStore(t)
	ctx := context.Background()
	convID := "conv-compacted-image-meter"
	if err := store.EnsureConversation(ctx, convID, "", "test-model"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	frozenAt := time.Unix(100, 0)
	if err := store.Append(ctx, conversation.Turn{
		ConversationID: convID,
		Role:           "user",
		Content:        "old frozen turn",
		CreatedAt:      frozenAt,
	}); err != nil {
		t.Fatalf("append frozen: %v", err)
	}
	largeImage := strings.Repeat("A", 512*1024)
	blocksJSON, _ := json.Marshal([]llm.Block{{
		Type:      llm.BlockImage,
		MediaType: "image/png",
		ImageData: largeImage,
	}})
	if err := store.Append(ctx, conversation.Turn{
		ConversationID: convID,
		Role:           "user",
		BlocksJSON:     string(blocksJSON),
		CreatedAt:      time.Unix(101, 0),
	}); err != nil {
		t.Fatalf("append live image: %v", err)
	}
	consolidated, _ := json.Marshal(compaction.StructuredSummary{Goal: "summarized", State: "ready"})
	if err := store.SaveCompaction(ctx, conversation.Compaction{
		ConversationID:       convID,
		FrozenThrough:        frozenAt.Unix(),
		ConsolidatedJSON:     string(consolidated),
		CompactedTokens:      10,
		SegmentSummariesJSON: "[]",
		UpdatedAt:            time.Unix(102, 0),
	}); err != nil {
		t.Fatalf("save compaction: %v", err)
	}

	resp, err := srv.GetContextUsage(ctx, &proto.GetContextUsageRequest{ConversationId: convID})
	if err != nil {
		t.Fatalf("GetContextUsage: %v", err)
	}
	if resp.GetTokensUsed() > 10_000 {
		t.Fatalf("compacted meter counted raw live image payload: tokens=%d raw=%d", resp.GetTokensUsed(), resp.GetRawTokens())
	}
	if resp.GetRawTokens() <= resp.GetTokensUsed() {
		t.Fatalf("raw storage pressure should remain larger than provider-facing sent tokens: raw=%d sent=%d", resp.GetRawTokens(), resp.GetTokensUsed())
	}
}

func TestContextTurnView_BodyMultilineAndCap(t *testing.T) {
	tok := contextmeter.Default()
	// multi-line text turn → body keeps newlines; preview is flattened.
	multi := conversation.Turn{Role: "assistant", Content: "line one\nline two\nline three"}
	ct := contextTurnView(multi, tok)
	if ct.Body != "line one\nline two\nline three" {
		t.Errorf("body should preserve newlines, got %q", ct.Body)
	}
	if ct.Truncated {
		t.Error("short body should not be truncated")
	}
	if contains(ct.Preview, "\n") {
		t.Errorf("preview should stay single-line, got %q", ct.Preview)
	}
	// over-cap body → truncated, valid UTF-8.
	big := conversation.Turn{Role: "assistant", Content: strings.Repeat("x", 5000)}
	bct := contextTurnView(big, tok)
	if !bct.Truncated || len(bct.Body) > 4096 {
		t.Errorf("body should be capped+flagged: len=%d truncated=%v", len(bct.Body), bct.Truncated)
	}
	if !utf8.ValidString(bct.Body) {
		t.Error("capped body must be valid UTF-8")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// TestContextTurnView_MixedBlocksPreservesAllContent guards against the
// regression where an assistant turn with interleaved text and tool_use
// blocks (a common pattern — "let me check X" + Grep + "and Y" + Read)
// collapsed to only the last tool_use's preview/body because the earlier
// implementation unconditionally overwrote them per block. The full content
// of every block must appear in the preview and body, in order.
func TestContextTurnView_MixedBlocksPreservesAllContent(t *testing.T) {
	tok := contextmeter.Default()
	blocks := []llm.Block{
		{Type: llm.BlockText, Text: "here's my analysis"},
		{Type: llm.BlockToolUse, ToolUseID: "u1", ToolName: "Grep", ToolInput: json.RawMessage(`{"pattern":"foo"}`)},
		{Type: llm.BlockText, Text: "and now let me check Y"},
		{Type: llm.BlockToolUse, ToolUseID: "u2", ToolName: "Read", ToolInput: json.RawMessage(`{"path":"a.go"}`)},
	}
	raw, _ := json.Marshal(blocks)
	turn := conversation.Turn{Role: "assistant", BlocksJSON: string(raw)}
	got := contextTurnView(turn, tok)

	for _, want := range []string{"here's my analysis", "Grep", "and now let me check Y", "Read"} {
		if !strings.Contains(got.Preview, want) {
			t.Errorf("preview missing %q — the block-overwrite regression is back:\n%s", want, got.Preview)
		}
		if !strings.Contains(got.Body, want) {
			t.Errorf("body missing %q:\n%s", want, got.Body)
		}
	}
	// Kind still reflects the last non-text block so client-side styling is
	// unchanged.
	if got.Kind != "tool_use" {
		t.Errorf("kind = %q, want tool_use (last non-text block)", got.Kind)
	}
}

func TestCtTruncate_RuneBoundary(t *testing.T) {
	// 60 CJK runes = 180 bytes; truncating at 121 bytes splits a rune.
	// A correct implementation must back up to a rune boundary.
	s := strings.Repeat("世", 60)
	got := ctTruncate(s, 121)
	if !utf8.ValidString(got) {
		t.Fatalf("ctTruncate produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
}

func TestGetConversationTurns_ToolMetadata(t *testing.T) {
	srv, store := newServerWithStore(t)
	ctx := context.Background()
	convID := "conv-toolmeta"
	if err := store.EnsureConversation(ctx, convID, "", "test-model"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	useJSON, _ := json.Marshal([]llm.Block{{Type: llm.BlockToolUse, ToolUseID: "u9", ToolName: "Read", ToolInput: json.RawMessage(`{"path":"main.go"}`)}})
	resJSON, _ := json.Marshal([]llm.Block{{Type: llm.BlockToolResult, ToolUseRef: "u9", Content: "package main"}})
	for _, tn := range []conversation.Turn{
		{ConversationID: convID, Role: "assistant", BlocksJSON: string(useJSON)},
		{ConversationID: convID, Role: "user", BlocksJSON: string(resJSON)},
	} {
		if err := store.Append(ctx, tn); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	resp, err := srv.GetConversationTurns(ctx, &proto.GetConversationTurnsRequest{ConversationId: convID})
	if err != nil {
		t.Fatalf("GetConversationTurns: %v", err)
	}
	if len(resp.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(resp.Turns))
	}
	use, res := resp.Turns[0], resp.Turns[1]
	if use.ToolName != "Read" || use.ToolUseId != "u9" || use.ToolArgs != `{"path":"main.go"}` {
		t.Errorf("tool_use metadata: name %q id %q args %q", use.ToolName, use.ToolUseId, use.ToolArgs)
	}
	if res.ToolUseRef != "u9" {
		t.Errorf("tool_result ref = %q, want u9", res.ToolUseRef)
	}
}
