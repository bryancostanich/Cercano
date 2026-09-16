package ui

import (
	"fmt"
	"math"
	"strings"

	"cercano/source/server/pkg/proto"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func metricsCompact(v float64) string {
	for _, unit := range []struct {
		scale  float64
		suffix string
	}{{1e12, "T"}, {1e9, "B"}, {1e6, "M"}, {1e3, "k"}} {
		if v >= unit.scale {
			return fmt.Sprintf("%.1f%s", v/unit.scale, unit.suffix)
		}
	}
	return fmt.Sprintf("%.0f", v)
}
func metricsPad(s string, w int) string {
	s = ansi.Truncate(s, maxInt(0, w), "…")
	return s + strings.Repeat(" ", maxInt(0, w-ansi.StringWidth(s)))
}
func (p *tokenMetricsPage) heading(label string, w int) string {
	title := p.styles.Accent.Bold(true).Render(label)
	return title + " " + p.styles.BorderDim.Render(strings.Repeat("─", maxInt(0, w-ansi.StringWidth(label)-1)))
}
func (p *tokenMetricsPage) cards(t *proto.TokenMetricTotals, population string, w int) []string {
	count := t.GetRecords()
	noun := "ATTEMPTS"
	if population == "external" {
		noun = "REPORTS"
	}
	type card struct {
		label, value, note string
		style              lipgloss.Style
	}
	value := func(c *proto.TokenMetricCount) string {
		if c.GetKnownRecords() == 0 {
			return "unknown"
		}
		return metricsCompact(float64(c.GetTokens()))
	}
	known := func(c *proto.TokenMetricCount) string { return fmt.Sprintf("%d/%d known", c.GetKnownRecords(), count) }
	total := metricTotal(t)
	if total != "unknown" {
		total = metricsCompact(metricMagnitude(t))
	}
	note := "reported tokens"
	if t.GetIncomplete() > 0 {
		note = "partial · reported"
	}
	cards := []card{{"INPUT", value(t.GetInput()), known(t.GetInput()), p.styles.Accent}, {"OUTPUT", value(t.GetOutput()), known(t.GetOutput()), p.styles.Info}, {"TOTAL", total, note, p.styles.Bright}, {noun, metricsCompact(float64(count)), fmt.Sprintf("%d incomplete", t.GetIncomplete()), p.styles.Primary}}
	columns := 1
	if w >= 76 {
		columns = 4
	} else if w >= 38 {
		columns = 2
	}
	width := (w - (columns - 1)) / columns
	var out []string
	for start := 0; start < len(cards); start += columns {
		var rows [5][]string
		for _, c := range cards[start:minInt(len(cards), start+columns)] {
			inside := maxInt(1, width-4)
			rows[0] = append(rows[0], p.styles.BorderDim.Render("╭"+strings.Repeat("─", maxInt(0, width-2))+"╮"))
			rows[1] = append(rows[1], p.styles.BorderDim.Render("│")+" "+p.styles.Dim.Render(metricsPad(c.label, inside))+" "+p.styles.BorderDim.Render("│"))
			rows[2] = append(rows[2], p.styles.BorderDim.Render("│")+" "+c.style.Bold(true).Render(metricsPad(c.value, inside))+" "+p.styles.BorderDim.Render("│"))
			rows[3] = append(rows[3], p.styles.BorderDim.Render("│")+" "+p.styles.Muted.Render(metricsPad(c.note, inside))+" "+p.styles.BorderDim.Render("│"))
			rows[4] = append(rows[4], p.styles.BorderDim.Render("╰"+strings.Repeat("─", maxInt(0, width-2))+"╯"))
		}
		for _, row := range rows {
			out = append(out, strings.Join(row, " "))
		}
	}
	return out
}

type metricsPlotSample struct {
	input, output       float64
	records, known      int64
	partial, incomplete bool
	label               string
}

// Each source bucket belongs to exactly one contiguous bin. Sum, never sample
// or average: narrow terminals must not hide spikes or discard reported usage.
func metricsPlotSamples(buckets []*proto.TokenMetricBucket, columns int) []metricsPlotSample {
	if len(buckets) == 0 || columns <= 0 {
		return nil
	}
	n := minInt(columns, len(buckets))
	out := make([]metricsPlotSample, n)
	for i, b := range buckets {
		index := i * n / len(buckets)
		v := &out[index]
		t := b.GetTotals()
		if v.label == "" {
			v.label = b.GetLabel()
		}
		v.input += float64(t.GetInput().GetTokens())
		v.output += float64(t.GetOutput().GetTokens())
		v.records += t.GetRecords()
		v.known += t.GetInput().GetKnownRecords() + t.GetOutput().GetKnownRecords()
		v.partial = v.partial || b.GetPartial()
		v.incomplete = v.incomplete || t.GetIncomplete() > 0 || t.GetInput().GetKnownRecords() < t.GetRecords() || t.GetOutput().GetKnownRecords() < t.GetRecords() || t.GetRecords() == 0
	}
	return out
}
func (p *tokenMetricsPage) timeline(buckets []*proto.TokenMetricBucket, w int) []string {
	if len(buckets) == 0 {
		return []string{p.styles.Muted.Render("No recorded usage in this range.")}
	}
	if w < 22 {
		return []string{p.styles.Muted.Render("Widen terminal to see the usage chart.")}
	}
	samples := metricsPlotSamples(buckets, maxInt(1, (w-9)/2))
	peak := 0.0
	for _, s := range samples {
		peak = math.Max(peak, s.input+s.output)
	}
	cellWidth := maxInt(2, (w-8)/len(samples))
	height := 5
	out := []string{p.styles.Accent.Render("█ input") + "  " + p.styles.Info.Render("█ output") + p.styles.Dim.Render("  · reported tokens")}
	scale := peak
	if scale == 0 {
		scale = 1
	}
	for row := height - 1; row >= 0; row-- {
		label := "      "
		if row == height-1 {
			label = metricsPad(metricsCompact(peak), 6)
		}
		line := p.styles.Dim.Render(label + "│ ")
		for _, s := range samples {
			amount := (s.input+s.output)/scale*float64(height) - float64(row)
			level := int(math.Ceil(math.Min(1, math.Max(0, amount)) * 8))
			glyph := " "
			if level > 0 {
				glyph = string([]rune(" ▁▂▃▄▅▆▇█")[level])
			}
			style := p.styles.Accent
			if float64(row)+math.Min(1, math.Max(0, amount))/2 > s.input/scale*float64(height) {
				style = p.styles.Info
			}
			line += style.Render(strings.Repeat(glyph, cellWidth-1)) + " "
		}
		out = append(out, line)
	}
	baseline := p.styles.Dim.Render("     0└ ")
	for _, s := range samples {
		glyph := "─"
		style := p.styles.Border
		switch {
		case s.records == 0:
			glyph = "·"
		case s.known == 0:
			glyph = "?"
			style = p.styles.Warn
		case s.partial || s.incomplete:
			glyph = "!"
			style = p.styles.Warn
		case s.input+s.output == 0:
			glyph = "0"
		}
		if glyph == "─" {
			baseline += style.Render(strings.Repeat(glyph, cellWidth-1)) + " "
		} else {
			baseline += style.Render(metricsPad(glyph, cellWidth))
		}
	}
	out = append(out, baseline)
	first, last := metricsSafe(buckets[0].Label), metricsSafe(buckets[len(buckets)-1].Label)
	plotWidth := len(samples) * cellWidth
	dates := ansi.Truncate(first, plotWidth, "")
	if plotWidth >= len(first)+len(last)+2 {
		dates = metricsPad(first, plotWidth-len(last)) + last
	}
	out = append(out, p.styles.Dim.Render("        "+dates))
	out = append(out, p.styles.Dim.Render("· no records   ? unknown   0 reported zero   ! partial / incomplete"))
	if len(samples) < len(buckets) {
		out = append(out, p.styles.Dim.Render(fmt.Sprintf("%d calendar buckets → %d contiguous summed columns", len(buckets), len(samples))))
	}
	return out
}
func (p *tokenMetricsPage) breakdown(dimension string, r *proto.GetTokenMetricsResponse, w int) []string {
	var rows []*proto.TokenMetricBreakdown
	peak := 0.0
	for _, b := range r.Breakdowns {
		if b.Dimension == dimension {
			rows = append(rows, b)
			peak = math.Max(peak, metricMagnitude(b.Totals))
		}
	}
	if len(rows) == 0 {
		return []string{p.styles.Muted.Render("No recorded usage.")}
	}
	var lines []string
	for i, b := range rows {
		label := metricsSafe(b.Value)
		if label == "" {
			label = "(unknown)"
		}
		total := metricTotal(b.Totals)
		style := p.styles.Accent
		if dimension == "model" {
			style = p.styles.Info
		}
		labelWidth := maxInt(6, minInt(24, w/3))
		barWidth := maxInt(1, w-labelWidth-16)
		bar := metricBar(b.Totals, peak, barWidth)
		track := p.styles.BorderDim.Render(strings.Repeat("░", maxInt(0, barWidth-ansi.StringWidth(bar))))
		display := total
		if total != "unknown" {
			display = metricsCompact(metricMagnitude(b.Totals))
		}
		if w < 34 {
			lines = append(lines, fmt.Sprintf("%s  %s", label, total))
			continue
		}
		lines = append(lines, p.styles.Dim.Render(fmt.Sprintf("%2d ", i+1))+metricsPad(label, labelWidth)+" "+style.Render(bar)+track+" "+p.styles.Bright.Render(display))
	}
	return lines
}

// Compact controls keep the actual charts above the fold. An active text editor
// gets a full row so its caret remains usable for long filter values.
func (p *tokenMetricsPage) filterRows(w int) ([]string, int) {
	population := "Internal attempts"
	if p.external {
		population = "External reports (separate)"
	}
	values := []string{metricsPresetLabels[p.preset], population, p.fields[0].Value(), p.fields[1].Value(), p.fields[2].Value(), p.fields[3].Value(), p.fields[4].Value(), p.fields[5].Value(), "Enter / r"}
	labels := []string{"Range", "Population", "Provider", "Model", "Source/reporter", "From date", "Through date", "Timezone", "Apply / refresh"}
	columns := 1
	if w >= 100 {
		columns = 3
	} else if w >= 64 {
		columns = 2
	}
	width := (w - (columns-1)*2) / columns
	var out, cells []string
	focus := -1
	flush := func() {
		if len(cells) > 0 {
			out = append(out, strings.Join(cells, "  "))
			cells = nil
		}
	}
	for i, label := range labels {
		if p.editing && i == p.cursor && i >= 2 && i < 8 {
			flush()
			focus = len(out)
			out = append(out, "> "+label+": "+p.fields[i-2].View())
			continue
		}
		prefix := "  "
		style := p.styles.Muted
		if i == p.cursor {
			prefix = "> "
			style = p.styles.Primary
			focus = len(out)
		}
		cells = append(cells, style.Render(metricsPad(prefix+label+": "+metricsSafe(values[i]), width)))
		if len(cells) == columns {
			flush()
		}
	}
	flush()
	return out, focus
}
