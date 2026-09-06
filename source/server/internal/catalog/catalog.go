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

// Registry holds the available sources and which one is active. Safe for
// concurrent use. The wiring layer (main.go) constructs each source and
// registers it; nothing here imports a concrete source.
type Registry struct {
	mu       sync.RWMutex
	backends map[string]Source
	active   string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{backends: make(map[string]Source)}
}

// Register adds a source. The first source registered becomes active until
// SetActive says otherwise. Re-registering a name replaces it.
func (r *Registry) Register(b Source) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backends[b.Name()] = b
	if r.active == "" {
		r.active = b.Name()
	}
}

// SetActive selects the active source by name, erroring if it isn't
// registered — so a bad config value fails loudly instead of silently serving
// the wrong source.
func (r *Registry) SetActive(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.backends[name]; !ok {
		return fmt.Errorf("catalog: unknown backend %q (available: %s)", name, r.availableLocked())
	}
	r.active = name
	return nil
}

// Active returns the active source, or ok=false when none is registered.
func (r *Registry) Active() (Source, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.backends[r.active]
	return b, ok
}

// ActiveName returns the active backend's name, or "" when none is registered.
func (r *Registry) ActiveName() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active
}

// Available returns the registered backend names, sorted.
func (r *Registry) Available() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.backends))
	for name := range r.backends {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (r *Registry) availableLocked() string {
	names := make([]string, 0, len(r.backends))
	for name := range r.backends {
		names = append(names, name)
	}
	sort.Strings(names)
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
