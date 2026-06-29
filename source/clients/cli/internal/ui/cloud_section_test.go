package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func cloudSamplePage() *settingsPage {
	p := theme.Cracker()
	sp := &settingsPage{
		palette: p, styles: theme.NewStyles(p), width: 96, height: 60,
		cfg:  &agentclient.Config{Port: "50052", LocusMode: "cloud_only"},
		mode: "permissive",
		themes:  theme.NewRegistry(theme.BuiltinThemes()),
		working: theme.Theme{Name: "cr4k3r_j4x", Palette: p},
		profiles: []agentclient.CloudProfileInfo{
			{Name: "work-openai", Flavor: "chat_completions", Backend: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-x", HasKey: true},
		},
		activeProfile:  "work-openai",
		profilesLoaded: true,
	}
	return sp
}

func TestCloudSectionListsProfilesAndTemplates(t *testing.T) {
	sp := cloudSamplePage()
	sec := sp.buildCloudSection()
	var labels []string
	for _, f := range sec.Fields {
		labels = append(labels, f.Label())
	}
	joined := strings.Join(labels, "|")
	if !strings.Contains(joined, "work-openai") {
		t.Errorf("configured profile row missing: %v", labels)
	}
	if !strings.Contains(joined, "gemini") || !strings.Contains(joined, "+ other") {
		t.Errorf("template/other rows missing: %v", labels)
	}
}

func TestCloudSectionNoDetailWhenNothingSelected(t *testing.T) {
	sp := cloudSamplePage()
	sec := sp.buildCloudSection()
	for _, f := range sec.Fields {
		if f.Key() == "cloud-base-url" {
			t.Fatal("detail fields should not appear until a row is selected")
		}
	}
}

func TestCloudSectionShowsDetailForSelectedProfile(t *testing.T) {
	sp := cloudSamplePage()
	sp.selectCloudRow("profile:work-openai")
	sec := sp.buildCloudSection()
	var keys []string
	for _, f := range sec.Fields {
		keys = append(keys, f.Key())
	}
	j := strings.Join(keys, "|")
	for _, want := range []string{"cloud-base-url", "cloud-model", "cloud-key", "cloud-save", "cloud-activate", "cloud-delete"} {
		if !strings.Contains(j, want) {
			t.Errorf("missing detail field %q in %v", want, keys)
		}
	}
	if sp.cloudDraft.Model != "gpt-x" || sp.cloudDraft.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("draft not seeded from profile: %+v", sp.cloudDraft)
	}
	if sp.cloudDraftNew {
		t.Error("editing an existing profile is not a new draft")
	}
}

func TestCloudSectionTemplateSeedsDraftAndIsNew(t *testing.T) {
	sp := cloudSamplePage()
	sp.selectCloudRow("template:gemini")
	if !sp.cloudDraftNew {
		t.Error("selecting a template is a new draft")
	}
	if sp.cloudDraft.Name != "gemini" || sp.cloudDraft.Backend != "gemini" {
		t.Errorf("template draft wrong: %+v", sp.cloudDraft)
	}
	if sp.cloudDraft.BaseURL != "https://generativelanguage.googleapis.com/v1beta/openai" {
		t.Errorf("template base URL not seeded: %q", sp.cloudDraft.BaseURL)
	}
	// New draft from a template shows a name field; delete is absent.
	sec := sp.buildCloudSection()
	var keys []string
	for _, f := range sec.Fields {
		keys = append(keys, f.Key())
	}
	j := strings.Join(keys, "|")
	if !strings.Contains(j, "cloud-name") {
		t.Errorf("new draft should expose cloud-name: %v", keys)
	}
	if strings.Contains(j, "cloud-delete") {
		t.Errorf("new draft should not expose cloud-delete: %v", keys)
	}
}

func TestCloudSectionComingSoonDisablesActivate(t *testing.T) {
	sp := cloudSamplePage()
	sp.selectCloudRow("template:bedrock")
	sec := sp.buildCloudSection()
	for _, f := range sec.Fields {
		if f.Key() == "cloud-activate" {
			// A disabled ButtonField renders with the Dim style and never commits.
			if _, committed, _ := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); committed {
				t.Error("coming-soon activate must be disabled (no commit)")
			}
		}
	}
}
