package llm

import (
	"encoding/json"
	"testing"
)

func TestTool_Fields(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	tl := Tool{Name: "read_file", Description: "Read a file", Schema: schema, Permission: PermR}
	if tl.Name != "read_file" || tl.Permission != PermR {
		t.Errorf("Tool fields: %+v", tl)
	}
}

func TestToolChoice_Constants(t *testing.T) {
	cases := []ToolChoice{
		{Type: ToolChoiceAuto},
		{Type: ToolChoiceAny},
		{Type: ToolChoiceNone},
		{Type: ToolChoiceTool, Name: "read_file"},
	}
	for _, c := range cases {
		if c.Type == "" {
			t.Errorf("empty type")
		}
	}
}
