package render

import (
	"strings"
	"testing"

	"cercano/source/server/internal/cli/theme"
)

func TestTable_FitsWidth_RendersGrid(t *testing.T) {
	tbl := Table{
		Cols: []Column{
			{Name: "name"},
			{Name: "size"},
		},
		Rows: []map[string]string{
			{"name": "qwen", "size": "4.7GB"},
			{"name": "nomic", "size": "274MB"},
		},
	}
	out := tbl.Render(80, theme.NewStyles(theme.Cracker()))
	if !strings.Contains(stripAnsi(out), "qwen") || !strings.Contains(stripAnsi(out), "274MB") {
		t.Errorf("missing data rows: %q", stripAnsi(out))
	}
	if !strings.Contains(stripAnsi(out), "┌") || !strings.Contains(stripAnsi(out), "┘") {
		t.Errorf("grid borders missing: %q", stripAnsi(out))
	}
}

func TestTable_TooWide_DropsNothing(t *testing.T) {
	tbl := Table{
		Cols: []Column{
			{Name: "essential"},
			{Name: "secondary"},
		},
		Rows: []map[string]string{
			{"essential": "VA", "secondary": "VB"},
		},
	}
	plain := stripAnsi(tbl.Render(20, theme.NewStyles(theme.Cracker())))
	// No data is ever dropped — both columns and both values survive (the table
	// transposes if it can't fit as a grid), and there is no "dropped" footnote.
	for _, want := range []string{"essential", "VA", "secondary", "VB"} {
		if !strings.Contains(plain, want) {
			t.Errorf("expected %q preserved, got %q", want, plain)
		}
	}
	if strings.Contains(plain, "dropped") {
		t.Errorf("nothing should be dropped, got %q", plain)
	}
}

func TestTable_WrappableWrapsToFit(t *testing.T) {
	tbl := Table{
		Cols: []Column{
			{Name: "id"},
			{Name: "desc", Wrappable: true},
		},
		Rows: []map[string]string{
			{"id": "01", "desc": "a very long description that should wrap"},
		},
	}
	out := stripAnsi(tbl.Render(30, theme.NewStyles(theme.Cracker())))
	if !strings.Contains(out, "id") || !strings.Contains(out, "desc") {
		t.Errorf("expected both cols to remain via wrapping; got %q", out)
	}
	// Wrap, never truncate: no ellipsis, and every word survives.
	if strings.Contains(out, "…") {
		t.Errorf("expected wrapping, not truncation; got %q", out)
	}
	for _, word := range []string{"description", "should", "wrap"} {
		if !strings.Contains(out, word) {
			t.Errorf("expected word %q preserved by wrapping; got %q", word, out)
		}
	}
	// Wrapping produces a multi-line row → more than the single-row grid height.
	if strings.Count(out, "\n") < 5 {
		t.Errorf("expected a multi-line wrapped row; got %q", out)
	}
}

func TestTable_TransposesWhenStillTooNarrow(t *testing.T) {
	// Wide column names + narrow target → even the highest-priority column
	// won't fit in grid form (border + 2-col pad + header text > maxWidth),
	// forcing the transpose path.
	tbl := Table{
		Cols: []Column{
			{Name: "primary-key-column"},
			{Name: "secondary-data"},
		},
		Rows: []map[string]string{
			{"primary-key-column": "v1", "secondary-data": "v2"},
		},
	}
	out := stripAnsi(tbl.Render(6, theme.NewStyles(theme.Cracker())))
	// Transposed form looks like "primary-key-column:\n     v1\nsecondary-data:..."
	if !strings.Contains(out, "primary-key-column:") {
		t.Errorf("expected transposed key:value form, got %q", out)
	}
	// No box-draw chars in transposed form.
	if strings.Contains(out, "┌") || strings.Contains(out, "│") {
		t.Errorf("transpose path should not draw grid borders, got %q", out)
	}
}

func TestTable_Empty(t *testing.T) {
	out := Table{}.Render(80, theme.NewStyles(theme.Cracker()))
	if !strings.Contains(stripAnsi(out), "empty") {
		t.Errorf("expected empty-table placeholder: %q", stripAnsi(out))
	}
}

func TestWrapCell(t *testing.T) {
	// Word wrap on spaces.
	if got := wrapCell("hello world", 5); len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Errorf("wrapCell word-wrap: %q", got)
	}
	// Hard-break a word wider than the column.
	if got := wrapCell("abcdefgh", 5); len(got) != 2 || got[0] != "abcde" || got[1] != "fgh" {
		t.Errorf("wrapCell hard-break: %q", got)
	}
	// Fits on one line.
	if got := wrapCell("hi", 10); len(got) != 1 || got[0] != "hi" {
		t.Errorf("wrapCell single line: %q", got)
	}
}

// stripAnsi removes ANSI CSI sequences from a string so tests can match literal text.
func stripAnsi(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7E) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
