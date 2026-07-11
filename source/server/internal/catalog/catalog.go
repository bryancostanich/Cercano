// Package catalog abstracts model discovery behind a pluggable Backend
// interface. Exactly one backend is active at a time (config-selected); the
// server's browse, search, and download resolution all go through the active
// backend, so adding a source — HuggingFace, Ollama, or something later — is a
// new Backend implementation, not a change to the server.
//
// The package deliberately depends on neither backend and knows nothing about
// llama.cpp: the compatibility gate is a consumer concern (the server applies
// it against Detail.Architecture when preparing a download into llama-server),
// so a backend stays a pure source of models.
package catalog

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Backend is a source of downloadable GGUF models (HuggingFace, Ollama, …).
type Backend interface {
	// Name is the backend's stable identifier ("huggingface", "ollama").
	Name() string
	// List returns discoverable models, ranked/curated by the backend.
	List(ctx context.Context, opts ListOptions) ([]Model, error)
	// Detail returns one model's quant files, architecture, tool support,
	// and sizes — enough for the consumer to gate and to offer a quant pick.
	Detail(ctx context.Context, id string) (Detail, error)
	// ResolveDownload turns a chosen model + file into a concrete download
	// plan (URLs the download manager fetches). A backend that needs manifest
	// resolution (Ollama's OCI flow) does it here, so the download manager
	// stays backend-agnostic.
	ResolveDownload(ctx context.Context, id, file string) (DownloadPlan, error)
}

// ListOptions bounds and filters a List call.
type ListOptions struct {
	Limit int    // 0 = backend default
	Query string // optional free-text filter; "" = no filter
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
	Architecture  string // gate input (general.architecture)
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

// Registry holds the available backends and which one is active. Safe for
// concurrent use. The wiring layer (main.go) constructs each backend and
// registers it; nothing here imports a concrete backend.
type Registry struct {
	mu       sync.RWMutex
	backends map[string]Backend
	active   string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{backends: make(map[string]Backend)}
}

// Register adds a backend. The first backend registered becomes active until
// SetActive says otherwise. Re-registering a name replaces it.
func (r *Registry) Register(b Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backends[b.Name()] = b
	if r.active == "" {
		r.active = b.Name()
	}
}

// SetActive selects the active backend by name, erroring if it isn't
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

// Active returns the active backend, or ok=false when none is registered.
func (r *Registry) Active() (Backend, bool) {
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
