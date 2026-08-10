package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/server/pkg/agentclient"
)

func TestIsRuntimeDashboardKey(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		want bool
	}{
		{
			name: "super m",
			msg:  tea.KeyPressMsg{Code: 'm', Mod: tea.ModSuper},
			want: true,
		},
		{
			name: "meta m",
			msg:  tea.KeyPressMsg{Code: 'm', Mod: tea.ModMeta},
			want: true,
		},
		{
			name: "alternate base code",
			msg:  tea.KeyPressMsg{Code: 'µ', BaseCode: 'm', Mod: tea.ModSuper},
			want: true,
		},
		{
			name: "plain m",
			msg:  tea.KeyPressMsg{Code: 'm'},
			want: false,
		},
		{
			name: "super c",
			msg:  tea.KeyPressMsg{Code: 'c', Mod: tea.ModSuper},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRuntimeDashboardKey(tc.msg); got != tc.want {
				t.Fatalf("isRuntimeDashboardKey = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRuntimeDashboardViewShowsRuntimeDashboardData(t *testing.T) {
	m := New(nil, false)
	snapshot := runtimeDashboardSnapshot{
		Config: &agentclient.Config{
			OpenRuntime:    "llama_server",
			OpenModel:      "qwen.gguf",
			EmbeddingModel: "nomic-embed-text",
			CloudProvider:  "openai",
			CloudModel:     "gpt-test",
			CloudBaseURL:   "https://llm.example.test/v1",
		},
		Status: &agentclient.RuntimeStatus{
			Endpoints: []agentclient.RuntimeEndpoint{{
				ID:          "cloud:openai",
				Kind:        "openai_compatible",
				DisplayName: "openai proxy",
				BaseURL:     "https://llm.example.test/v1",
				Scope:       "remote",
				State:       "unknown",
				ActiveRoles: []string{"cloud_fallback"},
				Models:      []string{"gpt-test"},
				AuthState:   "configured",
			}},
			Models: []agentclient.RuntimeModel{{
				ID:            "/models/qwen.gguf",
				DisplayName:   "qwen",
				Runtime:       "llama_server",
				DownloadState: "downloaded",
				SupportsChat:  true,
				SizeBytes:     2 * 1024 * 1024 * 1024,
			}},
			Instances: []agentclient.RuntimeInstance{{
				ID:        "llama-1234567890",
				Runtime:   "llama_server",
				ModelID:   "/models/running.gguf",
				State:     "running",
				PID:       4242,
				Endpoint:  "http://127.0.0.1:18080",
				StartedAt: time.Now().Add(-2 * time.Minute),
			}},
			Logs: []agentclient.RuntimeLogEntry{{
				Timestamp: time.Now(),
				Source:    "llama-server",
				Level:     "info",
				RuntimeID: "llama_server",
				ModelID:   "/models/running.gguf",
				Message:   "server ready",
			}},
		},
	}
	dashboard := &runtimeDashboard{
		width:    118,
		height:   45,
		palette:  m.palette,
		styles:   m.styles,
		snapshot: snapshot,
		loaded:   true,
		focus:    runtimeFocusFilter,
	}
	dashboard.catalogSearch = textinput.New()

	full, _ := dashboard.fullContent()
	view := ansi.Strip(full)
	for _, want := range []string{
		"local config",
		"runtime status",
		"download catalog",
		"installed models",
		"running processes",
		"local model server log",
		"qwen",
		"server ready",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("dashboard view missing %q:\n%s", want, view)
		}
	}
	rows := dashboard.operationRows()
	assertActionRowContains(t, rows, "start", "qwen.gguf")
	assertActionRowContains(t, rows, "stop", "running.gguf")
	assertActionRowContains(t, rows, "restart", "running.gguf")
}

func TestRuntimeDashboardViewSeparatesConfigAndOpenLogs(t *testing.T) {
	m := New(nil, false)
	snapshot := runtimeDashboardSnapshot{
		Config: &agentclient.Config{
			OllamaURL:      "http://mac-studio.local:11434",
			OpenRuntime:    "llama_server",
			OpenModel:      "qwen.gguf",
			EmbeddingModel: "nomic-embed-text",
			CloudProvider:  "openai",
			CloudModel:     "gpt-test",
			CloudBaseURL:   "https://llm.example.test/v1",
			CloudAPIKeySet: true,
			CloudState:     "ok",
		},
		Status: &agentclient.RuntimeStatus{
			Endpoints: []agentclient.RuntimeEndpoint{{
				ID:          "cloud:openai",
				Kind:        "openai_compatible",
				DisplayName: "openai proxy",
				BaseURL:     "https://llm.example.test/v1",
				Scope:       "remote",
				State:       "healthy",
			}},
			Models: []agentclient.RuntimeModel{{
				ID:            "/models/qwen.gguf",
				DisplayName:   "qwen",
				Runtime:       "llama_server",
				DownloadState: "downloaded",
				SupportsChat:  true,
			}, {
				ID:            "catalog:qwen2.5-coder-7b-q4",
				DisplayName:   "Qwen2.5 Coder 7B Q4_K_M",
				Runtime:       "llama_server",
				Source:        "catalog",
				Family:        "qwen",
				Quantization:  "Q4_K_M",
				SizeBytes:     4 * 1024 * 1024 * 1024,
				DownloadState: "not_downloaded",
				SupportsChat:  true,
				SupportsTools: true,
			}},
			Instances: []agentclient.RuntimeInstance{{
				ID:       "llama-1234567890",
				Runtime:  "llama_server",
				ModelID:  "/models/qwen.gguf",
				State:    "running",
				PID:      4242,
				Endpoint: "http://127.0.0.1:18080",
			}},
			Logs: []agentclient.RuntimeLogEntry{
				{
					Timestamp: time.Date(2026, 6, 23, 12, 34, 56, 0, time.Local),
					Source:    "cercano.runtime.llama_server",
					Level:     "info",
					RuntimeID: "llama_server",
					ModelID:   "/models/qwen.gguf",
					Message:   "server ready",
				},
				{
					Source:  "cloud",
					Level:   "info",
					Message: "not a local runtime log",
				},
			},
		},
	}
	dashboard := &runtimeDashboard{
		width:    110,
		height:   45,
		palette:  m.palette,
		styles:   m.styles,
		snapshot: snapshot,
		loaded:   true,
	}
	dashboard.catalogSearch = textinput.New()

	full, _ := dashboard.fullContent()
	view := ansi.Strip(full)
	for _, want := range []string{
		"local config",
		"download catalog",
		"local model server log",
		"llama-server",
		"Qwen2.5 Coder",
		"server ready",
		"installed models",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("dashboard view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "not a local runtime log") {
		t.Fatalf("dashboard log block should filter non-local logs:\n%s", view)
	}
	modelsIdx := strings.Index(view, "installed models")
	logIdx := strings.Index(view, "local model server log")
	if modelsIdx == -1 || logIdx == -1 || logIdx < modelsIdx {
		t.Fatalf("log block should render below installed/process sections:\n%s", view)
	}
	if strings.Contains(view, "- models and processes") {
		t.Fatalf("dashboard should not render the old overlay title:\n%s", view)
	}
}

func TestRuntimeDashboardCatalogSearchFiltersAndShowsDetails(t *testing.T) {
	m := New(nil, false)
	dashboard := &runtimeDashboard{
		width:   112,
		height:  37,
		palette: m.palette,
		styles:  m.styles,
		loaded:  true,
		snapshot: runtimeDashboardSnapshot{
			Config: &agentclient.Config{OpenRuntime: "llama_server"},
			Status: &agentclient.RuntimeStatus{
				Models: []agentclient.RuntimeModel{
					{
						ID:            "catalog:qwen2.5-coder-7b-q4",
						DisplayName:   "Qwen2.5 Coder 7B Q4_K_M",
						Runtime:       "llama_server",
						Source:        "catalog",
						Format:        "gguf",
						Family:        "qwen",
						Quantization:  "Q4_K_M",
						SizeBytes:     4 * 1024 * 1024 * 1024,
						DownloadState: "not_downloaded",
						SupportsChat:  true,
						SupportsTools: true,
					},
					{
						ID:            "catalog:llama-3.2-3b-q4",
						DisplayName:   "Llama 3.2 3B Q4_K_M",
						Runtime:       "llama_server",
						Source:        "catalog",
						Family:        "llama",
						Quantization:  "Q4_K_M",
						DownloadState: "not_downloaded",
						SupportsChat:  true,
					},
				},
			},
		},
	}
	dashboard.catalogSearch.SetValue("Qwen")

	view := ansi.Strip(dashboard.renderCatalogBlock(maxCatalogRows))
	for _, want := range []string{"filter", "Qwen2.5 Coder", "family: qwen", "supports: chat,tools"} {
		if !strings.Contains(view, want) {
			t.Fatalf("catalog view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Llama 3.2") {
		t.Fatalf("catalog search should filter out Llama row:\n%s", view)
	}
}

func TestRuntimeDashboardCatalogTypingFiltersByDefault(t *testing.T) {
	m := New(nil, false)
	dashboard := &runtimeDashboard{
		width:   112,
		height:  37,
		palette: m.palette,
		styles:  m.styles,
		loaded:  true,
		snapshot: runtimeDashboardSnapshot{
			Config: &agentclient.Config{OpenRuntime: "llama_server"},
			Status: &agentclient.RuntimeStatus{
				Models: []agentclient.RuntimeModel{{
					ID:            "catalog:qwen2.5-coder-7b-q4",
					DisplayName:   "Qwen2.5 Coder 7B Q4_K_M",
					Runtime:       "llama_server",
					Source:        "catalog",
					Family:        "qwen",
					DownloadState: "not_downloaded",
				}},
			},
		},
		focus: runtimeFocusFilter,
	}
	dashboard.catalogSearch = textinput.New()
	_ = dashboard.catalogSearch.Focus()

	for _, ch := range "qwen" {
		if _, closed := dashboard.Update(tea.KeyPressMsg{Code: ch, Text: string(ch)}); closed {
			t.Fatal("typing in catalog search should not close dashboard")
		}
	}
	if got := dashboard.catalogSearch.Value(); got != "qwen" {
		t.Fatalf("catalog search value = %q, want qwen", got)
	}
	if view := ansi.Strip(dashboard.renderCatalogBlock(maxCatalogRows)); !strings.Contains(view, "Qwen2.5 Coder") {
		t.Fatalf("catalog should show Qwen after typing:\n%s", view)
	}
}

func TestRuntimeDashboardBlocksUseFullPageWidth(t *testing.T) {
	m := New(nil, false)
	dashboard := &runtimeDashboard{
		width:   110,
		height:  32,
		palette: m.palette,
		styles:  m.styles,
		loaded:  true,
		snapshot: runtimeDashboardSnapshot{
			Config: &agentclient.Config{
				OpenRuntime:   "llama_server",
				OpenModel:     "qwen.gguf",
				CloudProvider: "openai",
				CloudModel:    "gpt-test",
			},
			Status: &agentclient.RuntimeStatus{
				Logs: []agentclient.RuntimeLogEntry{{
					Source:    "cercano.runtime.llama_server",
					RuntimeID: "llama_server",
					Message:   "server ready",
				}},
			},
		},
	}

	pageW := dashboardPanelWidth(dashboard.width)
	localBlock := renderRuntimeDashboardBlock("local config", localConfigFields(dashboard.snapshot), pageW, dashboard.palette, dashboard.styles)
	if got := maxRenderedLineWidth(localBlock); got != pageW {
		t.Fatalf("local block width = %d, want %d:\n%s", got, pageW, ansi.Strip(localBlock))
	}
	if got := maxRenderedLineWidth(dashboard.renderConfigBlocks()); got != pageW {
		t.Fatalf("config row width = %d, want %d:\n%s", got, pageW, ansi.Strip(dashboard.renderConfigBlocks()))
	}
	logBlock := dashboard.renderOpenServerLogBlock(8)
	if got := maxRenderedLineWidth(logBlock); got != pageW {
		t.Fatalf("log block width = %d, want %d:\n%s", got, pageW, ansi.Strip(logBlock))
	}
	installedBlock := dashboard.renderInstalledModelsBlock()
	if got := maxRenderedLineWidth(installedBlock); got != pageW {
		t.Fatalf("installed block width = %d, want %d:\n%s", got, pageW, ansi.Strip(installedBlock))
	}
	downloadsBlock := dashboard.renderDownloadsBlock()
	if got := maxRenderedLineWidth(downloadsBlock); got != pageW {
		t.Fatalf("downloads block width = %d, want %d:\n%s", got, pageW, ansi.Strip(downloadsBlock))
	}
	processesBlock := dashboard.renderProcessesBlock()
	if got := maxRenderedLineWidth(processesBlock); got != pageW {
		t.Fatalf("processes block width = %d, want %d:\n%s", got, pageW, ansi.Strip(processesBlock))
	}
}

func TestRuntimeDashboardLogBlockFillsRemainingContentHeight(t *testing.T) {
	m := New(nil, false)
	dashboard := &runtimeDashboard{
		width:   110,
		height:  37,
		palette: m.palette,
		styles:  m.styles,
		loaded:  true,
		snapshot: runtimeDashboardSnapshot{
			Config: &agentclient.Config{OpenRuntime: "llama_server"},
			Status: &agentclient.RuntimeStatus{
				Logs: []agentclient.RuntimeLogEntry{{
					Source:    "cercano.runtime.llama_server",
					RuntimeID: "llama_server",
					Message:   "server ready",
				}},
			},
		},
	}

	view := dashboard.View()
	if got, want := strings.Count(view, "\n")+1, dashboardContentHeight(dashboard.height); got != want {
		t.Fatalf("dashboard content height = %d, want %d:\n%s", got, want, ansi.Strip(view))
	}
}

func TestRuntimeDashboardShowsDownloadProgressAndActions(t *testing.T) {
	snapshot := runtimeDashboardSnapshot{
		Status: &agentclient.RuntimeStatus{
			Models: []agentclient.RuntimeModel{{
				ID:                 "catalog:qwen",
				DisplayName:        "Qwen Coder",
				Runtime:            "llama_server",
				Source:             "catalog",
				Family:             "qwen",
				Quantization:       "Q4_K_M",
				SizeBytes:          100,
				DownloadState:      "downloading",
				DownloadURL:        "https://example.test/qwen.gguf",
				DownloadedBytes:    50,
				DownloadTotalBytes: 100,
				SupportsChat:       true,
			}},
		},
	}

	m := New(nil, false)
	dashboard := &runtimeDashboard{
		width:    110,
		height:   24,
		palette:  m.palette,
		styles:   m.styles,
		snapshot: snapshot,
		loaded:   true,
		focus:    runtimeFocusFilter,
	}
	dashboard.catalogSearch = textinput.New()
	rows := dashboard.downloadRows()
	assertActionRowContains(t, rows, "Qwen Coder", "downloading 50%")
	assertActionRowContains(t, rows, "cancel", "catalog:qwen")
	view := ansi.Strip(dashboard.renderCatalogBlock(maxCatalogRows) + "\n" + dashboard.renderDownloadsBlock())
	if !strings.Contains(view, "[") || !strings.Contains(view, "50%") {
		t.Fatalf("dashboard should render download progress:\n%s", view)
	}
}

func TestRuntimeDashboardShowsScrollbarWhenContentOverflows(t *testing.T) {
	dashboard := overflowingRuntimeDashboard(t, 14)

	view := dashboard.View()
	if got, want := strings.Count(view, "\n")+1, dashboardContentHeight(dashboard.height); got != want {
		t.Fatalf("dashboard content height = %d, want %d:\n%s", got, want, ansi.Strip(view))
	}
	if !strings.Contains(view, "█") || !strings.Contains(view, "░") {
		t.Fatalf("overflowing dashboard should render scrollbar thumb and track:\n%s", ansi.Strip(view))
	}
}

func TestRuntimeDashboardScrollByMovesVisibleWindow(t *testing.T) {
	dashboard := overflowingRuntimeDashboard(t, 14)

	top := ansi.Strip(dashboard.View())
	if !strings.Contains(top, "local config") {
		t.Fatalf("expected top of dashboard before scrolling:\n%s", top)
	}
	dashboard.ScrollBy(999)
	bottom := ansi.Strip(dashboard.View())
	if !strings.Contains(bottom, "download log line") {
		t.Fatalf("expected log rows after scrolling down:\n%s", bottom)
	}
	if top == bottom {
		t.Fatalf("scrolling should change visible dashboard window:\n%s", top)
	}
}

func TestRuntimeDashboardWheelScrollsContentPage(t *testing.T) {
	m := New(nil, false)
	m.width = 110
	m.height = 14
	dashboard := overflowingRuntimeDashboard(t, 14)
	m.content = dashboard

	next, _ := m.Update(tea.MouseWheelMsg{X: 2, Y: 2, Button: tea.MouseWheelDown})
	got := next.(Model)
	scrolled := got.content.(*runtimeDashboard)
	if scrolled.scrollOffset == 0 {
		t.Fatalf("mouse wheel should scroll runtime dashboard content page")
	}
}

func TestRuntimeDashboardScrollbarDragScrollsContentPage(t *testing.T) {
	m := New(nil, false)
	m.width = 110
	m.height = 14
	m.splashShown = false
	m.content = overflowingRuntimeDashboard(t, 14)

	top := m.contentTop()
	bottom := top + dashboardContentHeight(m.height) - 1
	m = send(t, m, tea.MouseClickMsg{X: m.width, Y: top, Button: tea.MouseLeft})
	if !m.contentScrollbarDragging {
		t.Fatalf("content page scrollbar should be dragging after right-edge click")
	}
	m = send(t, m, tea.MouseMotionMsg{X: m.width, Y: bottom, Button: tea.MouseLeft})

	scrolled := m.content.(*runtimeDashboard)
	if scrolled.scrollOffset == 0 {
		t.Fatalf("dragging content page scrollbar should move runtime dashboard offset")
	}
	m = send(t, m, tea.MouseReleaseMsg{X: m.width, Y: bottom, Button: tea.MouseLeft})
	if m.contentScrollbarDragging {
		t.Fatalf("content page scrollbar drag should end on release")
	}
}

func TestRuntimeDashboardActionRoundTrip(t *testing.T) {
	cases := []runtimeDashboardAction{{
		Kind:       runtimeActionRestart,
		Runtime:    "llama_server",
		ModelID:    "/models/qwen.gguf",
		InstanceID: "llama-abc",
	}, {
		Kind:    runtimeActionDownload,
		Runtime: "llama_server",
		ModelID: "catalog:qwen",
	}, {
		Kind:    runtimeActionCancel,
		Runtime: "llama_server",
		ModelID: "catalog:qwen",
	}, {
		Kind:    runtimeActionDelete,
		Runtime: "llama_server",
		ModelID: "catalog:qwen",
	}}
	for _, want := range cases {
		got, err := parseRuntimeDashboardAction(encodeRuntimeDashboardAction(want))
		if err != nil {
			t.Fatalf("parse action: %v", err)
		}
		if got != want {
			t.Fatalf("action = %+v, want %+v", got, want)
		}
	}
}

func assertActionRowContains(t *testing.T, rows []runtimeDashboardActionRow, label, value string) {
	t.Helper()
	for _, row := range rows {
		if row.Label == label && strings.Contains(row.Value, value) {
			return
		}
	}
	t.Fatalf("missing row label %q containing value %q in %#v", label, value, rows)
}

func maxRenderedLineWidth(value string) int {
	maxW := 0
	for _, line := range strings.Split(value, "\n") {
		if w := ansi.StringWidth(line); w > maxW {
			maxW = w
		}
	}
	return maxW
}

func overflowingRuntimeDashboard(t *testing.T, height int) *runtimeDashboard {
	t.Helper()
	m := New(nil, false)
	logs := make([]agentclient.RuntimeLogEntry, 0, 10)
	for i := 0; i < 10; i++ {
		logs = append(logs, agentclient.RuntimeLogEntry{
			Timestamp: time.Date(2026, 6, 23, 12, 34, i, 0, time.Local),
			Source:    "cercano.runtime.llama_server",
			RuntimeID: "llama_server",
			ModelID:   "catalog:qwen2.5-coder-7b-q4",
			Message:   "download log line",
		})
	}
	snapshot := runtimeDashboardSnapshot{
		Config: &agentclient.Config{
			OpenRuntime:    "llama_server",
			OpenModel:      "qwen.gguf",
			EmbeddingModel: "nomic-embed-text",
			CloudProvider:  "anthropic",
			CloudModel:     "claude-test",
			CloudBaseURL:   "http://127.0.0.1:3456",
		},
		Status: &agentclient.RuntimeStatus{
			Models: []agentclient.RuntimeModel{{
				ID:            "catalog:qwen2.5-coder-7b-q4",
				DisplayName:   "Qwen2.5 Coder 7B Q4_K_M",
				Runtime:       "llama_server",
				Source:        "catalog",
				Family:        "qwen",
				Quantization:  "Q4_K_M",
				DownloadState: "not_downloaded",
				SupportsChat:  true,
				SupportsTools: true,
			}},
			Logs: logs,
		},
	}
	dashboard := &runtimeDashboard{
		width:    110,
		height:   height,
		palette:  m.palette,
		styles:   m.styles,
		snapshot: snapshot,
		loaded:   true,
		focus:    runtimeFocusFilter,
	}
	dashboard.catalogSearch = textinput.New()
	_ = dashboard.catalogSearch.Focus()
	return dashboard
}
