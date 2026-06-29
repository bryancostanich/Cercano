package protocols

import "testing"

func TestGetAndForDomain(t *testing.T) {
	all := Builtins()
	if len(all) < 4 {
		t.Fatalf("expected >=4 builtin protocols, got %d", len(all))
	}
	p, ok := Get("design-decisions")
	if !ok {
		t.Fatal("design-decisions not found")
	}
	if p.Trigger == "" || p.Body == "" {
		t.Fatal("design-decisions missing trigger/body")
	}
	for _, c := range ForDomain(DomainCore) {
		if c.Domain != DomainCore {
			t.Fatalf("ForDomain returned non-core: %s", c.Name)
		}
	}
	if _, ok := Get("nope"); ok {
		t.Fatal("unknown protocol should not be found")
	}
}

// Builtins is specced to return its catalog sorted by name; assert it so a
// sort-order regression can't pass silently.
func TestBuiltinsSortedByName(t *testing.T) {
	all := Builtins()
	for i := 1; i < len(all); i++ {
		if all[i-1].Name > all[i].Name {
			t.Fatalf("Builtins not sorted by name: %q before %q", all[i-1].Name, all[i].Name)
		}
	}
}
