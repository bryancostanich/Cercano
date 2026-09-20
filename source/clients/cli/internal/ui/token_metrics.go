package ui

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/proto"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type tokenMetricsClient interface {
	GetTokenMetrics(context.Context, *proto.GetTokenMetricsRequest) (*proto.GetTokenMetricsResponse, error)
}

var metricsPageIDs atomic.Uint64
var metricsPresets = []string{"today", "7d", "30d", "all", "custom"}
var metricsPresetLabels = []string{"Today", "Past 7 days", "Past 30 days", "All time", "Custom inclusive dates"}

type tokenMetricsPage struct {
	palette                                   theme.Palette
	agent                                     tokenMetricsClient
	styles                                    theme.Styles
	width, height, offset, cursor, preset     int
	controlRow                                int
	external, editing, dirty, loading, closed bool
	fields                                    [6]textinput.Model // provider, model, source, start, end, timezone
	id, revision                              uint64
	cancel                                    context.CancelFunc
	response                                  *proto.GetTokenMetricsResponse
	err                                       error
	timezoneNote                              string
}
type tokenMetricsResultMsg struct {
	id, revision uint64
	response     *proto.GetTokenMetricsResponse
	err          error
}
type tokenMetricsTickMsg struct{ id, revision uint64 }

func viewerMetricsTimezone() (string, string) {
	candidates := []string{os.Getenv("TZ"), time.Local.String()}
	if path, e := filepath.EvalSymlinks("/etc/localtime"); e == nil {
		if i := strings.Index(path, "zoneinfo/"); i >= 0 {
			candidates = append(candidates, path[i+len("zoneinfo/"):])
		}
	}
	for _, name := range candidates {
		name = strings.TrimPrefix(name, ":")
		if name != "" && name != "Local" {
			if _, e := time.LoadLocation(name); e == nil {
				return name, ""
			}
		}
	}
	return "UTC", "Viewer timezone could not be detected; using UTC. Edit Timezone to your IANA zone."
}

// newTokenMetricsPage takes the theme palette alongside the prebuilt styles
// (matching every other config-surface page constructor) so the embedded text
// editors can adopt theme-compatible foregrounds instead of the bubbles
// default — which is plain terminal white and unreadable on light themes.
func newTokenMetricsPage(agent tokenMetricsClient, palette theme.Palette, s theme.Styles, w, h int) (*tokenMetricsPage, tea.Cmd) {
	p := &tokenMetricsPage{agent: agent, palette: palette, styles: s, width: w, height: h, id: metricsPageIDs.Add(1), preset: 1}
	zone, note := viewerMetricsTimezone()
	p.timezoneNote = note
	values := []string{"*", "*", "*", time.Now().Format("2006-01-02"), time.Now().Format("2006-01-02"), zone}
	for i := range p.fields {
		field := textinput.New()
		field.CharLimit = 1025
		// The editing row already renders its own "> label: " prefix, so the
		// widget's built-in prompt would only duplicate it.
		field.Prompt = ""
		field.SetValue(values[i])
		field.SetWidth(maxInt(1, w-20))
		st := field.Styles()
		st.Focused.Text = s.Primary
		st.Blurred.Text = s.Primary
		st.Focused.Prompt = s.Bright
		st.Blurred.Prompt = s.Muted
		st.Cursor.Color = palette.SelectionCaret
		field.SetStyles(st)
		p.fields[i] = field
	}
	return p, p.load()
}
func (p *tokenMetricsPage) ID() contentPageID { return contentPageMetrics }
func (p *tokenMetricsPage) SetSize(w, h int) {
	p.width = w
	p.height = h
	for i := range p.fields {
		p.fields[i].SetWidth(maxInt(1, w-20))
	}
	p.ScrollBy(0)
}
func (p *tokenMetricsPage) Close() {
	p.closed = true
	p.revision++
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
}
func (p *tokenMetricsPage) wantsEscape() bool          { return p.editing }
func (p *tokenMetricsPage) stripForwardKeys() []string { return []string{"r"} }
func (p *tokenMetricsPage) blurBody() {
	p.editing = false
	for i := range p.fields {
		p.fields[i].Blur()
	}
}
func metricsFilter(v string) *string {
	if v == "*" {
		return nil
	}
	if v == "?" {
		v = ""
	} else {
		v = strings.TrimPrefix(v, "=")
	}
	return &v
}
func (p *tokenMetricsPage) request() *proto.GetTokenMetricsRequest {
	population := "attempts"
	if p.external {
		population = "external"
	}
	return &proto.GetTokenMetricsRequest{Preset: metricsPresets[p.preset], Timezone: p.fields[5].Value(), Provider: metricsFilter(p.fields[0].Value()), Model: metricsFilter(p.fields[1].Value()), Source: metricsFilter(p.fields[2].Value()), StartDate: p.fields[3].Value(), EndDate: p.fields[4].Value(), Population: population}
}
func (p *tokenMetricsPage) invalidate() {
	p.revision++
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.loading = false
	p.dirty = true
	p.response = nil
	p.err = nil
}
func (p *tokenMetricsPage) load() tea.Cmd {
	if p.closed {
		return nil
	}
	if p.cancel != nil {
		p.cancel()
	}
	p.revision++
	p.loading = true
	p.dirty = false
	p.err = nil
	req := p.request()
	id, rev, agent := p.id, p.revision, p.agent
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	p.cancel = cancel
	return func() tea.Msg {
		defer cancel()
		if agent == nil {
			return tokenMetricsResultMsg{id: id, revision: rev, err: errors.New("agent is disconnected")}
		}
		out, err := agent.GetTokenMetrics(ctx, req)
		return tokenMetricsResultMsg{id: id, revision: rev, response: out, err: err}
	}
}
func (p *tokenMetricsPage) apply(msg tokenMetricsResultMsg) tea.Cmd {
	if p.closed || msg.id != p.id || msg.revision != p.revision {
		return nil
	}
	p.loading = false
	p.response = msg.response
	p.err = msg.err
	if p.response == nil && p.err == nil {
		p.err = errors.New("empty metrics response")
	}
	p.ScrollBy(0)
	id, rev := p.id, p.revision
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return tokenMetricsTickMsg{id: id, revision: rev} })
}
func (p *tokenMetricsPage) tick(msg tokenMetricsTickMsg) tea.Cmd {
	if p.closed || msg.id != p.id || msg.revision != p.revision || p.dirty || p.loading {
		return nil
	}
	if p.editing {
		return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return msg })
	}
	return p.load()
}
func (p *tokenMetricsPage) Update(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	key := msg.String()
	if key == "up" || key == "down" || key == "tab" || key == "enter" {
		defer p.revealControl()
	}
	if p.editing {
		if key == "esc" || key == "enter" || key == "tab" {
			p.fields[p.cursor-2].Blur()
			p.editing = false
			if key == "enter" {
				return p.load(), false
			}
			if key == "tab" {
				p.cursor = (p.cursor + 1) % 9
			}
			if !p.dirty {
				return p.load(), false
			}
			return nil, false
		}
		before := p.fields[p.cursor-2].Value()
		var cmd tea.Cmd
		p.fields[p.cursor-2], cmd = p.fields[p.cursor-2].Update(msg)
		if before != p.fields[p.cursor-2].Value() {
			p.invalidate()
		}
		return cmd, false
	}
	switch key {
	case "r":
		return p.load(), false
	case "up":
		p.cursor = (p.cursor + 8) % 9
		p.offset = 0
	case "down", "tab":
		p.cursor = (p.cursor + 1) % 9
		p.offset = 0
	case "left", "right":
		dir := 1
		if key == "left" {
			dir = -1
		}
		if p.cursor == 0 {
			p.preset = (p.preset + dir + len(metricsPresets)) % len(metricsPresets)
			p.invalidate()
			return p.load(), false
		}
		if p.cursor == 1 {
			p.external = !p.external
			p.invalidate()
			return p.load(), false
		}
	case "enter":
		if p.cursor == 0 {
			p.preset = (p.preset + 1) % len(metricsPresets)
			p.invalidate()
			return p.load(), false
		}
		if p.cursor == 1 {
			p.external = !p.external
			p.invalidate()
			return p.load(), false
		}
		if p.cursor == 8 {
			return p.load(), false
		}
		p.editing = true
		return p.fields[p.cursor-2].Focus(), false
	case "pgdown":
		p.ScrollBy(dashboardContentHeight(p.height))
	case "pgup":
		p.ScrollBy(-dashboardContentHeight(p.height))
	case "home":
		p.ScrollTo(0)
	case "end":
		p.ScrollTo(len(p.lines()))
	}
	return nil, false
}
func (p *tokenMetricsPage) handlePaste(text string) bool {
	if !p.editing {
		return false
	}
	i := p.cursor - 2
	p.fields[i].SetValue(metricsSafe(text))
	p.invalidate()
	return true
}
func metricsSafe(v string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, ansi.Strip(v))
}
func metricNumber(c *proto.TokenMetricCount, records int64) string {
	if c == nil || c.KnownRecords == 0 {
		return "unknown (no reported counters)"
	}
	if c.KnownRecords < records {
		return fmt.Sprintf("%d reported (%d/%d known)", c.Tokens, c.KnownRecords, records)
	}
	return fmt.Sprintf("%d", c.Tokens)
}
func metricTotal(t *proto.TokenMetricTotals) string {
	if t == nil {
		return "unknown"
	}
	if t.GetInput().GetKnownRecords() == 0 && t.GetOutput().GetKnownRecords() == 0 {
		return "unknown"
	}
	a := big.NewInt(t.GetInput().GetTokens())
	return a.Add(a, big.NewInt(t.GetOutput().GetTokens())).String()
}
func metricMagnitude(t *proto.TokenMetricTotals) float64 {
	return float64(t.GetInput().GetTokens()) + float64(t.GetOutput().GetTokens())
}
func metricBar(t *proto.TokenMetricTotals, peak float64, width int) string {
	n := 0
	if peak > 0 {
		n = int(metricMagnitude(t) / peak * float64(width))
	}
	n = clampInt(n, 0, width)
	return strings.Repeat("█", n)
}
func (p *tokenMetricsPage) lines() []string {
	totalW := maxInt(1, p.width-2)
	w := maxInt(1, totalW-4)
	var lines, body []string
	title := ""
	controlIndex := -1
	add := func(text string) { body = append(body, metricsWrapStyled(text, w)...) }
	flush := func() {
		if title == "" {
			lines = append(lines, body...)
			body = nil
			return
		}
		lines = append(lines, "")
		if totalW >= 16 {
			if controlIndex >= 0 {
				p.controlRow = len(lines) + 2 + controlIndex
			}
			lines = append(lines, strings.Split(renderRuntimeDashboardTextBlock(title, body, totalW, 0, p.palette, p.styles), "\n")...)
		} else {
			lines = append(lines, metricsWrapStyled(p.styles.Accent.Render(title), totalW)...)
			if controlIndex >= 0 {
				p.controlRow = len(lines) + controlIndex
			}
			lines = append(lines, body...)
		}
		body = nil
		controlIndex = -1
	}
	section := func(label string) { flush(); title = label }
	add(p.styles.Accent.Bold(true).Render("◈ Token Metrics") + p.styles.Muted.Render("  /  reported consumption"))
	add(p.styles.Muted.Render("↑↓ select · Enter edit · ←→ options · r refresh · PgUp/PgDn scroll · Shift+Tab tabs"))
	section("Filters")
	controls, focus := p.filterRows(w)
	for i, row := range controls {
		if i == focus {
			controlIndex = len(body)
		}
		add(row)
	}
	if p.dirty {
		add(p.styles.Warn.Render("Filters changed — press Enter or refresh to apply."))
		flush()
		return lines
	}
	if p.loading {
		add(p.styles.Info.Render("◌ Loading metrics… (persistence is asynchronous)"))
	}
	if p.err != nil {
		add(p.styles.Error.Render("Query error: " + metricsSafe(p.err.Error())))
		add(p.styles.Muted.Render("Press r to retry. No zero-usage claim is made while metrics are unavailable."))
		flush()
		return lines
	}
	r := p.response
	if r == nil {
		flush()
		return lines
	}
	t, h := r.GetTotals(), r.GetHealth()
	records := t.GetRecords()
	state := "No known collection gaps"
	statusStyle := p.styles.Success
	if h.GetCoverageIncomplete() || h.GetLost() > 0 || h.GetUncertain() > 0 {
		state = "DEGRADED — known coverage gaps"
		statusStyle = p.styles.Warn
	} else if h.GetLastError() != "" {
		state = "DEGRADED — persistence error"
		statusStyle = p.styles.Error
	} else if h.GetPending() > 0 {
		state = "Pending persistence"
		statusStyle = p.styles.Info
	}
	section("Summary")
	add(statusStyle.Render("● "+state) + p.styles.Muted.Render(fmt.Sprintf("  ·  %d incomplete  ·  %d unknown attribution", t.GetIncomplete(), t.GetUnknownAttribution())))
	if h.GetCoverageIncomplete() || h.GetLost() > 0 || h.GetUncertain() > 0 {
		add(p.styles.Warn.Render("! Coverage gaps — gap timing unknown; empty periods are not measured zeros."))
	}
	if p.external {
		add(p.styles.Warn.Render("External reports are separate — never add them to internal attempts."))
	}
	if len(r.Warnings) > 0 {
		add(p.styles.Muted.Render("Coverage / precision limitations apply · see Accounting notes below."))
	}
	for _, row := range p.cards(t, r.Population, w) {
		add(row)
	}
	section("Usage over time")
	for _, row := range p.timeline(r.Buckets, w) {
		add(row)
	}
	add(p.styles.Muted.Render("Tracking since: " + metricsSafe(r.TrackingSince) + " · timezone " + metricsSafe(r.Timezone)))
	if records == 0 {
		add(p.styles.Muted.Render("No recorded usage in this range. This is not evidence of measured zero usage."))
	}
	for _, dimension := range []string{"provider", "model"} {
		section("By " + dimension)
		for _, row := range p.breakdown(dimension, r, w) {
			add(row)
		}
	}
	if r.BreakdownsTruncated {
		add(p.styles.Muted.Render("Breakdowns show the top 50 values per dimension; totals include all matching records."))
	}
	section("Exact reported usage")
	noun := "Inference attempts"
	if r.Population == "external" {
		noun = "External reports (not internal attempts)"
	}
	add(p.styles.Primary.Render(fmt.Sprintf("%s: %d", noun, records)))
	add(p.styles.Primary.Render("Input: " + metricNumber(t.GetInput(), records) + " · Output: " + metricNumber(t.GetOutput(), records)))
	add(p.styles.Primary.Render("Total reported input + output: " + metricTotal(t) + " (partial when usage is incomplete)"))
	add(p.styles.Primary.Render("Cache read: " + metricNumber(t.GetCacheRead(), records) + " · Cache write: " + metricNumber(t.GetCacheWrite(), records) + " · Reasoning: " + metricNumber(t.GetReasoning(), records)))
	add(p.styles.Primary.Render(fmt.Sprintf("Incomplete usage: %d · Unknown attribution: %d · Unfinished: %d", t.GetIncomplete(), t.GetUnknownAttribution(), t.GetUnfinished())))
	// Per-bucket details retain exact amounts and calendar labels below the plot.
	for _, b := range r.Buckets {
		label := metricsSafe(b.Label)
		if b.Partial {
			label += " (partial)"
		}
		if b.GetTotals().GetIncomplete() > 0 {
			label += " (incomplete)"
		}
		value := metricTotal(b.Totals)
		if b.GetTotals().GetRecords() == 0 {
			value = "no records"
		}
		add(p.styles.Muted.Render(label) + "  " + p.styles.Primary.Render(value))
	}
	section("Accounting notes")
	add(p.styles.Primary.Render("Accounting health (global): ") + statusStyle.Render(state))
	add(p.styles.Primary.Render(fmt.Sprintf("Pending: %d · Retries: %d · Write failures: %d · Lost observations: %d · Uncertain: %d", h.GetPending(), h.GetRetries(), h.GetWriteFailures(), h.GetLost(), h.GetUncertain())))
	last := h.GetLastPersistence()
	if last == "" {
		last = "none recorded"
	}
	add(p.styles.Primary.Render("Last persistence: " + metricsSafe(last)))
	if h.GetOldestPending() != "" {
		add(p.styles.Primary.Render("Oldest pending: " + metricsSafe(h.GetOldestPending())))
	}
	if h.GetLastError() != "" {
		add(p.styles.Warn.Render("Persistence warning: " + metricsSafe(h.LastError)))
	}
	for _, warning := range r.Warnings {
		add(p.styles.Muted.Render("Note: " + metricsSafe(warning)))
	}
	loc, e := time.LoadLocation(r.Timezone)
	if e != nil {
		loc = time.UTC
	}
	add(p.styles.Primary.Render(fmt.Sprintf("Window: %s to %s (exclusive)", time.UnixMicro(r.StartUnixMicros).In(loc).Format("2006-01-02 15:04 MST"), time.UnixMicro(r.EndUnixMicros).In(loc).Format("2006-01-02 15:04 MST"))))
	add(p.styles.Muted.Render("Snapshot: " + metricsSafe(r.GeneratedAt) + " · updates every 5s while open"))
	add(p.styles.Muted.Render("Filters: * all, ? unknown, =literal for reserved values. Custom dates include both endpoints."))
	if p.timezoneNote != "" {
		add(p.styles.Muted.Render(p.timezoneNote))
	}
	flush()
	return lines
}
func (p *tokenMetricsPage) ScrollState() contentPageScrollState {
	total := len(p.lines())
	height := maxInt(1, dashboardContentHeight(p.height))
	return contentPageScrollState{Total: total, Height: height, Offset: clampInt(p.offset, 0, maxInt(0, total-height))}
}
func (p *tokenMetricsPage) ScrollBy(delta int)  { p.offset += delta; p.offset = p.ScrollState().Offset }
func (p *tokenMetricsPage) ScrollTo(offset int) { p.offset = offset; p.ScrollBy(0) }
func (p *tokenMetricsPage) View() string {
	state := p.ScrollState()
	rendered := renderScrollable(p.lines(), state.Height, maxInt(1, p.width-2), state.Offset, p.styles)
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		lines[i] = ansi.Cut(line, 0, maxInt(0, p.width))
	}
	return strings.Join(lines, "\n")
}

func (p *tokenMetricsPage) revealControl() {
	height := maxInt(1, dashboardContentHeight(p.height))
	p.lines()
	if p.controlRow < p.offset {
		p.offset = p.controlRow
	}
	if p.controlRow >= p.offset+height {
		p.offset = p.controlRow - height + 1
	}
}
