package mcpadapter

import (
	"encoding/json"
	"testing"
)

func TestCapMetaToolName(t *testing.T) {
	m := CapMeta{Name: "read_file", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)}
	if ToolName(m) != "cercano_read_file" {
		t.Fatalf("tool name = %q", ToolName(m))
	}
}

func TestToolNamePrefix(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"grep", "cercano_grep"},
		{"git_status", "cercano_git_status"},
		{"write_file", "cercano_write_file"},
	}
	for _, tc := range cases {
		m := CapMeta{Name: tc.name, Description: "d", Schema: json.RawMessage(`{"type":"object"}`)}
		if got := ToolName(m); got != tc.want {
			t.Errorf("ToolName(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}
