// Package openmodels owns the single answer to "what is the effective open
// (local) model for a tier on the active runtime?"
//
// The effective model is the user's per-runtime override if set, otherwise the
// runtime's curated catalog default (by detected RAM). Config holds only the
// overrides — it cannot compute the catalog default, because the catalog lives
// in internal/localruntime and pkg/config must not import it. So the merge has
// exactly one home: this package. Everything that needs the effective model
// (main-chat provider selection, context metering, endpoint display, the
// server's own resolver) receives a *Resolver as a first-class collaborator,
// constructed once with its real dependencies — no setter injection, no
// half-built objects, no nil-fallback alternate path.
package openmodels

import (
	cfg "cercano/source/server/pkg/config"
)

// ConfigSource is the read side of the config service the resolver needs.
type ConfigSource interface {
	Get() cfg.Config
}

// CatalogDefaults returns the curated default tier→model map for a runtime at a
// given RAM size (the "catalog default" half of the merge). The server binds an
// implementation that dispatches to the per-runtime catalog subpackages, so
// this package never imports them.
type CatalogDefaults func(runtime string, ramBytes uint64) map[string]string

// RAMBytes reports total system RAM; injected so the resolver stays pure and
// testable.
type RAMBytes func() uint64

// Resolver merges per-runtime overrides over catalog defaults. Construct it once
// with New; it is safe for concurrent use if its dependencies are.
type Resolver struct {
	cfgSrc  ConfigSource
	catalog CatalogDefaults
	ram     RAMBytes
}

// New builds a Resolver. All three dependencies are required; passing nil is a
// programming error and will panic on first use rather than silently return the
// wrong model.
func New(cfgSrc ConfigSource, catalog CatalogDefaults, ram RAMBytes) *Resolver {
	return &Resolver{cfgSrc: cfgSrc, catalog: catalog, ram: ram}
}

// Model returns the effective model id for tier t on the active open runtime:
// the override if the user set one, else the runtime's catalog default. Returns
// "" only when neither exists for the tier.
func (r *Resolver) Model(t cfg.Tier) string {
	c := r.cfgSrc.Get()
	if id, ok := c.Models.OverrideFor(c.OpenRuntime, t); ok {
		return id
	}
	return r.catalog(c.OpenRuntime, r.ram())[string(t)]
}

// ChatModel is the effective everyday (interactive local chat) model.
func (r *Resolver) ChatModel() string { return r.Model(cfg.TierEveryday) }

// EmbeddingModel is the effective embedding model.
func (r *Resolver) EmbeddingModel() string { return r.Model(cfg.TierEmbedding) }

// EffectiveModel is the stateless one-shot form for callers (setup/doctor CLIs,
// mid-mutation server paths) that hold a plain config rather than a Resolver:
// the override for c's active runtime, else the catalog default. catalog is the
// per-runtime default source; ramBytes the detected RAM.
func EffectiveModel(c cfg.Config, t cfg.Tier, catalog CatalogDefaults, ramBytes uint64) string {
	if id, ok := c.Models.OverrideFor(c.OpenRuntime, t); ok {
		return id
	}
	if catalog == nil {
		return ""
	}
	return catalog(c.OpenRuntime, ramBytes)[string(t)]
}
