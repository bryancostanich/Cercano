package agent

import (
	"errors"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

func TestEstimateTokens_RoughFourCharsPerToken(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},       // ceil(1/4)
		{"abcd", 1},    // exactly 4 chars -> 1
		{"abcde", 2},   // 5 chars -> ceil(5/4)=2
		{"abcdefgh", 2}, // 8 chars -> 2
	}
	for _, c := range cases {
		if got := estimateTokens(c.in); got != c.want {
			t.Errorf("estimateTokens(%q) = %d, want %d", c.in, got, c.want)
		}
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
	// Build an input that estimates well over a small window's 90% budget.
	// window=1000 -> budget=900 tokens -> need >900 tokens -> >3600 chars.
	input := strings.Repeat("x", 8000) // ~2000 tokens
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
