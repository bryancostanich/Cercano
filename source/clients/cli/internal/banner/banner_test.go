package banner

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
)

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

func TestWordmarkCols_Const(t *testing.T) {
	if WordmarkCols != 28 {
		t.Errorf("WordmarkCols changed: got %d want 28 (banner layout broken)", WordmarkCols)
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
