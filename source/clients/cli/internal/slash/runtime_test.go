package slash

import "testing"

func TestRegisterRuntime_RegistersM(t *testing.T) {
	r := New()
	RegisterRuntime(r)
	if _, ok := r.Lookup("m"); !ok {
		t.Fatal("missing /m command")
	}
}

func TestSlash_M_DispatchesRuntimeDashboard(t *testing.T) {
	r := New()
	RegisterRuntime(r)
	res, ok := r.Dispatch("/m")
	if !ok {
		t.Fatal("expected /m to dispatch")
	}
	if res.Kind != ResultOpenRuntimeDashboard {
		t.Errorf("kind: got %v want ResultOpenRuntimeDashboard", res.Kind)
	}
}
