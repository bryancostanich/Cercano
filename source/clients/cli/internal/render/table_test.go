package render

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/theme"
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

func TestTable_FitsWidth_DrawsHorizontalRulesBetweenDataRows(t *testing.T) {
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
	plain := stripAnsi(tbl.Render(80, theme.NewStyles(theme.Cracker())))
	lines := strings.Split(plain, "\n")
	qwenLine := -1
	nomicLine := -1
	for i, line := range lines {
		if strings.Contains(line, "qwen") {
			qwenLine = i
		}
		if strings.Contains(line, "nomic") {
			nomicLine = i
		}
	}
	if qwenLine < 0 || nomicLine < 0 || qwenLine >= nomicLine {
		t.Fatalf("expected qwen row before nomic row, got %q", plain)
	}
	between := strings.Join(lines[qwenLine+1:nomicLine], "\n")
	if !strings.Contains(between, "├") || !strings.Contains(between, "┼") || !strings.Contains(between, "┤") {
		t.Fatalf("expected horizontal grid rule between data rows, got %q", plain)
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

func TestTable_WideDecisionMatrix_RendersGridAt126(t *testing.T) {
	// A decision matrix: a rigid key column plus several long option columns.
	// Natural widths blow well past 126, but because every option column may
	// wrap, the overflow distributes across them and the table still grids
	// instead of collapsing to a transpose.
	tbl := Table{
		Cols: []Column{
			{Name: "Axis"},
			{Name: "A: Observer list", Wrappable: true},
			{Name: "B: Return event from method", Wrappable: true},
			{Name: "C: Single injected sink", Wrappable: true},
		},
		Rows: []map[string]string{
			{"Axis": "Cost", "A: Observer list": "Medium: listener slice, mutex, subscribe/unsubscribe, ~40 lines", "B: Return event from method": "Low: widen return signatures, no new state", "C: Single injected sink": "Low-Medium: one field + nil-check per mutation, ~15 lines"},
			{"Axis": "Risk", "A: Observer list": "Leaked subscriptions, listener-ordering, lock held during callback deadlock risk", "B: Return event from method": "Caller can silently drop the event (forgets to forward)", "C: Single injected sink": "Sink set once; misuse is hard. Nil sink = no-op"},
			{"Axis": "Reward", "A: Observer list": "Many independent consumers, fully decoupled", "B: Return event from method": "Dead simple; no store-side state at all", "C: Single injected sink": "One clean seam; matches one server bridges reality"},
		},
	}
	plain := stripAnsi(tbl.Render(126, theme.NewStyles(theme.Cracker())))
	// It must be a grid, not a transpose.
	if !strings.Contains(plain, "┌") || !strings.Contains(plain, "┼") || !strings.Contains(plain, "┘") {
		t.Fatalf("expected a grid at 126 cols, got transpose/other:\n%s", plain)
	}
	// No line may exceed the budget.
	for _, line := range strings.Split(plain, "\n") {
		if w := len([]rune(line)); w > 126 {
			t.Errorf("line exceeds 126 cols (%d): %q", w, line)
		}
	}
	// Nothing dropped: distinctive words from every column survive.
	for _, want := range []string{"subscribe", "signatures", "nil-check", "deadlock", "decoupled", "bridges"} {
		if !strings.Contains(plain, want) {
			t.Errorf("expected %q preserved, got:\n%s", want, plain)
		}
	}
}

func TestTable_LongHeaders_WrapWithinBudget(t *testing.T) {
	// Regression: a wrappable column whose HEADER name is longer than the width
	// shrinkWrappable leaves it must wrap the header across lines, not overflow
	// the cell border. Mirrors the screenshot where a long option-column header
	// ran past the right border while body cells wrapped fine.
	tbl := Table{
		Cols: []Column{
			{Name: "Axis"},
			{Name: "Keep fraction only (fix `PagedAttn` default)", Wrappable: true},
			{Name: "Add absolute `pa_memory_mb`, keep fraction as fallback", Wrappable: true},
		},
		Rows: []map[string]string{
			{"Axis": "Cost", "Keep fraction only (fix `PagedAttn` default)": "Very low: 1-line default change + tests", "Add absolute `pa_memory_mb`, keep fraction as fallback": "Medium-high: field swap + migrate existing configs/tests"},
			{"Axis": "Risk", "Keep fraction only (fix `PagedAttn` default)": "Low: reuses proven path", "Add absolute `pa_memory_mb`, keep fraction as fallback": "Med: breaks existing configs that set fraction"},
		},
	}
	const budget = 100
	plain := stripAnsi(tbl.Render(budget, theme.NewStyles(theme.Cracker())))
	// Must be a grid (headers are the case we care about).
	if !strings.Contains(plain, "┌") || !strings.Contains(plain, "┘") {
		t.Fatalf("expected a grid, got:\n%s", plain)
	}
	// No line — header lines included — may exceed the budget.
	for _, line := range strings.Split(plain, "\n") {
		if w := len([]rune(line)); w > budget {
			t.Errorf("line exceeds %d cols (%d): %q", budget, w, line)
		}
	}
	// Every header word survives (wrapped, never truncated).
	for _, want := range []string{"fraction", "PagedAttn", "absolute", "pa_memory_mb", "fallback"} {
		if !strings.Contains(plain, want) {
			t.Errorf("expected header word %q preserved, got:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "…") {
		t.Errorf("headers should wrap, not truncate; got:\n%s", plain)
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

// TestTable_LongFirstColumnGridsThenTransposes is the regression for the
// CERCANO - LUNIE FIXES mangle: a table whose FIRST column held ~180 chars of
// prose. Before all-columns-wrappable, column 0 was rigid and forced
// naturalGridW well past any terminal, so it always transposed. Now it must
// grid at a normal width and only transpose when genuinely too narrow.
func TestTable_LongFirstColumnGridsThenTransposes(t *testing.T) {
	long := "Detection of no-op sub-agent runs: if a sub-agent is granted Edit or Bash " +
		"or asked to trace or investigate but only calls read tools once, flag the run " +
		"as incomplete instead of returning a silent success placeholder here"
	tbl := Table{
		Cols: []Column{
			{Name: "Fix", Wrappable: true},
			{Name: "What it addresses", Wrappable: true},
			{Name: "Cost", Wrappable: true},
		},
		Rows: []map[string]string{
			{"Fix": long, "What it addresses": "silent sub-agent no-ops that look like success", "Cost": "medium"},
			{"Fix": "shorter one", "What it addresses": "another concern", "Cost": "low"},
		},
	}
	st := theme.NewStyles(theme.Cracker())

	// Wide terminal: must GRID now (borders present), not transpose.
	wide := stripAnsi(tbl.Render(120, st))
	if !strings.Contains(wide, "\u250c") || !strings.Contains(wide, "\u2518") {
		t.Errorf("expected grid at width 120, got transpose:\n%s", wide)
	}
	if !strings.Contains(wide, "incomplete") || !strings.Contains(wide, "medium") {
		t.Errorf("grid dropped data at width 120:\n%s", wide)
	}

	// Genuinely narrow: 3 cols * (16+2) + 4 = 58 min; at 40 it cannot grid,
	// so it must transpose to key:value (no top border, has \"Fix:\" label).
	narrow := stripAnsi(tbl.Render(40, st))
	if strings.Contains(narrow, "\u250c") {
		t.Errorf("expected transpose at width 40, got grid:\n%s", narrow)
	}
	if !strings.Contains(narrow, "Fix:") {
		t.Errorf("expected key:value labels at width 40:\n%s", narrow)
	}
	// Nothing lost in either mode.
	if !strings.Contains(narrow, "incomplete") {
		t.Errorf("transpose dropped data at width 40:\n%s", narrow)
	}
}
