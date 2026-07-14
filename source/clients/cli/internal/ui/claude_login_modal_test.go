package ui

import (
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestClaudeLoginModalTransitions(t *testing.T) {
	mo := newClaudeLoginModal("claude", "claude-sonnet-5")
	if mo.state != claudeLoginWaiting {
		t.Fatalf("initial state = %v, want waiting", mo.state)
	}
	mo.setURL("https://claude.ai/oauth/authorize?x=1")
	if mo.authorizeURL == "" {
		t.Error("setURL did not record the authorize URL")
	}
	mo.setDone()
	if mo.state != claudeLoginDone {
		t.Errorf("after setDone state = %v, want done", mo.state)
	}
	if mo.cancel != nil {
		t.Error("setDone should clear cancel")
	}
}

func TestClaudeLoginModalSetFailed(t *testing.T) {
	mo := newClaudeLoginModal("claude", "")
	mo.setFailed("declined by user")
	if mo.state != claudeLoginFailed || mo.errMsg != "declined by user" {
		t.Errorf("setFailed → state=%v msg=%q, want failed/declined", mo.state, mo.errMsg)
	}
}

// A stream channel that closes without a terminal frame must synthesize a
// failure frame so the modal never hangs in the waiting state.
func TestDrainClaudeLoginClosedChannel(t *testing.T) {
	ch := make(chan agentclient.ClaudeLoginMsg)
	close(ch)
	msg := drainClaudeLoginCmd(ch)()
	frame, ok := msg.(claudeLoginFrameMsg)
	if !ok {
		t.Fatalf("want claudeLoginFrameMsg, got %T", msg)
	}
	if !frame.frame.Done || frame.frame.Ok {
		t.Errorf("closed channel should yield a terminal failure frame, got %+v", frame.frame)
	}
}

func TestDrainClaudeLoginDeliversFrame(t *testing.T) {
	ch := make(chan agentclient.ClaudeLoginMsg, 1)
	ch <- agentclient.ClaudeLoginMsg{AuthorizeURL: "https://claude.ai/oauth/authorize?y=2"}
	msg := drainClaudeLoginCmd(ch)()
	frame := msg.(claudeLoginFrameMsg)
	if frame.frame.AuthorizeURL == "" {
		t.Error("frame did not carry the authorize URL through")
	}
	if frame.ch == nil {
		t.Error("drain should re-arm with the channel for the next frame")
	}
}
