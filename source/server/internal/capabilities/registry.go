package capabilities

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Registry holds capabilities and the Services they share. Thread-safe.
type Registry struct {
	mu    sync.RWMutex
	svc   Services
	items map[string]Capability
}

// NewRegistry returns an empty Registry bound to svc.
func NewRegistry(svc Services) *Registry {
	return &Registry{svc: svc, items: map[string]Capability{}}
}

// Services returns the injected dependency container.
func (r *Registry) Services() Services { return r.svc }

// Register adds a capability; errors on empty or duplicate name.
func (r *Registry) Register(c Capability) error {
	if c == nil {
		return errors.New("capabilities: nil Capability")
	}
	name := c.Name()
	if name == "" {
		return errors.New("capabilities: empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[name]; ok {
		return fmt.Errorf("capabilities: duplicate name %q", name)
	}
	r.items[name] = c
	return nil
}

// MustRegister panics on error — for startup wiring.
func (r *Registry) MustRegister(c Capability) {
	if err := r.Register(c); err != nil {
		panic(err)
	}
}

// Get looks up a capability by canonical name.
func (r *Registry) Get(name string) (Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.items[name]
	return c, ok
}

// All returns every capability, sorted by name.
func (r *Registry) All() []Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Capability, 0, len(r.items))
	for _, c := range r.items {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// ForSurface returns capabilities exposed on the given surface, sorted by name.
func (r *Registry) ForSurface(s Surface) []Capability {
	out := make([]Capability, 0)
	for _, c := range r.All() {
		if c.Surfaces().Has(s) {
			out = append(out, c)
		}
	}
	return out
}
