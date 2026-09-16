package ui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/proto"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type metricsTestClient struct {
	calls   atomic.Int32
	request chan *proto.GetTokenMetricsRequest
	block   bool
}

func (c *metricsTestClient) GetTokenMetrics(ctx context.Context, r *proto.GetTokenMetricsRequest) (*proto.GetTokenMetricsResponse, error) {
	c.calls.Add(1)
	if c.request != nil {
		c.request <- r
	}
	if c.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return metricsTestResponse(), nil
}
func metricsTestResponse() *proto.GetTokenMetricsResponse {
	total := &proto.TokenMetricTotals{Records: 2, Input: &proto.TokenMetricCount{Tokens: 12, KnownRecords: 2}, Output: &proto.TokenMetricCount{Tokens: 0, KnownRecords: 1}, CacheRead: &proto.TokenMetricCount{Tokens: 3, KnownRecords: 1}, Incomplete: 1, UnknownAttribution: 1, Unfinished: 1}
	return &proto.GetTokenMetricsResponse{Population: "attempts", Timezone: "UTC", TrackingSince: "2026-09-01T00:00:00Z", Totals: total, Health: &proto.TokenMetricsHealth{}, Buckets: []*proto.TokenMetricBucket{{Label: "2026-09-15", Partial: true, Totals: total}}, Breakdowns: []*proto.TokenMetricBreakdown{{Dimension: "provider", Value: "fake", Totals: total}, {Dimension: "model", Value: "", Totals: total}}, Warnings: []string{"Unknown counters are not zero."}}
}
func TestTokenMetricsAsyncFiltersAndStaleResults(t *testing.T) {
	client := &metricsTestClient{request: make(chan *proto.GetTokenMetricsRequest, 4)}
	p, cmd := newTokenMetricsPage(client, theme.Styles{}, 100, 40)
	defer p.Close()
	if client.calls.Load() != 0 || !p.loading {
		t.Fatal("constructor must not block on RPC")
	}
	first := cmd().(tokenMetricsResultMsg)
	if client.calls.Load() != 1 {
		t.Fatal("missing call")
	}
	<-client.request
	p.fields[0].SetValue("?")
	p.fields[1].SetValue("=*")
	p.fields[2].SetValue("vision")
	p.invalidate()
	cmd = p.load()
	second := cmd().(tokenMetricsResultMsg)
	req := <-client.request
	if req.Provider == nil || *req.Provider != "" || req.Model == nil || *req.Model != "*" || req.Source == nil || *req.Source != "vision" {
		t.Fatalf("filters %+v", req)
	}
	if p.apply(first) != nil || p.response != nil {
		t.Fatal("stale response accepted")
	}
	if p.apply(second) == nil || p.response == nil || p.loading {
		t.Fatal("current response not applied")
	}
	if p.tick(tokenMetricsTickMsg{id: p.id, revision: first.revision}) != nil {
		t.Fatal("stale tick accepted")
	}
	p.Close()
	if p.tick(tokenMetricsTickMsg{id: p.id, revision: second.revision}) != nil || p.apply(second) != nil {
		t.Fatal("closed page restarted")
	}
	next, _ := newTokenMetricsPage(client, theme.Styles{}, 80, 25)
	defer next.Close()
	if next.apply(second) != nil {
		t.Fatal("reopened page accepted previous page response")
	}
}
func TestTokenMetricsCloseCancelsRPC(t *testing.T) {
	client := &metricsTestClient{request: make(chan *proto.GetTokenMetricsRequest, 1), block: true}
	p, cmd := newTokenMetricsPage(client, theme.Styles{}, 80, 25)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-client.request
	m := Model{content: p, configSurface: &configSurface{active: configTabMetrics}}
	m.closeConfigSurface()
	select {
	case msg := <-done:
		if !errors.Is(msg.(tokenMetricsResultMsg).err, context.Canceled) {
			t.Fatal(msg)
		}
	case <-time.After(time.Second):
		t.Fatal("page close did not cancel request")
	}
	if m.content != nil || !p.closed {
		t.Fatal("page not closed")
	}
}
func TestTokenMetricsRenderStatesAndWidth(t *testing.T) {
	p, _ := newTokenMetricsPage(nil, theme.Styles{}, 90, 45)
	defer p.Close()
	if !strings.Contains(strings.Join(p.lines(), "\n"), "Loading") {
		t.Fatal("loading missing")
	}
	p.apply(tokenMetricsResultMsg{id: p.id, revision: p.revision, response: metricsTestResponse()})
	all := strings.Join(p.lines(), "\n")
	for _, want := range []string{"Tracking since:", "12", "0 reported (1/2 known)", "Incomplete usage: 1", "Unknown attribution: 1", "(partial)", "(incomplete)", "Cache read:", "Reasoning: unknown", "By provider", "By model", "(unknown)"} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %q:\n%s", want, all)
		}
	}
	p.response.Health = &proto.TokenMetricsHealth{Pending: 3, Lost: 1, CoverageIncomplete: true, LastError: "disk busy"}
	all = strings.Join(p.lines(), "\n")
	if !strings.Contains(all, "DEGRADED") || !strings.Contains(all, "gap timing unknown") {
		t.Fatal(all)
	}
	p.response.Health = &proto.TokenMetricsHealth{Pending: 1}
	if !strings.Contains(strings.Join(p.lines(), "\n"), "Pending persistence") {
		t.Fatal("pending missing")
	}
	p.response.Health = &proto.TokenMetricsHealth{}
	if strings.Contains(strings.Join(p.lines(), "\n"), "DEGRADED") {
		t.Fatal("health recovery not rendered")
	}
	p.response.Totals = &proto.TokenMetricTotals{}
	if !strings.Contains(strings.Join(p.lines(), "\n"), "No recorded usage") {
		t.Fatal("empty missing")
	}
	p.response.Population = "external"
	if !strings.Contains(strings.Join(p.lines(), "\n"), "External reports (not internal attempts)") {
		t.Fatal("external label missing")
	}
	for _, width := range []int{1, 20, 40, 80} {
		p.SetSize(width, 20)
		for _, line := range strings.Split(p.View(), "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("width=%d line width=%d %q", width, ansi.StringWidth(line), line)
			}
		}
	}
	p.SetSize(90, 30)
	p.err = errors.New("query failed\x1b[2J")
	all = strings.Join(p.lines(), "\n")
	if !strings.Contains(all, "Query error") || strings.Contains(all, "\x1b[2J") {
		t.Fatal("error rendering", all)
	}
}
func TestTokenMetricsKeyboardAndTabNavigation(t *testing.T) {
	m := Model{width: 80, height: 40}
	cmd := m.openConfigSurface(configTabMetrics)
	p, ok := m.content.(*tokenMetricsPage)
	if !ok || cmd == nil {
		t.Fatal("metrics tab missing")
	}
	defer p.Close()
	m, _, handled := m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled || m.configSurface.focused {
		t.Fatal("focus failed")
	}
	p.cursor = 2
	p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !p.editing {
		t.Fatal("edit missing")
	}
	p.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if !p.dirty || p.loading {
		t.Fatal("typing did not invalidate request")
	}
	m, _, handled = m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !handled || p.editing || m.configSurface.focused {
		t.Fatal("escape must leave editor first")
	}
	p.cursor = 0
	cmd, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if cmd == nil || p.preset != 2 {
		t.Fatal("preset cycling")
	}
	p.cursor = 1
	cmd, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if cmd == nil || !p.external || p.request().Population != "external" {
		t.Fatal("population switching")
	}
	m.configSurface.focused = true
	m, _, handled = m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: '8', Text: "8"})
	if !handled || m.configSurface.active != configTabMetrics {
		t.Fatal("eighth tab shortcut")
	}
	for _, width := range []int{20, 40, 80, 160} {
		line := renderConfigTabStrip(width, configTabMetrics, true, theme.Styles{})
		if ansi.StringWidth(line) > width || !strings.Contains(ansi.Strip(line), "Token Metrics") {
			t.Fatalf("strip %d %q", width, line)
		}
		found := false
		for x := 0; x < width; x++ {
			if configTabAtVisibleX(x, width, configTabMetrics) == configTabMetrics {
				found = true
			}
		}
		if !found {
			t.Fatal("visible tab hit testing")
		}
	}
	m.switchConfigTab(configTabContext)
	if !p.closed {
		t.Fatal("switch did not cancel metrics page")
	}
}
func TestTokenMetricsNoRefreshAfterFilterEditing(t *testing.T) {
	p, cmd := newTokenMetricsPage(&metricsTestClient{}, theme.Styles{}, 80, 30)
	defer p.Close()
	msg := cmd().(tokenMetricsResultMsg)
	p.apply(msg)
	p.cursor = 2
	p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if p.tick(tokenMetricsTickMsg{id: p.id, revision: p.revision}) == nil || p.loading {
		t.Fatal("editing should defer refresh, not terminate its timer")
	}
	cmd, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("unchanged edit exit did not resume refresh")
	}
	// Leaving edit without changing filters must resume refresh.
	if cmd, _ := p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"}); cmd == nil {
		t.Fatal("refresh did not resume")
	}
}

func TestTokenMetricsNarrowFocusedFieldVisible(t *testing.T) {
	p, _ := newTokenMetricsPage(nil, theme.Styles{}, 30, 10)
	defer p.Close()
	for i := 0; i < 7; i++ {
		p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if !strings.Contains(ansi.Strip(p.View()), "> Timezone:") {
		t.Fatalf("focused field hidden:\n%s", p.View())
	}
}

func TestTokenMetricsShiftTabDoesNotLoseRefresh(t *testing.T) {
	p, cmd := newTokenMetricsPage(&metricsTestClient{}, theme.Styles{}, 80, 30)
	defer p.Close()
	p.apply(cmd().(tokenMetricsResultMsg))
	p.cursor = 2
	p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	tick := tokenMetricsTickMsg{id: p.id, revision: p.revision}
	if p.tick(tick) == nil {
		t.Fatal("timer lost during edit")
	}
	m := Model{content: p, configSurface: &configSurface{active: configTabMetrics}}
	m, _, _ = m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if p.editing || !m.configSurface.focused {
		t.Fatal("shift-tab failed")
	}
	if p.tick(tick) == nil || !p.loading {
		t.Fatal("timer did not resume after blur")
	}
}
