package agent

import (
	"errors"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

// TestEstimateTokens_UsesRealTokenization pins that estimateTokens performs
// actual BPE tokenization rather than char/4 arithmetic.
//
// This test previously asserted the char/4 heuristic directly (e.g.
// "abcdefgh" -> 2). Those expectations encoded the estimator's arithmetic,
// not any property of tokenization, and every one of them was wrong about
// real token counts: "abcdefgh" is a single cl100k token, not two.
func TestEstimateTokens_UsesRealTokenization(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abcdefgh", 1},    // one merged BPE token, char/4 guessed 2
		{"hello world", 2}, // common words are one token each
	}
	for _, c := range cases {
		if got := estimateTokens(c.in); got != c.want {
			t.Errorf("estimateTokens(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestEstimateTokens_DenseContentExceedsCharDiv4 guards the property that
// motivated the switch: high-entropy content tokenizes far worse than 4
// chars/token, so char/4 undercounts it — the direction that lets an
// oversized prompt past the guard and into a provider-side overflow.
func TestEstimateTokens_DenseContentExceedsCharDiv4(t *testing.T) {
	const dense = "h1:85ENo+3FpWgAACBaEUVp+lctuTcYUO7BtmfhlN/QTRo=" +
		"9NiV+i9mJKGj1rYOT+njbv+ZwA/zJxYdewGl6qVatpg="
	if got, div4 := estimateTokens(dense), (len(dense)+3)/4; got <= div4 {
		t.Errorf("estimateTokens(dense) = %d, expected to exceed char/4 estimate %d", got, div4)
	}
}

func TestReduceHistoryToContextTailKeepsNewestWithinBudget(t *testing.T) {
	msg := func(text string) llm.Message {
		return llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: text}}}
	}
	history := []llm.Message{msg(strings.Repeat("a", 1600)), msg(strings.Repeat("b", 1600)), msg(strings.Repeat("c", 1600))}
	reduced, trimmed := reduceHistoryToContextTail("sys", history, "current", 0, 1000) // 800-token budget; each history msg ~=400 tokens
	if !trimmed {
		t.Fatal("expected history to be trimmed")
	}
	if len(reduced) != 1 {
		t.Fatalf("len(reduced) = %d, want 1", len(reduced))
	}
	if got := reduced[0].Blocks[0].Text[:1]; got != "c" {
		t.Fatalf("kept oldest/wrong message prefix %q, want newest c", got)
	}
}

func TestPreflightContextCheck_ZeroWindowDisables(t *testing.T) {
	// A huge prompt but window 0 => check is a no-op.
	huge := strings.Repeat("x", 1_000_000)
	if err := preflightContextCheck(huge, nil, huge, 0, 0); err != nil {
		t.Fatalf("window 0 must disable the check, got: %v", err)
	}
}

func TestPreflightContextCheck_UnderBudgetPasses(t *testing.T) {
	// ~250 tokens of input against a 16k window is comfortably under the 90%
	// budget — must pass.
	input := strings.Repeat("token ", 200) // ~1200 chars -> ~300 tokens
	if err := preflightContextCheck("you are a helpful agent", nil, input, 0, 16384); err != nil {
		t.Fatalf("small prompt under budget must pass, got: %v", err)
	}
}

func TestPreflightContextCheck_OverBudgetReturnsContextOverflow(t *testing.T) {
	// Build an input that counts well over a small window's 90% budget.
	// window=1000 -> budget=900 tokens -> need >900 tokens.
	//
	// Fixture must be realistic prose, not strings.Repeat: BPE merges long
	// single-character runs aggressively (2000x"a" is only 250 tokens, ~8
	// chars/token), so a repeat-based fixture counts far lower than its byte
	// length suggests. Repeated prose tokenizes near 2.3 chars/token, so this
	// lands around 1080 tokens.
	input := strings.Repeat("the quick brown fox jumps over the lazy dog ", 120)
	err := preflightContextCheck("", nil, input, 0, 1000)
	if err == nil {
		t.Fatal("over-budget prompt must return an error")
	}
	if llm.ClassOf(err) != llm.ErrContextOverflow {
		t.Fatalf("error must classify as ErrContextOverflow, got class %q (%v)", llm.ClassOf(err), err)
	}
	var le *llm.Error
	if !errors.As(err, &le) {
		t.Fatal("error must be an *llm.Error")
	}
	if le.Used <= le.Limit {
		t.Errorf("Used (%d) should exceed Limit (%d) on an overflow", le.Used, le.Limit)
	}
	if le.Limit != 1000 {
		t.Errorf("Limit should carry the window, got %d", le.Limit)
	}
	// Message must be actionable, not opaque.
	if !strings.Contains(err.Error(), "trim") {
		t.Errorf("message should guide the caller to trim, got: %q", err.Error())
	}
}

func TestPreflightContextCheck_CountsHistoryAndImages(t *testing.T) {
	// Small system+input, but heavy history and images push it over a small
	// window — proving both are counted, not just the current turn.
	history := []llm.Message{
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: strings.Repeat("y", 2000)}}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolResult, Content: strings.Repeat("z", 2000)}}},
	}
	// history alone ~ (2000+2000)/4 = 1000 tokens; 2 images * 1000 = 2000 more.
	err := preflightContextCheck("sys", history, "hi", 2, 2000)
	if err == nil {
		t.Fatal("history + images over the window must trip the guard")
	}
	if llm.ClassOf(err) != llm.ErrContextOverflow {
		t.Fatalf("want ErrContextOverflow, got %q", llm.ClassOf(err))
	}
	var le *llm.Error
	errors.As(err, &le)
	if le.Used < 3000 {
		t.Errorf("Used should reflect history + image cost, got %d", le.Used)
	}
}
