package dispatch

import (
	"encoding/json"
	"testing"
)

func TestEventKindString(t *testing.T) {
	cases := map[EventKind]string{
		EventTextChunk:  "text_chunk",
		EventToolCall:   "tool_call",
		EventToolResult: "tool_result",
		EventDone:       "done",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("EventKind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

func TestEventMarshalsToJSON(t *testing.T) {
	ev := Event{
		Kind:       EventToolCall,
		ToolCallID: "tc_1",
		ToolName:   "read_file",
		ToolArgs:   json.RawMessage(`{"path":"/x"}`),
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(b), `"kind":"tool_call"`) {
		t.Errorf("expected kind:tool_call in %s", string(b))
	}
	if !contains(string(b), `"tool_name":"read_file"`) {
		t.Errorf("expected tool_name:read_file in %s", string(b))
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
