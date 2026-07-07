package fallback

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/llm"
)

func TestShouldFailoverClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"canceled", context.Canceled, false},
		{"wrapped canceled", fmt.Errorf("call: %w", context.Canceled), false},
		{"anthropic 401", &anthropic.Error{StatusCode: http.StatusUnauthorized}, true},
		{"anthropic 429", &anthropic.Error{StatusCode: http.StatusTooManyRequests}, true},
		{"anthropic 529", &anthropic.Error{StatusCode: 529}, true},
		{"anthropic 400", &anthropic.Error{StatusCode: http.StatusBadRequest}, false},
		{"anthropic 404", &anthropic.Error{StatusCode: http.StatusNotFound}, false},
		{"openai 403", &goopenai.APIError{HTTPStatusCode: http.StatusForbidden}, true},
		{"openai 500", &goopenai.APIError{HTTPStatusCode: http.StatusInternalServerError}, true},
		{"openai 400", &goopenai.APIError{HTTPStatusCode: http.StatusBadRequest}, false},
		{"openai request 429", &goopenai.RequestError{HTTPStatusCode: http.StatusTooManyRequests}, true},
		{"wrapped anthropic 429", fmt.Errorf("turn: %w", &anthropic.Error{StatusCode: 429}), true},
		{"unknown error", errors.New("connection refused"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldFailover(tc.err); got != tc.want {
				t.Fatalf("ShouldFailover(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// fakeProvider scripts Chat/StreamChat results and records the requests it saw.
type fakeProvider struct {
	name      string
	chatErr   error
	chatResp  llm.ChatResponse
	streamErr error
	stream    llm.StreamReader
	gotChat   []llm.ChatRequest
	gotStream []llm.ChatRequest
}

func (f *fakeProvider) Name() string                   { return f.name }
func (f *fakeProvider) Capabilities() llm.Capabilities { return llm.Capabilities{} }
func (f *fakeProvider) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.gotChat = append(f.gotChat, req)
	return f.chatResp, f.chatErr
}
func (f *fakeProvider) StreamChat(_ context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	f.gotStream = append(f.gotStream, req)
	return f.stream, f.streamErr
}

// fakeReader yields a scripted event sequence, then done.
type fakeReader struct {
	events []llm.StreamEvent
	errs   []error
	i      int
	closed bool
}

func (r *fakeReader) Next() (llm.StreamEvent, bool, error) {
	if r.i >= len(r.events) {
		return llm.StreamEvent{}, false, nil
	}
	ev, err := r.events[r.i], error(nil)
	if r.i < len(r.errs) {
		err = r.errs[r.i]
	}
	r.i++
	if err != nil {
		return llm.StreamEvent{}, false, err
	}
	return ev, true, nil
}
func (r *fakeReader) Close() error { r.closed = true; return nil }

func TestChatFailsOverAndRewritesModel(t *testing.T) {
	primary := &fakeProvider{name: "anthropic", chatErr: &anthropic.Error{StatusCode: 429}}
	backup := &fakeProvider{name: "openai", chatResp: llm.ChatResponse{StopReason: "end_turn"}}
	var stage string
	p := New(primary, backup, "gpt-5.5", func(s string, _ error) { stage = s })

	resp, err := p.Chat(context.Background(), llm.ChatRequest{Model: "claude-fable-5"})
	if err != nil {
		t.Fatalf("want backup success, got %v", err)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("response not from backup: %+v", resp)
	}
	if stage != "chat" {
		t.Fatalf("onFailover stage = %q, want chat", stage)
	}
	if len(backup.gotChat) != 1 || backup.gotChat[0].Model != "gpt-5.5" {
		t.Fatalf("backup request model = %+v, want gpt-5.5", backup.gotChat)
	}
}

func TestChatDoesNotFailOverOnRequestError(t *testing.T) {
	primary := &fakeProvider{name: "anthropic", chatErr: &anthropic.Error{StatusCode: 400}}
	backup := &fakeProvider{name: "openai"}
	p := New(primary, backup, "gpt-5.5", nil)

	_, err := p.Chat(context.Background(), llm.ChatRequest{Model: "claude-fable-5"})
	if err == nil {
		t.Fatal("want the primary's 400 back")
	}
	if len(backup.gotChat) != 0 {
		t.Fatal("backup must not be called on a request-shaped error")
	}
}

func TestChatDoesNotFailOverWhenContextDone(t *testing.T) {
	primary := &fakeProvider{name: "anthropic", chatErr: &anthropic.Error{StatusCode: 500}}
	backup := &fakeProvider{name: "openai"}
	p := New(primary, backup, "gpt-5.5", nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Chat(ctx, llm.ChatRequest{})
	if err == nil {
		t.Fatal("want error")
	}
	if len(backup.gotChat) != 0 {
		t.Fatal("backup must not be called once the context is done")
	}
}

func TestStreamDialFailover(t *testing.T) {
	primary := &fakeProvider{name: "anthropic", streamErr: &anthropic.Error{StatusCode: 529}}
	backup := &fakeProvider{name: "openai", stream: &fakeReader{
		events: []llm.StreamEvent{{Type: llm.EventMessageStart}},
	}}
	p := New(primary, backup, "gpt-5.5", nil)

	r, err := p.StreamChat(context.Background(), llm.ChatRequest{Model: "claude-fable-5"})
	if err != nil {
		t.Fatalf("want backup stream, got %v", err)
	}
	ev, ok, err := r.Next()
	if err != nil || !ok || ev.Type != llm.EventMessageStart {
		t.Fatalf("backup stream first event = %+v ok=%v err=%v", ev, ok, err)
	}
	if len(backup.gotStream) != 1 || backup.gotStream[0].Model != "gpt-5.5" {
		t.Fatalf("backup stream request = %+v, want model gpt-5.5", backup.gotStream)
	}
}

func TestStreamFirstEventErrorFailsOver(t *testing.T) {
	prim := &fakeReader{errs: []error{&anthropic.Error{StatusCode: 429}}, events: []llm.StreamEvent{{}}}
	primary := &fakeProvider{name: "anthropic", stream: prim}
	backup := &fakeProvider{name: "openai", stream: &fakeReader{
		events: []llm.StreamEvent{{Type: llm.EventMessageStart}, {Type: llm.EventTextDelta, TextDelta: "hi"}},
	}}
	p := New(primary, backup, "gpt-5.5", nil)

	r, err := p.StreamChat(context.Background(), llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	ev, ok, err := r.Next()
	if err != nil || !ok || ev.Type != llm.EventMessageStart {
		t.Fatalf("want backup's first event, got %+v ok=%v err=%v", ev, ok, err)
	}
	if !prim.closed {
		t.Fatal("primary reader must be closed on failover")
	}
}

func TestStreamInBandErrorEventFailsOver(t *testing.T) {
	primary := &fakeProvider{name: "anthropic", stream: &fakeReader{
		events: []llm.StreamEvent{{Type: llm.EventError, ErrText: "overloaded"}},
	}}
	backup := &fakeProvider{name: "openai", stream: &fakeReader{
		events: []llm.StreamEvent{{Type: llm.EventMessageStart}},
	}}
	p := New(primary, backup, "gpt-5.5", nil)

	r, _ := p.StreamChat(context.Background(), llm.ChatRequest{})
	ev, ok, err := r.Next()
	if err != nil || !ok || ev.Type != llm.EventMessageStart {
		t.Fatalf("want backup's first event, got %+v ok=%v err=%v", ev, ok, err)
	}
}

func TestStreamNoFailoverAfterEmission(t *testing.T) {
	primary := &fakeProvider{name: "anthropic", stream: &fakeReader{
		events: []llm.StreamEvent{{Type: llm.EventMessageStart}, {}},
		errs:   []error{nil, &anthropic.Error{StatusCode: 500}},
	}}
	backup := &fakeProvider{name: "openai"}
	p := New(primary, backup, "gpt-5.5", nil)

	r, _ := p.StreamChat(context.Background(), llm.ChatRequest{})
	if _, ok, err := r.Next(); !ok || err != nil {
		t.Fatalf("first event should pass through, ok=%v err=%v", ok, err)
	}
	if _, _, err := r.Next(); err == nil {
		t.Fatal("mid-stream failure must surface as an error, not fail over")
	}
	if len(backup.gotStream) != 0 {
		t.Fatal("backup must not be dialed after emission")
	}
}

func TestStreamBackupFailureDoesNotCascade(t *testing.T) {
	primary := &fakeProvider{name: "anthropic", stream: &fakeReader{
		events: []llm.StreamEvent{{Type: llm.EventError, ErrText: "overloaded"}},
	}}
	backup := &fakeProvider{name: "openai", stream: &fakeReader{
		events: []llm.StreamEvent{{Type: llm.EventError, ErrText: "backup also down"}},
	}}
	p := New(primary, backup, "gpt-5.5", nil)

	r, _ := p.StreamChat(context.Background(), llm.ChatRequest{})
	ev, ok, err := r.Next()
	if err != nil || !ok || ev.Type != llm.EventError {
		t.Fatalf("backup's error event must pass through untouched, got %+v ok=%v err=%v", ev, ok, err)
	}
	if len(backup.gotStream) != 1 {
		t.Fatalf("backup dialed %d times, want exactly 1", len(backup.gotStream))
	}
}
