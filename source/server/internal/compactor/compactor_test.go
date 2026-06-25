package compactor

import (
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/llm"
)

func turn(id, role, content string, at int64) conversation.Turn {
	return conversation.Turn{ID: id, Role: role, Content: content, CreatedAt: time.Unix(at, 0)}
}

func flat(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		for _, blk := range m.Blocks {
			b.WriteString(blk.Text)
			b.WriteString(blk.Content)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func TestBuildSendView_NoStateIsFullHistory(t *testing.T) {
	turns := []conversation.Turn{turn("a", "user", "hello", 100), turn("b", "assistant", "hi", 101)}
	view, err := BuildSendView(turns, conversation.Compaction{})
	if err != nil {
		t.Fatal(err)
	}
	out := flat(view)
	if !strings.Contains(out, "hello") || !strings.Contains(out, "hi") {
		t.Errorf("no compaction state → full history, got:\n%s", out)
	}
}

func TestBuildSendView_WithStatePreamblePlusLiveTail(t *testing.T) {
	turns := []conversation.Turn{
		turn("a", "user", "OLD-FROZEN", 100),
		turn("b", "assistant", "ALSO-FROZEN", 150),
		turn("c", "user", "LIVE-TAIL", 200),
	}
	state := conversation.Compaction{
		FrozenThrough:    150, // turns at/before 150 are frozen (a, b)
		ConsolidatedJSON: `{"Goal":"SUMMARY-GOAL","State":"done"}`,
	}
	view, err := BuildSendView(turns, state)
	if err != nil {
		t.Fatal(err)
	}
	out := flat(view)
	if !strings.Contains(out, "SUMMARY-GOAL") {
		t.Error("expected consolidated summary preamble")
	}
	if !strings.Contains(out, "LIVE-TAIL") {
		t.Error("expected the live tail verbatim")
	}
	if strings.Contains(out, "OLD-FROZEN") || strings.Contains(out, "ALSO-FROZEN") {
		t.Error("frozen turns must NOT appear verbatim — they're in the summary")
	}
	if !llm.IsValidPairing(view) {
		t.Error("send-view must be pairing-valid")
	}
}
