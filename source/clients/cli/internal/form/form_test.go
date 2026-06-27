package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

func testStyles() (theme.Palette, theme.Styles) {
	p := theme.Cracker()
	return p, theme.NewStyles(p)
}

func TestFormNavSkipsHeadersAndClamps(t *testing.T) {
	sections := []Section{
		{Title: "A", Fields: []Field{NewText("a1", "a1", "v", ""), NewReadOnly("a2", "a2", "v", "")}},
		{Title: "B", Fields: []Field{NewText("b1", "b1", "v", "")}},
	}
	f := New(sections)
	if f.Cursor() != 0 {
		t.Fatalf("initial cursor = %d, want 0", f.Cursor())
	}
	f.Update(arrowDown())
	f.Update(arrowDown())
	if f.Cursor() != 2 {
		t.Fatalf("cursor after 2 downs = %d, want 2 (3 flat fields)", f.Cursor())
	}
	f.Update(arrowDown()) // clamp at last
	if f.Cursor() != 2 {
		t.Fatalf("cursor should clamp at 2, got %d", f.Cursor())
	}
}

func TestFormCommitRoutesToHook(t *testing.T) {
	var gotKey, gotVal string
	f := New([]Section{{Title: "A", Fields: []Field{NewText("a1", "a1", "old", "")}}})
	f.OnCommit = func(key, value string) (string, tea.Cmd, error) {
		gotKey, gotVal = key, value
		return "saved", nil, nil
	}
	f.Update(enter()) // begin edit
	for _, r := range "new" {
		f.Update(typ(r))
	}
	f.Update(enter()) // commit -> OnCommit
	if gotKey != "a1" || gotVal != "oldnew" {
		t.Fatalf("OnCommit got key=%q val=%q, want a1/oldnew", gotKey, gotVal)
	}
}

func TestFormEscClosesWhenNotEditing(t *testing.T) {
	f := New([]Section{{Title: "A", Fields: []Field{NewText("a1", "a1", "v", "")}}})
	_, closed := f.Update(esc())
	if !closed {
		t.Fatal("esc with no field editing should close the form")
	}
}

func TestFormViewRendersSectionTitles(t *testing.T) {
	p, s := testStyles()
	f := New([]Section{{Title: "Cloud", Fields: []Field{NewText("cloud-model", "cloud-model", "x", "")}}})
	out := f.View(80, p, s)
	if !strings.Contains(out, "Cloud") || !strings.Contains(out, "cloud-model") {
		t.Fatalf("View missing section title or field label:\n%s", out)
	}
}
