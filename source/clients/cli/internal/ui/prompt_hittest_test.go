package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Ground-truth test for the prompt mouse hit-test.
//
// This asserts promptTop() equals the row where the input is ACTUALLY rendered
// in the frame string — not a re-derivation of the layout. The old probe fed
// coordinates derived from promptTop() itself, so it could never catch a
// promptTop() that drifted from the real render. Here we scan the rendered
// output for the input's sentinel value and compare that row to promptTop().
//
// Regression guarded: promptTop() used to hand-sum the layout and omitted the
// sub-agent tab-strip rows and the spare-row padding above the prompt, so the
// hit-test rejected real prompt clicks in every case except a plain, splash-
// off, full-height frame. That made click-drag selection in the prompt appear
// dead while the scrollback viewport worked.
func TestPromptTop_MatchesRenderedInputRow(t *testing.T) {
	const sentinel = "ZZHITTESTSENTINEL"

	// Row of the sentinel in a rendered frame, 0-based. -1 if absent.
	sentinelRow := func(frame string) int {
		for i, line := range strings.Split(frame, "\n") {
			if strings.Contains(line, sentinel) {
				return i
			}
		}
		return -1
	}

	cases := []struct {
		name  string
		build func() Model
	}{
		{
			name: "plain full height",
			build: func() Model {
				m := New(nil, false)
				m = m.SeedAssistantMarkdown(strings.Repeat("body line\n\n", 40))
				m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
				return m
			},
		},
		{
			name: "splash visible, mostly empty (spare-row padding)",
			build: func() Model {
				m := New(nil, false) // splashShown defaults true
				m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 40})
				return m
			},
		},
		{
			name: "short content, tall terminal (heavy spare padding)",
			build: func() Model {
				m := New(nil, false)
				m = m.SeedAssistantMarkdown("one short line\n")
				m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 50})
				return m
			},
		},
		{
			name: "narrow terminal",
			build: func() Model {
				m := New(nil, false)
				m = m.SeedAssistantMarkdown(strings.Repeat("wrap me across the whole width please ", 6))
				m = send(t, m, tea.WindowSizeMsg{Width: 48, Height: 30})
				return m
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.build()
			m.input.Focus()
			m.input.SetValue(sentinel)

			parts, _ := m.composeFrame()
			frame := strings.Join(parts, "\n")
			gotRow := sentinelRow(frame)
			if gotRow < 0 {
				t.Fatalf("sentinel %q not found in rendered frame; input not drawn?", sentinel)
			}

			want := m.promptTop()
			if gotRow != want {
				t.Fatalf("promptTop()=%d but input actually rendered at row %d — hit-test would reject real clicks by %d row(s)",
					want, gotRow, want-gotRow)
			}

			// And the hit-test must accept a click on that real row.
			if !m.mouseInPrompt(tea.Mouse{X: 2, Y: gotRow}) {
				t.Fatalf("mouseInPrompt rejected a click on the real input row %d", gotRow)
			}
		})
	}
}
