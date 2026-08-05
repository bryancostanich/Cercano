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

func TestRegisterRuntime_RegistersRuntime(t *testing.T) {
	r := New()
	RegisterRuntime(r)
	if _, ok := r.Lookup("runtime"); !ok {
		t.Fatal("missing /runtime command")
	}
}

func TestSlash_Runtime_DispatchesRuntimeConfig(t *testing.T) {
	r := New()
	RegisterRuntime(r)
	res, ok := r.Dispatch("/runtime")
	if !ok {
		t.Fatal("expected /runtime to dispatch")
	}
	if res.Kind != ResultOpenRuntimeConfig {
		t.Errorf("kind: got %v want ResultOpenRuntimeConfig", res.Kind)
	}
}
