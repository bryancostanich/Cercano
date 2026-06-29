package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestRowFieldActivateCommitsSelect(t *testing.T) {
	r := NewRow("cloud-row:template:openai", "openai", "(untested)", false)
	if r.Editing() {
		t.Fatal("RowField is never in editing mode")
	}
	cmd, committed, val := r.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = cmd
	if !committed || val != RowSelect {
		t.Fatalf("enter should commit RowSelect; committed=%v val=%q", committed, val)
	}
}

func TestRowFieldViewShowsAnnotation(t *testing.T) {
	_, s := testStyles()
	r := NewRow("cloud-row:profile:my", "my", "✓ key", false)
	out := r.View(false, 30, s)
	if !strings.Contains(out, "✓ key") {
		t.Fatalf("View should render the annotation; got %q", out)
	}
}

func TestRowFieldNonActivateDoesNotCommit(t *testing.T) {
	r := NewRow("k", "l", "", false)
	if _, committed, _ := r.Update(tea.KeyPressMsg{Code: tea.KeyLeft}); committed {
		t.Fatal("left arrow must not commit")
	}
}
