package theme

import "fmt"

// Registry holds the ordered set of available themes (built-ins + custom).
type Registry struct {
	order   []string
	themes  map[string]Theme
	builtin map[string]bool
}

// NewRegistry seeds a registry with built-ins (in order).
func NewRegistry(builtins []Theme) *Registry {
	r := &Registry{themes: map[string]Theme{}, builtin: map[string]bool{}}
	for _, t := range builtins {
		r.order = append(r.order, t.Name)
		r.themes[t.Name] = t
		r.builtin[t.Name] = true
	}
	return r
}

func (r *Registry) Names() []string { return append([]string(nil), r.order...) }

func (r *Registry) Get(name string) (Theme, bool) { t, ok := r.themes[name]; return t, ok }

func (r *Registry) IsBuiltin(name string) bool { return r.builtin[name] }

// Add registers a custom theme. Errors on empty name or a built-in name collision.
func (r *Registry) Add(t Theme) error {
	if t.Name == "" {
		return fmt.Errorf("theme name required")
	}
	if r.builtin[t.Name] {
		return fmt.Errorf("%q is a built-in theme name", t.Name)
	}
	if _, exists := r.themes[t.Name]; !exists {
		r.order = append(r.order, t.Name)
	}
	r.themes[t.Name] = t
	return nil
}

// Remove deletes a custom theme. Built-ins cannot be removed.
func (r *Registry) Remove(name string) error {
	if r.builtin[name] {
		return fmt.Errorf("cannot remove built-in theme %q", name)
	}
	if _, ok := r.themes[name]; !ok {
		return fmt.Errorf("no such theme %q", name)
	}
	delete(r.themes, name)
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return nil
}
