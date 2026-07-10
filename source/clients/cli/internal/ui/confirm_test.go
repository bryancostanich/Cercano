package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
	tea "charm.land/bubbletea/v2"
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
	for _, want := range []string{"write_file", "[y]es", "[n]o", "[d]etails"} {
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

func TestRenderConfirmPrompt_DestructiveMCP_HasWarnGlyph(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name: "mcp__github__delete_repo", Args: "{}", Permission: "W", Destructive: true,
	}))
	if !strings.Contains(s, "⚠") {
		t.Errorf("destructive MCP tool prompt should carry ⚠ glyph: %q", s)
	}
}

func TestRenderConfirmPrompt_NonDestructiveMCP_NoWarnGlyph(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name: "mcp__github__list_issues", Args: "{}", Permission: "W", Destructive: false,
	}))
	if strings.Contains(s, "⚠") {
		t.Errorf("non-destructive MCP tool prompt must not carry ⚠ glyph: %q", s)
	}
}

func TestRenderConfirmPrompt_DispatchFallsBackToTaskAndTools(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "dispatch",
		Args:       `{"task":"In /repo, audit the SKILL.md catalog recommendation","tools":["Read","Grep","Bash"]}`,
		Permission: "X",
	}))
	for _, want := range []string{"dispatch wants to run a delegated agent", "Task: In /repo, audit the SKILL.md catalog recommendation", "Tools: Read, Grep, Bash"} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q in prompt, got: %q", want, s)
		}
	}
	if strings.Contains(s, `{"task"`) || strings.Contains(s, `"tools"`) {
		t.Errorf("prompt should summarize args instead of showing raw JSON: %q", s)
	}
}

func TestRenderConfirmPrompt_DispatchPrefersIntent(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "dispatch",
		Args:       `{"intent":"Audit CLI docs against implementation","task":"In /repo, audit the SKILL.md catalog recommendation","tools":["Read","Grep","Bash"]}`,
		Permission: "X",
	}))
	for _, want := range []string{"dispatch wants to run a delegated agent", "Intent: Audit CLI docs against implementation", "Tools: Read, Grep, Bash"} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q in prompt, got: %q", want, s)
		}
	}
	if strings.Contains(s, "Task:") {
		t.Errorf("intent should replace the task fallback, got: %q", s)
	}
}

func TestRenderConfirmPrompt_GitLandUsesHumanSummaryNotCWD(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "git_land",
		Args:       `{"feature":"","trunk":"main","strategy":"rebase","continue":false,"cwd":"/Users/bryancostanich/git_repos/bryan_costanich/Cercano/.worktrees/sub-agent-grants"}`,
		Permission: "X",
	}))
	if !strings.Contains(s, "git_land land current branch onto main") {
		t.Fatalf("expected human git_land action summary, got: %q", s)
	}
	if strings.Contains(s, "/Users/bryancostanich") || strings.Contains(s, "cwd=") {
		t.Fatalf("git_land prompt title should not be dominated by cwd, got: %q", s)
	}
}

func TestRenderConfirmPrompt_GitLandShowsIntentWithoutCWD(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "git_land",
		Args:       `{"intent":"Land sub-agent grant UX onto main","feature":"","trunk":"main","strategy":"rebase","continue":false,"cwd":"/Users/bryancostanich/git_repos/bryan_costanich/Cercano/.worktrees/sub-agent-grants"}`,
		Permission: "X",
	}))
	if !strings.Contains(s, "Intent: Land sub-agent grant UX onto main") {
		t.Fatalf("expected intent line, got: %q", s)
	}
	if strings.Contains(s, "/Users/bryancostanich") || strings.Contains(s, "cwd=") {
		t.Fatalf("intent-bearing git_land prompt should not show raw cwd, got: %q", s)
	}
}

func TestRenderConfirmPrompt_TruncatesLongArgs(t *testing.T) {
	m := minimalModel()
	bigArgs := `{"content":"` + strings.Repeat("x", 500) + `"}`
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name: "write_file", Args: bigArgs, Permission: "W",
	}))
	if !strings.Contains(s, "content=") {
		t.Errorf("expected summarized key in prompt, got: %q", s)
	}
	if !strings.Contains(s, "…") {
		t.Errorf("expected ellipsis from arg truncation, got: %q", s)
	}
}

func TestResolveConfirmKey_N_Cancels(t *testing.T) {
	m := minimalModel()
	m.pendingConfirm = toolConfirm(&pendingToolCall{Name: "rm_file", Args: "{}", Permission: "X"})

	next, cmd := m.resolveConfirmKey("n")
	if next.pendingConfirm != nil {
		t.Errorf("n should clear pendingConfirm")
	}
	if cmd != nil {
		t.Errorf("n should not return a cmd; got %v", cmd)
	}
	if len(next.mainChat().Entries()) == 0 {
		t.Errorf("expected a cancellation system entry")
	}
}

func TestResolveConfirmKey_Esc_Cancels(t *testing.T) {
	m := minimalModel()
	m.pendingConfirm = toolConfirm(&pendingToolCall{Name: "write_file", Args: "{}", Permission: "W"})
	next, _ := m.resolveConfirmKey("esc")
	if next.pendingConfirm != nil {
		t.Errorf("esc should clear pendingConfirm")
	}
}

func TestResolveConfirmKey_D_TogglesDetailsAndKeepsPending(t *testing.T) {
	m := minimalModel()
	m.pendingConfirm = toolConfirm(&pendingToolCall{Name: "edit_file", Args: `{"path":"a.go"}`, Permission: "W"})

	next, cmd := m.resolveConfirmKey("d")
	if next.pendingConfirm == nil {
		t.Errorf("d must NOT clear pendingConfirm (user still needs to y/n)")
	}
	if cmd != nil {
		t.Errorf("d should not return a cmd")
	}
	if got := countDetailEntries(next, `"path":"a.go"`); got != 1 {
		t.Fatalf("first d should append exactly one details entry, got %d; entries: %+v", got, next.mainChat().Entries())
	}

	next, cmd = next.resolveConfirmKey("d")
	if next.pendingConfirm == nil {
		t.Errorf("second d must NOT clear pendingConfirm (user still needs to y/n)")
	}
	if cmd != nil {
		t.Errorf("second d should not return a cmd")
	}
	if got := countDetailEntries(next, `"path":"a.go"`); got != 0 {
		t.Fatalf("second d should collapse details, got %d; entries: %+v", got, next.mainChat().Entries())
	}
}

func countDetailEntries(m Model, needle string) int {
	count := 0
	for _, e := range m.mainChat().Entries() {
		if strings.Contains(e.Content, "details:") && strings.Contains(e.Content, needle) {
			count++
		}
	}
	return count
}

func TestResolveConfirmKey_Y_ClearsAndReturnsCmd(t *testing.T) {
	m := minimalModel()
	m.pendingConfirm = toolConfirm(&pendingToolCall{Name: "write_file", Args: "{}", Permission: "W"})

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
	m.pendingConfirm = toolConfirm(&pendingToolCall{
		ToolUseID:  "tu_123",
		Name:       "write_file",
		Args:       "{}",
		Permission: "W",
	})

	next, cmd := m.resolveConfirmKey("y")
	if next.pendingConfirm != nil {
		t.Errorf("y should clear pendingConfirm")
	}
	// Stream-event path: server has the call; UI must not double-fire it.
	if cmd != nil {
		t.Errorf("y with ToolUseID should NOT return a tea.Cmd; got %v", cmd)
	}
}

func TestResolveConfirmKey_N_WithToolUseID_NoCmd(t *testing.T) {
	m := minimalModel()
	m.pendingConfirm = toolConfirm(&pendingToolCall{
		ToolUseID:  "tu_456",
		Name:       "rm_file",
		Args:       "{}",
		Permission: "X",
	})

	next, cmd := m.resolveConfirmKey("n")
	if next.pendingConfirm != nil {
		t.Errorf("n should clear pendingConfirm")
	}
	if cmd != nil {
		t.Errorf("n should not return a tea.Cmd; got %v", cmd)
	}
}

func TestResolveConfirmKey_OtherKey_Ignored(t *testing.T) {
	m := minimalModel()
	m.pendingConfirm = toolConfirm(&pendingToolCall{Name: "rm_file", Args: "{}", Permission: "X"})

	next, cmd := m.resolveConfirmKey("a")
	if next.pendingConfirm == nil {
		t.Errorf("unrelated key should NOT clear pendingConfirm")
	}
	if cmd != nil {
		t.Errorf("unrelated key should not return a cmd")
	}
}

func TestResolveConfirmKey_Generic(t *testing.T) {
	yes, no, details := false, false, false
	mk := func() Model {
		m := minimalModel()
		m.pendingConfirm = &confirmRequest{
			onYes:  func(m Model) (Model, tea.Cmd) { yes = true; m.pendingConfirm = nil; return m, nil },
			onNo:   func(m Model) (Model, tea.Cmd) { no = true; m.pendingConfirm = nil; return m, nil },
			extras: map[string]func(Model) (Model, tea.Cmd){"d": func(m Model) (Model, tea.Cmd) { details = true; return m, nil }},
		}
		return m
	}
	// y → onYes, clears
	m := mk()
	m, _ = m.resolveConfirmKey("y")
	if !yes || m.pendingConfirm != nil {
		t.Errorf("y: yes=%v pending=%v", yes, m.pendingConfirm != nil)
	}
	// n → onNo, clears
	yes = false
	m = mk()
	m, _ = m.resolveConfirmKey("n")
	if !no || m.pendingConfirm != nil {
		t.Errorf("n: no=%v pending=%v", no, m.pendingConfirm != nil)
	}
	// d (extra) → handler, does NOT clear
	m = mk()
	m, _ = m.resolveConfirmKey("d")
	if !details || m.pendingConfirm == nil {
		t.Errorf("d: details=%v pending=%v", details, m.pendingConfirm == nil)
	}
	// unknown key → ignored, still pending
	m = mk()
	m, _ = m.resolveConfirmKey("x")
	if m.pendingConfirm == nil {
		t.Error("unknown key cleared the confirm")
	}
}

func TestMCPConfirmOffersAlwaysAllow(t *testing.T) {
	tc := &pendingToolCall{Name: "mcp__github__create_issue", Permission: "W", ToolUseID: "t1"}
	c := toolConfirm(tc)
	if _, ok := c.extras["a"]; !ok {
		t.Fatal("MCP tool confirm should expose an [a]lways-allow key")
	}
}

func TestBuiltinConfirmHasNoAlwaysAllow(t *testing.T) {
	tc := &pendingToolCall{Name: "Write", Permission: "W", ToolUseID: "t2"}
	c := toolConfirm(tc)
	if _, ok := c.extras["a"]; ok {
		t.Fatal("built-in confirm must not expose always-allow")
	}
}

func TestDisplayToolName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"mcp__github__create_issue", "mcp/github/create_issue"},
		{"mcp__myserver__do_thing", "mcp/myserver/do_thing"},
		{"Write", "Write"},
		{"Bash", "Bash"},
	}
	for _, tc := range cases {
		got := displayToolName(tc.in)
		if got != tc.want {
			t.Errorf("displayToolName(%q) = %q, want %q", tc.in, got, tc.want)
		}
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
