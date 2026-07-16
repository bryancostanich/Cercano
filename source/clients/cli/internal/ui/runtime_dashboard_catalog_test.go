package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"cercano/source/server/pkg/agentclient"
)

func newCatalogTestDashboard(snapshot runtimeDashboardSnapshot) *runtimeDashboard {
	m := New(nil, false)
	return &runtimeDashboard{
		width:    112,
		height:   37,
		palette:  m.palette,
		styles:   m.styles,
		snapshot: snapshot,
		loaded:   true,
		focus:    runtimeFocusCatalog,
	}
}

func TestCatalogModelsPrefersListRuntimeModelsCatalog(t *testing.T) {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{
		Status: &agentclient.RuntimeStatus{
			Models: []agentclient.RuntimeModel{{ID: "local:only"}},
		},
		Catalog: agentclient.RuntimeModelCatalog{
			Models: []agentclient.RuntimeModel{
				{ID: "local:only"},
				{ID: "ollama:qwen2.5-coder:7b", Source: "ollama_library"},
			},
		},
	})
	models := d.catalogModels()
	if len(models) != 2 {
		t.Fatalf("catalogModels returned %d models, want 2 (the merged list)", len(models))
	}
	if models[1].ID != "ollama:qwen2.5-coder:7b" {
		t.Fatalf("catalogModels[1] = %q, want the online entry", models[1].ID)
	}
}

func TestCatalogModelsFallsBackToStatusModelsOnError(t *testing.T) {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{
		Status: &agentclient.RuntimeStatus{
			Models: []agentclient.RuntimeModel{{ID: "local:fallback"}},
		},
		CatalogErr: errors.New("rpc unavailable"),
	})
	models := d.catalogModels()
	if len(models) != 1 || models[0].ID != "local:fallback" {
		t.Fatalf("catalogModels = %+v, want the status-model fallback", models)
	}
}

func TestCatalogCtrlRStartsRefreshAndGuardsReentry(t *testing.T) {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{
		Status: &agentclient.RuntimeStatus{},
	})
	cmd, closed := d.updateCatalog(ctrlR())
	if closed {
		t.Fatal("ctrl+r should not close the dashboard")
	}
	if cmd == nil {
		t.Fatal("ctrl+r should return a refresh command")
	}
	if !d.catalogBusy {
		t.Fatal("ctrl+r should mark the catalog busy")
	}

	cmd, _ = d.updateCatalog(ctrlR())
	if cmd != nil {
		t.Fatal("second ctrl+r while busy should be a no-op")
	}
	if d.catalogMessage != "refresh already running" {
		t.Fatalf("catalogMessage = %q, want reentry guard message", d.catalogMessage)
	}
}

func TestApplyCatalogRefreshFailureKeepsFooterUsable(t *testing.T) {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{
		Status: &agentclient.RuntimeStatus{},
	})
	d.catalogBusy = true
	cmd := d.applyCatalogRefresh(runtimeCatalogRefreshDoneMsg{
		result: agentclient.CatalogRefreshResult{Err: errors.New("network down")},
	})
	if cmd != nil {
		t.Fatal("failed refresh should not trigger a snapshot reload")
	}
	if d.catalogBusy {
		t.Fatal("failed refresh must clear the busy flag")
	}
	if !strings.Contains(d.catalogMessage, "refresh failed") {
		t.Fatalf("catalogMessage = %q, want refresh-failed message", d.catalogMessage)
	}
}

func TestApplyCatalogRefreshSuccessReloadsSnapshot(t *testing.T) {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{
		Status: &agentclient.RuntimeStatus{},
	})
	d.catalogBusy = true
	cmd := d.applyCatalogRefresh(runtimeCatalogRefreshDoneMsg{
		result: agentclient.CatalogRefreshResult{UpdatedAt: time.Now(), ModelCount: 236},
	})
	_ = cmd
	if d.catalogBusy {
		t.Fatal("successful refresh must clear the busy flag")
	}
	if !strings.Contains(d.catalogMessage, "236 models") {
		t.Fatalf("catalogMessage = %q, want model count", d.catalogMessage)
	}
}

func TestRenderCatalogFooterStates(t *testing.T) {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{
		Status: &agentclient.RuntimeStatus{},
	})

	if got := ansi.Strip(d.renderCatalogFooter(80)); !strings.Contains(got, "online catalog not loaded") {
		t.Fatalf("zero-timestamp footer = %q, want not-loaded hint", got)
	}

	d.snapshot.Catalog.CatalogUpdatedAt = time.Now().Add(-2 * time.Hour)
	if got := ansi.Strip(d.renderCatalogFooter(80)); !strings.Contains(got, "catalog updated") || !strings.Contains(got, "ctrl+r to refresh") {
		t.Fatalf("fresh footer = %q, want timestamp + refresh hint", got)
	}

	d.catalogBusy = true
	if got := ansi.Strip(d.renderCatalogFooter(80)); !strings.Contains(got, "refreshing catalog") {
		t.Fatalf("busy footer = %q, want refreshing message", got)
	}
}

func TestRenderCatalogBlockIncludesFooter(t *testing.T) {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{
		Status: &agentclient.RuntimeStatus{},
		Catalog: agentclient.RuntimeModelCatalog{
			CatalogUpdatedAt: time.Now().Add(-30 * time.Minute),
		},
	})
	view := ansi.Strip(d.renderCatalogBlock(maxCatalogRows))
	if !strings.Contains(view, "catalog updated") {
		t.Fatalf("catalog block missing freshness footer:\n%s", view)
	}
}

// ctrlR builds a ctrl+r key press; the shared keyPress helper only
// handles unmodified keys.
func ctrlR() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}
}

func TestRuntimeDashboardCatalogFiltersToActiveRuntime(t *testing.T) {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{
		Config: &agentclient.Config{OpenRuntime: "mistralrs"},
		Catalog: agentclient.RuntimeModelCatalog{Models: []agentclient.RuntimeModel{
			{ID: "llama_server:catalog:phi", Runtime: "llama_server", Source: "catalog", DisplayName: "Phi"},
			{ID: "mistralrs:catalog:qwen", Runtime: "mistralrs", Source: "catalog", DisplayName: "Qwen"},
		}},
	})
	models := d.catalogModels()
	if len(models) != 1 || models[0].Runtime != "mistralrs" {
		t.Fatalf("catalogModels = %+v, want only mistralrs models", models)
	}
}

func TestRuntimeDashboardStatusFiltersToActiveRuntime(t *testing.T) {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{
		Config: &agentclient.Config{OpenRuntime: "mistralrs"},
		Status: &agentclient.RuntimeStatus{
			Instances: []agentclient.RuntimeInstance{
				{Runtime: "llama_server", State: "running", ModelID: "llama", PID: 1, Endpoint: "http://llama"},
				{Runtime: "mistralrs", State: "downloading", ModelID: "qwen", PID: 2, Endpoint: "http://mistral"},
			},
			Models: []agentclient.RuntimeModel{
				{Runtime: "llama_server", DownloadState: "downloaded"},
				{Runtime: "mistralrs", DownloadState: "downloading"},
			},
		},
	})
	line := stripAnsiCSI(d.renderRuntimeStatusBlock())
	if strings.Contains(line, "llama") || strings.Contains(line, "http://llama") {
		t.Fatalf("runtime status should not show inactive llama-server instance: %q", line)
	}
	if !strings.Contains(line, "qwen") || !strings.Contains(line, "http://mistral") || !strings.Contains(line, "1 job") {
		t.Fatalf("runtime status should show active mistralrs status/download, got %q", line)
	}
}
