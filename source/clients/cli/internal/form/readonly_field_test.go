package form

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestReadOnlyFieldBasics(t *testing.T) {
	f := NewReadOnly("port", "port", "50052", "(read-only)")
	if f.Key() != "port" {
		t.Fatalf("Key() = %q, want port", f.Key())
	}
	if f.Label() != "port" {
		t.Fatalf("Label() = %q, want port", f.Label())
	}
	if f.Display() != "50052" {
		t.Fatalf("Display() = %q, want 50052", f.Display())
	}
	if f.Editing() {
		t.Fatal("ReadOnly field must never report Editing")
	}
	cmd, committed, val := f.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if cmd != nil || committed || val != "" {
		t.Fatalf("ReadOnly Update must be inert, got cmd=%v committed=%v val=%q", cmd, committed, val)
	}
}
