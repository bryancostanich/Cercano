package ui

import (
	"testing"

	"cercano/source/server/pkg/agentclient"
)

// The live-update loop must survive a failed or momentarily
// inconsistent snapshot: refreshSnapshot ALWAYS reschedules while the
// page is open. Gating the reschedule on hasActiveDownloads meant one
// bad GetRuntimeStatus (easy under download load) permanently froze
// the dashboard.
func TestRefreshSnapshotAlwaysReschedules(t *testing.T) {
	d := newCatalogTestDashboard(runtimeDashboardSnapshot{})
	// nil agent: the reload inside refreshSnapshot fails entirely and
	// no downloads are visible afterward — the worst case.
	if cmd := d.refreshSnapshot(); cmd == nil {
		t.Fatal("refreshSnapshot returned nil — the live-update loop would die")
	}
}

func TestRefreshTickFastWhileDownloading(t *testing.T) {
	idle := newCatalogTestDashboard(runtimeDashboardSnapshot{})
	if idle.hasActiveDownloads() {
		t.Fatal("fixture should have no downloads")
	}
	busy := newCatalogTestDashboard(runtimeDashboardSnapshot{
		Status: &agentclient.RuntimeStatus{Models: []agentclient.RuntimeModel{{
			ID: "x", DownloadState: "downloading",
		}}},
	})
	if !busy.hasActiveDownloads() {
		t.Fatal("fixture should report an active download")
	}
	// Both must reschedule; cadence differs (500ms vs 2s) but a tea.Cmd
	// is opaque — what we can pin is that NEITHER is nil.
	if idle.refreshTick() == nil || busy.refreshTick() == nil {
		t.Fatal("refreshTick must always produce a command")
	}
}
