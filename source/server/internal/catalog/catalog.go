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

// Model is one entry from List — enough to rank and drill into.
type Model struct {
	Backend   string // which backend produced this
	ID        string // backend-scoped id (HF repo, or Ollama name)
	Author    string
	Downloads int
	Likes     int
}

// File is one downloadable quant variant within a model.
type File struct {
	Name      string
	SizeBytes int64
}

// Detail is a model's per-file and identity metadata.
type Detail struct {
	Backend       string
	ID            string
	Format        string // "gguf" | "safetensors" — the model's on-disk format
	Architecture  string // gate input (GGUF general.architecture, or config model_type)
	ContextLength int
	SupportsTools bool
	Files         []File
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
