package ui

import (
	"cercano/source/clients/cli/internal/form"
	"cercano/source/server/pkg/agentclient"
	tea "charm.land/bubbletea/v2"
	"strings"
	"testing"
)

func draftTestPage() *settingsPage {
	sp := cloudSamplePage()
	sp.profiles[0].Choices = &agentclient.CloudModelChoices{TierOverrides: map[string]string{"premium": "original"}, ImageModel: "image"}
	sp.profiles[0].RecommendedQualityModels = map[string]string{"premium": "recommended"}
	sp.selectCloudRow("profile:work-openai")
	sp.form = form.New([]form.Section{sp.buildCloudSection()})
	sp.form.OnCommit = sp.onCommit
	sp.form.OnReload = func() []form.Section { return []form.Section{sp.buildCloudSection()} }
	return sp
}
func TestCloudChoicesStayDraftUntilSave(t *testing.T) {
	sp := draftTestPage()
	for key, value := range map[string]string{"cloud-quality-premium": "custom", "cloud-image": "new-image"} {
		action := classifyCloudCommit(key, value)
		if cloudCommitNeedsAgent(action, false) {
			t.Fatal("draft edit requires server")
		}
		if _, cmd, err := sp.commitCloud(action); err != nil || cmd != nil {
			t.Fatal("draft edit launched command", err)
		}
	}
	if sp.profiles[0].Choices.TierOverrides["premium"] != "original" || sp.profiles[0].Choices.ImageModel != "image" {
		t.Fatal("draft mutated loaded profile")
	}
	if !sp.cloudDirty || sp.cloudDraft.Choices.ImageModel != "new-image" {
		t.Fatal("draft change lost")
	}
	sp.applyCloudDraftEdit("cloud-quality-premium", "")
	if _, ok := sp.cloudDraft.Choices.TierOverrides["premium"]; ok {
		t.Fatal("reset kept override")
	}
	if sp.cloudDraft.Effective["premium"] != "recommended" {
		t.Fatal("reset displays old override as inheritance")
	}
	sp.commitCloud(classifyCloudCommit("cloud-discard", ""))
	if sp.cloudDirty || sp.cloudDraft.Choices.ImageModel != "image" {
		t.Fatal("discard failed")
	}
}
func TestCloudRowNavigationRequiresConfirmation(t *testing.T) {
	sp := draftTestPage()
	sp.applyCloudDraftEdit("cloud-image", "changed")
	status, _, _ := sp.commitCloud(classifyCloudCommit("cloud-row:other", ""))
	if !strings.Contains(status, "y/n") || sp.cloudSelected != "profile:work-openai" {
		t.Fatal("navigation dropped draft")
	}
	sp.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if !sp.cloudDirty || sp.settingsPendingLeave != "" {
		t.Fatal("cancel lost draft")
	}
	sp.commitCloud(classifyCloudCommit("cloud-row:other", ""))
	sp.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if sp.cloudDirty || sp.cloudSelected != "other" {
		t.Fatal("confirmed navigation failed")
	}
}
func TestRoutingTabCloseRequiresConfirmation(t *testing.T) {
	sp := draftTestPage()
	sp.scope = scopeRouting
	sp.commitRouting("routing-secondary", "new")
	m := Model{content: sp, configSurface: &configSurface{active: configTabRouting, focused: true}}
	if m.closeConfigSurface() != nil || m.configSurface == nil || !sp.settingsNavigationPrompt {
		t.Fatal("closed dirty page")
	}
	m, _, _ = m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.configSurface == nil || !sp.routingDirty {
		t.Fatal("cancel failed")
	}
	m.closeConfigSurface()
	m, _, _ = m.handleConfigSurfaceKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if m.configSurface != nil || sp.hasUnsavedSettings() {
		t.Fatal("confirmed discard did not close")
	}
}
func TestRoutingDraftAndLocalCatalog(t *testing.T) {
	sp := draftTestPage()
	sp.cloudView.Assignments = &agentclient.RoutingAssignments{Primary: "p", Secondary: "s", Tasks: map[string]agentclient.TaskAssignment{}}
	sp.routingDraft = nil
	sp.commitRouting("routing-task-dispatch-quality", "economy")
	if len(sp.cloudView.Assignments.Tasks) != 0 || !sp.routingDirty {
		t.Fatal("routing draft aliases loaded config")
	}
	sp.commitRouting("routing-discard", "")
	sp.ensureRoutingDraft()
	if sp.routingDirty || len(sp.routingDraft.Tasks) != 0 {
		t.Fatal("routing discard failed")
	}
	if configTabModels.label() != "Local Models" {
		t.Fatal("incorrect tab title")
	}
	d := newDashboardWith(servedModel(), downloadableModel())
	if got := d.catalogModels(); len(got) != 1 || got[0].Served() {
		t.Fatal("hosted model in Local Models")
	}
	if estimateKey(servedModel()) != "" {
		t.Fatal("hosted RAM estimate allowed")
	}
}

func TestCloudPickerCancellationDoesNotChangeDraft(t *testing.T) {
	sp := draftTestPage()
	fields := sp.buildCloudSection().Fields
	for i, f := range fields {
		if f.Key() == "cloud-image" {
			sp.form.SetCursor(i)
			break
		}
	}
	sp.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	sp.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	sp.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if sp.cloudDirty || sp.cloudDraft.Choices.ImageModel != "image" {
		t.Fatal("cancelled picker modified draft")
	}
}
