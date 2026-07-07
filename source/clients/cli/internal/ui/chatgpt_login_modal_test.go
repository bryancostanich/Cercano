package ui

import (
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestChatGPTLoginModalTransitions(t *testing.T) {
	mo := newChatGPTLoginModal("chatgpt", "gpt-5.3-codex")
	if mo.state != chatgptLoginWaiting {
		t.Fatalf("initial state = %d, want waiting", mo.state)
	}
	mo.setCode("https://auth.openai.com/codex/device", "ABCD-1234")
	if mo.userCode != "ABCD-1234" || mo.verificationURL == "" {
		t.Errorf("setCode didn't record code/url: %+v", mo)
	}
	mo.setDone("acct-9")
	if mo.state != chatgptLoginDone || mo.accountID != "acct-9" {
		t.Errorf("setDone: state=%d acct=%q", mo.state, mo.accountID)
	}
	if mo.cancel != nil {
		t.Error("setDone must clear cancel")
	}
}

func TestChatGPTLoginModalSetFailed(t *testing.T) {
	mo := newChatGPTLoginModal("chatgpt", "")
	mo.setFailed("declined by user")
	if mo.state != chatgptLoginFailed || mo.errMsg != "declined by user" {
		t.Errorf("setFailed: state=%d err=%q", mo.state, mo.errMsg)
	}
}

// TestDrainChatGPTLoginClosedChannel verifies a channel that closes without a
// terminal frame yields a synthetic failure frame so the modal never hangs.
func TestDrainChatGPTLoginClosedChannel(t *testing.T) {
	ch := make(chan agentclient.ChatGPTLoginMsg)
	close(ch)
	msg := drainChatGPTLoginCmd(ch)()
	frame, ok := msg.(chatgptLoginFrameMsg)
	if !ok {
		t.Fatalf("want chatgptLoginFrameMsg, got %T", msg)
	}
	if !frame.frame.Done || frame.frame.Ok {
		t.Errorf("closed channel should yield done+failed frame: %+v", frame.frame)
	}
}

// TestDrainChatGPTLoginDeliversFrame verifies a normal frame is passed through
// with the channel retained for re-draining.
func TestDrainChatGPTLoginDeliversFrame(t *testing.T) {
	ch := make(chan agentclient.ChatGPTLoginMsg, 1)
	ch <- agentclient.ChatGPTLoginMsg{UserCode: "WXYZ-9", VerificationURL: "u"}
	msg := drainChatGPTLoginCmd(ch)()
	frame := msg.(chatgptLoginFrameMsg)
	if frame.frame.UserCode != "WXYZ-9" {
		t.Errorf("frame not passed through: %+v", frame.frame)
	}
	if frame.ch == nil {
		t.Error("channel should be retained for re-drain on a non-closed stream")
	}
}
