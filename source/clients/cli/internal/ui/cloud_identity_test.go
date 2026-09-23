package ui

import (
	"cercano/source/clients/cli/internal/form"
	"cercano/source/server/pkg/agentclient"
	"strings"
	"testing"
)

func TestCloudAndRoutingAccountIdentityLabels(t *testing.T) {
	profiles := []agentclient.CloudProfileInfo{{Name: "chatgpt", AccountEmail: "same@example.com"}, {Name: "chatgpt-2", AccountEmail: "same@example.com"}}
	view := agentclient.CloudProvidersView{Providers: []agentclient.CloudProvider{{ID: "openai-responses", Label: "ChatGPT", Profiles: profiles}}, CustomProfiles: []agentclient.CloudProfileInfo{{Name: "custom", AccountDisplayName: "Person"}}}
	rows := buildCloudRowsFromProviders(view)
	if len(rows) != 4 {
		t.Fatalf("rows: %d", len(rows))
	}
	for i, p := range profiles {
		if rows[i].ID != "profile:"+p.Name || !strings.Contains(rows[i].Label, p.AccountEmail) || !strings.Contains(rows[i].Label, p.Name) {
			t.Fatalf("row %d: %+v", i, rows[i])
		}
	}
	if !strings.Contains(rows[2].Label, "Person (custom)") {
		t.Fatal(rows[2].Label)
	}
	sp := cloudSamplePage()
	sp.cloudView = view
	sp.profiles = profiles
	for _, p := range profiles {
		if got := sp.routingAccountLabel(p.Name); got != p.AccountLabel("ChatGPT") {
			t.Fatal(got)
		}
	}
	// Display identity must never replace the action's stable account key.
	sp.selectCloudRow("profile:chatgpt-2")
	if sp.cloudDraft.Name != "chatgpt-2" {
		t.Fatal("selected by email instead of account key")
	}
}

func TestSuccessfulSignInRefreshesAccountLabels(t *testing.T) {
	sp := cloudSamplePage()
	sp.cloudDraft = cloudDraft{Name: "account", Choices: (&agentclient.CloudModelChoices{}).Clone()}
	sp.form = form.New(nil)
	refreshed := false
	sp.form.OnReload = func() []form.Section { refreshed = true; return nil }
	sp.cloudAccountSignedIn("account", "chatgpt")
	if !refreshed {
		t.Fatal("successful sign-in left stale account labels visible")
	}
}
