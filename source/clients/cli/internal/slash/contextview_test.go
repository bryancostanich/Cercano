package slash

import "testing"

func TestSlash_C_OpensContextView(t *testing.T) {
	r := New()
	RegisterContextView(r)
	res, ok := r.Dispatch("/c")
	if !ok {
		t.Fatal("/c not dispatched")
	}
	if res.Kind != ResultOpenContextView {
		t.Errorf("kind = %v, want ResultOpenContextView", res.Kind)
	}
}
