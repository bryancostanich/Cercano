package ui

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The metrics tab must render with explicit, theme-compatible foregrounds: no
// text may inherit the terminal default (white on light themes), and no text
// may use palette tokens whose contrast fails on the daylight background.

var metricsSGR = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// metricsForegrounds returns the truecolor foregrounds ("#RRGGBB") used by one
// rendered line.
func metricsForegrounds(line string) []string {
	var out []string
	for _, m := range metricsSGR.FindAllStringSubmatch(line, -1) {
		parts := strings.Split(m[1], ";")
		for i := 0; i+4 < len(parts); i++ {
			if parts[i] == "38" && parts[i+1] == "2" {
				r, _ := strconv.Atoi(parts[i+2])
				g, _ := strconv.Atoi(parts[i+3])
				b, _ := strconv.Atoi(parts[i+4])
				out = append(out, fmt.Sprintf("#%02X%02X%02X", r, g, b))
			}
		}
	}
	return out
}

func metricsLuminance(hex string) float64 {
	var r, g, b int
	fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	lin := func(v float64) float64 {
		s := v / 255.0
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(float64(r)) + 0.7152*lin(float64(g)) + 0.0722*lin(float64(b))
}

func metricsContrast(a, b string) float64 {
	la, lb := metricsLuminance(a), metricsLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func metricsDaylightPage(t *testing.T, width int) *tokenMetricsPage {
	t.Helper()
	var daylight theme.Theme
	for _, th := range theme.BuiltinThemes() {
		if th.Name == "daylight" {
			daylight = th
		}
	}
	p, _ := newTokenMetricsPage(nil, daylight.Palette, theme.NewStyles(daylight.Palette), width, 40)
	t.Cleanup(p.Close)
	p.apply(tokenMetricsResultMsg{id: p.id, revision: p.revision, response: metricsTestResponse()})
	return p
}

// decorativeRunes are chrome glyphs (section rules, card borders, bar tracks).
// Lines made only of these are structural, not text, and are exempt from the
// text-contrast floor — the same BorderDim chrome the settings form uses.
func metricsDecorativeOnly(plain string) bool {
	for _, r := range strings.Trim(plain, " ") {
		if !strings.ContainsRune("─│╭╮╰╯░", r) {
			return false
		}
	}
	return plain != ""
}

func TestTokenMetricsThemeTextContrast(t *testing.T) {
	for _, th := range theme.BuiltinThemes() {
		if th.Name != "daylight" && th.Name != "phosphor" {
			continue
		}
		for _, width := range []int{40, 100} {
			p, _ := newTokenMetricsPage(nil, th.Palette, theme.NewStyles(th.Palette), width, 40)
			t.Cleanup(p.Close)
			p.apply(tokenMetricsResultMsg{id: p.id, revision: p.revision, response: metricsTestResponse()})
			for _, line := range p.lines() {
				fg, pos := "", 0
				check := func(text string) {
					plain := strings.TrimSpace(ansi.Strip(text))
					if plain == "" || metricsDecorativeOnly(plain) {
						return
					}
					if fg == "" {
						t.Fatalf("%s width=%d unthemed text %q", th.Name, width, plain)
					}
					r, g, b, _ := th.Palette.BgDeep.RGBA()
					if metricsContrast(fg, fmt.Sprintf("#%02X%02X%02X", r>>8, g>>8, b>>8)) < 3 {
						t.Fatalf("%s width=%d low contrast %s: %q", th.Name, width, fg, plain)
					}
				}
				for _, loc := range metricsSGR.FindAllStringSubmatchIndex(line, -1) {
					check(line[pos:loc[0]])
					seq := line[loc[0]:loc[1]]
					body := line[loc[2]:loc[3]]
					if body == "" || body == "0" || body == "39" {
						fg = ""
					}
					colors := metricsForegrounds(seq)
					if len(colors) > 0 {
						fg = colors[len(colors)-1]
					}
					pos = loc[1]
				}
				check(line[pos:])
			}
		}
	}
}

// The focused editor row must adopt the theme foreground — the bubbles
// textinput default is terminal white (ANSI 37), invisible on daylight's tan.
func TestTokenMetricsEditingRowUsesThemeForeground(t *testing.T) {
	p := metricsDaylightPage(t, 80)
	p.cursor = 2
	p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	row := ""
	for _, line := range p.lines() {
		if strings.Contains(ansi.Strip(line), "> Provider:") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatal("editing row missing")
	}
	if !strings.Contains(row, "\x1b[38;2;59;48;32m") { // daylight Primary #3B3020
		t.Fatalf("typed value not themed: %q", row)
	}
	if strings.Contains(row, "\x1b[37m") || strings.Contains(row, "\x1b[7;37") {
		t.Fatalf("hardcoded white prompt/cursor survives: %q", row)
	}
	if !strings.Contains(row, "\x1b[38;2;194;65;12m") && !strings.Contains(row, "7;38;2;194;65;12") {
		t.Fatalf("cursor not using theme SelectionCaret #C2410C: %q", row)
	}
}

// Every section renders with the shared config-page section wrapper (the same
// "─ Title ───" chrome the settings form uses), wide and narrow.
func TestTokenMetricsSectionWrappers(t *testing.T) {
	sections := []string{"Filters", "Summary", "Usage over time", "By provider", "By model", "Exact reported usage", "Accounting notes"}
	for _, width := range []int{40, 100} {
		p := metricsDaylightPage(t, width)
		all := ansi.Strip(strings.Join(p.lines(), "\n"))
		if strings.Count(all, "╭") < len(sections) {
			t.Fatal("missing section containers")
		}
		for _, label := range sections {
			if !strings.Contains(all, "│ "+label+" ") {
				t.Fatalf("width=%d missing config-page section wrapper for %q:\n%s", width, label, all)
			}
		}
	}
}
