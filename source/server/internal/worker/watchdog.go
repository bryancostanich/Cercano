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
// OneShot fast-model lane dispatches through the WORKER's engine (engine),
// which resolves the worker's own local provider — the model call never leaves
// the worker.
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

	// OneShot is the fast-model handle the checks call, running on the
	// co-processor lane (dispatch.RoleCoproc) through the worker's engine.
	// Model resolution mirrors the host: explicit watchdog.model wins, else the
	// fast_light_text tier's OPEN side (this lane is local), else the lane
	// default.
	oneShotModel := workerWatchdogModel(wc, cfg.Models)
	oneShot := func(ctx context.Context, prompt string) (string, error) {
		res, err := engine.Dispatch(ctx, dispatch.Spec{
			Mode:          dispatch.OneShot,
			Role:          dispatch.RoleCoproc,
			Prompt:        prompt,
			ModelOverride: oneShotModel, // "" → RoleCoproc model resolution
			Source:        "watchdog",
		})
		if err != nil {
			return "", err
		}
		return res.Text, nil
	}

	// EscalateAfter 0 is normalized to 2 inside watchdog.New — don't re-default.
	return watchdog.New(watchdog.Config{Mode: mode, EscalateAfter: wc.EscalateAfter}, checks, oneShot)
}

// workerWatchdogModel mirrors the host's watchdogModelFor: explicit
// watchdog.model config wins; otherwise the fast_light_text tier's OPEN side
// (the watchdog's oneShot lane dispatches to the local co-processor, so a cloud
// model id must never leak into it). Empty means the lane keeps its own default
// resolution.
func workerWatchdogModel(wc pkgcfg.WatchdogConfig, mc pkgcfg.ModelsConfig) string {
	if wc.Model != "" {
		return wc.Model
	}
	if id, _, ok := mc.Resolve(pkgcfg.TierFastLightText, pkgcfg.ProviderOpen, true); ok {
		return id
	}
	// Mirror the host engine's model resolution (DispatchModelFor): an
	// unconfigured fast_light_text tier falls back to the everyday open model
	// before giving up. Without this, a sparse taxonomy leaves the watchdog
	// oneShot with an empty model → the model call errors → supervision silently
	// fails open, a divergence from in-process (which resolves the everyday model).
	if id, _, ok := mc.Resolve(pkgcfg.TierEveryday, pkgcfg.ProviderOpen, true); ok {
		return id
	}
	return ""
}
