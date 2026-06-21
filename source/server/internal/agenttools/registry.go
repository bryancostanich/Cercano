package agenttools

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Registry holds the set of available Tools. Thread-safe.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register adds a Tool. Returns an error on duplicate name so misconfig
// surfaces at startup rather than silently mounting a shadowed tool.
func (r *Registry) Register(t Tool) error {
	if t == nil {
		return errors.New("agenttools: nil Tool")
	}
	name := t.Name()
	if name == "" {
		return errors.New("agenttools: empty Tool name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; ok {
		return fmt.Errorf("agenttools: duplicate Tool name %q", name)
	}
	r.tools[name] = t
	return nil
}

// MustRegister panics on duplicate — convenience for init() blocks where the
// caller has decided early failure is acceptable.
func (r *Registry) MustRegister(t Tool) {
	if err := r.Register(t); err != nil {
		panic(err)
	}
}

// Get looks up a Tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// All returns all registered Tools, sorted by name. Stable order so the CLI
// /tools listing is deterministic across invocations.
func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Filter returns tools matching the given Permission (or all when perm is
// empty). Useful for /tools split-by-tier displays.
func (r *Registry) Filter(perm Permission) []Tool {
	all := r.All()
	if perm == "" {
		return all
	}
	out := make([]Tool, 0, len(all))
	for _, t := range all {
		if t.Permission() == perm {
			out = append(out, t)
		}
	}
	return out
}
