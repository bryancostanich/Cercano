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

func TestFormOpenSelectKeepsFocusedLineOnLabel(t *testing.T) {
	p, s := testStyles()
	sel := NewSelect("locus", "locus", []Option{
		{Label: "cloud_only", Value: "cloud_only"},
		{Label: "cloud_primary", Value: "cloud_primary"},
		{Label: "open_primary", Value: "open_primary"},
		{Label: "open_only", Value: "open_only"},
	}, "cloud_only")
	f := New([]Section{{Title: "Routing", Fields: []Field{sel}}})
	f.Update(enter()) // open the picker (focus is on sel)
	if !sel.Editing() {
		t.Fatal("select should be open after enter")
	}
	out := f.View(50, p, s) // narrow enough that options wrap to multiple lines
	if !strings.Contains(out, "cloud_only") || !strings.Contains(out, "open_only") {
		t.Fatalf("open picker should render all options:\n%s", out)
	}
	// The focused line must still point at the field's label line, not drift
	// because the picker added lines below it. The section header is 3 lines
	// (title, rule, blank) plus the box's top border, so the first field is at
	// line 4.
	if f.FocusedLine() != 4 {
		t.Fatalf("focusedLine = %d, want 4 (label line of the open select)", f.FocusedLine())
	}
}

func TestFormSetCursorClamps(t *testing.T) {
	f := New([]Section{{Title: "A", Fields: []Field{NewText("a", "a", "v", ""), NewText("b", "b", "v", "")}}})
	f.SetCursor(1)
	if f.Cursor() != 1 {
		t.Fatalf("SetCursor(1) -> %d, want 1", f.Cursor())
	}
	f.SetCursor(99)
	if f.Cursor() != 1 {
		t.Fatalf("SetCursor(99) should clamp to last (1), got %d", f.Cursor())
	}
	f.SetCursor(-5)
	if f.Cursor() != 0 {
		t.Fatalf("SetCursor(-5) should clamp to 0, got %d", f.Cursor())
	}
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

func TestFormCommitTriggersReload(t *testing.T) {
	reloaded := []Section{{Title: "B", Fields: []Field{NewReadOnly("b1", "b1", "v", "")}}}
	f := New([]Section{{Title: "A", Fields: []Field{NewText("a1", "a1", "old", "")}}})
	f.OnCommit = func(key, value string) (string, tea.Cmd, error) { return "saved", nil, nil }
	f.OnReload = func() []Section { return reloaded }
	f.Update(enter())          // begin edit
	f.Update(enter())          // commit -> OnCommit -> OnReload
	if len(f.Sections) != 1 || f.Sections[0].Title != "B" {
		t.Fatalf("OnReload should have replaced Sections with B, got %+v", f.Sections)
	}
}

// A section with Groups must (a) render the group title as a subheading,
// (b) render each group's fields, and (c) let nav flow through the flattened
// group fields as if the whole section were flat.
func TestFormGroupsRenderAndNavigate(t *testing.T) {
	p, s := testStyles()
	sections := []Section{
		{Title: "Development Tools", Groups: []Group{
			{Title: "Context Management", Fields: []Field{
				NewToggle("elide", "elide-tool-results", false),
			}},
			{Title: "Diagnostics", Fields: []Field{
				NewText("verbose", "verbose", "off", ""),
			}},
		}},
	}
	f := New(sections)
	out := f.View(80, p, s)
	for _, want := range []string{"Development Tools", "Context Management", "elide-tool-results", "Diagnostics", "verbose"} {
		if !strings.Contains(out, want) {
			t.Errorf("View missing %q\n%s", want, out)
		}
	}
	// flat() must include both group fields, so nav lands on the second one.
	f.Update(arrowDown())
	if f.Cursor() != 1 {
		t.Errorf("cursor after one down = %d, want 1 (into second group's field)", f.Cursor())
	}
}

// Falling back to Fields when Groups is empty must still work — this
// backwards-compatibility path is what every existing section relies on.
func TestFormFieldsWithoutGroupsStillRender(t *testing.T) {
	p, s := testStyles()
	f := New([]Section{{Title: "Plain", Fields: []Field{NewText("k", "k", "v", "")}}})
	out := f.View(80, p, s)
	if !strings.Contains(out, "Plain") || !strings.Contains(out, "k") {
		t.Fatalf("plain (no-groups) section should still render:\n%s", out)
	}
}

func TestFormFocusedLineTracksCursor(t *testing.T) {
	p, s := testStyles()
	sections := []Section{
		{Title: "A", Fields: []Field{NewText("a1", "a1", "v", ""), NewText("a2", "a2", "v", "")}},
		{Title: "B", Fields: []Field{NewText("b1", "b1", "v", "")}},
	}
	f := New(sections)
	f.View(80, p, s)
	first := f.FocusedLine()
	if first != 4 {
		t.Fatalf("focused line for first field = %d, want 4 (top border + title + rule + blank)", first)
	}
	f.Update(arrowDown())
	f.Update(arrowDown()) // into section B's first field
	f.View(80, p, s)
	if f.FocusedLine() <= first {
		t.Fatalf("focused line should grow as cursor descends across sections; got %d (was %d)", f.FocusedLine(), first)
	}
}
