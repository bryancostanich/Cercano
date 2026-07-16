package localruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// EnsureModelsPresent is the engine-agnostic "make these models present" step
// of the model lifecycle. Given a runtime and the set of model refs that
// SHOULD be on disk for it (its default / tier set, sourced from config or
// policy by the caller), it resolves each ref against the provider-discovered
// inventory and enqueues a download for any that isn't already Downloaded.
//
// This is the one place download-on-demand lives, and it is deliberately free
// of any backend branching: a provider's only contribution is the ModelRecords
// it Discovers (each carrying its own DownloadState + URLs + target path);
// the ensure/resolve/download lifecycle is identical for llama-server,
// mistral.rs, or any future runtime. DownloadModel is idempotent (it no-ops on
// an already-Downloading or already-Downloaded record), so calling this on
// every runtime switch is safe and cheap.
//
// Refs are matched with the same fuzzy MatchesModel resolver the runtime uses
// at Start, so "ensured present" agrees with "what actually launches". An
// unresolvable ref (matches no discovered record) is collected as an error but
// does not abort the rest — a partial tier set still gets what it can.
func (m *InMemoryManager) EnsureModelsPresent(ctx context.Context, runtime string, want []string) error {
	runtime = strings.TrimSpace(runtime)
	if runtime == "" {
		return errors.New("runtime is required")
	}
	// Nothing wanted → nothing to do (e.g. ollama, or a runtime with no
	// configured default). Not an error.
	wanted := nonEmpty(want)
	if len(wanted) == 0 {
		return nil
	}

	inv, err := m.Inventory(ctx)
	if err != nil {
		// Inventory is a soft failure surface (one provider erroring shouldn't
		// sink the others), but if we can't see inventory at all we can't
		// resolve anything — report it.
		return fmt.Errorf("ensure models present: inventory: %w", err)
	}

	var errs []error
	for _, ref := range wanted {
		rec, ok := resolveInInventory(inv, runtime, ref)
		if !ok {
			errs = append(errs, fmt.Errorf("model %q not found for runtime %q", ref, runtime))
			continue
		}
		if rec.DownloadState == Downloaded {
			continue
		}
		// Enqueue by the record's canonical ID (findDownloadModel matches on the
		// exact ID, not the fuzzy ref). DownloadModel is idempotent and
		// non-blocking: it claims the slot and spawns the fetch, returning at
		// once. Observers drive the chip + auto-start when it completes.
		if _, err := m.DownloadModel(ctx, DownloadRequest{Runtime: runtime, ModelID: rec.ID}); err != nil {
			errs = append(errs, fmt.Errorf("enqueue %q: %w", rec.ID, err))
		}
	}
	return errors.Join(errs...)
}

// resolveInInventory finds the record for a ref within one runtime's slice of
// the inventory, using the shared fuzzy matcher.
func resolveInInventory(inv []ModelRecord, runtime, ref string) (ModelRecord, bool) {
	for _, m := range inv {
		if m.Runtime != runtime {
			continue
		}
		if MatchesModel(ref, m) {
			return m, true
		}
	}
	return ModelRecord{}, false
}

// nonEmpty returns the input with blank/whitespace-only entries dropped.
func nonEmpty(in []string) []string {
	out := in[:0:0]
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
