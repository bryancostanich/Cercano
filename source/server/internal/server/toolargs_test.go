package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// summarizeArgs must cap oversized string fields (Edit/Write file bodies) but
// keep the result valid JSON and leave short structural fields (path, cmd)
// intact — the CLI json.Unmarshals the wire ArgsSummary to render a tool entry.
func TestSummarizeArgs_CapsBigFieldsKeepsValidJSON(t *testing.T) {
	big := strings.Repeat("x", 10_000)
	in := `{"path":"internal/foo.go","old_string":"` + big + `","new_string":"` + big + `"}`

	out := summarizeArgs(in)

	if len(out) > 2_000 {
		t.Errorf("summary still huge: %d bytes", len(out))
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("summary is not valid JSON: %v\n%s", err, out)
	}
	if m["path"] != "internal/foo.go" {
		t.Errorf("path field mangled: %v", m["path"])
	}
	if s, _ := m["old_string"].(string); len(s) >= 10_000 {
		t.Errorf("old_string was not capped: %d runes", len(s))
	}
}

// Short args pass through untouched — a small Bash/Read/Grep call is left exactly
// as sent (no needless re-marshal churn).
func TestSummarizeArgs_ShortArgsUnchanged(t *testing.T) {
	for _, in := range []string{
		`{"cmd":["go","test","./..."]}`,
		`{"path":"README.md"}`,
		`{"pattern":"TODO","path":"src"}`,
	} {
		if got := summarizeArgs(in); got != in {
			t.Errorf("short args changed:\n in=%s\nout=%s", in, got)
		}
	}
}

// Unparseable args fall back to a rune-capped raw string (never the full blob),
// and never panic.
func TestSummarizeArgs_InvalidJSONFallsBackCapped(t *testing.T) {
	junk := "not json " + strings.Repeat("y", 10_000)
	out := summarizeArgs(junk)
	if len(out) > 2_000 {
		t.Errorf("invalid-JSON fallback not capped: %d bytes", len(out))
	}
}
