package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	tea "charm.land/bubbletea/v2"
)

// pressing 'c' while waiting marks the code copied and issues a clipboard cmd.
func TestChatGPTLoginCopyCodeKey(t *testing.T) {
	m := Model{chatgptLoginModal: newChatGPTLoginModal("chatgpt", "")}
	m.chatgptLoginModal.setCode("https://auth.openai.com/codex/device", "ABCD-1234")
	next, cmd := m.handleChatGPTLoginModalKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
	nm := next.(Model)
	if nm.chatgptLoginModal == nil || !nm.chatgptLoginModal.copied {
		t.Fatal("pressing c should mark the code copied")
	}
	if cmd == nil {
		t.Fatal("pressing c should issue a clipboard command")
	}
}

// the waiting modal advertises the copy key, and confirms once copied.
func TestChatGPTLoginCopyRendersInActions(t *testing.T) {
	pal := theme.Cracker()
	styles := theme.NewStyles(pal)
	mo := newChatGPTLoginModal("chatgpt", "")
	mo.setCode("https://auth.openai.com/codex/device", "ABCD-1234")
	if !strings.Contains(mo.View(styles, pal, 80, 24), "copy code") {
		t.Error("waiting modal should advertise [c] copy code")
	}
	mo.copied = true
	if !strings.Contains(mo.View(styles, pal, 80, 24), "copied") {
		t.Error("after copy, modal should confirm 'copied'")
	}
}
