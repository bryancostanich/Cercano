package responses

// Integration test between the Responses adapter's stream reader and the
// resilience retry wrapper: a real in-band OpenAI overload frame ("Our servers
// are currently overloaded. Please try again later.") arriving right after
// response.created — before any content — must take the busy path (one
// narrated same-provider retry), not surface to the user. The framing event is
// content-free, so replaying the fresh attempt cannot duplicate delivered text.

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/inference/resilience"
	"cercano/source/server/internal/llm"
)

// busyThenRecoveredSSE is what the wire looked like: response.created, then the
// overload error frame, stream over.
const busyThenRecoveredSSE = `event: response.created
data: {"type":"response.created","response":{"id":"resp_1"}}

data: {"type":"error","message":"Our servers are currently overloaded. Please try again later."}

`

const recoveredSSE = `event: response.created
data: {"type":"response.created","response":{"id":"resp_2"}}

data: {"type":"response.output_text.delta","delta":"recovered"}

data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":5,"output_tokens":1}}}

`

type sseProvider struct {
	calls  int
	bodies []string
}

func (p *sseProvider) Name() string                         { return "openai-responses" }
func (p *sseProvider) Capabilities() inference.Capabilities { return inference.Capabilities{} }
func (p *sseProvider) Chat(context.Context, inference.Call) (inference.Result, error) {
	return inference.Result{}, nil
}
func (p *sseProvider) StreamChat(context.Context, inference.Call) (inference.Stream, error) {
	body := p.bodies[min(p.calls, len(p.bodies)-1)]
	p.calls++
	return newStreamReader(io.NopCloser(strings.NewReader(body)), "openai-responses"), nil
}

func TestStreamResilience_InBandOverloadAfterResponseCreatedRetries(t *testing.T) {
	primary := &sseProvider{bodies: []string{busyThenRecoveredSSE, recoveredSSE}}
	var events []resilience.Event
	p := resilience.New(primary, resilience.Options{
		RetryWait:      10 * time.Millisecond,
		PrimaryBlocked: false,
		OnEvent:        func(e resilience.Event) { events = append(events, e) },
	})

	r, err := p.StreamChat(context.Background(), inference.Call{})
	if err != nil {
		t.Fatalf("StreamChat dial err = %v", err)
	}
	defer r.Close()

	var texts []string
	var notices []string
	for {
		ev, ok, err := r.Next()
		if err != nil {
			t.Fatalf("Next err = %v — the overload frame must retry, not surface", err)
		}
		if !ok {
			break
		}
		switch ev.Type {
		case llm.EventNotice:
			notices = append(notices, ev.Notice)
		case llm.EventTextDelta:
			texts = append(texts, ev.TextDelta)
		}
	}
	if primary.calls != 2 {
		t.Errorf("provider calls = %d, want exactly one busy retry", primary.calls)
	}
	if len(notices) != 1 || notices[0] != "openai-responses server busy — trying once more" {
		t.Errorf("notices = %v, want one narrated busy retry", notices)
	}
	if len(texts) != 1 || texts[0] != "recovered" {
		t.Errorf("texts = %v, want the retry's content", texts)
	}
	if len(events) != 1 || events[0].Action != resilience.ActionRetry || events[0].Class != llm.ErrBusy {
		t.Errorf("engine events = %+v, want one busy retry decision", events)
	}
}
