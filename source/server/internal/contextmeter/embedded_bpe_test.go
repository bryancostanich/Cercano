package contextmeter

import (
	"strings"
	"testing"
)

// TestEmbeddedBpeParses is the packaging guard: if the vendored table is
// truncated, LFS-stubbed, or otherwise corrupt, this fails loudly at test
// time rather than silently demoting every budget in production to char/4.
func TestEmbeddedBpeParses(t *testing.T) {
	ranks, err := parseTiktokenBpe(cl100kBase)
	if err != nil {
		t.Fatalf("parse embedded cl100k_base: %v", err)
	}
	// The published cl100k_base table has 100256 merge entries.
	if got, want := len(ranks), 100_256; got != want {
		t.Errorf("embedded BPE rank count = %d, want %d", got, want)
	}
}

// TestEmbeddedLoaderRejectsUnknownEncoding pins the deliberate narrowness of
// the loader: only cl100k_base is vendored, and asking for anything else must
// error rather than get mis-decoded as cl100k.
func TestEmbeddedLoaderRejectsUnknownEncoding(t *testing.T) {
	_, err := embeddedBpeLoader{}.LoadTiktokenBpe("https://openaipublic.blob.core.windows.net/encodings/o200k_base.tiktoken")
	if err == nil {
		t.Fatal("expected error for non-vendored encoding, got nil")
	}
	if !strings.Contains(err.Error(), "o200k_base") {
		t.Errorf("error should name the requested encoding, got: %v", err)
	}
}

// TestDefaultIsExact asserts we get real BPE tokenization. This is the
// regression that matters: before the table was embedded, a machine with no
// network and no warm cache would silently fall back to char/4.
func TestDefaultIsExact(t *testing.T) {
	if !Exact() {
		t.Fatal("Default() fell back to char/4; embedded BPE table did not load")
	}
}

// TestKnownTokenCounts pins counts for strings with independently known
// cl100k_base tokenizations, proving the embedded table produces real BPE
// output and not merely *some* self-consistent numbers.
func TestKnownTokenCounts(t *testing.T) {
	tok := Default()
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"hello world", 2},
		{" hello world", 2},
		{"tokenization", 2},
	}
	for _, c := range cases {
		if got := tok.Count(c.in); got != c.want {
			t.Errorf("Count(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestHighEntropyContentDivergesFromCharDiv4 documents *why* the fallback is
// dangerous, and guards the property that motivated embedding the table.
// Dense base64-ish content tokenizes far worse than 4 chars/token, so char/4
// materially undercounts it — the direction that overflows a context window.
func TestHighEntropyContentDivergesFromCharDiv4(t *testing.T) {
	// Shaped like a go.sum line: base64 hashes, which tokenize ~1.8 chars/tok.
	const dense = "h1:85ENo+3FpWgAACBaEUVp+lctuTcYUO7BtmfhlN/QTRo=" +
		"9NiV+i9mJKGj1rYOT+njbv+ZwA/zJxYdewGl6qVatpg=" +
		"223921b76ee99bde995b7ff738513eef100fb51d18c93597a113bcffe865b2a7"

	real := Default().Count(dense)
	div4 := fallbackTokenizer{}.Count(dense)

	if real <= div4 {
		t.Fatalf("expected real count (%d) to exceed char/4 (%d) on dense content", real, div4)
	}
	// Guard the magnitude, not just the direction: char/4 should be under
	// ~70%% of truth here. If this ever gets close to 1.0 the fallback stopped
	// being obviously wrong and the loudness rationale needs revisiting.
	if ratio := float64(div4) / float64(real); ratio > 0.7 {
		t.Errorf("char/4 ratio %.3f unexpectedly close to accurate count (real=%d div4=%d)", ratio, real, div4)
	}
}
