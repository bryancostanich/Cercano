package agentclient

import "testing"

func TestAccountDisplayIdentityFirst(t *testing.T) {
	p := CloudProfileInfo{Name: "chatgpt-2", Flavor: "responses", Route: "chatgpt", AccountEmail: "person@example.com"}
	if got := p.AccountLabel("openai (subscription)"); got != "ChatGPT — person@example.com" {
		t.Fatalf("identity should be primary, got %q", got)
	}
	p.AccountEmail = ""
	if got := p.AccountLabel("openai (subscription)"); got != "ChatGPT — unidentified account (chatgpt-2)" {
		t.Fatalf("missing identity must be explicit, got %q", got)
	}
}
