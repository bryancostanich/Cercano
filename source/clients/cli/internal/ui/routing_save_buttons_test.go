package ui

import (
	"cercano/source/clients/cli/internal/form"
	tea "charm.land/bubbletea/v2"
	"testing"
)

func TestTaskRoutingRetainsExplicitSaveButton(t *testing.T) {
	sp := draftTestPage()
	sp.scope = scopeRouting
	stub := attachRoutingAutosaveAgent(t, sp)
	find := func() *form.ButtonField {
		t.Helper()
		for _, g := range sp.buildRoutingSections()[1].Groups {
			for _, f := range g.Fields {
				if f.Key() == "routing-save" {
					return f.(*form.ButtonField)
				}
			}
		}
		t.Fatal("Task routing missing save button")
		return nil
	}
	if _, commit, _ := find().Update(tea.KeyPressMsg{Code: tea.KeyEnter}); commit {
		t.Fatal("clean Save enabled")
	}
	if _, _, err := sp.onCommit("routing-secondary", "fixture-secondary"); err != nil {
		t.Fatal(err)
	}
	request := <-stub.requests
	if request.Secondary != "fixture-secondary" || len(request.Tasks) != 0 {
		t.Fatal("model tier was not saved separately")
	}
	if _, commit, _ := find().Update(tea.KeyPressMsg{Code: tea.KeyEnter}); commit {
		t.Fatal("auto-save left Task routing Save enabled")
	}
	if _, _, err := sp.onCommit("routing-task-review-quality", "economy"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stub.requests:
		t.Fatal("task changes must still wait for Save")
	default:
	}
	button := find()
	_, commit, value := button.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !commit {
		t.Fatal("dirty task Save disabled")
	}
	if _, _, err := sp.onCommit(button.Key(), value); err != nil {
		t.Fatal(err)
	}
	request = <-stub.requests
	if request.Secondary != "fixture-secondary" || request.Tasks["review"].Quality != "economy" {
		t.Fatal("task save lost persisted model tier")
	}
	if sp.routingDirty || sp.cloudView.Assignments.Secondary != "fixture-secondary" {
		t.Fatal("saved state not updated")
	}
}
