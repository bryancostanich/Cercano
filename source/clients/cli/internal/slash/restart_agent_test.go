package slash

import "testing"

func TestRegisterRestartAgent_Dispatch(t *testing.T) {
	r := New()
	RegisterRestartAgent(r)

	res, ok := r.Dispatch("/restart-agent")
	if !ok {
		t.Fatal("dispatch /restart-agent not ok")
	}
	if res.Kind != ResultRestartAgent {
		t.Fatalf("kind = %v, want ResultRestartAgent", res.Kind)
	}
	if res.Text == "" {
		t.Fatal("expected a default reason in Text")
	}
}

func TestRegisterRestartAgent_AliasAndReason(t *testing.T) {
	r := New()
	RegisterRestartAgent(r)

	res, ok := r.Dispatch("/bounce binary rebuilt")
	if !ok {
		t.Fatal("dispatch /bounce not ok")
	}
	if res.Kind != ResultRestartAgent {
		t.Fatalf("kind = %v, want ResultRestartAgent", res.Kind)
	}
	if res.Text != "binary rebuilt" {
		t.Fatalf("reason = %q, want %q", res.Text, "binary rebuilt")
	}
}
