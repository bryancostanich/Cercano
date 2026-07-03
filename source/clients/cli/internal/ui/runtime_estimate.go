// runtime_estimate.go — RAM-fit estimates for the /m dashboard's
// download catalog.
//
// When a catalog row is selected, the dashboard lazily asks the server
// for the model's RAM-estimation numbers (weights size, KV-cache cost
// per token, max context, and the machine's physical RAM) and renders
// a projected-memory line plus a fit verdict in the detail panel. The
// point is to answer "will this even run on my machine?" BEFORE the
// user commits to a multi-gigabyte download.
//
// Results are cached per model for the dashboard's lifetime; the
// server additionally caches by blob digest on disk, so repeat lookups
// cost nothing anywhere.
package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"cercano/source/server/pkg/agentclient"
)

// estimateUsableFraction approximates how much of total system RAM a
// model can realistically claim. On Apple Silicon, Metal caps the GPU
// working set at roughly 75% of unified memory; on other platforms it
// doubles as a "leave room for the OS" margin.
const estimateUsableFraction = 0.75

// estimateMinContextTokens is the smallest context anyone realistically
// runs an agent with — the floor for the "won't fit at all" verdict.
const estimateMinContextTokens = 4096

// estimateOverheadBytes approximates llama.cpp allocations beyond
// weights + KV cache (compute graph buffers, scratch). Deliberately a
// labeled estimate — everything renders with "~".
func estimateOverheadBytes(weights int64) int64 {
	return 512<<20 + weights/20 // 512 MiB + 5% of weights
}

// runtimeEstimateMsg delivers one resolved estimate to the dashboard.
type runtimeEstimateMsg struct {
	key string
	est agentclient.ModelRAMEstimate
}

// estimateIsLocal reports whether the model's GGUF is already on disk,
// in which case the server reads the header locally instead of hitting
// the registry.
func estimateIsLocal(m agentclient.RuntimeModel) bool {
	return m.Path != "" || strings.EqualFold(m.DownloadState, "downloaded")
}

// estimateKey identifies a model in the estimate cache. Empty means
// "can't estimate this entry" (not on disk and no ollama ref — e.g.
// hardcoded catalog entries backed by direct HF URLs).
func estimateKey(m agentclient.RuntimeModel) string {
	if estimateIsLocal(m) {
		return "local:" + m.Runtime + ":" + m.ID
	}
	if m.OllamaRef != "" {
		return "ref:" + m.OllamaRef
	}
	return ""
}

// runtimeEstimateCmd fetches the estimate for one model off-thread.
func runtimeEstimateCmd(ag *agentclient.Client, key string, model agentclient.RuntimeModel) tea.Cmd {
	local := estimateIsLocal(model)
	runtime, modelID, ref := model.Runtime, model.ID, model.OllamaRef
	return func() tea.Msg {
		if ag == nil {
			return runtimeEstimateMsg{key: key, est: agentclient.ModelRAMEstimate{Err: errors.New("agent client unavailable")}}
		}
		// Generous timeout: a cold resolve is a manifest fetch plus a
		// 256 KiB ranged download; warm ones return instantly.
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if local {
			return runtimeEstimateMsg{key: key, est: ag.GetModelRAMEstimate(ctx, "", runtime, modelID)}
		}
		return runtimeEstimateMsg{key: key, est: ag.GetModelRAMEstimate(ctx, ref, "", "")}
	}
}

// maybeFetchEstimate returns a fetch command for the currently
// selected catalog model, or nil when there's nothing to do (no
// selection, un-estimatable entry, already cached, or in flight).
func (d *runtimeDashboard) maybeFetchEstimate() tea.Cmd {
	models := filteredCatalogModels(d.catalogModels(), d.catalogSearch.Value())
	if len(models) == 0 {
		return nil
	}
	model := models[clampIndex(d.catalogCursor, len(models))]
	key := estimateKey(model)
	if key == "" {
		return nil
	}
	if _, ok := d.estimates[key]; ok {
		return nil
	}
	if d.estimatePending[key] {
		return nil
	}
	if d.estimatePending == nil {
		d.estimatePending = make(map[string]bool)
	}
	d.estimatePending[key] = true
	return runtimeEstimateCmd(d.agent, key, model)
}

// applyEstimate records a resolved estimate. Returns a follow-up fetch
// in case the cursor moved to a different model while this one was in
// flight.
func (d *runtimeDashboard) applyEstimate(msg runtimeEstimateMsg) tea.Cmd {
	delete(d.estimatePending, msg.key)
	if d.estimates == nil {
		d.estimates = make(map[string]agentclient.ModelRAMEstimate)
	}
	d.estimates[msg.key] = msg.est
	return d.maybeFetchEstimate()
}

// selectedEstimate returns the cached estimate for the given model and
// whether a fetch is currently in flight.
func (d *runtimeDashboard) selectedEstimate(model agentclient.RuntimeModel) (est *agentclient.ModelRAMEstimate, pending bool) {
	key := estimateKey(model)
	if key == "" {
		return nil, false
	}
	if e, ok := d.estimates[key]; ok {
		return &e, false
	}
	return nil, d.estimatePending[key]
}

// estimateContextPoints picks the context sizes the memory line
// renders at: 8k and 32k where the model allows them, always ending at
// the model's max.
func estimateContextPoints(maxCtx int64) []int64 {
	if maxCtx <= 0 {
		return nil
	}
	var points []int64
	for _, p := range []int64{8192, 32768} {
		if p < maxCtx {
			points = append(points, p)
		}
	}
	return append(points, maxCtx)
}

// estimateTotalAt projects total RAM at a context size.
func estimateTotalAt(est agentclient.ModelRAMEstimate, ctx int64) int64 {
	return est.WeightsBytes + estimateOverheadBytes(est.WeightsBytes) + ctx*est.KVBytesPerToken
}

func fmtContextTokens(n int64) string {
	return fmt.Sprintf("%dk", n/1024)
}

// estimateMemoryLine renders the projected-RAM-at-context summary,
// e.g. "~5.7 GB @8k · ~7.3 GB @32k · ~12.4 GB @131k (max)".
func estimateMemoryLine(est agentclient.ModelRAMEstimate) string {
	points := estimateContextPoints(est.MaxContextTokens)
	if len(points) == 0 || est.KVBytesPerToken <= 0 || est.WeightsBytes <= 0 {
		return ""
	}
	parts := make([]string, 0, len(points))
	for i, ctx := range points {
		s := fmt.Sprintf("~%s @%s", formatBytes(estimateTotalAt(est, ctx)), fmtContextTokens(ctx))
		if i == len(points)-1 {
			s += " (max)"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}

// estimateFitLine renders the fit verdict against usable system RAM.
// Glyph-coded rather than color-coded so it survives detailLine's
// uniform styling and width-based truncation.
func estimateFitLine(est agentclient.ModelRAMEstimate) string {
	if est.SystemRAMBytes <= 0 || est.KVBytesPerToken <= 0 || est.WeightsBytes <= 0 {
		return ""
	}
	usable := int64(float64(est.SystemRAMBytes) * estimateUsableFraction)
	fixed := est.WeightsBytes + estimateOverheadBytes(est.WeightsBytes)
	minCtx := min64(estimateMinContextTokens, est.MaxContextTokens)
	if fixed+minCtx*est.KVBytesPerToken > usable {
		return fmt.Sprintf("✗ won't fit — needs ~%s, ~%s usable of %s",
			formatBytes(fixed+minCtx*est.KVBytesPerToken), formatBytes(usable), formatBytes(est.SystemRAMBytes))
	}
	if estimateTotalAt(est, est.MaxContextTokens) <= usable {
		return fmt.Sprintf("✓ fits — full %s context, ~%s usable", fmtContextTokens(est.MaxContextTokens), formatBytes(usable))
	}
	maxFit := (usable - fixed) / est.KVBytesPerToken
	return fmt.Sprintf("△ fits up to ~%s context (~%s usable)", fmtContextTokens(maxFit), formatBytes(usable))
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
