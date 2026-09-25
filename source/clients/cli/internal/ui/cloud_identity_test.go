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
	if !strings.Contains(rows[2].Label, "Person") {
		t.Fatal(rows[2].Label)
	}
	sp := cloudSamplePage()
	sp.cloudView = view
	sp.profiles = profiles
	for _, p := range profiles {
		if got := sp.routingAccountLabel(p.Name); got != p.DistinctAccountLabel("ChatGPT", profiles) {
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

func TestCloudDetailsSeparatesIdentityFromProfileID(t *testing.T) {
	sp := cloudSamplePage()
	profile := agentclient.CloudProfileInfo{Name: "chatgpt-2", Route: "chatgpt", Flavor: "responses", AccountEmail: "person@example.com"}
	sp.profiles = []agentclient.CloudProfileInfo{profile}
	sp.cloudView = agentclient.CloudProvidersView{Providers: []agentclient.CloudProvider{{ID: "openai-responses", Label: "ChatGPT", Profiles: sp.profiles}}}
	sp.selectCloudRow("profile:chatgpt-2")
	section := sp.buildCloudSection()
	seen := map[string]form.Field{}
	for _, field := range section.Fields {
		seen[field.Key()] = field
	}
	account := seen["cloud-account"]
	if account == nil || account.Display() != "person@example.com" || !strings.Contains(account.Label(), "Account") {
		t.Fatalf("identity absent from details: %+v", account)
	}
	id := seen["cloud-name"]
	if id == nil || id.Display() != "chatgpt-2" || !strings.Contains(id.Label(), "Profile ID") {
		t.Fatal("internal key still masquerades as name")
	}
	if sp.cloudDraft.Name != "chatgpt-2" {
		t.Fatal("credential key changed")
	}
}

func TestRoutingRenderedIdentityAndSelectionValue(t *testing.T) {
	sp := cloudSamplePage()
	p := agentclient.CloudProfileInfo{Name: "chatgpt-2", Route: "chatgpt", Flavor: "responses", AccountEmail: "person@example.com"}
	sp.profiles = []agentclient.CloudProfileInfo{p}
	sp.cloudView = agentclient.CloudProvidersView{Providers: []agentclient.CloudProvider{{Label: "openai (subscription)", Profiles: sp.profiles}}, Assignments: &agentclient.RoutingAssignments{Primary: p.Name}}
	sp.routingDraft = nil
	sections := sp.buildRoutingSections()
	field := sections[0].Groups[0].Fields[0]
	if field.Display() != "ChatGPT — person@example.com" {
		t.Fatalf("routing displays %q", field.Display())
	}
	if sp.routingDraft.Primary != p.Name {
		t.Fatal("display label changed routing key")
	}
	p.AccountEmail = ""
	sp.profiles[0] = p
	sp.cloudView.Providers[0].Profiles[0] = p
	sections = sp.buildRoutingSections()
	if got := sections[0].Groups[0].Fields[0].Display(); got != "ChatGPT — unidentified account (chatgpt-2)" {
		t.Fatal(got)
	}
}
