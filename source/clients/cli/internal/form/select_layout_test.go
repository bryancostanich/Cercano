package form

import (
	"strings"
	"testing"
)

// ansiStrip removes ANSI escape sequences so layout can be asserted on the
// visible glyphs.
func ansiStrip(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestFormOpenSelectDoesNotWrapFlushLeft reproduces the width=53 screenshot:
// a section with a wide label (so colOffset is large) plus an open locus-mode
// picker. Before the fix, the value column was sized against panelW instead of
// the box's usable width (panelW-4), so the first option line overflowed and
// lipgloss re-wrapped the remainder flush against the left border ("│ option").
// Options must always stay indented in the right-hand column.
func TestFormOpenSelectDoesNotWrapFlushLeft(t *testing.T) {
	p, s := testStyles()
	sel := NewSelect("locus-mode", "locus-mode", []Option{
		{Label: "cloud_only", Value: "cloud_only"},
		{Label: "cloud_primary", Value: "cloud_primary"},
		{Label: "open_primary", Value: "open_primary"},
		{Label: "open_only", Value: "open_only"},
	}, "cloud_only")
	// A sibling read-only field with a 15-char label forces colOffset = 20,
	// matching the real settings page (widest label "embedding-model").
	wideLabel := NewReadOnly("embedding-model", "embedding-model", "x", "")
	f := New([]Section{{Title: "Routing", Fields: []Field{wideLabel, sel}}})
	f.Update(arrowDown()) // focus the select
	f.Update(enter())     // open it
	if !sel.Editing() {
		t.Fatal("select should be open")
	}

	out := ansiStrip(f.View(53, p, s))
	for _, opt := range []string{"cloud_primary", "open_primary", "open_only"} {
		// A flush-left wrap renders as the option immediately after the box
		// border + single padding space: "│ <option>".
		if strings.Contains(out, "│ "+opt) {
			t.Fatalf("option %q wrapped flush against the box edge; it must stay in the right column.\n%s", opt, out)
		}
	}
	// Sanity: every option is still present somewhere.
	for _, opt := range []string{"cloud_only", "cloud_primary", "open_primary", "open_only"} {
		if !strings.Contains(out, opt) {
			t.Fatalf("option %q missing from render:\n%s", opt, out)
		}
	}
}
