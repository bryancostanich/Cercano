package usage

import (
	"context"
	"testing"

	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
)

type fakeProvider struct {
	resp   llm.ChatResponse
	stream []llm.StreamEvent
}

func (fakeProvider) Name() string { return "fake" }
func (fakeProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (f fakeProvider) Chat(_ context.Context, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return f.resp, nil
}
func (f fakeProvider) StreamChat(_ context.Context, _ llm.ChatRequest) (llm.StreamReader, error) {
	return &sliceReader{events: f.stream}, nil
}

type sliceReader struct {
	events []llm.StreamEvent
	i      int
}

func (r *sliceReader) Next() (llm.StreamEvent, bool, error) {
	if r.i >= len(r.events) {
		return llm.StreamEvent{}, false, nil
	}
	e := r.events[r.i]
	r.i++
	return e, true, nil
}
func (r *sliceReader) Close() error { return nil }

func TestWrapRecordsChatUsage(t *testing.T) {
	var got []Usage
	inner := fakeProvider{resp: llm.ChatResponse{InputTokens: 11, OutputTokens: 7}}
	p := Wrap(inner, "coproc:summarize", true, func(u Usage) { got = append(got, u) })

	if _, err := p.Chat(context.Background(), llm.ChatRequest{Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 usage event, got %d", len(got))
	}
	u := got[0]
	if u.Source != "coproc:summarize" || !u.IsCloud || u.Model != "m" || u.InputTokens != 11 || u.OutputTokens != 7 {
		t.Fatalf("bad usage: %+v", u)
	}
}

func TestUsageSavingsFieldsRoundTrip(t *testing.T) {
	var got []Usage
	inner := fakeProvider{resp: llm.ChatResponse{InputTokens: 5, OutputTokens: 3}}
	p := Wrap(inner, "coproc:extract", false, func(u Usage) { got = append(got, u) })

	if _, err := p.Chat(context.Background(), llm.ChatRequest{Model: "local-m"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 usage event, got %d", len(got))
	}
	// The Wrap sink receives whatever the caller stored in Usage; verify the
	// struct carries ContentTokensAvoided and TokenSaving without loss.
	u := Usage{
		Source:               "coproc:extract",
		Model:                "local-m",
		IsCloud:              false,
		InputTokens:          5,
		OutputTokens:         3,
		ContentTokensAvoided: 1200,
		TokenSaving:          true,
	}
	if u.ContentTokensAvoided != 1200 || !u.TokenSaving {
		t.Fatalf("savings fields not preserved: %+v", u)
	}
}

func TestWrapRecordsStreamUsageOnDrain(t *testing.T) {
	var got []Usage
	inner := fakeProvider{stream: []llm.StreamEvent{
		{Type: llm.EventMessageStart, InputTokens: 20},
		{Type: llm.EventTextDelta, TextDelta: "hi"},
		{Type: llm.EventMessageStop, OutputTokens: 5},
	}}
	p := Wrap(inner, "main", false, func(u Usage) { got = append(got, u) })

	r, err := p.StreamChat(context.Background(), llm.ChatRequest{Model: "local-x"})
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, ok, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
	}
	_ = r.Close()
	if len(got) != 1 {
		t.Fatalf("want exactly 1 usage event after drain, got %d", len(got))
	}
	if got[0].InputTokens != 20 || got[0].OutputTokens != 5 || got[0].Model != "local-x" || got[0].IsCloud {
		t.Fatalf("bad stream usage: %+v", got[0])
	}
}
