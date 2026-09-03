package contextmeter

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// countingTokenizer records how many times the underlying (expensive) encode
// actually ran, so tests can assert cache hits rather than just equal results.
type countingTokenizer struct {
	mu    sync.Mutex
	calls int
}

func (c *countingTokenizer) Count(s string) int {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return len(s)
}

func (c *countingTokenizer) n() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// The memoizing wrapper is a pure optimization: it must never change a count.
func TestMemoizingMatchesUnderlyingCounts(t *testing.T) {
	inner := &tiktokenTokenizer{}
	if enc := Default(); enc == nil {
		t.Fatal("no default tokenizer")
	}
	raw, ok := rawTiktoken()
	if !ok {
		t.Skip("tiktoken encoding unavailable")
	}
	inner = raw
	memo := Memoizing(inner)

	cases := []string{
		"",
		"short",
		strings.Repeat("a", minMemoLen-1),   // below the memo threshold
		strings.Repeat("b", minMemoLen),     // exactly at it
		strings.Repeat("hello world ", 500), // well above
		strings.Repeat("→ unicode ✓ ", 400), // multi-byte
		`{"tool":"Bash","input":{"cmd":["ls","-la"]}}` + strings.Repeat(" x", 300),
	}
	for _, s := range cases {
		want := inner.Count(s)
		// Call twice: once cold, once warm. Both must equal the real count.
		if got := memo.Count(s); got != want {
			t.Errorf("cold count len=%d: got %d, want %d", len(s), got, want)
		}
		if got := memo.Count(s); got != want {
			t.Errorf("warm count len=%d: got %d, want %d", len(s), got, want)
		}
	}
}

// The whole point: repeated counts of identical content must not re-encode.
func TestMemoizingServesRepeatsFromCache(t *testing.T) {
	inner := &countingTokenizer{}
	memo := Memoizing(inner)
	s := strings.Repeat("immutable turn content ", 100)

	for i := 0; i < 50; i++ {
		if got := memo.Count(s); got != len(s) {
			t.Fatalf("count changed under caching: got %d, want %d", got, len(s))
		}
	}
	if inner.n() != 1 {
		t.Errorf("underlying tokenizer ran %d times, want 1 (49 should be cache hits)", inner.n())
	}
}

// Strings below the threshold are cheap to encode; caching them would burn
// entries for no gain, so they must pass straight through.
func TestMemoizingSkipsShortStrings(t *testing.T) {
	inner := &countingTokenizer{}
	memo := Memoizing(inner)
	short := strings.Repeat("s", minMemoLen-1)

	for i := 0; i < 10; i++ {
		memo.Count(short)
	}
	if inner.n() != 10 {
		t.Errorf("short strings were cached: underlying ran %d times, want 10", inner.n())
	}
}

// Distinct content of the same length must not collide into one entry — this
// is the failure mode that would silently corrupt budget accounting.
func TestMemoizingDistinguishesSameLengthContent(t *testing.T) {
	raw, ok := rawTiktoken()
	if !ok {
		t.Skip("tiktoken encoding unavailable")
	}
	memo := Memoizing(raw)

	// Same byte length, very different token counts: "aaaa" packs into few
	// BPE tokens while distinct words do not. If the cache keyed on length
	// alone, the second lookup would wrongly return the first's count.
	a := strings.Repeat("aaaa", 200)
	b := strings.Repeat("word ", 160)
	if len(a) != len(b) {
		t.Fatalf("test setup: want equal lengths, got %d and %d", len(a), len(b))
	}
	if raw.Count(a) == raw.Count(b) {
		t.Fatalf("test setup: want differing token counts to detect collisions")
	}
	for _, s := range []string{a, b} {
		want := raw.Count(s)
		if got := memo.Count(s); got != want {
			t.Errorf("len=%d: got %d, want %d", len(s), got, want)
		}
	}
}

// Default() is a process-wide singleton shared across concurrent requests, so
// the cache must be safe under parallel access. Run with -race.
func TestMemoizingIsConcurrencySafe(t *testing.T) {
	inner := &countingTokenizer{}
	memo := Memoizing(inner)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				s := strings.Repeat(fmt.Sprintf("payload-%d ", i%20), 40)
				if got := memo.Count(s); got != len(s) {
					t.Errorf("bad count under concurrency: got %d, want %d", got, len(s))
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// The cache is bounded; exceeding the cap must not grow without limit or
// return wrong counts after the reset.
func TestMemoizingStaysBounded(t *testing.T) {
	inner := &countingTokenizer{}
	mt := &memoTokenizer{inner: inner, cache: make(map[uint64]memoEntry)}

	for i := 0; i < maxMemoEntries+1000; i++ {
		s := fmt.Sprintf("%d-%s", i, strings.Repeat("z", minMemoLen))
		if got := mt.Count(s); got != len(s) {
			t.Fatalf("wrong count at i=%d: got %d, want %d", i, got, len(s))
		}
	}
	mt.mu.RLock()
	size := len(mt.cache)
	mt.mu.RUnlock()
	if size > maxMemoEntries {
		t.Errorf("cache grew past cap: %d > %d", size, maxMemoEntries)
	}
}

// Double-wrapping would add a redundant map lookup on every count.
func TestMemoizingIsIdempotent(t *testing.T) {
	inner := &countingTokenizer{}
	once := Memoizing(inner)
	twice := Memoizing(once)
	if once != twice {
		t.Error("Memoizing(Memoizing(t)) added a second cache layer")
	}
}

// rawTiktoken builds an unmemoized tiktoken tokenizer for comparison, or
// reports false when the encoding cannot be loaded in this environment.
func rawTiktoken() (*tiktokenTokenizer, bool) {
	tok, ok := Default().(*memoTokenizer)
	if !ok {
		return nil, false
	}
	inner, ok := tok.inner.(*tiktokenTokenizer)
	if !ok || inner.enc == nil {
		return nil, false
	}
	return inner, true
}
