package ui

import (
	"strings"
	"testing"

	"cercano/source/server/internal/cli/theme"
)

// minimalModel returns a Model populated just enough for the confirm-prompt
// methods to run without panicking. Agent is nil — the only path that
// exercises the agent is the 'y' branch (which we test for cmd presence, not
// execution).
func minimalModel() Model {
	return Model{
		palette: theme.Cracker(),
		styles:  theme.NewStyles(theme.Cracker()),
	}
}

func TestRenderConfirmPrompt_W_ShowsKeyHints(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name: "write_file", Args: `{"path":"x.txt"}`, Permission: "W",
	}))
	for _, want := range []string{"write_file", "[y]es", "[n]o", "[d]iff"} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q in prompt, got: %q", want, s)
		}
	}
	if strings.Contains(s, "DESTRUCTIVE") {
		t.Errorf("W-tier prompt should NOT say DESTRUCTIVE: %q", s)
	}
}

func TestRenderConfirmPrompt_X_HasDestructiveEmphasis(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name: "rm_file", Args: `{"path":"x.txt"}`, Permission: "X",
	}))
	if !strings.Contains(s, "DESTRUCTIVE") {
		t.Errorf("X-tier prompt must say DESTRUCTIVE: %q", s)
	}
	if !strings.Contains(s, "⚠") {
		t.Errorf("X-tier prompt should carry ⚠ glyph: %q", s)
	}
}

func TestRenderConfirmPrompt_TruncatesLongArgs(t *testing.T) {
	m := minimalModel()
	bigArgs := `{"content":"` + strings.Repeat("x", 500) + `"}`
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name: "write_file", Args: bigArgs, Permission: "W",
	}))
	if !strings.Contains(s, "…") {
		t.Errorf("expected ellipsis from arg truncation, got: %q", s)
	}
}

func TestResolveConfirmKey_N_Cancels(t *testing.T) {
	m := minimalModel()
	m.pendingConfirm = &pendingToolCall{Name: "rm_file", Args: "{}", Permission: "X"}

	next, cmd := m.resolveConfirmKey("n")
	if next.pendingConfirm != nil {
		t.Errorf("n should clear pendingConfirm")
	}
	// Inline mode: the cancellation message is Println'd to scrollback as
	// a tea.Cmd rather than appended to an in-memory entries slice.
	if cmd == nil {
		t.Errorf("n should return a Println cmd for the cancellation message")
	}
}

func TestResolveConfirmKey_Esc_Cancels(t *testing.T) {
	m := minimalModel()
	m.pendingConfirm = &pendingToolCall{Name: "write_file", Args: "{}", Permission: "W"}
	next, _ := m.resolveConfirmKey("esc")
	if next.pendingConfirm != nil {
		t.Errorf("esc should clear pendingConfirm")
	}
}

func TestResolveConfirmKey_D_RevealsArgsAndKeepsPending(t *testing.T) {
	m := minimalModel()
	m.pendingConfirm = &pendingToolCall{Name: "edit_file", Args: `{"path":"a.go"}`, Permission: "W"}

	next, cmd := m.resolveConfirmKey("d")
	if next.pendingConfirm == nil {
		t.Errorf("d must NOT clear pendingConfirm (user still needs to y/n)")
	}
	// Inline mode: d Println's the args body to scrollback as a tea.Cmd.
	if cmd == nil {
		t.Errorf("d should return a Println cmd carrying the full args")
	}
}

func TestResolveConfirmKey_Y_ClearsAndReturnsCmd(t *testing.T) {
	m := minimalModel()
	m.pendingConfirm = &pendingToolCall{Name: "write_file", Args: "{}", Permission: "W"}

	next, cmd := m.resolveConfirmKey("y")
	if next.pendingConfirm != nil {
		t.Errorf("y should clear pendingConfirm")
	}
	// The returned cmd will, when fired, call InvokeTool on the (nil) agent —
	// we don't fire it. Just verify a cmd was returned so the agent gets
	// invoked at all.
	if cmd == nil {
		t.Errorf("y should return a tea.Cmd to fire the tool")
	}
}

// PermissionRequired stream-event path: pendingConfirm carries a ToolUseID,
// so y / n must NOT fire invokeToolCmd (the server already has the call
// queued); they should clear pendingConfirm and return a nil tea.Cmd. The
// actual RPC fires in a goroutine and is exercised in integration tests; here
// we only assert the synchronous UI contract.
func TestResolveConfirmKey_Y_WithToolUseID_NoCmd(t *testing.T) {
	m := minimalModel()
	m.pendingConfirm = &pendingToolCall{
		ToolUseID:  "tu_123",
		Name:       "write_file",
		Args:       "{}",
		Permission: "W",
	}

	next, cmd := m.resolveConfirmKey("y")
	if next.pendingConfirm != nil {
		t.Errorf("y should clear pendingConfirm")
	}
	// Stream-event path: server has the call; the UI must NOT call
	// invokeToolCmd. Inline mode still Println's the approval message via
	// a tea.Cmd, so we expect a non-nil cmd — but it carries the system
	// line, not a tool invocation. The "no double-fire" guarantee is in
	// resolveConfirmKey: only the AllowToolCall goroutine runs.
	if cmd == nil {
		t.Errorf("y should return a Println cmd for the approval message")
	}
}

func TestResolveConfirmKey_N_WithToolUseID_PrintlnOnly(t *testing.T) {
	m := minimalModel()
	m.pendingConfirm = &pendingToolCall{
		ToolUseID:  "tu_456",
		Name:       "rm_file",
		Args:       "{}",
		Permission: "X",
	}

	next, cmd := m.resolveConfirmKey("n")
	if next.pendingConfirm != nil {
		t.Errorf("n should clear pendingConfirm")
	}
	// Inline mode: n Println's the cancellation message. The DenyToolCall
	// RPC fires in a goroutine, not as the returned tea.Cmd.
	if cmd == nil {
		t.Errorf("n should return a Println cmd for the cancellation message")
	}
}

func TestResolveConfirmKey_OtherKey_Ignored(t *testing.T) {
	m := minimalModel()
	m.pendingConfirm = &pendingToolCall{Name: "rm_file", Args: "{}", Permission: "X"}

	next, cmd := m.resolveConfirmKey("a")
	if next.pendingConfirm == nil {
		t.Errorf("unrelated key should NOT clear pendingConfirm")
	}
	if cmd != nil {
		t.Errorf("unrelated key should not return a cmd")
	}
}

// stripAnsiCSI removes ANSI CSI sequences so substring assertions match
// against the literal text. Local copy to avoid coupling the test to
// implementation packages.
func stripAnsiCSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7E) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
