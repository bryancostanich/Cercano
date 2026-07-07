package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/form"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func cloudSamplePage() *settingsPage {
	p := theme.Cracker()
	// Grouped view as the agent would return it: the catalog, with one
	// configured openai profile (active) merged under the openai provider.
	view := agentclient.CloudProvidersView{
		Active: "work-openai",
		Providers: []agentclient.CloudProvider{
			{ID: "anthropic", Label: "anthropic", Flavor: "messages", Tier: "verified"},
			{ID: "openai-responses", Label: "openai (responses)", Flavor: "responses", BaseURL: "https://api.openai.com/v1", Tier: "untested"},
			{ID: "openai", Label: "openai", Flavor: "chat_completions", Backend: "openai", BaseURL: "https://api.openai.com/v1", Tier: "untested",
				PrimaryProfile: "work-openai",
				Profiles: []agentclient.CloudProfileInfo{
					{Name: "work-openai", Flavor: "chat_completions", Backend: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-x", HasKey: true},
				}},
			{ID: "gemini", Label: "gemini", Flavor: "chat_completions", Backend: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Tier: "verified"},
			{ID: "bedrock", Label: "bedrock", Flavor: "bedrock", Tier: "coming_soon"},
		},
	}
	sp := &settingsPage{
		palette: p, styles: theme.NewStyles(p), width: 96, height: 60,
		cfg:     &agentclient.Config{Port: "50052", LocusMode: "cloud_only"},
		mode:    "permissive",
		themes:  theme.NewRegistry(theme.BuiltinThemes()),
		working: theme.Theme{Name: "cr4k3r_j4x", Palette: p},
		cloudView: view,
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
	keys := map[string]bool{}
	labels := map[string]bool{}
	for _, f := range sec.Fields {
		keys[f.Key()] = true
		labels[f.Label()] = true
	}
	// The configured profile is merged into its provider row (keyed by the
	// primary profile name), labeled by the friendly provider label.
	if !keys["cloud-row:profile:work-openai"] {
		t.Errorf("merged openai provider row (primary work-openai) missing: %v", keys)
	}
	if !labels["openai"] {
		t.Errorf("merged provider row should be labeled by provider: %v", labels)
	}
	// Providers without profiles render as template rows; the trailing custom
	// row is always present.
	if !keys["cloud-row:template:gemini"] {
		t.Errorf("gemini template row missing: %v", keys)
	}
	if !keys["cloud-row:other"] {
		t.Errorf("+ other row missing: %v", keys)
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
	// cloud-name is read-only for an existing (non-new) profile.
	var cloudNameField form.Field
	for _, f := range sec.Fields {
		if f.Key() == "cloud-name" {
			cloudNameField = f
			break
		}
	}
	if cloudNameField == nil {
		t.Fatal("cloud-name field not found")
	}
	if _, ok := cloudNameField.(*form.ReadOnlyField); !ok {
		t.Errorf("cloud-name field for existing profile must be *ReadOnlyField, got %T", cloudNameField)
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
