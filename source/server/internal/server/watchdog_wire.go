package server

import (
	"context"

	"cercano/source/server/internal/capabilities/builtins"
	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/watchdog"
	"cercano/source/server/pkg/config"
)

// buildWatchdog constructs the protocol-supervision watchdog from the current
// config. Returns nil when disabled (the default), in which case the server
// behaves exactly as if the watchdog did not exist. dispatchEngine must already
// be set — the fast-model OneShot lane routes through it.
func (s *Server) buildWatchdog() *watchdog.Watchdog {
	cfg := s.cfgSvc.Get()
	return s.buildWatchdogFrom(cfg.Watchdog, cfg.Models)
}

// buildWatchdogFrom constructs the watchdog from already-read config sections.
// Lock-free: callers that already hold a config snapshot (UpdateConfig) call
// this directly with the values they have.
func (s *Server) buildWatchdogFrom(wc config.WatchdogConfig, mc config.ModelsConfig) *watchdog.Watchdog {
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
	// future checks — skip them silently rather than failing construction. An
	// empty resulting slice is fine: the watchdog then always allows.
	var checks []watchdog.Check
	for _, name := range wc.Checks {
		switch name {
		case "debug-loop":
			checks = append(checks, watchdog.DebugLoopCheck())
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
	// co-processor lane (dispatch.RoleCoproc). Model resolution: explicit
	// watchdog.model config wins, else the models taxonomy's fast_light_text
	// tier (open side — this lane is local), else the lane's own default.
	// Resolved at build time; a models-section change takes effect on the
	// next watchdog rebuild (config reload / watchdog-field update).
	oneShotModel := watchdogModelFor(wc, mc)
	oneShot := func(ctx context.Context, prompt string) (string, error) {
		res, err := s.dispatchEngine.Dispatch(ctx, dispatch.Spec{
			Mode:          dispatch.OneShot,
			Role:          dispatch.RoleCoproc,
			Prompt:        prompt,
			ModelOverride: oneShotModel, // "" → RoleCoproc model resolution (the lightweight lane)
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

// InitWatchdog builds the watchdog from config once at startup. Call after the
// dispatch engine is set (the OneShot lane depends on it).
func (s *Server) InitWatchdog() { s.watchdog = s.buildWatchdog() }
