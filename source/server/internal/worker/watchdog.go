package worker

import (
	"context"

	"cercano/source/server/internal/capabilities/builtins"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/watchdog"
	pkgcfg "cercano/source/server/pkg/config"
)

// buildWorkerWatchdog constructs the protocol-supervision watchdog from the
// snapshotted config, mirroring the host's buildWatchdogFrom. Returns nil when
// disabled (the default) — identical to in-process default-off behavior. The
// watchdog task dispatches through the worker engine using the snapshotted
// task assignment and destination configuration.
func buildWorkerWatchdog(cfg pkgcfg.Config, engine *dispatch.Engine) *watchdog.Watchdog {
	wc := cfg.Watchdog
	if !wc.Enabled {
		return nil
	}

	mode := watchdog.ModeChallenge
	if wc.Mode == "strict" {
		mode = watchdog.ModeStrict
	}

	// Checks match canonical capability names; the standalone registry emits
	// display aliases (Edit, Bash, …). Teach the watchdog the reverse map.
	watchdog.SetDisplayAliases(builtins.AgentAliases())

	// Map configured check names to Check implementations. Unknown names are
	// future checks — skip them silently rather than failing construction.
	// Mirror the host's name→check mapping EXACTLY (internal/server/watchdog_wire.go)
	// so an enabled watchdog runs the identical check set in worker mode. Missing a
	// case here silently drops that check — a supervision divergence. Unknown names
	// are future checks — skipped rather than failing construction.
	var checks []watchdog.Check
	for _, name := range wc.Checks {
		switch name {
		case "systematic-debugging", "debug-loop":
			checks = append(checks, watchdog.DebugLoopCheck())
		case "design-decisions":
			checks = append(checks, watchdog.DesignDecisionsCheck())
		case "verification-strategy":
			checks = append(checks, watchdog.VerificationStrategyCheck())
		case "compute-before-simulate":
			checks = append(checks, watchdog.ComputeBeforeSimulateCheck())
		case "commit-checkpoint":
			checks = append(checks, watchdog.CommitCheckpointCheck())
		case "plain-english":
			checks = append(checks, watchdog.PlainEnglishCheck())
		case "worktree-first":
			checks = append(checks, watchdog.WorktreeFirstCheck())
		case "follow-through":
			checks = append(checks, watchdog.FollowThroughCheck())
		}
	}

	// Watchdog is ordinary classified dispatch. Its live task assignment
	// selects destination and quality; legacy watchdog.model is not a pin.
	oneShot := func(ctx context.Context, prompt string) (string, error) {
		res, err := engine.Dispatch(ctx, dispatch.Spec{
			Mode:        dispatch.OneShot,
			RoutingTask: pkgcfg.TaskWatchdog,
			Prompt:      prompt,
			Source:      "watchdog",
		})
		if err != nil {
			return "", err
		}
		return res.Text, nil
	}

	// EscalateAfter 0 is normalized to 2 inside watchdog.New — don't re-default.
	return watchdog.New(watchdog.Config{Mode: mode, EscalateAfter: wc.EscalateAfter}, checks, oneShot)
}
