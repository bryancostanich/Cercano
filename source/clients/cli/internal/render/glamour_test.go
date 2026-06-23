package render

import (
	"strings"
	"testing"

	"charm.land/glamour/v2/styles"
)

func TestMarkdown_RendersBoldAndHeading(t *testing.T) {
	md := NewMarkdown(styles.DraculaStyleConfig)
	out := md.Render("# Title\n\nsome **bold** text\n", 80)
	if strings.Contains(out, "# Title") {
		t.Fatalf("heading markdown not transformed: %q", out)
	}
	if !strings.Contains(out, "Title") || !strings.Contains(out, "bold") {
		t.Fatalf("expected rendered words present: %q", out)
	}
	// ANSI escape sequences indicate styling was applied.
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected ANSI styling in output: %q", out)
	}
}

func TestMarkdown_CacheReturnsConsistent(t *testing.T) {
	md := NewMarkdown(styles.DraculaStyleConfig)
	a := md.Render("**x**", 40)
	b := md.Render("**x**", 40)
	if a != b {
		t.Fatalf("cached render differs:\n%q\n%q", a, b)
	}
}

// Documents why we keep render.Table. Glamour renders a 4-column table fine when
// it fits, but at a narrow width it truncates every cell to "…" instead of
// dropping low-priority columns or transposing — the result is unreadable.
// render.Table degrades gracefully; Glamour does not.
func TestMarkdown_MananglesNarrowTables(t *testing.T) {
	md := NewMarkdown(styles.DraculaStyleConfig)
	table := "| AAAA | BBBB | CCCC | DDDD |\n| --- | --- | --- | --- |\n| 1 | 2 | 3 | 4 |\n"

	// Wide enough: headers survive intact.
	wide := md.Render(table, 80)
	for _, h := range []string{"AAAA", "BBBB", "CCCC", "DDDD"} {
		if !strings.Contains(wide, h) {
			t.Fatalf("expected header %q present at width 80: %q", h, wide)
		}
	}

	// Narrow: Glamour cannot keep the table readable — headers are lost.
	narrow := md.Render(table, 20)
	intact := 0
	for _, h := range []string{"AAAA", "BBBB", "CCCC", "DDDD"} {
		if strings.Contains(narrow, h) {
			intact++
		}
	}
	if intact == len([]string{"AAAA", "BBBB", "CCCC", "DDDD"}) {
		t.Fatalf("expected Glamour to mangle the narrow table, but all headers survived: %q", narrow)
	}
}
