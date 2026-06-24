package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/clients/cli/internal/overlay"
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

func TestRuntimeRowsFromSnapshotShowsRuntimeDashboardData(t *testing.T) {
	rows := runtimeRowsFromSnapshot(runtimeDashboardSnapshot{
		Config: &agentclient.Config{
			LocalRuntime:   "llama_server",
			LocalModel:     "qwen.gguf",
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
	})

	assertRowContains(t, rows, "external endpoints", "1 endpoint")
	assertRowContains(t, rows, "endpoint openai proxy", "openai_compatible")
	assertRowContains(t, rows, "downloaded models", "1 model")
	assertRowContains(t, rows, "model qwen", "llama_server")
	assertRowContains(t, rows, "start qwen.gguf", "llama_server")
	assertRowContains(t, rows, "runtime processes", "1 process")
	assertRowContains(t, rows, "process running.gguf", "running")
	assertRowContains(t, rows, "stop running.gguf", "llama_server")
	assertRowContains(t, rows, "restart running.gguf", "llama_server")
	assertRowContains(t, rows, "recent logs", "1 entry")
	assertAnyRowContains(t, rows, "server ready")
}

func TestRuntimeDashboardViewSeparatesConfigAndLocalLogs(t *testing.T) {
	m := New(nil, false)
	snapshot := runtimeDashboardSnapshot{
		Config: &agentclient.Config{
			OllamaURL:      "http://mac-studio.local:11434",
			LocalRuntime:   "llama_server",
			LocalModel:     "qwen.gguf",
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
		list:     overlay.New("models and processes", runtimeActionRowsFromSnapshot(snapshot), overlay.Hooks{}),
	}

	view := ansi.Strip(dashboard.View())
	for _, want := range []string{
		"local config",
		"cloud / external",
		"download catalog",
		"local model server log",
		"llama-server",
		"openai",
		"Qwen2.5 Coder",
		"server ready",
		"models and processes",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("dashboard view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "not a local runtime log") {
		t.Fatalf("dashboard log block should filter non-local logs:\n%s", view)
	}
	modelsIdx := strings.Index(view, "models and processes")
	logIdx := strings.Index(view, "local model server log")
	if modelsIdx == -1 || logIdx == -1 || logIdx < modelsIdx {
		t.Fatalf("log block should render below models/processes:\n%s", view)
	}
}

func TestRuntimeDashboardCatalogSearchFiltersAndShowsDetails(t *testing.T) {
	m := New(nil, false)
	dashboard := &runtimeDashboard{
		width:   112,
		height:  37,
		palette: m.palette,
		styles:  m.styles,
		snapshot: runtimeDashboardSnapshot{
			Config: &agentclient.Config{LocalRuntime: "llama_server"},
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
	dashboard.list = overlay.New("models and processes", runtimeActionRowsFromSnapshot(dashboard.snapshot), overlay.Hooks{})

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
		snapshot: runtimeDashboardSnapshot{
			Config: &agentclient.Config{LocalRuntime: "llama_server"},
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
		focus: runtimeFocusCatalog,
	}
	dashboard.catalogSearch = textinput.New()
	_ = dashboard.catalogSearch.Focus()
	dashboard.list = overlay.New("models and processes", runtimeActionRowsFromSnapshot(dashboard.snapshot), overlay.Hooks{})

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
		snapshot: runtimeDashboardSnapshot{
			Config: &agentclient.Config{
				LocalRuntime:  "llama_server",
				LocalModel:    "qwen.gguf",
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
	dashboard.list = overlay.New("models and processes", runtimeActionRowsFromSnapshot(dashboard.snapshot), overlay.Hooks{})

	pageW := dashboardPanelWidth(dashboard.width)
	leftW, rightW := dashboardConfigBlockWidths(pageW)
	if leftW != pageW/2 || rightW != pageW-leftW {
		t.Fatalf("top block widths = %d/%d, want 50%% split of %d", leftW, rightW, pageW)
	}
	localBlock := renderRuntimeDashboardBlock("local config", localConfigFields(dashboard.snapshot), leftW, dashboard.palette, dashboard.styles)
	cloudBlock := renderRuntimeDashboardBlock("cloud / external", cloudConfigFields(dashboard.snapshot), rightW, dashboard.palette, dashboard.styles)
	if got := maxRenderedLineWidth(localBlock); got != leftW {
		t.Fatalf("local block width = %d, want %d:\n%s", got, leftW, ansi.Strip(localBlock))
	}
	if got := maxRenderedLineWidth(cloudBlock); got != rightW {
		t.Fatalf("cloud block width = %d, want %d:\n%s", got, rightW, ansi.Strip(cloudBlock))
	}
	if got := maxRenderedLineWidth(dashboard.renderConfigBlocks()); got != pageW {
		t.Fatalf("config row width = %d, want %d:\n%s", got, pageW, ansi.Strip(dashboard.renderConfigBlocks()))
	}
	logBlock := dashboard.renderLocalServerLogBlock(8)
	if got := maxRenderedLineWidth(logBlock); got != pageW {
		t.Fatalf("log block width = %d, want %d:\n%s", got, pageW, ansi.Strip(logBlock))
	}
	if got := maxRenderedLineWidth(dashboard.list.ViewPanel(pageW, dashboard.palette, dashboard.styles)); got != pageW {
		t.Fatalf("action block width = %d, want %d:\n%s", got, pageW, ansi.Strip(dashboard.list.ViewPanel(pageW, dashboard.palette, dashboard.styles)))
	}
}

func TestRuntimeDashboardLogBlockFillsRemainingContentHeight(t *testing.T) {
	m := New(nil, false)
	dashboard := &runtimeDashboard{
		width:   110,
		height:  37,
		palette: m.palette,
		styles:  m.styles,
		snapshot: runtimeDashboardSnapshot{
			Config: &agentclient.Config{LocalRuntime: "llama_server"},
			Status: &agentclient.RuntimeStatus{
				Logs: []agentclient.RuntimeLogEntry{{
					Source:    "cercano.runtime.llama_server",
					RuntimeID: "llama_server",
					Message:   "server ready",
				}},
			},
		},
	}
	dashboard.list = overlay.New("models and processes", runtimeActionRowsFromSnapshot(dashboard.snapshot), overlay.Hooks{})

	view := dashboard.View()
	if got, want := strings.Count(view, "\n")+1, dashboardContentHeight(dashboard.height); got != want {
		t.Fatalf("dashboard content height = %d, want %d:\n%s", got, want, ansi.Strip(view))
	}
}

func TestRuntimeDashboardActionRoundTrip(t *testing.T) {
	want := runtimeDashboardAction{
		Kind:       runtimeActionRestart,
		Runtime:    "llama_server",
		ModelID:    "/models/qwen.gguf",
		InstanceID: "llama-abc",
	}
	got, err := parseRuntimeDashboardAction(encodeRuntimeDashboardAction(want))
	if err != nil {
		t.Fatalf("parse action: %v", err)
	}
	if got != want {
		t.Fatalf("action = %+v, want %+v", got, want)
	}
}

func assertRowContains(t *testing.T, rows []overlay.Row, label, value string) {
	t.Helper()
	for _, row := range rows {
		if row.Label == label && strings.Contains(row.Value, value) {
			return
		}
	}
	t.Fatalf("missing row label %q containing value %q in %#v", label, value, rows)
}

func assertAnyRowContains(t *testing.T, rows []overlay.Row, value string) {
	t.Helper()
	for _, row := range rows {
		if strings.Contains(row.Value, value) {
			return
		}
	}
	t.Fatalf("missing any row containing value %q in %#v", value, rows)
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
