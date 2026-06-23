package agenttools

import (
	"strings"
	"testing"
)

// The tool loop feeds Result.LLMContent() back to the model as the tool result.
// Rows/JSON results must serialize to non-empty text, or the model sees nothing
// and re-calls the tool — which loops it to the iteration cap.

func TestLLMContent_Rows(t *testing.T) {
	r := NewRowsResult([]map[string]any{
		{"name": "README.md", "type": "file"},
		{"name": "docs", "type": "dir"},
	})
	got := r.LLMContent()
	if got == "" {
		t.Fatal("rows result produced empty LLMContent — model would see nothing")
	}
	if !strings.Contains(got, "README.md") || !strings.Contains(got, "docs") {
		t.Errorf("LLMContent missing row data: %q", got)
	}
}

func TestLLMContent_Text(t *testing.T) {
	if got := NewTextResult("hello").LLMContent(); got != "hello" {
		t.Errorf("text LLMContent = %q, want %q", got, "hello")
	}
}

func TestLLMContent_RowsTruncationNote(t *testing.T) {
	rows := make([]map[string]any, 250)
	for i := range rows {
		rows[i] = map[string]any{"name": "f"}
	}
	got := NewRowsResult(rows).LLMContent()
	if !strings.Contains(got, "showed first 200") {
		t.Errorf("truncation note not surfaced to model: %q", got)
	}
}
