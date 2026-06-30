package slash

import "testing"

func TestThemeOpensSettings(t *testing.T) {
	r := New()
	RegisterTheme(r)
	res, ok := r.Dispatch("/theme")
	if !ok || res.Kind != ResultOpenSettings {
		t.Fatalf("/theme -> (%v,%v), want ResultOpenSettings", res.Kind, ok)
	}
}
