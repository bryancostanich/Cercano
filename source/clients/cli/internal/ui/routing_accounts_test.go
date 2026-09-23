package ui

import (
	"reflect"
	"testing"

	"cercano/source/clients/cli/internal/form"
	"cercano/source/server/pkg/agentclient"
	tea "charm.land/bubbletea/v2"
)

func TestRoutingOrderedBackupEditor(t *testing.T) {
	sp := cloudSamplePage()
	sp.profiles = []agentclient.CloudProfileInfo{{Name: "a", Provider: "openai"}, {Name: "b", Provider: "openai"}, {Name: "c", Provider: "anthropic"}}
	sp.cloudView.Assignments = &agentclient.RoutingAssignments{Primary: "a", PrimaryBackup: "b"}
	sp.routingDraft = nil
	sections := sp.buildRoutingSections()
	var add *form.SelectField
	for _, f := range sections[0].Groups[0].Fields {
		if f.Key() == "routing-primary-backup-add" {
			add = f.(*form.SelectField)
		}
	}
	if add == nil {
		t.Fatal("missing Add backup")
	}
	add.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	add.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	_, committed, value := add.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !committed || value != "c" {
		t.Fatalf("selected existing account instead of available c: %q", value)
	}
	commit := func(key, value string) {
		t.Helper()
		if _, _, err := sp.commitRouting(key, value); err != nil {
			t.Fatal(err)
		}
	}
	commit("routing-primary-backup-add", value)
	if !reflect.DeepEqual(sp.routingDraft.PrimaryBackupAccounts(), []string{"b", "c"}) || !sp.routingDirty {
		t.Fatal("add lost order")
	}
	if _, _, err := sp.commitRouting("routing-primary-backup-add", "b"); err == nil {
		t.Fatal("duplicate allowed")
	}
	if _, _, err := sp.commitRouting("routing-primary", "c"); err == nil {
		t.Fatal("primary duplicates backup")
	}
	commit("routing-primary-backup-1-up", "")
	if !reflect.DeepEqual(sp.routingDraft.PrimaryBackupAccounts(), []string{"c", "b"}) || sp.routingDraft.PrimaryBackup != "c" {
		t.Fatal("reorder not synchronized")
	}
	sp.buildRoutingSections()
	if _, _, err := sp.finishRoutingSave("", nil); err != nil {
		t.Fatal(err)
	}
	commit("routing-primary-backup-0-remove", "")
	if !reflect.DeepEqual(sp.cloudView.Assignments.PrimaryBackupAccounts(), []string{"c", "b"}) {
		t.Fatal("draft mutates saved list")
	}
	commit("routing-discard", "")
	sp.ensureRoutingDraft()
	if !reflect.DeepEqual(sp.routingDraft.PrimaryBackupAccounts(), []string{"c", "b"}) {
		t.Fatal("discard lost order")
	}
	commit("routing-primary-backup-0-down", "")
	commit("routing-primary-backup-1-remove", "")
	commit("routing-primary-backup-0-remove", "")
	if len(sp.routingDraft.PrimaryBackupAccounts()) != 0 || sp.routingDraft.PrimaryBackup != "" {
		t.Fatal("clear resurrected legacy backup")
	}
}
