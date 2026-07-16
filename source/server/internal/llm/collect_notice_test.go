package llm

import (
	"context"
	"testing"
)

type noticeStream struct {
	events []StreamEvent
}

func (s *noticeStream) Next() (StreamEvent, bool, error) {
	if len(s.events) == 0 {
		return StreamEvent{}, false, nil
	}
	ev := s.events[0]
	s.events = s.events[1:]
	return ev, true, nil
}

func (s *noticeStream) Close() error { return nil }

// A notice injected before message framing (the resilience engine's failover
// narration) must reach the notice callback and must NEVER appear in the
// collected blocks — it is display-only and would otherwise be persisted into
// the conversation history.
func TestCollectStream_NoticeIsSurfacedButNeverPersisted(t *testing.T) {
	rdr := &noticeStream{events: []StreamEvent{
		{Type: EventNotice, Notice: "anthropic quota reached — switching to openai"},
		{Type: EventMessageStart},
		{Type: EventTextDelta, TextDelta: "hello"},
		{Type: EventMessageStop},
	}}
	var notices []string
	resp, err := CollectStream(context.Background(), rdr,
		nil, func(n string) { notices = append(notices, n) })
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(notices) != 1 || notices[0] != "anthropic quota reached — switching to openai" {
		t.Errorf("notices = %v", notices)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Text != "hello" {
		t.Fatalf("blocks = %+v — the notice must not become content", resp.Blocks)
	}
}

// Without a callback (background aggregation paths pass nil) the notice is
// silently dropped, never an error and never content.
func TestCollectStream_NoticeIgnoredWithoutCallback(t *testing.T) {
	rdr := &noticeStream{events: []StreamEvent{
		{Type: EventNotice, Notice: "openai server busy — trying once more"},
		{Type: EventMessageStart},
		{Type: EventTextDelta, TextDelta: "hi"},
		{Type: EventMessageStop},
	}}
	resp, err := CollectStream(context.Background(), rdr, nil, nil)
	if err != nil || len(resp.Blocks) != 1 || resp.Blocks[0].Text != "hi" {
		t.Fatalf("resp = %+v err = %v", resp, err)
	}
}
