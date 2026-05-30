package engine

import (
	"encoding/json"
	"testing"
)

func TestChatRequestJSONShape(t *testing.T) {
	req := ChatRequest{
		Model: "qwen3-coder",
		Messages: []ChatMessage{
			{Role: "user", Content: "hello"},
		},
		Tools: []ToolSchemaJSON{
			{Type: "function", Function: ToolFunctionJSON{Name: "x", Description: "y", Parameters: map[string]interface{}{"type": "object"}}},
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(b), `"model":"qwen3-coder"`) {
		t.Errorf("missing model in %s", string(b))
	}
	if !contains(string(b), `"function":`) {
		t.Errorf("missing function in %s", string(b))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
