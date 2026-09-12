package usage

import (
	"cercano/source/server/internal/llm"
	"context"
	"errors"
	"testing"
)

type failedAccountingProvider struct{ fakeProvider }

func (p failedAccountingProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{InputTokens: 11, OutputTokens: 7}, errors.New("provider failed after usage")
}
func (p failedAccountingProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return &failedAccountingReader{}, nil
}

type failedAccountingReader struct{}

func (*failedAccountingReader) Next() (llm.StreamEvent, bool, error) {
	return llm.StreamEvent{InputTokens: 11, OutputTokens: 7}, false, errors.New("stream failed after usage")
}
func (*failedAccountingReader) Close() error { return nil }

// Characterization of the old observation path, not the new accounting contract.
func TestLegacyAccountingFailureProbe(t *testing.T) {
	var got []Usage
	p := Wrap(failedAccountingProvider{}, "probe", true, func(u Usage) { got = append(got, u) })
	_, err := p.Chat(context.Background(), llm.ChatRequest{Model: "fake"})
	if err == nil {
		t.Fatal("expected injected error")
	}
	t.Logf("failed Chat returned 11 input/7 output; emitted observations=%d", len(got))
	if len(got) != 0 {
		t.Fatal("legacy behavior changed; replace characterization with accounting assertions")
	}
	r, err := p.StreamChat(context.Background(), llm.ChatRequest{Model: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = r.Next()
	if err == nil {
		t.Fatal("expected stream error")
	}
	t.Logf("stream error observations before Close=%d", len(got))
	if len(got) != 0 {
		t.Fatal("legacy behavior changed")
	}
	_ = r.Close()
	if len(got) != 1 || got[0].InputTokens != 0 || got[0].OutputTokens != 0 {
		t.Fatalf("unexpected legacy result: %+v", got)
	}
	t.Logf("Close emitted usage %+v: error-event counts were discarded", got[0])
}
