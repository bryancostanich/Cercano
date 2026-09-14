package form

import (
	tea "charm.land/bubbletea/v2"
	"errors"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
)

func pairFixture() *SelectPairField {
	return NewSelectPair("task", "Review", NewSelect("task-destination", "Model tier", []Option{{Label: "Secondary (overridden)", Value: "secondary"}, {Label: "Primary", Value: "primary"}}, "secondary"), NewSelect("task-quality", "Quality", []Option{{Label: "Light", Value: "economy"}, {Label: "Premium", Value: "premium"}}, "economy"), true, "")
}
func TestSelectPairIndependentEditAndReloadFocus(t *testing.T) {
	for _, fail := range []bool{false, true} {
		row := pairFixture()
		f := New([]Section{{Title: "Tasks", ColumnHeadings: [3]string{"Task", "Model tier", "Quality"}, Fields: []Field{row}}})
		var key, value string
		f.OnCommit = func(k, v string) (string, tea.Cmd, error) {
			key, value = k, v
			if fail {
				return "", nil, errors.New("fixture failure")
			}
			return "saved", nil, nil
		}
		f.OnReload = func() []Section {
			return []Section{{Title: "Tasks", ColumnHeadings: [3]string{"Task", "Model tier", "Quality"}, Fields: []Field{pairFixture()}}}
		}
		f.Update(tea.KeyPressMsg{Code: tea.KeyRight}) // focus Quality, not select an option yet
		if row.FocusPart() != 1 || row.Right.Display() != "Light" {
			t.Fatal("column navigation changed value")
		}
		f.Update(enter())
		f.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		f.Update(enter())
		if key != "task-quality" || value != "premium" {
			t.Fatalf("wrong component commit %s=%s", key, value)
		}
		rebuilt := f.flat()[0].(*SelectPairField)
		if rebuilt.FocusPart() != 1 {
			t.Fatal("reload lost active column")
		}
		f.Update(enter())
		f.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		if rebuilt.Editing() {
			t.Fatal("escape did not cancel picker")
		}
		f.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
		if key != "task-reset" {
			t.Fatalf("reset wrong key %s", key)
		}
	}
}
func TestSelectPairCompactAndNarrowRendering(t *testing.T) {
	palette, styles := testStyles()
	row := pairFixture()
	for _, width := range []int{45, 55, 15, 30} {
		view := ansi.Strip(row.View(false, width, styles))
		if width >= pairMinWidth {
			if strings.Contains(view, "\n") || !strings.Contains(view, "Secondary (overridden)") || !strings.Contains(view, "Light") {
				t.Fatalf("not compact: %q", view)
			}
		} else if !strings.Contains(view, "Model tier:") || !strings.Contains(view, "Quality:") {
			t.Fatalf("narrow values unlabeled: %q", view)
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("overflow width %d: %q", width, line)
			}
		}
	}
	row.Note = "Redirected to Primary"
	if !strings.Contains(ansi.Strip(row.View(false, 50, styles)), row.Note) {
		t.Fatal("redirect note missing")
	}
	if strings.Contains(ansi.Strip(row.View(false, 50, styles)), "Restore defaults") {
		t.Fatal("reset action clutters unfocused rows")
	}
	if !strings.Contains(ansi.Strip(row.View(true, 80, styles)), "Restore defaults") {
		t.Fatal("selected task reset undiscoverable")
	}
	f := New([]Section{{Title: "Tasks", ColumnHeadings: [3]string{"Task", "Model tier", "Quality"}, Fields: []Field{row}}})
	view := ansi.Strip(f.View(100, palette, styles))
	lines := strings.Split(view, "\n")
	if !strings.Contains(lines[f.FocusedLine()], "Review") {
		t.Fatalf("table headings broke focused line: %d %q", f.FocusedLine(), view)
	}
}
func TestSelectPairNoResetWhenAtDefaults(t *testing.T) {
	p := pairFixture()
	p.Resettable = false
	_, commit, _ := p.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if commit {
		t.Fatal("default task reset should not commit")
	}
}
