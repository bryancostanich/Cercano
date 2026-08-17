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
	rd := newStreamReader(io.NopCloser(strings.NewReader(body)), "openai-responses")
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

// In-band error frames surface as CLASSIFIED errors from Next (not EventError
// text), so the resilience engine and the turn runner can apply per-class
// policy — the incident driver was the codex backend's retryable
// "An error occurred while processing your request" arriving mid-stream and
// falling straight through to cross-tier degrade.
func TestStreamReaderError_Classified(t *testing.T) {
	cases := []struct {
		name string
		body string
		want llm.ErrorClass
		text string
	}{
		{"response.failed server-side", "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"boom\"}}}\n\n",
			llm.ErrBusy, "boom"},
		{"codex retryable processing error", "data: {\"type\":\"error\",\"message\":\"An error occurred while processing your request. You can retry your request, or contact us through our help center at help.openai.com if the error persists.\"}\n\n",
			llm.ErrBusy, "You can retry your request"},
		{"quota marker", "data: {\"type\":\"error\",\"code\":\"insufficient_quota\",\"message\":\"You exceeded your current quota.\"}\n\n",
			llm.ErrQuota, "quota"},
		{"context-window invalid request", "data: {\"type\":\"error\",\"code\":\"invalid_request\",\"message\":\"Your input exceeds the context window of this model. Please adjust your input and try again.\"}\n\n",
			llm.ErrContextOverflow, "context window"},
		{"generic invalid marker", "data: {\"type\":\"error\",\"code\":\"invalid_prompt\",\"message\":\"bad input\"}\n\n",
			llm.ErrInvalidRequest, "bad input"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newStreamReader(io.NopCloser(strings.NewReader(tc.body)), "openai-responses")
			_, ok, err := r.Next()
			if ok || err == nil {
				t.Fatalf("want classified error, got ok=%v err=%v", ok, err)
			}
			if got := llm.ClassOf(err); got != tc.want {
				t.Errorf("class = %q, want %q (err: %v)", got, tc.want, err)
			}
			if !strings.Contains(err.Error(), tc.text) {
				t.Errorf("err = %q, want it to carry %q", err, tc.text)
			}
		})
	}
}

// An error frame after content: the delivered events drain first, then the
// classified error surfaces.
func TestStreamReaderError_AfterContent(t *testing.T) {
	body := "data: {\"type\":\"response.created\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
		"data: {\"type\":\"error\",\"message\":\"An error occurred while processing your request. You can retry your request.\"}\n\n"
	r := newStreamReader(io.NopCloser(strings.NewReader(body)), "openai-responses")
	var texts []string
	var finalErr error
	for {
		ev, ok, err := r.Next()
		if err != nil {
			finalErr = err
			break
		}
		if !ok {
			break
		}
		if ev.Type == llm.EventTextDelta {
			texts = append(texts, ev.TextDelta)
		}
	}
	if len(texts) != 1 || texts[0] != "partial" {
		t.Errorf("texts = %v — content before the error must still deliver", texts)
	}
	if llm.ClassOf(finalErr) != llm.ErrBusy {
		t.Errorf("err = %v, want busy class after content", finalErr)
	}
}
