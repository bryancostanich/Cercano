package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/config"
)

// The Server implements localruntime.Observer so it can close the loop the
// dashboard chip previously dead-ended on: when the active runtime's default
// model finishes downloading, re-broadcast the (now-cleared) not-ready chip and
// warm the sidecar so the runtime is ready the moment the download lands.
//
// Both callbacks run synchronously on the goroutine that made the transition
// (the download worker, or a provider health poll). They must not block, so the
// heavier work — an inventory lookup and a runtime Start — is dispatched to a
// short-lived goroutine with its own timeout.

// OnDownloadStateChange reacts to model-acquisition transitions. The only
// transition we act on is → Downloaded for the ACTIVE runtime's default model:
// clear the not-ready chip and auto-start the sidecar. Every other transition
// is ignored here (the CLI already renders progress from the streamed model
// rows).
func (s *Server) OnDownloadStateChange(ev localruntime.DownloadEvent) {
	if ev.Next != localruntime.Downloaded {
		return
	}
	cfg := s.cfgSvc.Get()
	runtime := strings.TrimSpace(cfg.OpenRuntime)
	// Only the active runtime's own model matters — a background fetch for an
	// inactive runtime must not warm anything or touch the chip (D3: single
	// active runtime).
	if ev.Model.Runtime != runtime {
		return
	}
	if !isActiveRuntimeDefault(cfg, ev.Model) {
		return
	}
	go s.onActiveDefaultDownloaded(runtime, ev.Model)
}

// OnInstanceStateChange is present so the Server satisfies the Observer
// contract and to give a single, greppable home for future instance-driven
// reactions (e.g. surfacing a crash chip). It is intentionally a no-op today:
// the existing health-poll broadcast path already carries instance state to
// clients, and re-broadcasting here would duplicate it.
func (s *Server) OnInstanceStateChange(localruntime.InstanceEvent) {}

// isActiveRuntimeDefault reports whether model is the configured default for
// the currently-active runtime. Matching uses the same fuzzy MatchesModel the
// resolve helpers use, so a config value like a bare id or repo still lines up
// with the canonical record.
func isActiveRuntimeDefault(cfg config.Config, model localruntime.ModelRecord) bool {
	var want string
	switch strings.TrimSpace(cfg.OpenRuntime) {
	case "mistralrs":
		want = cfg.MistralRS.DefaultModel
	case "llama_server":
		want = cfg.LlamaServer.DefaultModel
	default:
		return false
	}
	if strings.TrimSpace(want) == "" {
		return false
	}
	return localruntime.MatchesModel(want, model)
}

// onActiveDefaultDownloaded runs off the transitioning goroutine. It clears the
// not-ready chip and warms the sidecar. Both steps are best-effort: a failure
// leaves the chip lit and the runtime cold, which is the same state the user
// was already in, so nothing regresses.
func (s *Server) onActiveDefaultDownloaded(runtime string, model localruntime.ModelRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Chip first: the model is now on disk, so the runtime is no longer
	// "missing model". Re-broadcast the appropriate status so the CLI clears
	// the (F1) chip without the user having to re-open the config page.
	cfg := s.cfgSvc.Get()
	switch runtime {
	case "mistralrs":
		s.broadcastOpenRuntimeStatus(buildMistralRSStatus(cfg, s.mistralRSModelMissing(ctx, cfg)))
	case "llama_server":
		s.broadcastOpenRuntimeStatus(buildOpenRuntimeStatus(runtime, cfg, nil))
	}

	// Warm the sidecar. Start is idempotent per runtime+model at the provider
	// level (a running instance for the same model is reused), so a redundant
	// call here is harmless.
	rm := s.runtimeMgr()
	if rm == nil {
		return
	}
	if _, err := rm.Start(ctx, localruntime.StartRequest{
		Runtime: runtime,
		ModelID: model.ID,
	}); err != nil {
		// Non-fatal: log and leave the runtime cold. It will warm on first use.
		fmt.Printf("runtime observer: auto-start of %s default %q after download failed: %v\n",
			runtime, model.ID, err)
	}
}
