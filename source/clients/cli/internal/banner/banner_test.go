package banner

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

// bannerWidth returns the common visible width of every banner line, failing
// the test if the lines are not all equal (a ragged box means the border broke).
func bannerWidth(t *testing.T, out string) int {
	t.Helper()
	lines := strings.Split(out, "\n")
	w0 := visibleWidth(lines[0])
	for i, ln := range lines {
		if got := visibleWidth(ln); got != w0 {
			t.Fatalf("banner line %d width = %d, want %d (ragged box):\n%s", i, got, w0, out)
		}
	}
	return w0
}

func TestRender_LineCountAndWidth(t *testing.T) {
	out := Render(theme.Cracker(), Meta{
		Tagline: "local-first ai coprocessor",
		Version: "v0.1.0",
		Model:   "qwen3-coder",
	})
	lines := strings.Split(out, "\n")
	if len(lines) != 8 {
		t.Fatalf("banner: got %d lines, want 8", len(lines))
	}
	// A short model keeps the familiar minimum-size box, and every line is
	// exactly that width.
	if w := bannerWidth(t, out); w != Width {
		t.Fatalf("short-model banner width = %d, want %d", w, Width)
	}
}

func TestRender_LongModelGrowsAndNeverClips(t *testing.T) {
	tagline, version := "local-first ai coprocessor", "v0.1.0"
	short := Render(theme.Cracker(), Meta{Tagline: tagline, Version: version, Model: "qwen3-coder"})
	long := Render(theme.Cracker(), Meta{Tagline: tagline, Version: version, Model: "claude-opus-5-0"})

	// Both banners are well-formed rectangles (all lines equal width) …
	sw := bannerWidth(t, short)
	lw := bannerWidth(t, long)
	if len(strings.Split(long, "\n")) != 8 {
		t.Fatalf("long-model banner should still be 8 lines")
	}
	// … the box grows to accommodate the longer model name …
	if lw <= sw {
		t.Fatalf("long-model width %d should exceed short-model width %d", lw, sw)
	}
	// … the model name renders intact on the status line (never clipped) …
	status := strings.Split(stripAnsi(long), "\n")[6] // border, blank, wm×2, blank, rail, status, border
	if !strings.Contains(status, "claude-opus-5-0") {
		t.Fatalf("long model name clipped from status line: %q", status)
	}
	// … and the status line fits exactly inside the walls (no overflow past the
	// right border, which is the bug this guards against).
	if got := visibleWidth(strings.Split(long, "\n")[6]); got != lw {
		t.Fatalf("status line width %d != box width %d (model spills the wall)", got, lw)
	}
}

func TestRenderWithSweep_OffScreenEqualsStatic(t *testing.T) {
	// A sweep position well off the wordmark (left or right) must paint base
	// color everywhere — the rendered string is identical to the static Render
	// modulo character content.
	staticOut := Render(theme.Cracker(), Meta{Tagline: "x", Version: "y", Model: "z"})
	leftOut := RenderWithSweep(theme.Cracker(), Meta{Tagline: "x", Version: "y", Model: "z"}, -100)
	rightOut := RenderWithSweep(theme.Cracker(), Meta{Tagline: "x", Version: "y", Model: "z"}, 1000)

	if stripAnsi(leftOut) != stripAnsi(staticOut) {
		t.Errorf("off-screen sweep (left) should match static text")
	}
	if stripAnsi(rightOut) != stripAnsi(staticOut) {
		t.Errorf("off-screen sweep (right) should match static text")
	}
}

func TestRender_EmptyModelOmitsSeparator(t *testing.T) {
	// With no model set (before the config load lands), the status line must
	// not render a dangling "· " separator, and must still be exactly 8 lines
	// of a well-formed (equal-width) box.
	out := Render(theme.Cracker(), Meta{
		Tagline: "local-first ai coprocessor",
		Version: "v0.1.0",
		// Model intentionally empty.
	})
	bannerWidth(t, out) // still a rectangle
	text := stripAnsi(out)
	lines := strings.Split(text, "\n")
	if len(lines) != 8 {
		t.Fatalf("banner: got %d lines, want 8", len(lines))
	}
	status := lines[6] // border, blank, wordmark×2, blank, rail, status, border
	// The version segment ends the visible content; nothing should follow the
	// version but padding and the right wall.
	if strings.Contains(status, "· \u2588") {
		t.Errorf("empty model still rendered a trailing separator: %q", status)
	}
	// The version's separator (" · ") appears once (tagline→version); a second
	// "·" would mean the model separator leaked through.
	if got := strings.Count(status, "·"); got != 1 {
		t.Errorf("status line has %d '·' separators, want 1 (empty model): %q", got, status)
	}
}

func TestWordmarkCols_Const(t *testing.T) {
	if WordmarkCols != 28 {
		t.Errorf("WordmarkCols changed: got %d want 28 (banner layout broken)", WordmarkCols)
	}
	// The layout math (right-pad = inner-2-WordmarkCols) assumes the wordmark
	// rows are exactly WordmarkCols visible columns wide.
	if got := visibleWidth(wordmarkTop); got != WordmarkCols {
		t.Errorf("wordmarkTop visible width = %d, want %d", got, WordmarkCols)
	}
	if got := visibleWidth(wordmarkBot); got != WordmarkCols {
		t.Errorf("wordmarkBot visible width = %d, want %d", got, WordmarkCols)
	}
}

// stripAnsi removes ANSI CSI sequences (color, bold, etc.) so we can compare
// the literal text content of styled strings.
func stripAnsi(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7E) {
				j++
			}
			if j < len(s) {
				j++ // consume the final byte
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
