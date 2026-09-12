package ui

import (
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/config"
	tea "charm.land/bubbletea/v2"
	"errors"
	"strings"
	"testing"
)

func TestRoutingPageUsesSharedMetadata(t *testing.T) {
	themes, active := scopeTestThemes(t)
	sp, _ := newScopedSettingsPage(nil, active.Palette, theme.NewStyles(active.Palette), "palette:accent", 100, 40, themes, active, scopeRouting)
	if titles := sectionTitles(sp); len(titles) != 1 || titles[0] != "Routing" {
		t.Fatalf("sections=%v", titles)
	}
	keys := map[string]bool{}
	for _, f := range sp.buildRoutingSection().Fields {
		keys[f.Key()] = true
		if strings.HasPrefix(f.Key(), "cloud-") {
			t.Fatal("Cloud control on Routing")
		}
	}
	for _, d := range config.TaskDefinitions() {
		for _, suffix := range []string{"destination", "quality", "effective", "reset"} {
			if !keys["routing-task-"+string(d.Task)+"-"+suffix] {
				t.Fatalf("missing %s/%s", d.Task, suffix)
			}
		}
	}
	for _, k := range []string{"routing-secondary-redirect", "routing-local-redirect", "routing-local-setup"} {
		if !keys[k] {
			t.Fatalf("missing %s", k)
		}
	}
	if q := sp.routingConfig().TaskAssignment(config.TaskWatchdog).Quality; q != config.CostStandard {
		t.Fatalf("Watchdog quality=%s", q)
	}
}
func TestRoutingDraftResetAndIsolation(t *testing.T) {
	sp := draftTestPage()
	sp.scope = scopeRouting
	sp.applyCloudDraftEdit("cloud-image", "unsaved-image")
	sp.onCommit("routing-local-redirect", "secondary")
	sp.onCommit("routing-task-watchdog-quality", "premium")
	sp.onCommit("routing-task-watchdog-destination", "primary")
	sp.snapshotSections() // reload does not discard either draft
	if sp.routingDraft.LocalRedirect != "secondary" || sp.cloudDraft.Choices.ImageModel != "unsaved-image" {
		t.Fatal("reload dropped draft")
	}
	if sp.cloudView.Assignments != nil && len(sp.cloudView.Assignments.Tasks) > 0 {
		t.Fatal("draft mutated saved assignments")
	}
	sp.onCommit("routing-task-watchdog-reset", "")
	if _, ok := sp.routingDraft.Tasks["watchdog"]; ok {
		t.Fatal("reset kept override")
	}
	sp.onCommit("routing-local-redirect", "")
	if sp.routingDraft.LocalRedirect != "" {
		t.Fatal("own config reset failed")
	}
	sp.onCommit("routing-discard", "")
	if sp.routingDirty || !sp.cloudDirty || sp.cloudDraft.Choices.ImageModel != "unsaved-image" {
		t.Fatal("Routing discard touched Cloud")
	}
	sp.onCommit("routing-secondary-redirect", "local")
	sp.commitCloud(classifyCloudCommit("cloud-discard", ""))
	if !sp.routingDirty || sp.routingDraft.SecondaryRedirect != "local" {
		t.Fatal("Cloud discard touched Routing")
	}
}
func TestRoutingFailedSaveAndUnavailableAgentRetainDraft(t *testing.T) {
	sp := draftTestPage()
	sp.scope = scopeRouting
	sp.onCommit("routing-task-review-quality", "economy")
	if _, _, err := sp.onCommit("routing-save", ""); err == nil || !sp.routingDirty {
		t.Fatal("disconnected save claimed success")
	}
	if _, _, err := sp.finishRoutingSave("", errors.New("fixture transport failure")); err == nil || !sp.routingDirty || sp.routingDraft.Tasks["review"].Quality != "economy" {
		t.Fatal("failed save lost draft")
	}
	sp.onCommit("routing-secondary-redirect", "local")
	sp.onCommit("routing-local-redirect", "secondary")
	if _, _, err := sp.onCommit("routing-save", ""); err == nil || !strings.Contains(err.Error(), "cycle") || !sp.routingDirty {
		t.Fatalf("cycle not retained/rejected: %v", err)
	}
	sp.onCommit("routing-local-redirect", "")
	sp.cloudDirty = true
	status, _, err := sp.finishRoutingSave("fixture unavailable", nil)
	if err != nil || sp.routingDirty || !sp.cloudDirty || !strings.Contains(status, "saved routing") {
		t.Fatalf("save completion %q %v", status, err)
	}
	if sp.cloudView.Assignments.Tasks["review"].Quality != "economy" {
		t.Fatal("saved baseline missing")
	}
	sp.onCommit("routing-task-review-quality", "premium")
	sp.onCommit("routing-discard", "")
	sp.ensureRoutingDraft()
	if sp.routingDraft.Tasks["review"].Quality != "economy" {
		t.Fatal("discard after save restored old baseline")
	}
}
func TestRoutingNavigationCancellationAndEighthTab(t *testing.T) {
	sp := draftTestPage()
	sp.scope = scopeRouting
	sp.onCommit("routing-secondary", "changed")
	m := Model{content: sp, configSurface: &configSurface{active: configTabRouting, focused: true}}
	m.switchConfigTab(configTabCloud)
	if m.configSurface.active != configTabRouting || m.configSurface.pendingTab == nil {
		t.Fatal("left dirty Routing without confirmation")
	}
	m, _, _ = m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.configSurface.active != configTabRouting || !sp.routingDirty {
		t.Fatal("cancel lost Routing draft")
	}
	// The eighth keyboard destination remains addressable; do not build it
	// while a dirty page still owns navigation.
	m, _, _ = m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: '8', Text: "8"})
	if m.configSurface.pendingTab == nil || *m.configSurface.pendingTab != configTabContext {
		t.Fatal("eighth tab navigation missing")
	}
}

func TestSelectingCurrentRoutingTabPreservesDraft(t *testing.T) {
	sp := draftTestPage()
	sp.scope = scopeRouting
	sp.onCommit("routing-secondary", "unsaved")
	m := Model{content: sp, configSurface: &configSurface{active: configTabRouting, focused: true}}
	m.switchConfigTab(configTabRouting)
	if m.content != sp || !sp.routingDirty || sp.routingDraft.Secondary != "unsaved" {
		t.Fatal("selecting current tab discarded routing page/draft")
	}
}
