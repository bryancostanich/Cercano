package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/form"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func layoutStrip(s string) string {
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

func sampleSettingsPage(w, h int) *settingsPage {
	p := theme.Cracker()
	s := theme.NewStyles(p)
	cfg := &agentclient.Config{
		LocalRuntime: "ollama", LocalModel: "qwen3-coder-next:latest", OllamaURL: "http://localhost:11434",
		EmbeddingModel: "nomic-embed-text", CloudProvider: "anthropic", CloudModel: "claude-opus-4-7",
		CloudBaseURL: "http://127.0.0.1:3456", CloudState: "ok", Port: "50052", LocusMode: "cloud_only",
	}
	sp := &settingsPage{
		palette: p, styles: s, width: w, height: h,
		cfg: cfg, mode: "permissive",
		themes:  theme.NewRegistry(theme.BuiltinThemes()),
		working: theme.Theme{Name: "cracker", Palette: theme.Cracker()},
	}
	sp.form = form.New(sp.snapshotSections())
	sp.form.OnCommit = sp.onCommit
	sp.form.OnReload = sp.snapshotSections
	return sp
}

// openLocus navigates to and opens the locus-mode select (flat field index 9:
// Local Model has 4 fields, Cloud has 5).
func openLocus(sp *settingsPage) {
	for i := 0; i < 9; i++ {
		sp.form.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	sp.form.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
}

// TestSettingsScrollReachesLastField navigates with the down arrow to the last
// field on a terminal short enough to require scrolling, and verifies the field
// lands inside the visible viewport (offset .. offset+viewportHeight) rather
// than below the fold behind the prompt bar.
func TestSettingsScrollReachesLastField(t *testing.T) {
	sp := sampleSettingsPage(96, 39)
	down := tea.KeyPressMsg{Code: tea.KeyDown}
	total := len(sp.form.Lines(sp.width, sp.palette, sp.styles))
	vh := sp.viewportHeight()
	if total <= vh {
		t.Fatalf("test needs a form taller than the viewport (total=%d vh=%d)", total, vh)
	}
	for i := 0; i < 40; i++ { // more than enough to reach the last field
		sp.Update(down)
	}
	fl := sp.form.FocusedLine()
	off := sp.ScrollState().Offset
	if fl < off || fl >= off+vh {
		t.Fatalf("last field at line %d is outside the visible window [%d,%d) — cannot scroll to bottom", fl, off, off+vh)
	}
}

// TestSettingsSetStylesPreservesCursor verifies a live theme edit (which
// rebuilds the form via SetStyles) keeps the focused field, rather than jumping
// the cursor back to the top.
func TestSettingsSetStylesPreservesCursor(t *testing.T) {
	sp := sampleSettingsPage(96, 40)
	for i := 0; i < 5; i++ {
		sp.form.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	want := sp.form.Cursor()
	if want == 0 {
		t.Fatal("precondition: cursor should have advanced past 0")
	}
	sp.SetStyles(theme.NewStyles(theme.Cracker()), theme.Cracker())
	if got := sp.form.Cursor(); got != want {
		t.Fatalf("cursor after SetStyles = %d, want %d (must be preserved)", got, want)
	}
}

// TestSettingsBoxesFillWidth checks the section boxes span the content region
// (width-2, matching the scrollbar reservation) at a wide terminal, rather than
// stopping short of the scrollbar.
func TestSettingsBoxesFillWidth(t *testing.T) {
	sp := sampleSettingsPage(96, 60)
	out := layoutStrip(sp.View())
	want := 96 - 2 // dashboardPanelWidth(96)
	found := false
	for _, ln := range strings.Split(out, "\n") {
		start := strings.IndexRune(ln, '╭')
		if start < 0 {
			continue
		}
		// width of the box border from ╭ to ╮ inclusive
		n := 0
		for _, r := range []rune(ln[start:]) {
			n++
			if r == '╮' {
				break
			}
		}
		if n == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no section box border of width %d found; boxes do not fill the content region:\n%s", want, out)
	}
}

// TestSettingsNarrowNoFlushLeftWrap checks that at a very narrow width every
// box content line begins (after the "│ " border+padding) with a space or a
// rule glyph — i.e. no value/option wrapped flush against the left margin.
func TestSettingsNarrowNoFlushLeftWrap(t *testing.T) {
	sp := sampleSettingsPage(30, 80)
	openLocus(sp)
	out := layoutStrip(sp.View())
	for _, ln := range strings.Split(out, "\n") {
		rs := []rune(ln)
		if len(rs) < 3 || rs[0] != '│' {
			continue // not a box content line
		}
		// rs[0]='│', rs[1]=' ' (padding). rs[2] is the first content glyph.
		if rs[1] != ' ' {
			continue
		}
		first := rs[2]
		if first != ' ' && first != '─' && first != '╰' && first != '╯' {
			t.Fatalf("content wrapped flush to the left margin (line %q in):\n%s", ln, out)
		}
	}
}

// TestSettingsNarrowSelectGoesUnderLabel verifies the open picker drops its
// options under the label (indented) at a narrow width, rather than into a
// right-hand column.
func TestSettingsNarrowSelectGoesUnderLabel(t *testing.T) {
	sp := sampleSettingsPage(30, 80)
	openLocus(sp)
	out := layoutStrip(sp.View())
	lines := strings.Split(out, "\n")
	labelRow := -1
	for i, ln := range lines {
		if strings.Contains(ln, "locus-mode") {
			labelRow = i
			break
		}
	}
	if labelRow < 0 {
		t.Fatalf("locus-mode label not found:\n%s", out)
	}
	// The label row must NOT also carry an option (options are underneath).
	if strings.Contains(lines[labelRow], "cloud_only") {
		t.Fatalf("at narrow width options should be under the label, not on it:\n%s", lines[labelRow])
	}
	// A following row must carry the first option, indented (leading spaces).
	joined := strings.Join(lines[labelRow+1:], "\n")
	if !strings.Contains(joined, "cloud_only") {
		t.Fatalf("options should appear under the label:\n%s", out)
	}
	// In the narrow under-label layout the options must form a single vertical
	// column: one per line, with no "·" separator joining options. (Only lines
	// that actually carry locus options are checked — section titles like
	// "Theme · Chrome" legitimately contain a middle dot.)
	opts := []string{"cloud_only", "cloud_primary", "local_primary", "local_only"}
	for _, ln := range lines {
		n := 0
		for _, o := range opts {
			if strings.Contains(ln, o) {
				n++
			}
		}
		if n == 0 {
			continue // not an option line
		}
		if n > 1 {
			t.Fatalf("narrow under-label options must be one per line, found %d on:\n%s", n, ln)
		}
		if strings.Contains(ln, "·") {
			t.Fatalf("narrow under-label option line must not use a horizontal separator:\n%s", ln)
		}
	}
}
