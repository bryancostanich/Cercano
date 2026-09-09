// Package modelevidence resolves capability evidence for the model a turn
// will actually talk to.
//
// It exists because a model id alone is not enough to know anything about a
// model. The same id can be served by several providers, and two providers
// serving "the same" model may expose different context windows and different
// input modalities. Every question this package answers is therefore keyed on
// a full modelmetadata.Identity (provider, base URL, route, model), never on
// the model name by itself.
//
// Two rules govern every answer:
//
//   - Absence is not denial, and absence is not permission. A missing entry
//     yields VisionUnknown and a zero context window. Callers decide what to
//     do with "unknown"; for image routing that must mean "do not send".
//   - Discovery is bounded and reuses the catalog's existing cache. This
//     package performs no unbounded hot-path I/O and maintains no second index.
//
// The resolver is deliberately additive: when it has nothing to say, existing
// conventional fallbacks (contextmeter's per-family table) still apply, so a
// provider this package knows nothing about behaves exactly as before.
package modelevidence

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"cercano/source/server/internal/catalog"
	"cercano/source/server/internal/modelmetadata"
)

// catalogSourceForHost maps a cloud profile's base URL host to the catalog
// source that indexes that provider's models.
//
// Only DeepInfra is mapped today because it is the only provider with a live
// model index wired into the catalog. Anything else resolves to "" and falls
// through to shipped evidence, which is the honest outcome: we have no index
// for it.
func catalogSourceForHost(baseURL string) string {
	host := hostOf(baseURL)
	switch {
	case host == "":
		return ""
	case strings.Contains(host, "deepinfra.com"):
		return "deepinfra"
	default:
		return ""
	}
}

func hostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "//") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// Registry is the subset of *catalog.Registry this package needs.
type Registry interface {
	Lookup(name string) (catalog.Source, bool)
}

// Resolver answers capability questions for exact identities.
//
// Lookups are memoized per identity for cacheTTL. The catalog source keeps its
// own index cache; this second, much smaller cache exists so a per-turn or
// per-request question does not re-enter the source's locking on every call.
type Resolver struct {
	reg     Registry
	ttl     time.Duration
	now     func() time.Time
	timeout time.Duration

	mu     sync.Mutex
	cached map[modelmetadata.Identity]cacheEntry
}

type cacheEntry struct {
	ev modelmetadata.Evidence
	at time.Time
}

const (
	defaultCacheTTL = 15 * time.Minute
	// defaultLookupTimeout bounds a cold lookup. The catalog source has its own
	// fetch timeout; this one bounds the whole resolve so a hot path (an image
	// routing decision, a budget computation) can never hang on discovery.
	defaultLookupTimeout = 5 * time.Second
)

// Option customizes a Resolver.
type Option func(*Resolver)

// WithTTL overrides how long a resolved answer is reused.
func WithTTL(d time.Duration) Option { return func(r *Resolver) { r.ttl = d } }

// WithClock overrides the clock (tests).
func WithClock(fn func() time.Time) Option { return func(r *Resolver) { r.now = fn } }

// WithTimeout overrides the per-lookup bound.
func WithTimeout(d time.Duration) Option { return func(r *Resolver) { r.timeout = d } }

// New returns a Resolver over the given catalog registry. A nil registry is
// valid and yields shipped evidence only.
func New(reg Registry, opts ...Option) *Resolver {
	r := &Resolver{
		reg:     reg,
		ttl:     defaultCacheTTL,
		now:     time.Now,
		timeout: defaultLookupTimeout,
		cached:  make(map[modelmetadata.Identity]cacheEntry),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Resolve returns the evidence held for an identity.
//
// Order: shipped evidence for vendors we ship knowledge about, then the
// provider's catalog index. Shipped evidence wins because it is curated and
// free; the index fills in everything else. The result is always normalized,
// so a malformed upstream value cannot grant a capability.
func (r *Resolver) Resolve(ctx context.Context, id modelmetadata.Identity) modelmetadata.Evidence {
	if id.Model == "" {
		return modelmetadata.Evidence{}
	}
	if ev, ok := r.cachedFor(id); ok {
		return ev
	}
	ev := shippedEvidence(id)
	if ev.ContextWindow <= 0 || ev.Vision == modelmetadata.VisionUnknown {
		ev = merge(ev, r.fromCatalog(ctx, id))
	}
	ev = ev.Normalized()
	r.store(id, ev)
	return ev
}

// merge fills only the gaps in base from extra. Shipped evidence is never
// overwritten by index evidence.
func merge(base, extra modelmetadata.Evidence) modelmetadata.Evidence {
	if base.ContextWindow <= 0 {
		base.ContextWindow = extra.ContextWindow
	}
	if base.Vision == modelmetadata.VisionUnknown {
		base.Vision = extra.Vision
	}
	return base
}

func (r *Resolver) fromCatalog(ctx context.Context, id modelmetadata.Identity) modelmetadata.Evidence {
	if r.reg == nil {
		return modelmetadata.Evidence{}
	}
	name := catalogSourceForHost(id.BaseURL)
	if name == "" {
		return modelmetadata.Evidence{}
	}
	src, ok := r.reg.Lookup(name)
	if !ok {
		return modelmetadata.Evidence{}
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	detail, err := src.Detail(ctx, id.Model)
	if err != nil {
		// Discovery failure leaves everything unknown. That is the safe
		// direction: unknown blocks images and falls back to the conventional
		// context window rather than inventing capacity.
		return modelmetadata.Evidence{}
	}
	ev := modelmetadata.Evidence{ContextWindow: detail.ContextLength}
	if detail.SupportsVision {
		// Affirmative only. A source that publishes no vision signal leaves
		// this unknown; it must not be reported as unsupported, because the
		// model may well accept images and we simply cannot prove it.
		ev.Vision = modelmetadata.VisionSupported
	}
	return ev
}

func (r *Resolver) cachedFor(id modelmetadata.Identity) (modelmetadata.Evidence, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.cached[id]
	if !ok || r.now().Sub(e.at) > r.ttl {
		return modelmetadata.Evidence{}, false
	}
	return e.ev, true
}

func (r *Resolver) store(id modelmetadata.Identity, ev modelmetadata.Evidence) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cached[id] = cacheEntry{ev: ev, at: r.now()}
}

// Snapshot resolves evidence for a set of identities, for transport to a
// worker. Identities with nothing known are still included: an explicit
// "unknown" entry is more useful to the worker than a missing one, because it
// distinguishes "the host looked and found nothing" from "the host never
// considered this model".
func (r *Resolver) Snapshot(ctx context.Context, ids []modelmetadata.Identity) modelmetadata.Snapshot {
	seen := make(map[modelmetadata.Identity]bool, len(ids))
	out := make(modelmetadata.Snapshot, 0, len(ids))
	for _, id := range ids {
		if id.Model == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, modelmetadata.Entry{Identity: id, Evidence: r.Resolve(ctx, id)})
	}
	return out
}
