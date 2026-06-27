package form

import "testing"

func TestSelectFieldPick(t *testing.T) {
	opts := []Option{{Label: "strict", Value: "strict"}, {Label: "permissive", Value: "permissive"}, {Label: "bypass", Value: "bypass"}}
	f := NewSelect("permission-mode", "permission-mode", opts, "permissive")
	if f.Display() != "permissive" {
		t.Fatalf("Display = %q, want permissive", f.Display())
	}
	// enter opens the picker (cursor on current = index 1).
	_, committed, _ := f.Update(enter())
	if !f.Editing() || committed {
		t.Fatalf("enter should open picker without commit (editing=%v committed=%v)", f.Editing(), committed)
	}
	// move down to bypass and commit.
	f.Update(arrowDown())
	_, committed, val := f.Update(enter())
	if !committed || val != "bypass" {
		t.Fatalf("commit val = %q committed=%v, want bypass/true", val, committed)
	}
	if f.Editing() {
		t.Fatal("commit should close the picker")
	}
	if f.Display() != "bypass" {
		t.Fatalf("Display after commit = %q, want bypass", f.Display())
	}
}

func TestSelectFieldEscCancels(t *testing.T) {
	opts := []Option{{Label: "a", Value: "a"}, {Label: "b", Value: "b"}}
	f := NewSelect("k", "k", opts, "a")
	f.Update(enter())
	f.Update(arrowDown())
	_, committed, _ := f.Update(esc())
	if committed || f.Editing() {
		t.Fatalf("esc should cancel (committed=%v editing=%v)", committed, f.Editing())
	}
	if f.Display() != "a" {
		t.Fatalf("Display after cancel = %q, want a", f.Display())
	}
}

func TestSelectFieldEmptyOptionsNoPanic(t *testing.T) {
	f := NewSelect("k", "k", nil, "")
	f.Update(enter()) // open
	_, committed, val := f.Update(enter()) // must not panic
	if committed || val != "" {
		t.Fatalf("empty-options commit should be a no-op, got committed=%v val=%q", committed, val)
	}
}
