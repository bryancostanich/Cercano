package slash

import "testing"

func TestSettingsCommandsOpenSettings(t *testing.T) {
	r := New()
	RegisterSettings(r)
	for _, name := range []string{"s", "settings"} {
		res, ok := r.Dispatch("/" + name)
		if !ok {
			t.Fatalf("/%s did not dispatch", name)
		}
		if res.Kind != ResultOpenSettings {
			t.Fatalf("/%s -> kind %v, want ResultOpenSettings", name, res.Kind)
		}
	}
}
