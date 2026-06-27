package form

import (
	"strings"
	"testing"
)

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
	// move right to bypass and commit (options are horizontal → left/right).
	f.Update(arrowRight())
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
	f.Update(arrowRight())
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

func TestSelectFieldLeftRightNav(t *testing.T) {
	opts := []Option{{Label: "a", Value: "a"}, {Label: "b", Value: "b"}, {Label: "c", Value: "c"}}
	f := NewSelect("k", "k", opts, "a") // current index 0
	f.Update(enter())                   // open, cursor 0
	f.Update(arrowRight())              // -> 1
	f.Update(arrowRight())              // -> 2
	f.Update(arrowLeft())               // -> 1
	_, committed, val := f.Update(enter())
	if !committed || val != "b" {
		t.Fatalf("left/right nav: committed=%v val=%q, want b", committed, val)
	}
}

func TestSelectFieldWrapsToWidth(t *testing.T) {
	_, s := testStyles()
	opts := []Option{
		{Label: "cloud_only", Value: "cloud_only"},
		{Label: "cloud_primary", Value: "cloud_primary"},
		{Label: "local_primary", Value: "local_primary"},
		{Label: "local_only", Value: "local_only"},
	}
	f := NewSelect("locus", "locus", opts, "cloud_only")
	f.Update(enter()) // open
	if wide := f.View(true, 200, s); strings.Contains(wide, "\n") {
		t.Fatalf("wide picker should be a single line, got:\n%s", wide)
	}
	if narrow := f.View(true, 18, s); !strings.Contains(narrow, "\n") {
		t.Fatalf("narrow picker should wrap to multiple lines, got:\n%s", narrow)
	}
}
