package slash

import "testing"

func TestRegisterPermissions_RegistersAll(t *testing.T) {
	r := New()
	RegisterPermissions(r, nil)
	for _, n := range []string{"strict", "permissive", "bypass", "mode"} {
		if _, ok := r.cmds[n]; !ok {
			t.Errorf("missing /%s", n)
		}
	}
}

func TestSlash_StrictSetsMode(t *testing.T) {
	r := New()
	RegisterPermissions(r, nil)
	res, _ := r.Dispatch("/strict")
	if res.Kind != ResultSetPermissionMode || res.PermissionMode != "strict" {
		t.Errorf("strict: %+v", res)
	}
}

func TestSlash_ModeArgRequired(t *testing.T) {
	r := New()
	RegisterPermissions(r, nil)
	res, _ := r.Dispatch("/mode")
	if res.Kind != ResultText {
		t.Errorf("expected usage hint, got %+v", res)
	}
}
