package compaction

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"cercano/source/server/internal/contextmeter"
	"cercano/source/server/internal/llm"
)

// truncateOldestToFitNaive is the original quadratic implementation, kept
// verbatim as the differential oracle. The optimized version must agree with
// it on every input: the rewrite is a pure performance change, so any
// divergence is a bug in the optimization.
func truncateOldestToFitNaive(msgs []llm.Message, tok contextmeter.Tokenizer, limit, preserveLeading int) ([]llm.Message, int) {
	if TotalTokens(tok, msgs) <= limit || len(msgs) == 0 {
		return msgs, 0
	}
	if preserveLeading > len(msgs) {
		preserveLeading = len(msgs)
	}
	head := msgs[:preserveLeading]
	tail := msgs[preserveLeading:]
	dropped := 0
	for len(tail) > 1 && TotalTokens(tok, append(append([]llm.Message{}, head...), tail...)) > limit {
		tail = tail[1:]
		dropped++
	}
	for len(tail) > 1 && hasToolResult(tail[0]) {
		tail = tail[1:]
		dropped++
	}
	return append(append([]llm.Message{}, head...), tail...), dropped
}

func msg(role llm.Role, text string) llm.Message {
	return llm.Message{Role: role, Blocks: []llm.Block{{Type: llm.BlockText, Text: text}}}
}

func toolResultMsg(text string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, Content: text}}}
}

// Randomized differential test across message shapes, limits, and preserve
// counts -- including the boundaries (limit 0, limit above total, preserve
// larger than the slice) where off-by-one errors in the index bookkeeping
// would surface.
func TestTruncateOldestToFitMatchesNaive(t *testing.T) {
	tok := contextmeter.Default()
	rng := rand.New(rand.NewSource(1))

	for iter := 0; iter < 400; iter++ {
		n := rng.Intn(12)
		msgs := make([]llm.Message, 0, n)
		for i := 0; i < n; i++ {
			body := strings.Repeat(fmt.Sprintf("w%d ", i), 1+rng.Intn(20))
			switch rng.Intn(4) {
			case 0:
				msgs = append(msgs, toolResultMsg(body))
			case 1:
				msgs = append(msgs, msg(llm.RoleAssistant, body))
			default:
				msgs = append(msgs, msg(llm.RoleUser, body))
			}
		}
		total := TotalTokens(tok, msgs)
		// Sample limits around and beyond the true total.
		limit := rng.Intn(total + 10)
		preserve := rng.Intn(n + 3)

		wantMsgs, wantDropped := truncateOldestToFitNaive(msgs, tok, limit, preserve)
		gotMsgs, gotDropped := TruncateOldestToFit(msgs, tok, limit, preserve)

		if gotDropped != wantDropped {
			t.Fatalf("iter %d (n=%d limit=%d preserve=%d): dropped got %d, want %d",
				iter, n, limit, preserve, gotDropped, wantDropped)
		}
		if len(gotMsgs) != len(wantMsgs) {
			t.Fatalf("iter %d (n=%d limit=%d preserve=%d): len got %d, want %d",
				iter, n, limit, preserve, len(gotMsgs), len(wantMsgs))
		}
		for i := range wantMsgs {
			if TotalTokens(tok, []llm.Message{gotMsgs[i]}) != TotalTokens(tok, []llm.Message{wantMsgs[i]}) {
				t.Fatalf("iter %d: message %d differs", iter, i)
			}
		}
	}
}

// The documented contract: never return a view whose first non-preserved
// message is an orphaned tool result.
func TestTruncateOldestToFitNeverLeadsWithToolResult(t *testing.T) {
	tok := contextmeter.Default()
	msgs := []llm.Message{
		msg(llm.RoleUser, strings.Repeat("head ", 50)),
		toolResultMsg(strings.Repeat("tr1 ", 50)),
		toolResultMsg(strings.Repeat("tr2 ", 50)),
		msg(llm.RoleAssistant, strings.Repeat("tail ", 50)),
	}
	got, _ := TruncateOldestToFit(msgs, tok, 30, 0)
	if len(got) > 1 && hasToolResult(got[0]) {
		t.Errorf("view begins with an orphaned tool result")
	}
}

// A view that already fits must be returned untouched, with zero drops.
func TestTruncateOldestToFitPassesThroughWhenFitting(t *testing.T) {
	tok := contextmeter.Default()
	msgs := []llm.Message{msg(llm.RoleUser, "small"), msg(llm.RoleAssistant, "also small")}
	got, dropped := TruncateOldestToFit(msgs, tok, 1_000_000, 0)
	if dropped != 0 || len(got) != len(msgs) {
		t.Errorf("fitting view was modified: dropped=%d len=%d want 0/%d", dropped, len(got), len(msgs))
	}
}

// Guard the actual complexity claim: count tokenizer calls and assert they
// scale with the number of messages, not with messages squared. Without this
// a future refactor could silently reintroduce the quadratic behavior while
// all correctness tests stay green.
type callCountingTokenizer struct {
	inner contextmeter.Tokenizer
	calls int
}

func (c *callCountingTokenizer) Count(s string) int {
	c.calls++
	return c.inner.Count(s)
}

func TestTruncateOldestToFitIsLinear(t *testing.T) {
	base := contextmeter.Default()

	measure := func(n int) int {
		msgs := make([]llm.Message, n)
		for i := range msgs {
			msgs[i] = msg(llm.RoleUser, strings.Repeat("token ", 20))
		}
		c := &callCountingTokenizer{inner: base}
		// A limit of 1 forces dropping all the way down to a single message,
		// the worst case for the old loop.
		TruncateOldestToFit(msgs, c, 1, 0)
		return c.calls
	}

	small := measure(50)
	large := measure(400)

	// Linear: 8x the messages should cost ~8x the calls. Quadratic would be
	// ~64x. A 16x ceiling separates the two without being flaky.
	if large > small*16 {
		t.Errorf("tokenizer calls scaled superlinearly: n=50 -> %d calls, n=400 -> %d calls (ratio %.1fx, want ~8x)",
			small, large, float64(large)/float64(small))
	}
}
