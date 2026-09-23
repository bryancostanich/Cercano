package agentclient

import (
	"cercano/source/server/pkg/proto"
	"strings"
	"testing"
)

func TestAccountIdentityWireAndLabels(t *testing.T) {
	p := cloudProfileInfoFromProto(&proto.CloudProfileInfo{Name: "chatgpt-work", AccountEmail: "person@example.com", AccountDisplayName: "Person"})
	if got := p.AccountLabel("ChatGPT"); got != "ChatGPT — person@example.com (chatgpt-work)" {
		t.Fatal(got)
	}
	other := p
	other.Name = "chatgpt-personal"
	if p.AccountLabel("ChatGPT") == other.AccountLabel("ChatGPT") {
		t.Fatal("same-email accounts indistinguishable")
	}
	p.AccountEmail = ""
	if got := p.AccountLabel("ChatGPT"); got != "ChatGPT — Person (chatgpt-work)" {
		t.Fatal(got)
	}
	p.AccountDisplayName = ""
	if got := p.AccountLabel("ChatGPT"); got != "ChatGPT — chatgpt-work" {
		t.Fatal(got)
	}
	p.AccountEmail = "bad\x1b\n\u202e@example.com"
	if got := p.AccountLabel("ChatGPT"); strings.ContainsAny(got, "\x1b\n\u202e") {
		t.Fatal("terminal control in label")
	}
}
