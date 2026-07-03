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

// TestShouldApplyModelEdit pins when a committed cloud-section field edit is
// pushed to the server immediately instead of parking in the draft. Picking a
// model on an EXISTING profile must apply right away — the draft-only path
// silently discarded the choice when the user left the page without pressing
// save. New drafts still go through explicit save (they may lack name/flavor),
// and structural fields (name, base_url, …) keep the draft+save flow.
func TestShouldApplyModelEdit(t *testing.T) {
	cases := []struct {
		field    string
		draftNew bool
		want     bool
	}{
		{"cloud-model", false, true},
		{"cloud-model", true, false},
		{"cloud-base-url", false, false},
		{"cloud-name", false, false},
		{"cloud-flavor", false, false},
	}
	for _, c := range cases {
		if got := shouldApplyModelEdit(c.field, c.draftNew); got != c.want {
			t.Errorf("shouldApplyModelEdit(%q, draftNew=%v) = %v, want %v", c.field, c.draftNew, got, c.want)
		}
	}
}

// TestFallbackClaudeModelsIncludeCurrentGeneration pins that the offline
// fallback catalog carries the current top-tier models, so a user without a
// live catalog fetch can still pick them.
func TestFallbackClaudeModelsIncludeCurrentGeneration(t *testing.T) {
	want := map[string]bool{
		"claude-fable-5":    false,
		"claude-opus-4-8":   false,
		"claude-sonnet-4-6": false,
	}
	for _, m := range fallbackClaudeModels() {
		if _, ok := want[m.ID]; ok {
			want[m.ID] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("fallbackClaudeModels missing %s", id)
		}
	}
}
