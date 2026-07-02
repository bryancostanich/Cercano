package server

import (
	"context"

	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/watchdog"
	"cercano/source/server/pkg/config"
)

// buildWatchdog constructs the protocol-supervision watchdog from the current
// config. Returns nil when disabled (the default), in which case the server
// behaves exactly as if the watchdog did not exist. dispatchEngine must already
// be set — the fast-model OneShot lane routes through it.
func (s *Server) buildWatchdog() *watchdog.Watchdog {
	s.cfgMu.RLock()
	wc := s.currentConfig.Watchdog
	s.cfgMu.RUnlock()
	return s.buildWatchdogFrom(wc)
}

// buildWatchdogFrom constructs the watchdog from an already-read config. It
// takes NO lock, so a caller already holding s.cfgMu (e.g. UpdateConfig) can
// rebuild the watchdog without deadlocking.
func (s *Server) buildWatchdogFrom(wc config.WatchdogConfig) *watchdog.Watchdog {
	if !wc.Enabled {
		return nil
	}

	mode := watchdog.ModeChallenge
	if wc.Mode == "strict" {
		mode = watchdog.ModeStrict
	}

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
		}
	}

	// OneShot is the fast-model handle the checks call. For now the "router fast
	// class" is the co-processor role's model lane (dispatch.RoleCoproc); wc.Model
	// overrides it. When the matrix-router lands this becomes a smarter resolution
	// with no call-site change.
	oneShot := func(ctx context.Context, prompt string) (string, error) {
		res, err := s.dispatchEngine.Dispatch(ctx, dispatch.Spec{
			Mode:          dispatch.OneShot,
			Role:          dispatch.RoleCoproc,
			Prompt:        prompt,
			ModelOverride: wc.Model, // "" → RoleCoproc model resolution (the lightweight lane)
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
