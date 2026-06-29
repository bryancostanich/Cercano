package responses

import (
	"io"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

// sseFixture is a recorded Responses SSE stream: a text delta, a function call
// (added → args delta → done), a reasoning item done, then completed w/ usage.
const sseFixture = `event: response.created
data: {"type":"response.created","response":{"id":"resp_1"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"Hel"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"lo"}

event: response.output_item.added
data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_1","name":"get_weather"}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","delta":"{\"city\":"}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","delta":"\"Paris\"}"}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}

event: response.output_item.done
data: {"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_1","encrypted_content":"ENC"}}

event: response.completed
data: {"type":"response.completed","response":{"usage":{"input_tokens":9,"output_tokens":4}}}

`

func collect(t *testing.T, body string) []llm.StreamEvent {
	t.Helper()
	rd := newStreamReader(io.NopCloser(strings.NewReader(body)))
	defer rd.Close()
	var evs []llm.StreamEvent
	for {
		ev, ok, err := rd.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if !ok {
			break
		}
		evs = append(evs, ev)
	}
	return evs
}

func TestStreamReader(t *testing.T) {
	evs := collect(t, sseFixture)

	var text string
	var toolArgs string
	var sawStart, sawToolStart, sawToolStop, sawReasoning, sawStop bool
	var in, out int
	var reasoningID, reasoningData string
	for _, ev := range evs {
		switch ev.Type {
		case llm.EventMessageStart:
			sawStart = true
		case llm.EventTextDelta:
			text += ev.TextDelta
		case llm.EventToolUseStart:
			sawToolStart = true
			if ev.ToolUseID != "call_1" || ev.ToolName != "get_weather" {
				t.Errorf("tool start = %+v", ev)
			}
		case llm.EventToolUseInputDelta:
			toolArgs += ev.TextDelta
		case llm.EventToolUseStop:
			sawToolStop = true
		case llm.EventReasoning:
			sawReasoning = true
			reasoningID, reasoningData = ev.ReasoningID, ev.ReasoningData
		case llm.EventMessageStop:
			sawStop = true
			in, out = ev.InputTokens, ev.OutputTokens
		}
	}
	if !sawStart || !sawToolStart || !sawToolStop || !sawReasoning || !sawStop {
		t.Fatalf("missing events: start=%v toolStart=%v toolStop=%v reasoning=%v stop=%v", sawStart, sawToolStart, sawToolStop, sawReasoning, sawStop)
	}
	if text != "Hello" {
		t.Errorf("text = %q", text)
	}
	if toolArgs != `{"city":"Paris"}` {
		t.Errorf("toolArgs = %q", toolArgs)
	}
	if reasoningID != "rs_1" || reasoningData != "ENC" {
		t.Errorf("reasoning = %q/%q", reasoningID, reasoningData)
	}
	if in != 9 || out != 4 {
		t.Errorf("usage = %d/%d", in, out)
	}
}

func TestStreamReaderError(t *testing.T) {
	body := "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"boom\"}}}\n\n"
	evs := collect(t, body)
	if len(evs) != 1 || evs[0].Type != llm.EventError || !strings.Contains(evs[0].ErrText, "boom") {
		t.Fatalf("events = %+v", evs)
	}
}
