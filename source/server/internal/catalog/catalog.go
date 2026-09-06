// Package catalog abstracts model discovery behind a pluggable Source
// interface. A Source is any place models come from — a download host like
// HuggingFace or Ollama, or a hosted inference provider whose models are
// served rather than downloaded.
//
// Acquisition is the only axis on which sources genuinely differ, so it is
// factored into an optional capability interface rather than baked into the
// core one: every Source implements List and Detail, and only a source whose
// models become local files implements Downloadable. Consumers that need to
// download type-assert for it, which makes routing a servable-only model into
// the download manager a compile-time error rather than a runtime one.
//
// The package deliberately depends on no concrete source and knows nothing
// about llama.cpp: the compatibility gate is a consumer concern (the server
// applies it against Detail.Architecture when preparing a download into
// llama-server), so a source stays a pure origin of models.
package catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Source is an origin of models — downloadable (HuggingFace, Ollama) or
// servable (a hosted inference provider). It carries only what every origin
// can answer: identity, a browsable list, and per-model detail.
type Source interface {
	// Name is the source's stable identifier ("huggingface", "ollama").
	Name() string
	// List returns discoverable models, ranked/curated by the source.
	List(ctx context.Context, opts ListOptions) ([]Model, error)
	// Detail returns one model's variants, architecture, tool support, and
	// sizes — enough for the consumer to gate and to offer a variant pick.
	Detail(ctx context.Context, id string) (Detail, error)
}

// Downloadable is implemented only by sources whose models become local
// files. Consumers that fetch bytes take a Downloadable, not a Source, so a
// servable-only source cannot reach the download manager at all.
type Downloadable interface {
	Source
	// ResolveDownload turns a chosen model + variant into a concrete download
	// plan (URLs the download manager fetches). A source that needs manifest
	// resolution (Ollama's OCI flow) does it here, so the download manager
	// stays source-agnostic.
	ResolveDownload(ctx context.Context, id, file string) (DownloadPlan, error)
}

// Backend is the former name of Source.
//
// Deprecated: use Source, or Downloadable when the consumer fetches bytes.
// Retained so the tree keeps building mid-refactor; removed once every call
// site is migrated.
type Backend = Source

// Ref identifies one model unambiguously: an id is only meaningful within the
// source that issued it ("qwen2.5-coder:7b" is an Ollama name and nothing to
// a hosted provider), so the two always travel together.
//
// A Ref with an empty Source is unqualified — it came from a client that
// predates source-qualified references. Consumers resolve those through
// Registry.Resolve, which searches for the id rather than assuming a source.
type Ref struct {
	Source string
	ID     string
}

// String renders a Ref as "source/id", or bare id when unqualified.
func (r Ref) String() string {
	if r.Source == "" {
		return r.ID
	}
	return r.Source + "/" + r.ID
}

// Qualified reports whether the Ref names its source.
func (r Ref) Qualified() bool { return r.Source != "" && r.ID != "" }

// ListOptions bounds and filters a List call.
type ListOptions struct {
	Limit int    // 0 = backend default
	Query string // optional free-text filter; "" = no filter
	// Format is the model format the caller wants surfaced — the active
	// runtime's primary format ("gguf" for llama-server, "safetensors" for
	// mistral.rs). A backend that indexes multiple formats (HuggingFace) uses
	// it to pick which to list; "" means the backend's default (gguf).
	Format string
}

// Kind is what a model does. Sources that index more than text generation
// (hosted providers commonly also serve image, speech, and embedding models)
// report it so consumers can filter to the kinds they can actually drive.
type Kind string

const (
	// KindUnknown is the zero value: the source did not say. Treated as
	// text generation by consumers that require a kind, since that is what
	// every source indexed so far serves by default.
	KindUnknown Kind = ""
	// KindTextGeneration is a chat/completion model — the only kind the
	// agent loop can drive.
	KindTextGeneration Kind = "text-generation"
	KindEmbedding      Kind = "embedding"
	KindImage          Kind = "image"
	KindSpeech         Kind = "speech"
	KindVideo          Kind = "video"
)

// Model is one entry from List — enough to rank, filter, and drill into.
type Model struct {
	Source string // which source produced this
	ID     string // source-scoped id (HF repo, Ollama name, provider model id)
	// Publisher is who put the model out: an HF author, or the org prefix of
	// a hosted provider's model id.
	Publisher string
	Kind      Kind
	// ContextLength is the model's window when the source's list surface
	// exposes it; 0 means "ask Detail".
	ContextLength int
	// SupportsTools reports whether the model can call tools. Cercano's agent
	// loop is useless without it, so a source that knows should say here
	// rather than making every consumer fetch Detail to find out.
	SupportsTools  bool
	SupportsVision bool
	// Deprecated marks a model the source has retired. Sources are expected
	// to filter these out of List by default; the field carries the state for
	// the case that matters — explaining a pinned model that has since died.
	Deprecated bool
	// ReplacedBy names the successor when Deprecated is set, so a stale pin
	// can be reported with a concrete migration target instead of just an
	// error.
	ReplacedBy string
	// Downloads and Likes are popularity signals; source-dependent and 0 when
	// the source publishes none.
	Downloads int
	Likes     int
}

// Variant is one selectable form of a model. For a downloadable source that
// is a quant file (with a size); for a hosted source it is a served flavor
// such as a turbo or pre-quantized build (with a price). Neither the size nor
// the price is mandatory — a variant carries whatever its source meters.
type Variant struct {
	Name         string
	Quantization string
	// SizeBytes is the on-disk size for a downloadable variant; 0 for a
	// served one.
	SizeBytes int64
	// PriceIn and PriceOut are per-million-token costs in micro-USD for a
	// metered variant; 0 for a downloadable one. Micro-USD (not float) keeps
	// the arithmetic exact.
	PriceIn  int64
	PriceOut int64
}

// Detail is a model's per-variant and identity metadata.
type Detail struct {
	Source        string
	ID            string
	Format        string // "gguf" | "safetensors" — the model's on-disk format
	Architecture  string // gate input (GGUF general.architecture, or config model_type)
	ContextLength int
	SupportsTools bool
	// Deprecated and ReplacedBy mirror Model, so a drill-in on a pinned model
	// can explain a retirement even when List filtered it out.
	Deprecated bool
	ReplacedBy string
	Variants   []Variant
}

// DownloadPlan is what the download manager consumes: concrete URLs (one, or
// several for a sharded split), the primary filename (what the runtime is
// pointed at), and the total byte size across all URLs.
type DownloadPlan struct {
	URLs        []string
	PrimaryFile string
	TotalBytes  int64
}

// Registry is a lookup table of the registered sources. Safe for concurrent
// use. The wiring layer (main.go) constructs each source and registers it;
// nothing here imports a concrete source.
//
// There is deliberately no "active" source. A model is identified by the pair
// (source, id) — an id is only meaningful within the source that issued it —
// so every consumer either names the source it wants (Lookup, for a model it
// already holds a Ref to) or wants all of them (All, for browse). Inferring
// the source from ambient state is what made an id silently resolve against
// the wrong source when the configured source changed.
type Registry struct {
	mu      sync.RWMutex
	sources map[string]Source
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{sources: make(map[string]Source)}
}

// Register adds a source. Re-registering a name replaces it.
func (r *Registry) Register(s Source) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sources[s.Name()] = s
}

// Lookup returns the source with the given name. ok=false means no such
// source is registered — the caller reports it against the Ref that named it,
// rather than falling back to some other source and resolving the id wrongly.
func (r *Registry) Lookup(name string) (Source, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sources[name]
	return s, ok
}

// LookupDownloadable returns the named source only if its models become local
// files. ok=false covers both "no such source" and "that source serves rather
// than downloads"; Available lets the caller say which.
func (r *Registry) LookupDownloadable(name string) (Downloadable, bool) {
	s, ok := r.Lookup(name)
	if !ok {
		return nil, false
	}
	d, ok := s.(Downloadable)
	return d, ok
}

// All returns every registered source, ordered by name so browse results are
// stable across calls.
func (r *Registry) All() []Source {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Source, 0, len(r.sources))
	for _, name := range r.namesLocked() {
		out = append(out, r.sources[name])
	}
	return out
}

// Resolve turns a Ref into the source that can serve it.
//
// A qualified Ref is a direct lookup. An unqualified one (from a client that
// predates source-qualified refs) is resolved by asking each source, in name
// order, whether it knows the id — the first that does wins, and the returned
// Ref is the qualified form the caller should use from then on. That probe is
// the compatibility path, not the normal one: it costs one Detail call per
// source until a hit, which is why qualified refs are what clients send.
func (r *Registry) Resolve(ctx context.Context, ref Ref) (Source, Ref, error) {
	if ref.ID == "" {
		return nil, ref, fmt.Errorf("catalog: empty model id")
	}
	if ref.Source != "" {
		s, ok := r.Lookup(ref.Source)
		if !ok {
			return nil, ref, fmt.Errorf("catalog: unknown source %q (available: %s)", ref.Source, r.AvailableList())
		}
		return s, ref, nil
	}
	for _, s := range r.All() {
		if _, err := s.Detail(ctx, ref.ID); err == nil {
			return s, Ref{Source: s.Name(), ID: ref.ID}, nil
		}
	}
	return nil, ref, fmt.Errorf("catalog: no source recognizes model %q (searched: %s)", ref.ID, r.AvailableList())
}

// ResolveDownloadable is Resolve restricted to sources that produce local
// files, so a servable-only model cannot reach the download manager.
func (r *Registry) ResolveDownloadable(ctx context.Context, ref Ref) (Downloadable, Ref, error) {
	s, got, err := r.Resolve(ctx, ref)
	if err != nil {
		return nil, got, err
	}
	d, ok := s.(Downloadable)
	if !ok {
		return nil, got, fmt.Errorf("catalog: source %q serves models rather than downloading them; nothing to fetch for %q", s.Name(), got.ID)
	}
	return d, got, nil
}

// Available returns the registered source names, sorted.
func (r *Registry) Available() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.namesLocked()
}

// AvailableList renders the registered names for an error message.
func (r *Registry) AvailableList() string {
	return strings.Join(r.Available(), ", ")
}

func (r *Registry) namesLocked() []string {
	names := make([]string, 0, len(r.sources))
	for name := range r.sources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
