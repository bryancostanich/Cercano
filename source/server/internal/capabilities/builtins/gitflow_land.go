package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/gitflow"
	"cercano/source/server/pkg/config"
)

type gitLandCap struct{}

// GitLand constructs the git_land capability. X-tier — orchestrates reconcile,
// test gate, adversarial review seam, and fast-forward finalize. Never pushes.
func GitLand() capabilities.Capability { return gitLandCap{} }

func (gitLandCap) Name() string            { return "git_land" }
func (gitLandCap) Tier() capabilities.Tier { return capabilities.TierX }
func (gitLandCap) Surfaces() capabilities.Surface {
	return capabilities.SurfaceAgent | capabilities.SurfaceMCP
}
func (gitLandCap) Description() string {
	return "Land a feature branch onto trunk: reconcile (rebase or merge), run the test gate, review any conflict resolution, then fast-forward trunk. Never pushes. Args: {feature?: string, trunk?: string, strategy?: \"rebase\"|\"merge\", continue?: bool, cwd?: string}."
}
func (gitLandCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"properties": {
			"feature":  {"type": "string", "description": "Feature branch to land (defaults to current branch)."},
			"trunk":    {"type": "string", "description": "Target branch (overrides .cercano/gitflow.yaml trunk)."},
			"strategy": {"type": "string", "enum": ["rebase", "merge"], "description": "Reconcile strategy; default rebase."},
			"continue": {"type": "boolean", "description": "Resume after resolving conflicts."},
			"cwd":      {"type": "string", "description": "Working directory; defaults to call.WorkDir."}
		}
	}`)
}

type gitLandArgs struct {
	Feature  string `json:"feature"`
	Trunk    string `json:"trunk"`
	Strategy string `json:"strategy"`
	Continue bool   `json:"continue"`
	Cwd      string `json:"cwd"`
}

func (gitLandCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	// 1. Parse args; open repo; resolve config; resolve feature; pick strategy.
	var a gitLandArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("git_land: parse args: %w", err)
	}

	dir := a.Cwd
	if dir == "" {
		dir = call.WorkDir
	}

	r, err := gitflow.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("git_land: %w", err)
	}

	cfg, err := gitflow.Resolve(ctx, r, gitflow.Config{Trunk: a.Trunk})
	if err != nil {
		return nil, fmt.Errorf("git_land: %w", err)
	}

	feature := a.Feature
	if feature == "" {
		feature, err = r.CurrentBranch(ctx)
		if err != nil {
			return nil, fmt.Errorf("git_land: resolve feature branch: %w", err)
		}
	}

	strategy := gitflow.StrategyRebase
	if strings.ToLower(a.Strategy) == "merge" {
		strategy = gitflow.StrategyMerge
	}

	trunk := cfg.Trunk

	// 2. Fresh land (not continue).
	if !a.Continue {
		div, divErr := r.Divergence(ctx, feature, trunk)
		if divErr != nil {
			return nil, fmt.Errorf("git_land: divergence: %w", divErr)
		}

		st, landErr := r.Land(ctx, feature, trunk, strategy)
		if landErr != nil {
			return nil, fmt.Errorf("git_land: %w", landErr)
		}

		// The reconcile may have run in the feature's linked worktree —
		// the test gate and finalize must run from there too.
		if st.Dir != "" && st.Dir != dir {
			if r2, oerr := gitflow.Open(st.Dir); oerr == nil {
				r = r2
			}
		}

		if len(st.Conflicts) > 0 {
			return capabilities.NewTextResult(buildLandConflictMessage(st.Conflicts, cfg, feature, div)), nil
		}

		// Clean reconcile — skip review gate entirely; go straight to test + finalize.
		return runTestAndFinalize(ctx, r, cfg, feature, trunk, "")
	}

	// 3. Continue path: capture the pre-resolution conflict set AND the pre-continue
	//    HEAD BEFORE LandContinue, so the review gate sees the resolved file set and
	//    the actual resolution diff (preHEAD..HEAD) rather than an empty post-commit diff.
	// The paused reconcile may live in the feature's linked worktree, not the
	// caller's cwd — retarget before reading its conflict state, or the
	// pre-continue snapshot (conflict set + HEAD) reads the wrong worktree.
	if wt, werr := r.WorktreeMidReconcile(ctx); werr == nil && wt != "" && wt != dir {
		if r2, oerr := gitflow.Open(wt); oerr == nil {
			r = r2
		}
	}
	preContinueConflicts, _ := r.ConflictedFiles(ctx)
	preHEAD, _ := r.RevParse(ctx, "HEAD")

	st, err := r.LandContinue(ctx, strategy)
	if err != nil {
		return nil, fmt.Errorf("git_land: continue: %w", err)
	}

	if len(st.Conflicts) > 0 {
		return capabilities.NewTextResult(buildStillConflictedMessage(st.Conflicts)), nil
	}

	// 4 + 5. Test gate, then review gate (continue path only), then finalize.
	return runTestAndReviewAndFinalize(ctx, call, r, cfg, feature, trunk, preContinueConflicts, preHEAD)
}

// runTestAndFinalize runs the test gate and finalizes. No review gate (clean path).
func runTestAndFinalize(
	ctx context.Context,
	r *gitflow.Repo,
	cfg gitflow.Config,
	feature, trunk string,
	verdictNote string,
) (*capabilities.Result, error) {
	noTestNote, stop := testGate(ctx, r, cfg)
	if stop != nil {
		return stop, nil
	}

	if err := r.Finalize(ctx, feature, trunk); err != nil {
		return nil, fmt.Errorf("git_land: %w", err)
	}
	return capabilities.NewTextResult(fmt.Sprintf(
		"landed %s → %s%s%s. ready to push: `git push origin %s` (not pushed).",
		feature, trunk, noTestNote, verdictNote, trunk,
	)), nil
}

// runTestAndReviewAndFinalize is the continue path: test gate, review gate, finalize.
func runTestAndReviewAndFinalize(
	ctx context.Context,
	call *capabilities.Call,
	r *gitflow.Repo,
	cfg gitflow.Config,
	feature, trunk string,
	preContinueConflicts []string,
	preHEAD string,
) (*capabilities.Result, error) {
	// 4. Test gate.
	noTestNote, stop := testGate(ctx, r, cfg)
	if stop != nil {
		return stop, nil
	}

	// 5. Review gate (only when there were conflicts to resolve).
	verdictNote := ""
	if len(preContinueConflicts) > 0 {
		sig := r.ResolutionSignalsFor(ctx, preContinueConflicts, cfg)
		// Feed the ACTUAL resolution (what the continue committed) to the review.
		if d, derr := r.DiffRange(ctx, preHEAD, "HEAD"); derr == nil {
			sig.Diff = d
		}

		// Deterministic floor: sensitive paths OR too many hand-edited files.
		if len(sig.SensitiveHits) > 0 || sig.HandEdited > cfg.ReviewFloor {
			details := fmt.Sprintf("hand-edited=%d, floor=%d, sensitive=%v",
				sig.HandEdited, cfg.ReviewFloor, sig.SensitiveHits)
			return capabilities.NewTextResult(fmt.Sprintf(
				"human review required before landing (sensitive paths or large hand-edited set): %s. Re-run after a human has reviewed.",
				details,
			)), nil
		}

		// Model review seam — only when Dispatch is wired.
		if call.Svc.Dispatch != nil {
			prompt := buildReviewPrompt(sig, feature, trunk)
			res, err := call.Svc.Dispatch(ctx, dispatch.Spec{
				Mode:    dispatch.OneShot,
				Role:    dispatch.RoleMain,
				Tier:    config.TierEveryday,
				Source:  "gitflow:land-review",
				Prompt:  prompt,
				WorkDir: call.WorkDir,
			})
			if err != nil {
				return nil, fmt.Errorf("git_land: review dispatch: %w", err)
			}
			v := parseVerdict(res.Text)
			if v.Risky {
				return capabilities.NewTextResult(fmt.Sprintf(
					"review flagged risk, not landing:\n%s", res.Text,
				)), nil
			}
			verdictNote = fmt.Sprintf(" [review: safe — %s]", v.Reasoning)
		}
	}

	// 6. Finalize.
	if err := r.Finalize(ctx, feature, trunk); err != nil {
		return nil, fmt.Errorf("git_land: %w", err)
	}
	return capabilities.NewTextResult(fmt.Sprintf(
		"landed %s → %s%s%s. ready to push: `git push origin %s` (not pushed).",
		feature, trunk, noTestNote, verdictNote, trunk,
	)), nil
}

// testGate runs cfg.TestCommand if set. Returns (noTestNote, earlyStop).
// earlyStop is non-nil when the test gate failed and Execute must return it.
func testGate(ctx context.Context, r *gitflow.Repo, cfg gitflow.Config) (string, *capabilities.Result) {
	if cfg.TestCommand == "" {
		return " (landed without a test gate — none configured)", nil
	}
	out, err := r.RunTests(ctx, cfg.TestCommand)
	if err != nil {
		return "", capabilities.NewTextResult(fmt.Sprintf("test gate failed, not landing:\n%s", out))
	}
	return "", nil
}

// buildLandConflictMessage formats the initial conflict result for a fresh Land.
func buildLandConflictMessage(conflicts []string, cfg gitflow.Config, feature string, div gitflow.Divergence) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Conflict during reconcile — divergence: %d ahead on %s, %d ahead on trunk.\n\n",
		div.FeatureAhead, feature, div.TrunkAhead)
	b.WriteString("Conflicted files:\n")
	for _, f := range conflicts {
		if landIsRegen(f, cfg.Regen) {
			cmd := landRegenCmd(f, cfg.Regen)
			fmt.Fprintf(&b, "  %s  [generated — regenerate with: %s]\n", f, cmd)
		} else {
			fmt.Fprintf(&b, "  %s\n", f)
		}
	}
	b.WriteString("\nResolve the conflicts on the feature branch, then call git_land again with continue=true.")
	return b.String()
}

// buildStillConflictedMessage is used when LandContinue finds still-unresolved files.
func buildStillConflictedMessage(conflicts []string) string {
	var b strings.Builder
	b.WriteString("Still-unresolved conflicts:\n")
	for _, f := range conflicts {
		fmt.Fprintf(&b, "  %s\n", f)
	}
	b.WriteString("\nResolve the remaining conflicts, then call git_land again with continue=true.")
	return b.String()
}

// landIsRegen returns true if f matches any Regen glob.
func landIsRegen(f string, regen map[string]string) bool {
	for g := range regen {
		if ok, _ := filepath.Match(g, f); ok {
			return true
		}
		if ok, _ := filepath.Match(g, filepath.Base(f)); ok {
			return true
		}
	}
	return false
}

// landRegenCmd returns the regen command for f (first matching glob wins).
func landRegenCmd(f string, regen map[string]string) string {
	for g, cmd := range regen {
		if ok, _ := filepath.Match(g, f); ok {
			return cmd
		}
		if ok, _ := filepath.Match(g, filepath.Base(f)); ok {
			return cmd
		}
	}
	return ""
}

// buildReviewPrompt produces the adversarial review prompt for the conflict resolution.
func buildReviewPrompt(sig gitflow.ResolutionSignals, feature, trunk string) string {
	var b strings.Builder
	b.WriteString("You are an adversarial reviewer for a git conflict resolution.\n")
	b.WriteString("Try to REFUTE that this resolution is safe to land. Look for: incorrect merge choices, semantic bugs introduced by the resolution, logic that was lost or duplicated, security-sensitive changes.\n\n")
	fmt.Fprintf(&b, "Branch landing: %s → %s\n", feature, trunk)
	fmt.Fprintf(&b, "Hand-edited files: %d\n", sig.HandEdited)
	if len(sig.SensitiveHits) > 0 {
		fmt.Fprintf(&b, "Sensitive files involved: %s\n", strings.Join(sig.SensitiveHits, ", "))
	}
	if sig.Diff != "" {
		b.WriteString("\nResolution diff (may be truncated):\n```\n")
		b.WriteString(sig.Diff)
		b.WriteString("\n```\n")
	}
	b.WriteString("\nRespond:\nVERDICT: SAFE|RISKY\nREASONING: <one or two sentences>\n")
	return b.String()
}
