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
		cfg:       &agentclient.Config{Port: "50052", LocusMode: "cloud_only"},
		mode:      "permissive",
		themes:    theme.NewRegistry(theme.BuiltinThemes()),
		working:   theme.Theme{Name: "cr4k3r_j4x", Palette: p},
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

func TestCloudSectionActiveRowShowsPrimaryDisabled(t *testing.T) {
	sp := cloudSamplePage() // primary profile is "work-openai"
	sp.selectCloudRow("profile:work-openai")
	sec := sp.buildCloudSection()
	var found bool
	for _, f := range sec.Fields {
		if f.Key() != "cloud-activate" {
			continue
		}
		found = true
		// The primary row's button reads "primary" so it reflects the current
		// state instead of inviting a no-op re-selection.
		if !strings.Contains(f.Label(), "primary") {
			t.Errorf("primary row's button should read \"primary\", got %q", f.Label())
		}
		if strings.Contains(f.Label(), "set as primary") {
			t.Errorf("primary row's button should not still say \"set as primary\", got %q", f.Label())
		}
		// It must be disabled: a disabled button never commits on Enter.
		if _, committed, _ := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); committed {
			t.Error("primary row's button must be disabled (no commit)")
		}
	}
	if !found {
		t.Fatal("cloud-activate field missing from primary profile row")
	}
}

func TestCloudSectionInactiveProfileShowsSetAsPrimaryEnabled(t *testing.T) {
	sp := cloudSamplePage()
	// Make a second, non-active profile the selected editable row.
	sp.cloudView.Providers[3].PrimaryProfile = "personal-gemini"
	sp.cloudView.Providers[3].Profiles = []agentclient.CloudProfileInfo{
		{Name: "personal-gemini", Flavor: "chat_completions", Backend: "gemini",
			BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Model: "gemini-x", HasKey: true},
	}
	sp.selectCloudRow("profile:personal-gemini")
	sec := sp.buildCloudSection()
	var found bool
	for _, f := range sec.Fields {
		if f.Key() != "cloud-activate" {
			continue
		}
		found = true
		if f.Label() == "" || !strings.Contains(f.Label(), "set as primary") {
			t.Errorf("inactive row's button should read \"set as primary\", got %q", f.Label())
		}
		if _, committed, _ := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); !committed {
			t.Error("inactive row's set-as-primary button must be enabled (commits)")
		}
	}
	if !found {
		t.Fatal("cloud-activate field missing from inactive profile row")
	}
}

func TestCloudSectionSubscriptionProfileIsOAuthOnly(t *testing.T) {
	sp := cloudSamplePage()
	sp.cloudView.Providers[0].PrimaryProfile = "claude"
	sp.cloudView.Providers[0].Profiles = []agentclient.CloudProfileInfo{{
		Name: "claude", Flavor: "messages", Route: "subscription", Model: "claude-opus-4-8",
	}}
	sp.profiles = append(sp.profiles, agentclient.CloudProfileInfo{
		Name: "claude", Flavor: "messages", Route: "subscription", Model: "claude-opus-4-8",
	})

	sp.selectCloudRow("profile:claude")
	sec := sp.buildCloudSection()
	keys := map[string]bool{}
	for _, f := range sec.Fields {
		keys[f.Key()] = true
	}
	if !keys["cloud-route"] {
		t.Fatalf("subscription profile should show auth route: %v", keys)
	}
	if keys["cloud-key"] {
		t.Fatalf("subscription profile must not show an API-key field: %v", keys)
	}
	if keys["cloud-base-url"] {
		t.Fatalf("subscription profile must not show a base-url field: %v", keys)
	}
	if sp.cloudDraft.Route != "subscription" {
		t.Fatalf("draft route = %q, want subscription", sp.cloudDraft.Route)
	}
}

func TestCloudSectionDirectAnthropicProfileDoesNotShowSubscriptionSignIn(t *testing.T) {
	sp := cloudSamplePage()
	sp.cloudView.Providers[0].PrimaryProfile = "anthropic"
	sp.cloudView.Providers[0].Profiles = []agentclient.CloudProfileInfo{{
		Name: "anthropic", Flavor: "messages", Model: "claude-opus-4-8", HasKey: true,
	}}
	sp.profiles = append(sp.profiles, agentclient.CloudProfileInfo{
		Name: "anthropic", Flavor: "messages", Model: "claude-opus-4-8", HasKey: true,
	})

	sp.selectCloudRow("profile:anthropic")
	sec := sp.buildCloudSection()
	keys := map[string]bool{}
	for _, f := range sec.Fields {
		keys[f.Key()] = true
	}
	if keys["cloud-signin-claude"] {
		t.Fatalf("direct API-key profile must not show subscription sign-in: %v", keys)
	}
	if !keys["cloud-key"] {
		t.Fatalf("direct API-key profile should still show API-key field: %v", keys)
	}
	if !keys["cloud-base-url"] {
		t.Fatalf("direct API-key profile should still show base-url field: %v", keys)
	}
}

func TestShouldShowClaudeSignIn(t *testing.T) {
	sp := cloudSamplePage()
	messagesPreset := &cloudPreset{ID: "anthropic", Flavor: "messages"}
	responsesPreset := &cloudPreset{ID: "openai-responses", Flavor: "responses"}
	cases := []struct {
		name string
		row  cloudRow
		d    cloudDraft
		want bool
	}{
		{name: "subscription profile", row: cloudRow{ID: "profile:claude", IsProfile: true, Preset: messagesPreset, Profile: &agentclient.CloudProfileInfo{Name: "claude", Flavor: "messages", Route: "subscription"}}, d: cloudDraft{Route: "subscription"}, want: true},
		{name: "bare subscription template", row: cloudRow{ID: "template:anthropic-subscription", Preset: messagesPreset}, d: cloudDraft{Route: "subscription"}, want: true},
		{name: "bare API-key template", row: cloudRow{ID: "template:anthropic", Preset: messagesPreset}, d: cloudDraft{}, want: false},
		{name: "direct anthropic profile", row: cloudRow{ID: "profile:anthropic", IsProfile: true, Preset: messagesPreset, Profile: &agentclient.CloudProfileInfo{Name: "anthropic", Flavor: "messages"}}, d: cloudDraft{}, want: false},
		{name: "responses profile", row: cloudRow{ID: "profile:chatgpt", Preset: responsesPreset}, d: cloudDraft{Route: "subscription"}, want: false},
		{name: "custom messages profile", row: cloudRow{ID: "profile:custom"}, d: cloudDraft{Route: "subscription"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sp.shouldShowClaudeSignIn(tc.row, tc.d); got != tc.want {
				t.Fatalf("shouldShowClaudeSignIn() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCloudSectionCanonicalSubscriptionOwnsSignIn(t *testing.T) {
	sp := cloudSamplePage()
	sp.cloudView.Providers[0].PrimaryProfile = "anthropic"
	sp.cloudView.Providers[0].Profiles = []agentclient.CloudProfileInfo{
		{Name: "anthropic", Flavor: "messages", Route: "subscription", Model: "claude-opus-4-8"},
		{Name: "claude", Flavor: "messages", Route: "subscription", Model: "claude-opus-4-8"},
	}
	sp.profiles = append(sp.profiles,
		agentclient.CloudProfileInfo{Name: "anthropic", Flavor: "messages", Route: "subscription", Model: "claude-opus-4-8"},
		agentclient.CloudProfileInfo{Name: "claude", Flavor: "messages", Route: "subscription", Model: "claude-opus-4-8"},
	)

	sp.selectCloudRow("profile:anthropic")
	anthropic := sp.buildCloudSection()
	anthropicKeys := map[string]bool{}
	for _, f := range anthropic.Fields {
		anthropicKeys[f.Key()] = true
	}
	if anthropicKeys["cloud-signin-claude"] {
		t.Fatalf("legacy subscription alias must not show sign-in when canonical profile exists: %v", anthropicKeys)
	}

	sp.selectCloudRow("profile:claude")
	canonical := sp.buildCloudSection()
	canonicalKeys := map[string]bool{}
	for _, f := range canonical.Fields {
		canonicalKeys[f.Key()] = true
	}
	if !canonicalKeys["cloud-signin-claude"] {
		t.Fatalf("canonical subscription profile should show sign-in: %v", canonicalKeys)
	}
}
