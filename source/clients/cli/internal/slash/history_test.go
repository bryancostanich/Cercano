package slash

import (
	"strings"
	"testing"
)

// The /history, /resume, /rename commands integrate with a live gRPC client.
// We can't fully unit-test them without spinning up an agent, but we can
// verify the command-shape contracts: that the registry knows them after
// RegisterHistory, that /history dispatches to ResultOpenHistoryPicker, that
// /rename with no args returns the usage hint, and that arg shape detection
// works without contacting the agent.

func TestRegisterHistory_RegistersExpectedCommands(t *testing.T) {
	r := New()
	RegisterHistory(r, nil, func() string { return "" })
	for _, want := range []string{"history", "resume", "rename"} {
		if _, ok := r.cmds[want]; !ok {
			t.Errorf("missing command /%s", want)
		}
	}
}

func TestSlash_History_DispatchesPicker(t *testing.T) {
	r := New()
	RegisterHistory(r, nil, func() string { return "" })
	res, ok := r.Dispatch("/history")
	if !ok {
		t.Fatal("expected /history to dispatch")
	}
	if res.Kind != ResultOpenHistoryPicker {
		t.Errorf("kind: got %v want ResultOpenHistoryPicker", res.Kind)
	}
}

func TestSlash_Resume_NoArgs_OpensPicker(t *testing.T) {
	r := New()
	RegisterHistory(r, nil, func() string { return "" })
	res, _ := r.Dispatch("/resume")
	if res.Kind != ResultOpenHistoryPicker {
		t.Errorf("kind: got %v want ResultOpenHistoryPicker", res.Kind)
	}
}

func TestSlash_Rename_NoArgs_ShowsUsage(t *testing.T) {
	r := New()
	RegisterHistory(r, nil, func() string { return "abc123" })
	res, _ := r.Dispatch("/rename")
	if res.Kind != ResultText {
		t.Errorf("kind: got %v want ResultText", res.Kind)
	}
	if !strings.Contains(res.Text, "usage:") {
		t.Errorf("expected usage hint, got %q", res.Text)
	}
}

func TestSlash_Rename_NoCurrentConv_Errors(t *testing.T) {
	r := New()
	RegisterHistory(r, nil, func() string { return "" })
	res, _ := r.Dispatch("/rename a new title")
	if !strings.Contains(res.Text, "no current conversation") {
		t.Errorf("expected 'no current conversation' hint, got %q", res.Text)
	}
}

func TestIsHex(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"0123456789abcdef", true},
		{"ABCDEF", true},
		{"deadbeef", true},
		{"deadbeeg", false}, // g is invalid
		{"", true},          // empty string is vacuously hex; caller guards length
		{"hello world", false},
	}
	for _, c := range cases {
		if got := isHex(c.in); got != c.want {
			t.Errorf("isHex(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
