package protocols

import (
	"strings"
	"testing"
)

func TestCoreCatalogComplete(t *testing.T) {
	want := []string{"autonomous-run", "compute-before-simulate", "delegate-git-plumbing", "design-decisions", "executing-plans", "planning-mode", "systematic-debugging", "verification-strategy", "worktree-first"}
	for _, name := range want {
		p, ok := Get(name)
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if p.Domain != DomainCore {
			t.Fatalf("%s should be core", name)
		}
		if len(strings.TrimSpace(p.Body)) < 200 {
			t.Fatalf("%s body looks too short to be the real protocol", name)
		}
		if !strings.HasSuffix(p.Trigger, ".") {
			t.Fatalf("%s trigger should be a full sentence", name)
		}
	}
}

func TestWorktreeFirstNamesTheGitWorktreeTool(t *testing.T) {
	p, ok := Get("worktree-first")
	if !ok {
		t.Fatal("worktree-first missing")
	}
	// The whole point of the protocol is to steer agents to the
	// git_worktree tool. If future edits accidentally rewrite it to
	// generic "use a worktree" language, agents won't know which tool
	// to reach for.
	if !strings.Contains(p.Body, "git_worktree") {
		t.Fatal("worktree-first body must name the git_worktree tool explicitly")
	}
	if !strings.Contains(p.Trigger, "git_worktree") {
		t.Fatal("worktree-first trigger must name the git_worktree tool so the steering block surfaces it")
	}
}

func TestWorktreeFirstRecommendsSiblingDirectoryConvention(t *testing.T) {
	p, _ := Get("worktree-first")
	// The convention on this box (and per the git_worktree design docs)
	// is a sibling directory to the repo root — never a subdirectory of
	// the tracked tree, which triggers git's submodule-pointer handling.
	// This test locks in that the body documents "sibling" and shows the
	// `../<repo>-<slug>` path shape as an example.
	if !strings.Contains(strings.ToLower(p.Body), "sibling") {
		t.Fatal("worktree-first body must call the sibling-directory convention out by name")
	}
	if !strings.Contains(p.Body, "../") {
		t.Fatal("worktree-first body must show the ../<repo>-<slug> sibling path shape so agents know what to pass")
	}
	// Regression guard: earlier versions of the protocol recommended
	// `.claude/worktrees/<slug>` inside the tracked tree, which caused
	// pointer-drift and rebase conflicts. Never again.
	if strings.Contains(p.Body, ".claude/worktrees/<") {
		t.Fatal("worktree-first body must not recommend the nested .claude/worktrees/<slug> path — sibling only")
	}
}

func TestWorktreeFirstDocumentsFastPathForTrivialChanges(t *testing.T) {
	p, _ := Get("worktree-first")
	// The full worktree ceremony is disproportionate for typo/label
	// fixes. The Fast Path section documents when it applies and how to
	// checkpoint only the intended files.
	if !strings.Contains(p.Body, "Fast Path") {
		t.Fatal("worktree-first body must document a Fast Path section for trivial changes")
	}
	if !strings.Contains(p.Body, "explicit `paths`") || !strings.Contains(p.Body, "allow_trunk") {
		t.Fatal("Fast Path must explain explicit paths and allow_trunk for safe current-branch checkpoints")
	}
}

func TestDesignDecisionsHasMergedSteps(t *testing.T) {
	p, _ := Get("design-decisions")
	if !strings.Contains(p.Body, "Symmetric quantification") && !strings.Contains(p.Body, "symmetric quantification") {
		t.Fatal("design-decisions missing the symmetric quantification rule")
	}
	if !strings.Contains(strings.ToLower(p.Body), "argue against your own recommendation") {
		t.Fatal("design-decisions missing the argue-against-yourself step")
	}
}

func TestDesignDecisionsRequiresMarkdownRollupWithTitlesInHeader(t *testing.T) {
	p, _ := Get("design-decisions")
	for _, want := range []string{
		"normal Markdown pipe table",
		"decision axes as rows",
		"actual option titles in the top row",
		"Do not hide the titles in a separate legend",
		"do not hand-draw ASCII/grid tables",
		"Markdown tables exist so the renderer can wrap cells",
		"| Axis | Disable by default | Disable one check | Tune checks now |",
		"| Cost | Low: config/default tests | Low: one check list | Medium: prompt + gate work |",
		"option titles belong in the header row",
		"cells may be short phrases and may wrap",
		"do not destroy clarity just to avoid wrapping",
	} {
		if !strings.Contains(p.Body, want) {
			t.Fatalf("design-decisions missing Markdown rollup table requirement %q", want)
		}
	}
	for _, forbidden := range []string{
		"terminal-safe Markdown **grid table**",
		"short option labels as columns",
		"+===============+================",
		"keep each cell under ~4 words",
	} {
		if strings.Contains(p.Body, forbidden) {
			t.Fatalf("design-decisions still contains rejected grid-table guidance %q", forbidden)
		}
	}
}

func TestDelegateGitPlumbingNamesDispatchTool(t *testing.T) {
	p, ok := Get("delegate-git-plumbing")
	if !ok {
		t.Fatal("delegate-git-plumbing missing")
	}
	// The point of the protocol is to steer git mechanics onto the dispatch
	// sub-agent. If a future edit rewrites it to generic "delegate it"
	// language, the model won't know which tool to reach for — so both the
	// steering trigger and the body must name the dispatch tool explicitly.
	if !strings.Contains(p.Trigger, "dispatch") {
		t.Fatal("delegate-git-plumbing trigger must name the dispatch tool so the steering block surfaces it")
	}
	if !strings.Contains(p.Body, "dispatch") {
		t.Fatal("delegate-git-plumbing body must name the dispatch tool explicitly")
	}
	// The core hazard the body must call out: a sub-agent's git silently
	// landing in the shared main checkout unless scoped with git -C.
	if !strings.Contains(p.Body, "git -C") {
		t.Fatal("delegate-git-plumbing body must show the git -C <worktree> scoping guardrail")
	}
}

func TestPlanningModeSpecifiesEffortStructure(t *testing.T) {
	p, ok := Get("planning-mode")
	if !ok {
		t.Fatal("planning-mode missing")
	}
	// The protocol must name the effort structure and both artifacts, or the
	// model won't know where to write or what the two files are for.
	for _, must := range []string{"efforts/", "spec.md", "plan.md"} {
		if !strings.Contains(p.Body, must) {
			t.Fatalf("planning-mode body must mention %q", must)
		}
	}
	// The spec-before-plan ordering and the sign-off gate are load-bearing.
	low := strings.ToLower(p.Body)
	if !strings.Contains(low, "sign-off") && !strings.Contains(low, "sign off") {
		t.Fatal("planning-mode body must require sign-off on the spec before the plan")
	}
}

func TestPlanningModeDefersToDesignDecisions(t *testing.T) {
	p, _ := Get("planning-mode")
	// Decisions during generation must route through the design-decisions
	// protocol and land in the spec's ## Decisions section (design §3.5). If a
	// future edit drops this, generation loses its decision discipline.
	if !strings.Contains(p.Body, "design-decisions") {
		t.Fatal("planning-mode body must reference the design-decisions protocol by name")
	}
	if !strings.Contains(p.Body, "## Decisions") {
		t.Fatal("planning-mode body must require recording decisions in the spec's ## Decisions section")
	}
}

func TestPlanningModeSurfacesDecisionsBeforeSpecWrite(t *testing.T) {
	p, _ := Get("planning-mode")
	for _, want := range []string{
		"Decision checkpoint before writing the spec",
		"present that decision's",
		"wait for the human's approval or selection",
		"bury decisions in a finished spec as the",
		"first time the human sees them",
		"If there are no real forks",
		"say that",
		"explicitly before writing the spec",
		"only after",
		"the decision checkpoint is complete",
		"not a substitute for surfacing forks first",
	} {
		if !strings.Contains(p.Body, want) {
			t.Fatalf("planning-mode body must force conversational decision surfacing before spec write; missing %q", want)
		}
	}
}

func TestPlanningModeDecisionCheckpointIsSequential(t *testing.T) {
	p, _ := Get("planning-mode")
	for _, want := range []string{
		"concise bulleted decision queue",
		"one bullet per decision",
		"no option matrix yet",
		"work through the queue one decision at a time",
		"only then move to the next decision",
		"Do **not** present all decision",
		"matrices at once",
		"Start with a concise bulleted list of all decisions",
		"one per",
		"exchange",
		"Do not dump every",
		"decision matrix in one response",
		"unless the human explicitly asks for the full",
		"batch",
		"Only after the queue is resolved",
	} {
		if !strings.Contains(p.Body, want) {
			t.Fatalf("planning-mode body must require sequential decision review; missing %q", want)
		}
	}
}

func TestPlanningModeDocumentsPlanGlyphs(t *testing.T) {
	p, _ := Get("planning-mode")
	// The plan.md format must match what the taskmodel codec parses: the four
	// checkbox glyphs and 2-space nesting. If the protocol drifts from the
	// codec, authored plans won't round-trip.
	for _, glyph := range []string{"- [ ]", "- [~]", "- [x]", "- [-]"} {
		if !strings.Contains(p.Body, glyph) {
			t.Fatalf("planning-mode body must document the %q status glyph (matches the codec)", glyph)
		}
	}
	if !strings.Contains(strings.ToLower(p.Body), "2-space") {
		t.Fatal("planning-mode body must specify 2-space nesting to match the codec")
	}
}

func TestPlanningModeTriggerNamesSuggestPlan(t *testing.T) {
	p, _ := Get("planning-mode")
	// The always-on trigger should point the model at suggest_plan (how it
	// proposes planning) and the protocol (how it executes planning).
	if !strings.Contains(p.Trigger, "suggest_plan") {
		t.Fatal("planning-mode trigger must name the suggest_plan capability")
	}
}

// TestPlanningModeForbidsHandAuthoringOutsidePlanningMode locks the fix for the
// observed failure where the model recognized planning work ("this is planning
// work, so I'll write a spec + plan") and then hand-authored spec.md/plan.md
// with file tools, never calling suggest_plan — skipping the approval gate and
// the read-only fence. Both the terse trigger and the full body must make it
// imperative that concluding a plan is needed means calling suggest_plan first,
// not writing the artifacts directly.
func TestPlanningModeForbidsHandAuthoringOutsidePlanningMode(t *testing.T) {
	p, _ := Get("planning-mode")
	// The always-on trigger must be imperative, not a soft "propose".
	if !strings.Contains(p.Trigger, "MUST call `suggest_plan`") {
		t.Fatal("planning-mode trigger must imperatively require calling suggest_plan first")
	}
	// The body must forbid authoring the artifacts outside planning mode.
	for _, must := range []string{
		"Never create or edit",
		"outside planning mode",
	} {
		if !strings.Contains(p.Body, must) {
			t.Fatalf("planning-mode body must forbid hand-authoring plan artifacts; missing %q", must)
		}
	}
}

func TestPlanningModeNamesRequestPlanApprovalForHandoff(t *testing.T) {
	p, _ := Get("planning-mode")
	if !strings.Contains(p.Body, "request_plan_approval") {
		t.Fatal("planning-mode body must name request_plan_approval for the plan->execution handoff")
	}
	if !strings.Contains(p.Body, "leaves the read-only planning profile") && !strings.Contains(p.Body, "drops the read-only fence") {
		t.Fatal("planning-mode body must explain that approval leaves the read-only planning profile")
	}
	for _, want := range []string{"request_autonomous_execution", "Execute\nit autonomously with this run brief?", "lightweight autonomous run brief", "single approval"} {
		if !strings.Contains(p.Body, want) {
			t.Fatalf("planning-mode body must document optional autonomous bridge; missing %q", want)
		}
	}
	for _, old := range []string{"separate y/n/d/c run-brief approval", "second approval boundary"} {
		if strings.Contains(p.Body, old) {
			t.Fatalf("planning-mode body must not preserve duplicate autonomous gate wording %q", old)
		}
	}
}

func TestExecutingPlansNamesSemanticStatusTool(t *testing.T) {
	p, ok := Get("executing-plans")
	if !ok {
		t.Fatal("executing-plans missing")
	}
	if !strings.Contains(p.Body, "plan_set_status") {
		t.Fatal("executing-plans must require semantic status updates via plan_set_status")
	}
	for _, selector := range []string{"phase_title", "task_title", "task_path"} {
		if !strings.Contains(p.Body, selector) {
			t.Fatalf("executing-plans must instruct human-readable selector %q", selector)
		}
	}
	if !strings.Contains(p.Body, "Do not add machine IDs") {
		t.Fatal("executing-plans must explicitly forbid adding machine IDs to plan.md")
	}
	for _, status := range []string{"in_progress", "done", "blocked"} {
		if !strings.Contains(p.Body, status) {
			t.Fatalf("executing-plans must mention status %q", status)
		}
	}
}

func TestExecutingPlansDocumentsSurpriseTiers(t *testing.T) {
	p, _ := Get("executing-plans")
	low := strings.ToLower(p.Body)
	for _, tier := range []string{"local surprise", "structural surprise", "foundational surprise"} {
		if !strings.Contains(low, tier) {
			t.Fatalf("executing-plans must document %s", tier)
		}
	}
	for _, mode := range []string{"Bypass", "Permissive", "Strict"} {
		if !strings.Contains(p.Body, mode) {
			t.Fatalf("executing-plans must tie surprise threshold to permission mode %s", mode)
		}
	}
}

func TestExecutingPlansStructuralHandoffUsesApprovalGate(t *testing.T) {
	p, _ := Get("executing-plans")
	if !strings.Contains(p.Body, "request_plan_approval") {
		t.Fatal("structural replanning must come back through request_plan_approval")
	}
	if !strings.Contains(p.Body, "spec.md") || !strings.Contains(p.Body, "plan.md") {
		t.Fatal("executing-plans must name spec.md and plan.md as handoff artifacts")
	}
}

func TestExecutingPlansDocumentsOptionalAutonomousBridge(t *testing.T) {
	p, _ := Get("executing-plans")
	for _, want := range []string{"Optional Autonomous Bridge", "request_autonomous_execution", "Execute it autonomously with this run brief?", "goal", "done_when", "constraints", "review_points", "single execution-style follow-up"} {
		if !strings.Contains(p.Body, want) {
			t.Fatalf("executing-plans must document autonomous bridge; missing %q", want)
		}
	}
	for _, old := range []string{"separate run-brief approval", "second approval boundary", "call `suggest_autonomous` with"} {
		if strings.Contains(p.Body, old) {
			t.Fatalf("executing-plans must not preserve duplicate autonomous gate wording %q", old)
		}
	}
}

func TestAutonomousRunProtocolLocksDecisionDisciplineAndReview(t *testing.T) {
	p, ok := Get("autonomous-run")
	if !ok {
		t.Fatal("autonomous-run missing")
	}
	for _, want := range []string{
		"approved lightweight run brief",
		"capture_decision",
		"design-decision protocol",
		"explicitly flag hacky options",
		"High Bar For Stopping Mid-Run",
		"effectively irreversible",
		"invalidate most downstream work",
		"request_autonomous_exit",
		"complete_autonomous_review",
		"walking through the captured",
		"Do not push or merge",
	} {
		if !strings.Contains(p.Body, want) {
			t.Fatalf("autonomous-run protocol missing %q", want)
		}
	}
}

func TestAutonomousRunProtocolDoesNotAskForEveryDecision(t *testing.T) {
	p, _ := Get("autonomous-run")
	for _, want := range []string{
		"Autonomy means normal in-scope decisions continue without human approval",
		"Log and",
		"Do not stop just because there are two reasonable implementation paths",
		"checkpoint boundary is not a pause boundary",
		"do not end your turn with a progress report",
	} {
		if !strings.Contains(p.Body, want) {
			t.Fatalf("autonomous-run protocol must keep the stop threshold high; missing %q", want)
		}
	}
}
