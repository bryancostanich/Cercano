package ui

import (
	"strings"
	"testing"
	"time"

	"cercano/source/clients/cli/internal/theme"
)

func TestContextMeter_SavingsBadgeWhenCompacted(t *testing.T) {
	m := Model{styles: theme.NewStyles(theme.Cracker()), cumIn: 18000, ctxRaw: 340000, modelMaxTokens: 200000}
	out := stripAnsiCSI(m.renderContextMeter())
	if !strings.Contains(out, "↓") {
		t.Errorf("expected a savings badge when raw > sent:\n%s", out)
	}
}

func TestContextMeter_NoBadgeWhenNotCompacted(t *testing.T) {
	m := Model{styles: theme.NewStyles(theme.Cracker()), cumIn: 18000, ctxRaw: 18000, modelMaxTokens: 200000}
	out := stripAnsiCSI(m.renderContextMeter())
	if strings.Contains(out, "↓") {
		t.Errorf("no badge when sent==raw:\n%s", out)
	}
}

func TestContextMeter_CompactingOverlay(t *testing.T) {
	m := Model{styles: theme.NewStyles(theme.Cracker()), palette: theme.Cracker(), cumIn: 18000, ctxRaw: 340000, modelMaxTokens: 200000, compacting: true}
	out := stripAnsiCSI(m.renderContextMeter())
	if !strings.Contains(out, "compacting") {
		t.Errorf("expected a compacting overlay while a pass runs:\n%s", out)
	}
	// During compaction every filled cell is BACKGROUND-painted (space glyph,
	// fill color as the cell background) so the bar height is uniform with
	// the background-painted label cells. A █ foreground glyph doesn't flood
	// the cell the way a background does, and mixing the two made the label
	// region render taller than the rest of the bar.
	if strings.Contains(out, "█") {
		t.Errorf("filled cells must be background-painted during compaction, not █ glyphs:\n%s", out)
	}
	if !strings.Contains(out, "░") {
		t.Errorf("expected un-overlaid empty cells to remain visible:\n%s", out)
	}
	if !strings.Contains(out, "18.0k") || !strings.Contains(out, "200.0k") {
		t.Errorf("expected token count to remain visible during compaction:\n%s", out)
	}
	// The label appears exactly once — overlaid on the bar — and is not
	// also appended as a separate badge after the meter.
	if strings.Count(out, "compacting") != 1 {
		t.Errorf("expected exactly one compacting label (overlaid, not appended):\n%s", out)
	}
}

func TestActivitySweepUsesReadableDarkGreenOnLightThemes(t *testing.T) {
	daylight := builtinPaletteForTest("daylight")
	out := animateActivitySweepAt("thinking", time.Unix(0, 0), daylight)
	if !strings.Contains(out, "38;2;30;122;60") {
		t.Fatalf("daylight thinking text should use readable theme accent (#1E7A3C), got %q", out)
	}
	if strings.Contains(out, "38;2;189;240;0") {
		t.Fatalf("daylight thinking text should not use neon lime (#BDF000) on a light background, got %q", out)
	}
}

func TestCompactingMeterUsesLightThemeContrast(t *testing.T) {
	daylight := builtinPaletteForTest("daylight")
	m := Model{styles: theme.NewStyles(daylight), palette: daylight, cumIn: 90000, ctxRaw: 340000, modelMaxTokens: 200000, compacting: true}
	out := m.renderContextMeter()
	if !strings.Contains(stripAnsiCSI(out), "compacting") {
		t.Fatalf("expected compacting label in rendered meter: %q", stripAnsiCSI(out))
	}
	if !strings.Contains(out, "38;2;251;243;224") {
		t.Fatalf("daylight compacting label over the filled dark-green bar should use light page text (#FBF3E0), got %q", out)
	}
}

func builtinPaletteForTest(name string) theme.Palette {
	for _, builtin := range theme.BuiltinThemes() {
		if builtin.Name == name {
			return builtin.Palette
		}
	}
	return theme.Cracker()
}
