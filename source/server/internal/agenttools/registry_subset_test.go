package agenttools

import "testing"

func TestRegistry_Subset(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(stubTool{name: "a", perm: PermR})
	r.MustRegister(stubTool{name: "b", perm: PermW})
	r.MustRegister(stubTool{name: "c", perm: PermX})

	sub := r.Subset([]string{"a", "c", "nonexistent"})
	all := sub.All()
	if len(all) != 2 {
		t.Fatalf("want 2 tools, got %d", len(all))
	}
	// All() is sorted by name, so expect "a" then "c".
	if all[0].Name() != "a" || all[1].Name() != "c" {
		t.Errorf("want [a c], got [%s %s]", all[0].Name(), all[1].Name())
	}
	if _, ok := sub.Get("b"); ok {
		t.Error("tool \"b\" must not be in subset")
	}
	if _, ok := sub.Get("nonexistent"); ok {
		t.Error("unknown name must not be in subset")
	}
}

func TestRegistry_Subset_Empty(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(stubTool{name: "a", perm: PermR})

	if got := r.Subset(nil).All(); len(got) != 0 {
		t.Errorf("Subset(nil): want 0 tools, got %d", len(got))
	}
	if got := r.Subset([]string{}).All(); len(got) != 0 {
		t.Errorf("Subset([]): want 0 tools, got %d", len(got))
	}
}
