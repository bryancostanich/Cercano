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
	raw := m.renderConfirmPrompt(&pendingToolCall{
		Name:       "request_plan_approval",
		Args:       `{"summary":"Fix handle-drag: 3 phases","effort":"viz-drag-audit"}`,
		Permission: "X",
	})
	s := stripAnsiCSI(raw)
	if !strings.Contains(s, "start executing") {
		t.Errorf("request_plan_approval prompt should ask to execute, got: %q", s)
	}
	if strings.Contains(s, "DESTRUCTIVE") || strings.Contains(s, "⚠") {
		t.Errorf("request_plan_approval must not be DESTRUCTIVE/⚠: %q", s)
	}
	for _, want := range []string{"Plan\n    Fix handle-drag: 3 phases", "Effort\n    viz-drag-audit"} {
		if !strings.Contains(s, want) {
			t.Errorf("prompt should render plan approval detail block %q, got: %q", want, s)
		}
	}
	if !strings.Contains(raw, "\x1b[1;38;2;") || !strings.Contains(raw, "mPlan\x1b[m") || !strings.Contains(raw, "mEffort\x1b[m") {
		t.Errorf("plan approval labels should be bold, got: %q", raw)
	}
}

func TestRenderConfirmPrompt_RequestPlanApproval_WrapsDetailsWithHangingIndent(t *testing.T) {
	m := minimalModel()
	m.width = 28
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "request_plan_approval",
		Args:       `{"summary":"alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau","effort":"floating-origin","spec_path":"efforts/floating-origin/with/a/deep/path/to/spec.md","plan_path":"efforts/floating-origin/with/a/deep/path/to/plan.md"}`,
		Permission: "X",
	}))
	for _, want := range []string{
		"Plan\n    alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron",
		"    pi rho sigma tau",
		"Spec\n    efforts/floating-origin/with/a/deep/path/to/spec.md",
		"Plan file\n    efforts/floating-origin/with/a/deep/path/to/plan.md",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("plan approval details should wrap with block indentation; missing %q in %q", want, s)
		}
	}
	if strings.Contains(s, "\nepsilon zeta") || strings.Contains(s, "\nfloating-origin/spec.md") {
		t.Fatalf("wrapped plan approval details should not restart at column 0, got: %q", s)
	}
}

func TestRenderConfirmPrompt_SuggestAutonomous_AsksToStartWithBrief(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "suggest_autonomous",
		Args:       `{"reason":"plan accepted","goal":"implement autonomous mode","done_when":["profile active","tests pass"],"constraints":["do not push"],"review_points":["storage choices"]}`,
		Permission: "X",
	}))
	for _, want := range []string{"Start autonomous mode with this run brief?", "Why\n    plan accepted", "Goal\n    implement autonomous mode", "Done when\n    • profile active\n    • tests pass", "Constraints\n    • do not push", "Review points\n    • storage choices"} {
		if !strings.Contains(s, want) {
			t.Errorf("suggest_autonomous prompt missing %q: %q", want, s)
		}
	}
	if strings.Contains(s, "Why:") || strings.Contains(s, "Goal:") || strings.Contains(s, "Done when:") {
		t.Errorf("suggest_autonomous brief should render block headings instead of inline labels: %q", s)
	}
	if strings.Contains(s, "DESTRUCTIVE") || strings.Contains(s, "⚠") {
		t.Errorf("suggest_autonomous is X-tier but destroys nothing; must not be DESTRUCTIVE/⚠: %q", s)
	}
}

func TestRenderConfirmPrompt_SuggestAutonomous_WrapsBriefBody(t *testing.T) {
	m := minimalModel()
	m.width = 46
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "suggest_autonomous",
		Args:       `{"reason":"multi step renderer migration with bounded scope","goal":"complete the globe renderer integration so mission startup uses globe metadata and rendering resources consistently","done_when":["mission startup loads globe metadata and initializes renderer resources correctly"]}`,
		Permission: "X",
	}))
	for _, want := range []string{"Goal\n    complete the globe renderer integration", "    so mission startup uses globe metadata", "    and rendering resources consistently", "Done when\n    • mission startup loads globe metadata", "      and initializes renderer resources"} {
		if !strings.Contains(s, want) {
			t.Errorf("wrapped suggest_autonomous prompt missing %q: %q", want, s)
		}
	}
}

func TestRenderConfirmPrompt_RequestAutonomousExecution_AsksCombinedPlanAndBriefQuestion(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "request_autonomous_execution",
		Args:       `{"summary":"three phases","effort":"efforts/demo","spec_path":"efforts/demo/spec.md","plan_path":"efforts/demo/plan.md","reason":"bounded approved plan","goal":"ship demo","done_when":["tests pass"],"constraints":["do not push"],"review_points":["protocol wording"]}`,
		Permission: "X",
	}))
	for _, want := range []string{"Plan approved. Execute it autonomously with this run brief?", "Plan\n    three phases", "Effort\n    efforts/demo", "Spec\n    efforts/demo/spec.md", "Plan file\n    efforts/demo/plan.md", "Why\n    bounded approved plan", "Goal\n    ship demo", "Done when\n    • tests pass", "Constraints\n    • do not push", "Review points\n    • protocol wording"} {
		if !strings.Contains(s, want) {
			t.Errorf("request_autonomous_execution prompt missing %q: %q", want, s)
		}
	}
	if strings.Contains(s, "DESTRUCTIVE") || strings.Contains(s, "⚠") {
		t.Errorf("request_autonomous_execution must not be DESTRUCTIVE/⚠: %q", s)
	}
	if strings.Contains(s, `{"summary"`) || strings.Contains(s, "suggest_autonomous") {
		t.Errorf("request_autonomous_execution prompt should not dump raw args or mention second gate: %q", s)
	}
}

func TestRenderConfirmPrompt_RequestAutonomousExit_ShowsCompletionDetailsAsBlocks(t *testing.T) {
	m := minimalModel()
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "request_autonomous_exit",
		Args:       `{"summary":"done","verification":"targeted tests passed"}`,
		Permission: "X",
	}))
	for _, want := range []string{"Autonomous run complete — review completion details?", "Summary\n    done", "Verification\n    targeted tests passed"} {
		if !strings.Contains(s, want) {
			t.Errorf("request_autonomous_exit prompt missing %q: %q", want, s)
		}
	}
	if strings.Contains(s, "begin final decision review") || strings.Contains(s, "Summary: done") || strings.Contains(s, "Verification: targeted tests passed") {
		t.Errorf("request_autonomous_exit should render prose as block sections, not inline metadata rows: %q", s)
	}
	if strings.Contains(s, "DESTRUCTIVE") || strings.Contains(s, "⚠") {
		t.Errorf("request_autonomous_exit must not be DESTRUCTIVE/⚠: %q", s)
	}
}

func TestRenderConfirmPrompt_RequestAutonomousExit_WrapsLongPayloadsWithoutEllipsis(t *testing.T) {
	m := minimalModel()
	m.width = 58
	s := stripAnsiCSI(m.renderConfirmPrompt(&pendingToolCall{
		Name:       "request_autonomous_exit",
		Args:       `{"summary":"Implemented dev-mode-gated task pane debug controls with a shared controller and slash commands","verification":"Ran cd source/clients/cli && go test ./internal/ui -run TestDebugTaskPane -count=1"}`,
		Permission: "X",
	}))
	for _, want := range []string{
		"Summary\n    Implemented dev-mode-gated task pane debug controls",
		"    with a shared controller and slash commands",
		"Verification\n    Ran cd source/clients/cli && go test ./internal/ui -",
		"    run TestDebugTaskPane -count=1",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("wrapped request_autonomous_exit prompt missing %q: %q", want, s)
		}
	}
	if strings.Contains(s, "…") {
		t.Errorf("request_autonomous_exit summary and verification should wrap instead of truncate: %q", s)
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

func TestConfirmPromptHintsDoNotAdvertisePromptSteer(t *testing.T) {
	m := minimalModel()
	out := m.confirmPromptHints(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch"})
	plain := stripAnsiCSI(out)
	if strings.Contains(plain, "type below") || strings.Contains(plain, "press [enter] to steer convo") {
		t.Fatalf("confirm hints should not advertise prompt typing while gate is up, got %q", plain)
	}
	if !strings.Contains(plain, "[c]hat") {
		t.Fatalf("confirm hints should keep explicit c hotkey, got %q", plain)
	}
}

func TestConfirmPromptHintsOmitEnterSteerForSessionControl(t *testing.T) {
	m := minimalModel()
	out := m.confirmPromptHints(&pendingToolCall{ToolUseID: "tool-1", Name: "request_autonomous_execution"})
	plain := stripAnsiCSI(out)
	if strings.Contains(plain, "[c]hat") || strings.Contains(plain, "press [enter] to steer convo") {
		t.Fatalf("session-control hints should not advertise chat/steer, got %q", plain)
	}
	for _, want := range []string{"[y]es", "[n]o", "[d]etails"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("session-control hints should keep explicit %s action, got %q", want, plain)
		}
	}
}

func TestToolConfirm_SessionControlDoesNotExposeChatExtra(t *testing.T) {
	c := toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "request_autonomous_execution", Args: `{}`, Permission: "X"})
	if _, ok := c.extras["c"]; ok {
		t.Fatal("session-control prompts must not expose lowercase chat extra")
	}
	if _, ok := c.extras["C"]; ok {
		t.Fatal("session-control prompts must not expose uppercase chat extra")
	}
	if _, ok := c.extras["d"]; !ok {
		t.Fatal("session-control prompts should still expose details")
	}
}

func TestConfirmPending_DisablesPromptTyping(t *testing.T) {
	m := New(nil, false)
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "git_push", Args: `{}`, Permission: "X"})
	before := len(m.mainChat().Entries())

	for _, r := range "/help" {
		next, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = next.(Model)
	}
	if m.input.Value() != "" {
		t.Fatalf("typing while confirm pending must not edit prompt, got %q", m.input.Value())
	}

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Text: "\n"})
	m = next.(Model)
	if m.pendingConfirm == nil {
		t.Fatal("enter while confirm pending should not resolve the gate")
	}
	if len(m.mainChat().Entries()) != before {
		t.Fatal("typing/enter while confirm pending should not run slash commands or steer")
	}
}

func TestConfirmPending_PasteDoesNotEditPrompt(t *testing.T) {
	m := New(nil, false)
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch", Args: `{}`, Permission: "X"})

	next, _ := m.Update(tea.PasteMsg{Content: "hello"})
	m = next.(Model)
	if m.input.Value() != "" {
		t.Fatalf("paste while confirm pending must not edit prompt, got %q", m.input.Value())
	}
	if m.pendingConfirm == nil {
		t.Fatal("paste while confirm pending should keep gate pending")
	}
}

func TestConfirmPending_CHotkeyStartsChatSteer(t *testing.T) {
	m := New(nil, false)
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch", Args: `{}`, Permission: "X"})

	next, _ := m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	m = next.(Model)
	if m.pendingConfirm != nil {
		t.Fatal("c hotkey should resolve into steer/chat mode")
	}
	if m.composeToolUseID != "tool-1" {
		t.Fatalf("composeToolUseID = %q", m.composeToolUseID)
	}
}

func TestConfirmPending_EmptyEnterDoesNotStartChatSteerForSessionControl(t *testing.T) {
	m := New(nil, false)
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "request_autonomous_execution", Args: `{}`, Permission: "X"})

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if m.pendingConfirm == nil {
		t.Fatal("empty enter on session-control prompt should keep confirm pending")
	}
	if m.composeToolUseID != "" {
		t.Fatalf("session-control prompt should not enter compose mode, composeToolUseID=%q", m.composeToolUseID)
	}
}

func TestConfirmPending_HotkeysResolveEvenWhenPromptHasDraft(t *testing.T) {
	m := New(nil, false)
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch", Args: `{}`, Permission: "X"})

	next, _ := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = next.(Model)
	if m.pendingConfirm != nil {
		t.Fatal("n with an empty prompt should deny")
	}

	m = New(nil, false)
	m.pendingConfirm = toolConfirm(&pendingToolCall{ToolUseID: "tool-1", Name: "dispatch", Args: `{}`, Permission: "X"})
	m.input.SetValue("draft")
	next, _ = m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = next.(Model)
	if m.pendingConfirm != nil {
		t.Fatal("n should deny even if a draft was already in the disabled prompt")
	}
	if m.input.Value() != "draft" {
		t.Fatalf("disabled prompt draft should be preserved, got %q", m.input.Value())
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
