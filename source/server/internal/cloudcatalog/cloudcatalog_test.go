package cloudcatalog

import "testing"

// providerByID finds a grouped provider in the result, failing the test if absent.
func providerByID(t *testing.T, ps []GroupedProvider, id string) GroupedProvider {
	t.Helper()
	for _, p := range ps {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("provider %q not present in grouped result", id)
	return GroupedProvider{}
}

func names(ps []ProfileRef) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}

func TestCatalogIsWellFormed(t *testing.T) {
	cat := Catalog()
	if len(cat) == 0 {
		t.Fatal("catalog is empty")
	}
	seen := map[string]bool{}
	for _, p := range cat {
		if p.ID == "" || p.Label == "" {
			t.Errorf("provider has empty id/label: %+v", p)
		}
		if seen[p.ID] {
			t.Errorf("duplicate provider id %q", p.ID)
		}
		seen[p.ID] = true
	}
	// The two historically-confusing entries must be distinct catalog rows.
	if !seen["openai"] || !seen["openai-responses"] {
		t.Error("expected both openai and openai-responses in catalog")
	}
}

func TestProviderIDForDerivation(t *testing.T) {
	cases := []struct {
		name string
		in   ProfileRef
		want string
	}{
		{"messages→anthropic (direct)", ProfileRef{Flavor: "messages"}, "anthropic"},
		{"messages→anthropic (meridian)", ProfileRef{Flavor: "messages", Route: "meridian"}, "anthropic"},
		{"responses→openai-responses", ProfileRef{Flavor: "responses"}, "openai-responses"},
		{"bedrock→bedrock", ProfileRef{Flavor: "bedrock"}, "bedrock"},
		{"chat+openai backend", ProfileRef{Flavor: "chat_completions", Backend: "openai"}, "openai"},
		{"chat+gemini backend", ProfileRef{Flavor: "chat_completions", Backend: "gemini"}, "gemini"},
		{"chat+groq backend", ProfileRef{Flavor: "chat_completions", Backend: "groq"}, "groq"},
		{"chat backendless deepinfra host", ProfileRef{Flavor: "chat_completions", BaseURL: "https://api.deepinfra.com/v1/openai"}, "deepinfra"},
		{"chat backendless together host", ProfileRef{Flavor: "chat_completions", BaseURL: "https://api.together.xyz/v1"}, "together"},
		{"chat backendless openrouter host", ProfileRef{Flavor: "chat_completions", BaseURL: "https://openrouter.ai/api/v1"}, "openrouter"},
		{"chat backendless deepseek host", ProfileRef{Flavor: "chat_completions", BaseURL: "https://api.deepseek.com"}, "deepseek"},
		{"chat unknown host → custom", ProfileRef{Flavor: "chat_completions", BaseURL: "https://api.example.com/v1"}, ""},
		{"unknown flavor → custom", ProfileRef{Flavor: "whatever"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ProviderIDFor(c.in); got != c.want {
				t.Errorf("ProviderIDFor(%+v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// A single anthropic profile must appear under exactly one provider, once — the
// core fix for the "two anthropic rows" complaint.
func TestGroupSingleProfileNoDuplicate(t *testing.T) {
	profiles := []ProfileRef{{Name: "default", Flavor: "messages", Route: "meridian"}}
	providers, custom := Group(profiles, "default")
	if len(custom) != 0 {
		t.Errorf("expected no custom profiles, got %v", names(custom))
	}
	anthropic := providerByID(t, providers, "anthropic")
	if len(anthropic.Profiles) != 1 || anthropic.Primary != "default" {
		t.Errorf("anthropic: got profiles=%v primary=%q, want [default]/default", names(anthropic.Profiles), anthropic.Primary)
	}
	// Every other provider must be empty — the profile must not leak elsewhere.
	for _, p := range providers {
		if p.ID != "anthropic" && len(p.Profiles) != 0 {
			t.Errorf("provider %q unexpectedly has profiles %v", p.ID, names(p.Profiles))
		}
	}
}

// Two anthropic accounts (e.g. two emails) group under one provider, active one primary.
func TestGroupMultipleProfilesActiveIsPrimary(t *testing.T) {
	profiles := []ProfileRef{
		{Name: "work", Flavor: "messages", Route: "meridian"},
		{Name: "personal", Flavor: "messages"},
	}
	providers, _ := Group(profiles, "personal")
	anthropic := providerByID(t, providers, "anthropic")
	got := names(anthropic.Profiles)
	if len(got) != 2 || got[0] != "personal" || got[1] != "work" {
		t.Errorf("expected [personal work] (active first), got %v", got)
	}
	if anthropic.Primary != "personal" {
		t.Errorf("primary = %q, want personal", anthropic.Primary)
	}
}

// With no active match in the bucket, primary prefers the same-named profile so
// the visible provider row edits/signs into the stable catalog profile instead
// of an older/default alias that happens to appear first in config order.
func TestGroupPrimaryPrefersSameNamedProfile(t *testing.T) {
	profiles := []ProfileRef{
		{Name: "default", Flavor: "messages"},
		{Name: "anthropic", Flavor: "messages"},
		{Name: "personal", Flavor: "messages"},
	}
	providers, _ := Group(profiles, "some-openai-profile")
	anthropic := providerByID(t, providers, "anthropic")
	if anthropic.Primary != "anthropic" {
		t.Errorf("primary = %q, want anthropic", anthropic.Primary)
	}
	got := names(anthropic.Profiles)
	if len(got) != 3 || got[0] != "anthropic" || got[1] != "default" || got[2] != "personal" {
		t.Errorf("profiles = %v, want [anthropic default personal]", got)
	}
}

// If neither the active profile nor the same-named profile belongs to the
// bucket, primary falls back to the first profile in input order.
func TestGroupPrimaryFallsBackToFirst(t *testing.T) {
	profiles := []ProfileRef{
		{Name: "work", Flavor: "messages"},
		{Name: "personal", Flavor: "messages"},
	}
	providers, _ := Group(profiles, "some-openai-profile")
	anthropic := providerByID(t, providers, "anthropic")
	if anthropic.Primary != "work" {
		t.Errorf("primary = %q, want work (first)", anthropic.Primary)
	}
}

// The two OpenAI entries stay separate: a responses profile and a chat profile
// must land on different providers, never merge.
func TestGroupOpenAIResponsesVsChatSeparate(t *testing.T) {
	profiles := []ProfileRef{
		{Name: "chatgpt-sub", Flavor: "responses", Route: "chatgpt"},
		{Name: "openai-key", Flavor: "chat_completions", Backend: "openai"},
	}
	providers, _ := Group(profiles, "")
	resp := providerByID(t, providers, "openai-responses")
	chat := providerByID(t, providers, "openai")
	if len(resp.Profiles) != 1 || resp.Profiles[0].Name != "chatgpt-sub" {
		t.Errorf("openai-responses profiles = %v, want [chatgpt-sub]", names(resp.Profiles))
	}
	if len(chat.Profiles) != 1 || chat.Profiles[0].Name != "openai-key" {
		t.Errorf("openai profiles = %v, want [openai-key]", names(chat.Profiles))
	}
}

func TestGroupCustomEndpointSplitOut(t *testing.T) {
	profiles := []ProfileRef{{Name: "my-llm", Flavor: "chat_completions", BaseURL: "https://api.example.com/v1"}}
	providers, custom := Group(profiles, "")
	if len(custom) != 1 || custom[0].Name != "my-llm" {
		t.Errorf("custom = %v, want [my-llm]", names(custom))
	}
	for _, p := range providers {
		if len(p.Profiles) != 0 {
			t.Errorf("provider %q should be empty, has %v", p.ID, names(p.Profiles))
		}
	}
}
