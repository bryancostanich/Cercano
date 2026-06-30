package ui

import (
	"testing"

	"cercano/source/clients/cli/internal/form"
)

func TestClassifyCloudCommit(t *testing.T) {
	if a := classifyCloudCommit("cloud-row:template:gemini", form.RowSelect); a.kind != cloudCommitSelect || a.rowID != "template:gemini" {
		t.Errorf("row select misrouted: %+v", a)
	}
	if a := classifyCloudCommit("cloud-base-url", "https://x"); a.kind != cloudCommitDraftEdit || a.field != "cloud-base-url" || a.value != "https://x" {
		t.Errorf("draft edit misrouted: %+v", a)
	}
	if a := classifyCloudCommit("cloud-key", "sk-1"); a.kind != cloudCommitKey || a.value != "sk-1" {
		t.Errorf("key misrouted: %+v", a)
	}
	if a := classifyCloudCommit("cloud-save", form.ButtonActivate); a.kind != cloudCommitSave {
		t.Errorf("save misrouted: %+v", a)
	}
	if a := classifyCloudCommit("cloud-activate", form.ButtonActivate); a.kind != cloudCommitActivate {
		t.Errorf("activate misrouted: %+v", a)
	}
	if a := classifyCloudCommit("cloud-delete", form.ButtonActivate); a.kind != cloudCommitDelete {
		t.Errorf("delete misrouted: %+v", a)
	}
	if a := classifyCloudCommit("local-model", "x"); a.kind != cloudCommitNone {
		t.Errorf("non-cloud key should be cloudCommitNone: %+v", a)
	}
}

func TestApplyCloudDraftEdit(t *testing.T) {
	sp := cloudSamplePage()
	sp.selectCloudRow("template:gemini")
	sp.applyCloudDraftEdit("cloud-model", "gemini-x")
	sp.applyCloudDraftEdit("cloud-base-url", "https://custom")
	sp.applyCloudDraftEdit("cloud-name", "my-gemini")
	if sp.cloudDraft.Model != "gemini-x" || sp.cloudDraft.BaseURL != "https://custom" || sp.cloudDraft.Name != "my-gemini" {
		t.Fatalf("draft edits not applied: %+v", sp.cloudDraft)
	}
}

func TestSelectCommitExpandsRow(t *testing.T) {
	sp := cloudSamplePage()
	// onCommit for a row-select sets cloudSelected and reloads.
	status, _, err := sp.onCommit("cloud-row:template:groq", form.RowSelect)
	if err != nil {
		t.Fatal(err)
	}
	if sp.cloudSelected != "template:groq" {
		t.Fatalf("row select did not expand groq: %q", sp.cloudSelected)
	}
	_ = status
}
