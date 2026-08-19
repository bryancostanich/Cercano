package agenttools

import (
	"encoding/json"
	"strings"
	"testing"
)

// grepRow mimics the shape grep/git_read/fs_read produce.
func grepRow(path string, line int, content string) map[string]any {
	return map[string]any{"path": path, "line": line, "content": content}
}

// incidentRows reproduces the production failure: 107 rows (under the 200-row
// cap, so the old row-count ceiling never fired), one ~20 KB minified value,
// ~346 KB total. See efforts/cap-tool-result-payloads/spec.md.
func incidentRows() []map[string]any {
	rows := make([]map[string]any, 0, 107)
	rows = append(rows, grepRow(
		"internal/compaction/testdata/real_conversation.json", 1,
		strings.Repeat("x", 20501),
	))
	for i := 1; i < 107; i++ {
		rows = append(rows, grepRow("source/server/internal/thing.go", i, strings.Repeat("y", 3000)))
	}
	return rows
}

func TestTruncateRows_IncidentShapeIsBounded(t *testing.T) {
	in := incidentRows()

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 300_000 {
		t.Fatalf("fixture too small to reproduce the incident: %d bytes", len(raw))
	}

	got := TruncateRows(in)

	out, err := json.Marshal(got.Rows)
	if err != nil {
		t.Fatalf("truncated rows must stay valid JSON: %v", err)
	}
	// Body must land within the ceiling, with headroom for the per-row
	// accounting overhead the budget loop allows.
	if len(out) > MaxResultBytes+MaxRowValueBytes {
		t.Fatalf("truncated body = %d bytes, want <= ~%d", len(out), MaxResultBytes)
	}
	if !got.Truncated {
		t.Fatal("Truncated must be set")
	}
	if got.TotalRows != 107 {
		t.Fatalf("TotalRows = %d, want 107", got.TotalRows)
	}
	if got.KeptRows >= got.TotalRows {
		t.Fatalf("KeptRows = %d, want fewer than %d", got.KeptRows, got.TotalRows)
	}
	t.Logf("incident: %d bytes/%d rows -> %d bytes/%d rows",
		len(raw), len(in), len(out), got.KeptRows)
}

func TestTruncateRows_NoteNamesCountsAndRemedy(t *testing.T) {
	got := TruncateRows(incidentRows())

	if !strings.Contains(got.Note, "of 107 rows") {
		t.Errorf("note must report the true total; got %q", got.Note)
	}
	// The incident query had neither path nor glob — naming them is the
	// actionable part of the message.
	if !strings.Contains(got.Note, "path") || !strings.Contains(got.Note, "glob") {
		t.Errorf("note must name the path/glob remedy; got %q", got.Note)
	}
}

func TestTruncateRows_OversizedValueTrimmedRowStaysValid(t *testing.T) {
	rows := []map[string]any{grepRow("a.json", 1, strings.Repeat("z", 20501))}

	got := TruncateRows(rows)

	if got.TrimmedVal != 1 {
		t.Fatalf("TrimmedVal = %d, want 1", got.TrimmedVal)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("the single row must be kept, got %d rows", len(got.Rows))
	}
	content, ok := got.Rows[0]["content"].(string)
	if !ok {
		t.Fatal("content must remain a string")
	}
	if len(content) > MaxRowValueBytes+64 {
		t.Fatalf("trimmed value = %d bytes, want <= ~%d", len(content), MaxRowValueBytes)
	}
	if !strings.Contains(content, "truncated") {
		t.Error("trimmed value must be marked as truncated")
	}
	// Sibling keys must survive intact.
	if got.Rows[0]["path"] != "a.json" || got.Rows[0]["line"] != 1 {
		t.Errorf("non-trimmed fields corrupted: %v", got.Rows[0])
	}
	if _, err := json.Marshal(got.Rows); err != nil {
		t.Fatalf("row must stay valid JSON: %v", err)
	}
}

func TestTruncateRows_DoesNotMutateCallerRows(t *testing.T) {
	original := strings.Repeat("z", 20501)
	rows := []map[string]any{grepRow("a.json", 1, original)}

	TruncateRows(rows)

	if rows[0]["content"].(string) != original {
		t.Fatal("TruncateRows must not mutate the caller's rows (copy-on-write)")
	}
}

func TestTruncateRows_SmallResultUnchanged(t *testing.T) {
	rows := []map[string]any{
		grepRow("a.go", 1, "package main"),
		grepRow("b.go", 2, "func main() {}"),
	}
	before, _ := json.Marshal(rows)

	got := TruncateRows(rows)

	after, _ := json.Marshal(got.Rows)
	if string(before) != string(after) {
		t.Fatalf("small results must pass through byte-identical:\n before %s\n after  %s", before, after)
	}
	if got.Truncated {
		t.Error("Truncated must be false for a small result")
	}
	if got.Note != "" {
		t.Errorf("Note must be empty for a small result, got %q", got.Note)
	}
}

func TestTruncateRows_RowCountCapStillFires(t *testing.T) {
	rows := make([]map[string]any, 500)
	for i := range rows {
		rows[i] = grepRow("a.go", i, "short")
	}

	got := TruncateRows(rows)

	if got.KeptRows != MaxRows {
		t.Fatalf("KeptRows = %d, want %d", got.KeptRows, MaxRows)
	}
	if !got.Truncated || !strings.Contains(got.Note, "of 500 rows") {
		t.Errorf("row-count cap note wrong: %q", got.Note)
	}
}

func TestTruncateRows_KeepsFirstRowEvenIfHuge(t *testing.T) {
	// Returning zero rows would make the model re-call the tool and burn
	// iterations — the exact failure this effort prevents.
	rows := []map[string]any{
		{"content": strings.Repeat("q", MaxResultBytes*3)},
		{"content": "second"},
	}

	got := TruncateRows(rows)

	if len(got.Rows) == 0 {
		t.Fatal("must never return zero rows when input was non-empty")
	}
}

func TestTruncateRows_EmptyAndNil(t *testing.T) {
	for _, in := range [][]map[string]any{nil, {}} {
		got := TruncateRows(in)
		if got.Truncated || got.Note != "" || len(got.Rows) != 0 {
			t.Fatalf("empty input must pass through cleanly, got %+v", got)
		}
	}
}

func TestTruncateRows_PreservesOrder(t *testing.T) {
	rows := make([]map[string]any, 50)
	for i := range rows {
		rows[i] = grepRow("a.go", i, strings.Repeat("w", 2000))
	}

	got := TruncateRows(rows)

	for i, row := range got.Rows {
		if row["line"] != i {
			t.Fatalf("row order changed at %d: %v", i, row)
		}
	}
}

func TestTruncateUTF8_StopsOnBoundary(t *testing.T) {
	s := strings.Repeat("é", 100) // 2 bytes each
	got := TruncateUTF8(s, 51)    // lands mid-rune
	if !utf8Valid(got) {
		t.Fatalf("result is not valid UTF-8: %q", got)
	}
	if len(got) > 51 {
		t.Fatalf("len = %d, want <= 51", len(got))
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}
