package ui

import (
	"fmt"
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/proto"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func metricsChartBucket(i int, in, out int64) *proto.TokenMetricBucket {
	return &proto.TokenMetricBucket{Label: fmt.Sprintf("2026-09-%02d", i+1), Totals: &proto.TokenMetricTotals{Records: 1, Input: &proto.TokenMetricCount{Tokens: in, KnownRecords: 1}, Output: &proto.TokenMetricCount{Tokens: out, KnownRecords: 1}}}
}
func TestMetricsPlotSummingPreservesUsage(t *testing.T) {
	var buckets []*proto.TokenMetricBucket
	for i := 0; i < 120; i++ {
		buckets = append(buckets, metricsChartBucket(i, int64(i+1), 2))
	}
	for _, columns := range []int{1, 3, 7, 40, 200} {
		samples := metricsPlotSamples(buckets, columns)
		if len(samples) > columns {
			t.Fatal("column cap")
		}
		in, out := 0.0, 0.0
		for _, s := range samples {
			in += s.input
			out += s.output
		}
		if in != 7260 || out != 240 {
			t.Fatalf("lost usage at width %d: %.0f %.0f", columns, in, out)
		}
	}
	if got := metricsPlotSamples(nil, 10); len(got) != 0 {
		t.Fatal(got)
	}
	buckets[1].Partial = true
	buckets[2].Totals = &proto.TokenMetricTotals{}
	sample := metricsPlotSamples(buckets, 1)[0]
	if !sample.partial || !sample.incomplete {
		t.Fatal("partial or empty source bucket hidden")
	}
}
func TestMetricsPlotUnknownEmptyAndZeroDistinct(t *testing.T) {
	p := &tokenMetricsPage{}
	buckets := []*proto.TokenMetricBucket{{Label: "empty", Totals: &proto.TokenMetricTotals{}}, {Label: "unknown", Totals: &proto.TokenMetricTotals{Records: 1}}, metricsChartBucket(2, 0, 0), metricsChartBucket(3, 30, 10)}
	buckets[3].Partial = true
	plot := p.timeline(buckets, 80)
	baseline := ansi.Strip(plot[6])
	for _, glyph := range []string{"·", "?", "0", "!"} {
		if !strings.Contains(baseline, glyph) {
			t.Fatalf("missing %s: %q", glyph, baseline)
		}
	}
	if !strings.Contains(ansi.Strip(plot[1]), "40") {
		t.Fatalf("peak axis missing: %q", plot[1])
	}
	// 40 tokens fills the full five-row scale, 0 and unknown stay blank.
	if !strings.Contains(ansi.Strip(plot[1]), "█") {
		t.Fatal("maximum bar does not reach scale")
	}
	if got := p.timeline(nil, 80); !strings.Contains(got[0], "No recorded usage") {
		t.Fatal(got)
	}
}
func TestMetricsDashboardResponsiveAndFocus(t *testing.T) {
	p, _ := newTokenMetricsPage(nil, theme.Styles{}, 110, 38)
	defer p.Close()
	r := metricsTestResponse()
	r.Totals.Input.Tokens = 17000
	r.Totals.Output.Tokens = 4100
	for i := 0; i < 14; i++ {
		r.Buckets = append(r.Buckets, metricsChartBucket(i, int64((i%5+1)*1100), int64(i*70)))
	}
	p.apply(tokenMetricsResultMsg{id: p.id, revision: p.revision, response: r})
	for _, width := range []int{1, 20, 39, 64, 80, 100, 140} {
		p.SetSize(width, 38)
		for _, line := range strings.Split(p.View(), "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("width %d overflow: %q", width, line)
			}
		}
	}
	p.SetSize(110, 38)
	p.ScrollTo(0)
	view := ansi.Strip(p.View())
	for _, want := range []string{"╭", "INPUT", "OUTPUT", "TOTAL", "ATTEMPTS", "17.0k", "Usage over time", "█ input", "█ output", "By provider", "By model"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing above-fold %s:\n%s", want, view)
		}
	}
	for cursor := 0; cursor < 9; cursor++ {
		p.cursor = cursor
		p.SetSize(100, 12)
		p.revealControl()
		if !strings.Contains(ansi.Strip(p.View()), "> ") {
			t.Fatalf("control %d hidden", cursor)
		}
	}
	p.cursor = 3
	p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	p.revealControl()
	if !p.editing || !strings.Contains(ansi.Strip(p.View()), "> Model:") {
		t.Fatal("full-width editor hidden")
	}
}
func TestMetricsDashboardWarningsBeforeChart(t *testing.T) {
	p, _ := newTokenMetricsPage(nil, theme.Styles{}, 100, 40)
	defer p.Close()
	r := metricsTestResponse()
	r.Health.CoverageIncomplete = true
	r.Health.Lost = 2
	p.apply(tokenMetricsResultMsg{id: p.id, revision: p.revision, response: r})
	all := ansi.Strip(strings.Join(p.lines(), "\n"))
	chart := strings.Index(all, "Usage over time")
	warning := strings.Index(all, "DEGRADED")
	if warning < 0 || warning > chart {
		t.Fatal("gap warning hidden below chart")
	}
	if !strings.Contains(all, "gap timing unknown") {
		t.Fatal("global gap must not be assigned a date")
	}
	p.external = true
	r.Population = "external"
	all = ansi.Strip(strings.Join(p.lines(), "\n"))
	if strings.Index(all, "never add") > strings.Index(all, "Usage over time") || !strings.Contains(all, "REPORTS") {
		t.Fatal("population warning or cards mislabeled")
	}
}
func TestMetricsDashboardPreview(t *testing.T) {
	p, _ := newTokenMetricsPage(nil, theme.NewStyles(theme.Cracker()), 104, 42)
	defer p.Close()
	r := metricsTestResponse()
	r.Totals = &proto.TokenMetricTotals{Records: 7, Input: &proto.TokenMetricCount{KnownRecords: 7}, Output: &proto.TokenMetricCount{KnownRecords: 7}}
	r.Warnings = []string{"Synthetic render fixture; not production history."}
	r.Buckets = nil
	for i, n := range []int64{1200, 4200, 2800, 8500, 6100, 12000, 4500} {
		r.Buckets = append(r.Buckets, metricsChartBucket(i, n, n/3))
	}
	r.Buckets[6].Partial = true
	for _, b := range r.Buckets {
		r.Totals.Input.Tokens += b.Totals.Input.Tokens
		r.Totals.Output.Tokens += b.Totals.Output.Tokens
	}
	r.Breakdowns = nil
	for group, name := range []string{"anthropic", "openai", "ollama"} {
		totals := &proto.TokenMetricTotals{Input: &proto.TokenMetricCount{}, Output: &proto.TokenMetricCount{}}
		for i, b := range r.Buckets {
			if i%3 == group {
				totals.Records++
				totals.Input.KnownRecords++
				totals.Output.KnownRecords++
				totals.Input.Tokens += b.Totals.Input.Tokens
				totals.Output.Tokens += b.Totals.Output.Tokens
			}
		}
		r.Breakdowns = append(r.Breakdowns, &proto.TokenMetricBreakdown{Dimension: "provider", Value: name, Totals: totals}, &proto.TokenMetricBreakdown{Dimension: "model", Value: []string{"model-alpha", "model-beta", "local-model"}[group], Totals: totals})
	}
	p.apply(tokenMetricsResultMsg{id: p.id, revision: p.revision, response: r})
	t.Log("\n" + ansi.Strip(p.View()))
}
