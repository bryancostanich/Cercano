package server

import (
	"context"
	"strings"

	"cercano/source/server/internal/localruntime"
	"cercano/source/server/internal/localruntime/llamaserver"
	"cercano/source/server/pkg/config"
	"cercano/source/server/pkg/proto"
)

// readinessState is the runtime-agnostic three-state readiness of the active
// open runtime's default model. It is the single vocabulary every surface (the
// push broadcast, the pull snapshot, the observer, the CLI chip, and the
// dispatch routing gate) speaks — replacing the per-runtime forks
// (buildMistralRSStatus vs buildOpenRuntimeStatus, the mistralrs-only
// resolveMistralRSDefault, and the `runtime != "llama_server"` gates).
type readinessState int

const (
	// readyToServe — the runtime can serve now (model present, binary found).
	readyToServe readinessState = iota
	// readyDownloading — not servable ONLY because the default model is
	// actively downloading. Distinct from missing: no user action needed.
	readyDownloading
	// readyMissing — needs user action: model absent / no default configured /
	// binary not installed.
	readyMissing
)

// openRuntimeReadiness is the resolved readiness of a runtime, plus the record
// it resolved to (when known) so callers can act on the canonical model.
type openRuntimeReadiness struct {
	State readinessState
	// Missing carries the wire "missing" reason ("model" | "binary") when
	// State == readyMissing, so the existing chip/modal flow is preserved.
	Missing string
	// Message is a human diagnostic.
	Message string
	// Model is the canonical record the runtime resolved to (zero when none).
	Model localruntime.ModelRecord
	// Binary/DefaultModel echo the resolved config fields for the snapshot.
	Binary       string
	DefaultModel string
	// SuggestedCommand is set for the binary-missing (llama-server) case.
	SuggestedCommand string
}

// runtimeDefaultModel returns the configured default model string for a given
// runtime — the one field that differs per runtime. This is the ONLY
// runtime-name switch in the readiness path, and it exists solely to read the
// right config field; everything downstream is uniform.
func runtimeDefaultModel(cfg config.Config, runtime string) string {
	switch runtime {
	case "mistralrs":
		return strings.TrimSpace(cfg.MistralRS.DefaultModel)
	case "llama_server":
		return strings.TrimSpace(cfg.LlamaServer.DefaultModel)
	default:
		return ""
	}
}

// resolveRuntimeDefault resolves a runtime's configured default model against
// the runtime manager's inventory using the same fuzzy matcher the provider
// uses at Start, so readiness agrees with what actually launches. found=false
// when unset, unmatched, or the manager is unavailable.
func (s *Server) resolveRuntimeDefault(ctx context.Context, cfg config.Config, runtime string) (localruntime.ModelRecord, bool) {
	want := runtimeDefaultModel(cfg, runtime)
	if want == "" {
		return localruntime.ModelRecord{}, false
	}
	rm := s.runtimeMgr()
	if rm == nil {
		return localruntime.ModelRecord{}, false
	}
	inv, err := rm.Inventory(ctx)
	if err != nil {
		return localruntime.ModelRecord{}, false
	}
	for _, m := range inv {
		if m.Runtime != runtime {
			continue
		}
		if localruntime.MatchesModel(want, m) {
			return m, true
		}
	}
	return localruntime.ModelRecord{}, false
}

// openRuntimeReadinessFor computes the runtime-agnostic readiness of a runtime.
// It is the one place readiness is decided; formatters and gates consume its
// result rather than re-deriving per-runtime.
//
//   - ollama: always ready (it manages its own model presence).
//   - llama_server: binary must be found (headless Detect) AND the default GGUF
//     present; a download in flight reports downloading.
//   - mistralrs (and any future model-download runtime): the default model's
//     DownloadState decides ready / downloading / missing.
func (s *Server) openRuntimeReadinessFor(ctx context.Context, cfg config.Config, runtime string) openRuntimeReadiness {
	switch runtime {
	case "ollama":
		return openRuntimeReadiness{State: readyToServe, Message: "ollama runtime active"}

	case "llama_server":
		return s.llamaServerReadiness(ctx, cfg)

	default:
		// Model-download runtimes (mistralrs today): readiness == the default
		// model's download state. No binary concept — the runtime is bundled.
		return s.modelDownloadReadiness(ctx, cfg, runtime)
	}
}

// modelDownloadReadiness computes readiness for a bundled, model-download
// runtime (mistralrs). ready when Downloaded; downloading when Downloading;
// missing otherwise (unset / unmatched / failed / cancelled).
func (s *Server) modelDownloadReadiness(ctx context.Context, cfg config.Config, runtime string) openRuntimeReadiness {
	r := openRuntimeReadiness{
		Binary:       runtimeBinary(cfg, runtime),
		DefaultModel: runtimeDefaultModel(cfg, runtime),
	}
	rec, found := s.resolveRuntimeDefault(ctx, cfg, runtime)
	if !found {
		r.State = readyMissing
		r.Missing = "model"
		if r.DefaultModel == "" {
			r.Message = runtime + " runtime: no default model configured"
		} else {
			r.Message = runtime + " default model not downloaded"
		}
		return r
	}
	r.Model = rec
	switch rec.DownloadState {
	case localruntime.Downloaded:
		r.State = readyToServe
		r.Message = runtime + " runtime configured"
	case localruntime.Downloading:
		r.State = readyDownloading
		r.Message = runtime + " default model downloading…"
	default:
		r.State = readyMissing
		r.Missing = "model"
		r.Message = runtime + " default model not downloaded"
	}
	return r
}

// llamaServerReadiness folds llama-server's binary-detect + model-presence into
// the same three-state vocabulary. A binary-missing case is readyMissing with
// Missing="binary"; a model still downloading is readyDownloading; otherwise it
// defers to Detect's model/GGUF resolution.
func (s *Server) llamaServerReadiness(ctx context.Context, cfg config.Config) openRuntimeReadiness {
	// If the default model resolves to a runtime record that is actively
	// downloading, surface downloading rather than a binary/GGUF diagnostic —
	// the file is on its way.
	if rec, found := s.resolveRuntimeDefault(ctx, cfg, "llama_server"); found {
		if rec.DownloadState == localruntime.Downloading {
			return openRuntimeReadiness{
				State:        readyDownloading,
				Model:        rec,
				Binary:       cfg.LlamaServer.Binary,
				DefaultModel: cfg.LlamaServer.DefaultModel,
				Message:      "llama-server default model downloading…",
			}
		}
	}

	llamaCfg := cfg.LlamaServer
	detectErr := llamaserver.Detect(context.Background(), &llamaCfg)
	if detectErr == nil {
		return openRuntimeReadiness{
			State:        readyToServe,
			Binary:       llamaCfg.Binary,
			DefaultModel: llamaCfg.DefaultModel,
			Message:      "llama-server runtime configured",
		}
	}
	r := openRuntimeReadiness{
		State:        readyMissing,
		Binary:       llamaCfg.Binary,
		DefaultModel: llamaCfg.DefaultModel,
	}
	if de, ok := detectErr.(*llamaserver.DetectError); ok {
		r.Missing = de.Missing
		r.Message = de.Error()
		r.SuggestedCommand = de.SuggestedCommand()
	} else {
		r.Missing = "model"
		r.Message = detectErr.Error()
	}
	return r
}

// runtimeBinary returns the configured binary path for a runtime (empty when
// n/a), used only to echo into the snapshot.
func runtimeBinary(cfg config.Config, runtime string) string {
	switch runtime {
	case "mistralrs":
		return cfg.MistralRS.Binary
	case "llama_server":
		return cfg.LlamaServer.Binary
	default:
		return ""
	}
}

// openRuntimeStatusFrom formats a readiness into the wire proto. This is the
// single formatter that replaces buildMistralRSStatus + buildOpenRuntimeStatus.
func openRuntimeStatusFrom(runtime string, r openRuntimeReadiness) *proto.OpenRuntimeStatus {
	st := &proto.OpenRuntimeStatus{
		Runtime:          runtime,
		Message:          r.Message,
		BinaryPath:       r.Binary,
		DefaultModel:     r.DefaultModel,
		SuggestedCommand: r.SuggestedCommand,
	}
	switch r.State {
	case readyToServe:
		st.Ok = true
	case readyDownloading:
		st.Ok = false
		st.Downloading = true
	case readyMissing:
		st.Ok = false
		st.Missing = r.Missing
	}
	return st
}

// openRuntimeStatus is the one-call convenience the push/pull sites use:
// resolve readiness for a runtime and format it.
func (s *Server) openRuntimeStatus(ctx context.Context, cfg config.Config, runtime string) *proto.OpenRuntimeStatus {
	if runtime == "" {
		runtime = cfg.OpenRuntime
	}
	if runtime == "" {
		runtime = "ollama"
	}
	return openRuntimeStatusFrom(runtime, s.openRuntimeReadinessFor(ctx, cfg, runtime))
}
