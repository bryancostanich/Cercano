package agent

import (
	"testing"
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
