package theme

import "testing"

func TestBuiltinsCrackerFirstAndExact(t *testing.T) {
	bs := BuiltinThemes()
	if len(bs) < 2 || bs[0].Name != "cr4k3r_j4x" {
		t.Fatalf("expected cracker first, got %v", names(bs))
	}
	// cracker built-in equals Cracker() exactly (golden).
	if HexOf(bs[0].Palette.Primary) != HexOf(Cracker().Primary) ||
		HexOf(bs[0].Palette.BufferCode) != HexOf(Cracker().BufferCode) {
		t.Fatal("cracker builtin diverged from Cracker()")
	}
}

func TestRegistryAddRemoveAndBuiltinProtection(t *testing.T) {
	r := NewRegistry(BuiltinThemes())
	if !r.IsBuiltin("cr4k3r_j4x") {
		t.Fatal("cracker should be builtin")
	}
	if err := r.Add(Theme{Name: "cr4k3r_j4x", Palette: Cracker()}); err == nil {
		t.Fatal("adding a theme named like a builtin must error")
	}
	if err := r.Add(Theme{Name: "mine", Palette: Cracker()}); err != nil {
		t.Fatalf("Add custom: %v", err)
	}
	if _, ok := r.Get("mine"); !ok {
		t.Fatal("custom theme not found after Add")
	}
	if err := r.Remove("cr4k3r_j4x"); err == nil {
		t.Fatal("removing a builtin must error")
	}
	if err := r.Remove("mine"); err != nil {
		t.Fatalf("Remove custom: %v", err)
	}
}

func names(ts []Theme) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}
