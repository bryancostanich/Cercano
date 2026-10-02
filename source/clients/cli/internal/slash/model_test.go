package slash

import "testing"

func TestSessionModelCommands(t *testing.T) {
	reg := New()
	RegisterModel(reg)
	for _, tc := range []struct{ input, action, profile, model string }{
		{"/model", "status", "", ""},
		{"/model status", "status", "", ""},
		{"/model models deepinfra", "models", "deepinfra", ""},
		{"/model set deepinfra provider/exact-model", "set", "deepinfra", "provider/exact-model"},
		{"/model clear", "clear", "", ""},
	} {
		got, ok := reg.Dispatch(tc.input)
		if !ok || got.Kind != ResultSessionModel || got.ModelAction != tc.action || got.ModelProfile != tc.profile || got.ModelID != tc.model {
			t.Fatalf("%s: %+v %v", tc.input, got, ok)
		}
	}
	for _, input := range []string{"/model set", "/model set deepinfra", "/model clear extra", "/model models", "/model unknown"} {
		got, ok := reg.Dispatch(input)
		if !ok || got.Kind != ResultText {
			t.Fatalf("bad syntax accepted: %s %+v", input, got)
		}
	}
}
