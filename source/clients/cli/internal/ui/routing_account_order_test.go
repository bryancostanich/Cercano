package ui

import (
	tea "charm.land/bubbletea/v2"
	"reflect"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func accountOrderPage() *settingsPage {
	sp := cloudSamplePage()
	sp.profiles = []agentclient.CloudProfileInfo{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	sp.cloudView.Assignments = &agentclient.RoutingAssignments{Primary: "a", PrimaryBackups: []string{"b", "c"}, PrimaryBackup: "b", Secondary: "secondary"}
	sp.routingDraft = nil
	return sp
}
func TestBackupCanMoveIntoFirstAccountPosition(t *testing.T) {
	sp := accountOrderPage()
	if _, _, err := sp.commitRouting("routing-primary-backup-0-up", ""); err != nil {
		t.Fatal(err)
	}
	if sp.routingDraft.Primary != "b" || !reflect.DeepEqual(sp.routingDraft.PrimaryBackupAccounts(), []string{"a", "c"}) {
		t.Fatalf("first backup cannot become first account: %+v", sp.routingDraft)
	}
	if sp.routingDraft.PrimaryBackup != "a" || sp.routingDraft.Secondary != "secondary" {
		t.Fatal("reorder corrupted other routing")
	}
	if !sp.routingDirty || sp.cloudView.Assignments.Primary != "a" {
		t.Fatal("reorder must stay in draft until save")
	}
}

func TestAccountOrderingCrossesFirstPositionAndPersists(t *testing.T) {
	sp := accountOrderPage()
	commit := func(key, value string) {
		t.Helper()
		if _, _, err := sp.commitRouting(key, value); err != nil {
			t.Fatal(err)
		}
	}
	sections := sp.buildRoutingSections()
	enabled := false
	for _, field := range sections[0].Groups[0].Fields {
		if field.Key() == "routing-primary-backup-0-up" {
			_, enabled, _ = field.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
	}
	if !enabled {
		t.Fatal("first backup Move up remains disabled")
	}
	commit("routing-primary-backup-1-up", "")
	commit("routing-primary-backup-0-up", "")
	if sp.routingDraft.Primary != "c" || !reflect.DeepEqual(sp.routingDraft.PrimaryBackupAccounts(), []string{"a", "b"}) {
		t.Fatal("cannot move last account to first")
	}
	if _, _, err := sp.finishRoutingSave("", nil); err != nil {
		t.Fatal(err)
	}
	commit("routing-primary-down", "")
	if sp.routingDraft.Primary != "a" || !reflect.DeepEqual(sp.routingDraft.PrimaryBackupAccounts(), []string{"c", "b"}) {
		t.Fatal("first account cannot move down")
	}
	commit("routing-discard", "")
	sp.ensureRoutingDraft()
	if sp.routingDraft.Primary != "c" || !reflect.DeepEqual(sp.routingDraft.PrimaryBackupAccounts(), []string{"a", "b"}) {
		t.Fatal("discard did not restore saved order")
	}
	commit("routing-primary-remove", "")
	if sp.routingDraft.Primary != "a" || !reflect.DeepEqual(sp.routingDraft.PrimaryBackupAccounts(), []string{"b"}) {
		t.Fatal("remove did not promote next account")
	}
	commit("routing-primary", "")
	if sp.routingDraft.Primary != "b" || len(sp.routingDraft.PrimaryBackupAccounts()) != 0 {
		t.Fatal("clearing first selection left an empty leading slot")
	}
	commit("routing-primary-remove", "")
	if sp.routingDraft.Primary != "" || sp.routingDraft.PrimaryBackup != "" {
		t.Fatal("last removal retained an account")
	}
	commit("routing-primary-backup-add", "c")
	if sp.routingDraft.Primary != "c" || len(sp.routingDraft.PrimaryBackupAccounts()) != 0 {
		t.Fatal("adding to empty list must fill first slot")
	}
}
