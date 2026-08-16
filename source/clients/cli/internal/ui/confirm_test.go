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

func TestReauthRequiredRaisesReusableConfirmPrompt(t *testing.T) {
	m := modelWithContextView()
	msg := reauthRequiredMsg{provider: "anthropic", profile: "claude", note: "anthropic auth failed — switching to openai-responses"}
	m2, _ := m.routeChatMsg(msg)
	if m2.pendingConfirm == nil {
		t.Fatal("reauth should raise pendingConfirm")
	}
	out := stripAnsiCSI(m2.renderConfirmRequest(m2.pendingConfirm))
	if !strings.Contains(out, "Claude sign-in expired") {
		t.Fatalf("prompt missing title: %q", out)
	}
	if !strings.Contains(out, "[y]es re-auth") || !strings.Contains(out, "[n]o dismiss") || !strings.Contains(out, "[d]etails") {
		t.Fatalf("prompt missing custom hints: %q", out)
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

func TestRenderConfirmPrompt_SuggestPlan_AsksPlainQuestionNotDestructive(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "suggest_plan",
		Args:       `{"reason":"spans 4 files; approach uncertain","effort":"viz-drag-audit"}`,
		Permission: "X",
	}))
	if !strings.Contains(s, "Enter plan mode") {
		t.Errorf("suggest_plan prompt should ask a plain question, got: %q", s)
	}
	if strings.Contains(s, "DESTRUCTIVE") || strings.Contains(s, "⚠") {
		t.Errorf("suggest_plan is X-tier but destroys nothing; must not be DESTRUCTIVE/⚠: %q", s)
	}
	if !strings.Contains(s, "Why: spans 4 files; approach uncertain") {
		t.Errorf("suggest_plan prompt should surface reason as Why detail, got: %q", s)
	}
	if strings.Contains(s, `{"reason"`) || strings.Contains(s, "reason=") {
		t.Errorf("prompt should not dump raw args, got: %q", s)
	}
}

func TestRenderConfirmPrompt_RequestPlanApproval_AsksToExecute(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "request_plan_approval",
		Args:       `{"summary":"Fix handle-drag: 3 phases","effort":"viz-drag-audit"}`,
		Permission: "X",
	}))
	if !strings.Contains(s, "start executing") {
		t.Errorf("request_plan_approval prompt should ask to execute, got: %q", s)
	}
	if strings.Contains(s, "DESTRUCTIVE") || strings.Contains(s, "⚠") {
		t.Errorf("request_plan_approval must not be DESTRUCTIVE/⚠: %q", s)
	}
	if !strings.Contains(s, "Plan: Fix handle-drag: 3 phases") {
		t.Errorf("prompt should surface summary as Plan detail, got: %q", s)
	}
}

func TestRenderConfirmPrompt_SuggestAutonomous_AsksToStartWithBrief(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "suggest_autonomous",
		Args:       `{"reason":"plan accepted","goal":"implement autonomous mode","done_when":["profile active","tests pass"],"constraints":["do not push"]}`,
		Permission: "X",
	}))
	for _, want := range []string{"Start autonomous mode with this run brief?", "Why: plan accepted", "Goal: implement autonomous mode", "Done when: profile active; tests pass"} {
		if !strings.Contains(s, want) {
			t.Errorf("suggest_autonomous prompt missing %q: %q", want, s)
		}
	}
	if strings.Contains(s, "DESTRUCTIVE") || strings.Contains(s, "⚠") {
		t.Errorf("suggest_autonomous is X-tier but destroys nothing; must not be DESTRUCTIVE/⚠: %q", s)
	}
}

func TestRenderConfirmPrompt_RequestAutonomousExit_AsksForReview(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "request_autonomous_exit",
		Args:       `{"summary":"done","verification":"targeted tests passed"}`,
		Permission: "X",
	}))
	for _, want := range []string{"begin final decision review", "Summary: done", "Verification: targeted tests passed"} {
		if !strings.Contains(s, want) {
			t.Errorf("request_autonomous_exit prompt missing %q: %q", want, s)
		}
	}
	if strings.Contains(s, "DESTRUCTIVE") || strings.Contains(s, "⚠") {
		t.Errorf("request_autonomous_exit must not be DESTRUCTIVE/⚠: %q", s)
	}
}

func TestRenderConfirmPrompt_CompleteAutonomousReview_AsksToExit(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "complete_autonomous_review",
		Args:       `{"summary":"decisions accepted"}`,
		Permission: "X",
	}))
	for _, want := range []string{"Final autonomous review accepted", "Summary: decisions accepted"} {
		if !strings.Contains(s, want) {
			t.Errorf("complete_autonomous_review prompt missing %q: %q", want, s)
		}
	}
	if strings.Contains(s, "DESTRUCTIVE") || strings.Contains(s, "⚠") {
		t.Errorf("complete_autonomous_review must not be DESTRUCTIVE/⚠: %q", s)
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

func TestRenderConfirmPrompt_DispatchBashUsesShellRiskNotDestructiveLabel(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "dispatch",
		Args:       `{"intent":"Check branch state before pushing","tools":["git_info","git_status","Bash"]}`,
		Permission: "X",
	}))
	for _, want := range []string{"DELEGATED dispatch wants to run a delegated agent", "Risk: Bash grants shell access; approve only trusted tasks."} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %q in prompt, got: %q", want, s)
		}
	}
	if strings.Contains(s, "DESTRUCTIVE dispatch") {
		t.Fatalf("dispatch prompt should explain delegated shell risk instead of labeling the delegation itself destructive: %q", s)
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

func TestConfirmPromptHintsAdvertiseEnterSteer(t *testing.T) {
	m := minimalModel()
	out := m.confirmPromptHints(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch"})
	plain := stripAnsiCSI(out)
	if !strings.Contains(plain, "press [enter] to steer convo") {
		t.Fatalf("confirm hints should advertise enter steering, got %q", plain)
	}
}

func TestConfirmPending_SlashCommandRunsInsteadOfSteering(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "git_push", Args: `{}`, Permission: "X"})
	before := len(m.mainChat().Entries())

	for _, r := range "/help" {
		next, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = next.(Model)
	}
	if m.input.Value() != "/help" {
		t.Fatalf("typing a slash command should edit the prompt, got %q", m.input.Value())
	}

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "\n"})
	m = next.(Model)

	if m.input.Value() != "" {
		t.Fatalf("running a slash command should clear the prompt, got %q", m.input.Value())
	}
	for _, e := range m.mainChat().Entries() {
		if strings.Contains(e.Content, "steer: /help") {
			t.Fatalf("slash command should not be sent as steer text: %+v", e)
		}
	}
	if len(m.mainChat().Entries()) <= before {
		t.Fatal("/help should produce output instead of steering the tool call")
	}
}

func TestConfirmPending_TypingThenEnterSteersToolCall(t *testing.T) {
	m := New(nil, false)
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch", Args: `{}`, Permission: "X"})

	next, _ := m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	m = next.(Model)
	next, _ = m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	m = next.(Model)
	if m.input.Value() != "pi" {
		t.Fatalf("typing while confirm pending should edit prompt, got %q", m.input.Value())
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.pendingConfirm != nil {
		t.Fatal("steering with enter should clear pending confirm")
	}
	if m.input.Value() != "" {
		t.Fatalf("steering should clear prompt, got %q", m.input.Value())
	}
	found := false
	for _, e := range m.mainChat().Entries() {
		if strings.Contains(e.Content, "↳ steer: pi") {
			found = true
		}
	}
	if !found {
		t.Fatal("steering should append a visible steer entry")
	}
}

func TestConfirmPending_EmptyEnterStartsChatSteer(t *testing.T) {
	for _, msg := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: tea.KeyEnter, Text: "\n"}} {
		m := New(nil, false)
		m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch", Args: `{}`, Permission: "X"})

		next, _ := m.Update(msg)
		m = next.(Model)
		if m.pendingConfirm != nil {
			t.Fatalf("empty enter %#v should resolve into steer/chat mode", msg)
		}
		if m.composeToolUseID != "tool-1" {
			t.Fatalf("composeToolUseID = %q", m.composeToolUseID)
		}
	}
}

func TestConfirmPending_HotkeysOnlyWhenPromptEmpty(t *testing.T) {
	m := New(nil, false)
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch", Args: `{}`, Permission: "X"})

	next, _ := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = next.(Model)
	if m.pendingConfirm != nil {
		t.Fatal("n with an empty prompt should still deny")
	}

	m = New(nil, false)
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch", Args: `{}`, Permission: "X"})
	m.input.SetValue("a")
	next, _ = m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = next.(Model)
	if m.pendingConfirm == nil {
		t.Fatal("n with non-empty steering text should not deny")
	}
	if m.input.Value() != "an" {
		t.Fatalf("n should append to steering text, got %q", m.input.Value())
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
