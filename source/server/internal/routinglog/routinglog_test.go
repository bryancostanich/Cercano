package routinglog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriter_AppendsJSONLineWithoutPromptFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turn-routing.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer w.Close()

	w.Log("turn.selected", Event{
		"conversation_id": "conv-1",
		"provider":        "openai-responses",
		"model":           "gpt-large",
		"is_cloud":        true,
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	line := strings.TrimSpace(string(data))
	if line == "" {
		t.Fatal("expected one JSONL record")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("unmarshal JSONL: %v\n%s", err, line)
	}
	if got["event"] != "turn.selected" || got["conversation_id"] != "conv-1" || got["provider"] != "openai-responses" {
		t.Fatalf("unexpected record: %#v", got)
	}
	if _, ok := got["prompt"]; ok {
		t.Fatalf("routing log must not record prompt bodies: %#v", got)
	}
	if _, ok := got["api_key"]; ok {
		t.Fatalf("routing log must not record API keys: %#v", got)
	}
}
