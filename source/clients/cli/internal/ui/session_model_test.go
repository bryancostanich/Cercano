package ui

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestSessionModelDisplay(t *testing.T) {
	got := sessionModelText(agentclient.SessionModelStatus{Override: &agentclient.SessionModelRoute{Profile: "deepinfra", Model: "exact-model"}, Note: "Next message.", Profiles: []agentclient.SessionModelProfile{{Name: "deepinfra", Provider: "deepinfra"}}}, true)
	for _, want := range []string{"deepinfra / exact-model", "no fallback", "Next message.", "Saved profiles:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if got := sessionModelText(agentclient.SessionModelStatus{}, false); !strings.Contains(got, "no override") {
		t.Fatal(got)
	}
}
func TestSessionModelStaleReplyIgnored(t *testing.T) {
	m := New(nil, false)
	model, cmd := m.Update(sessionModelMsg{convID: "another-session", status: agentclient.SessionModelStatus{Override: &agentclient.SessionModelRoute{Profile: "wrong", Model: "wrong"}}})
	got := model.(Model)
	if cmd != nil || got.convID != m.convID {
		t.Fatal("stale response changed session")
	}
}
