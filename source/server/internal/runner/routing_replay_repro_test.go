package runner

import (
	"cercano/source/server/internal/inference"
	"cercano/source/server/internal/llm"
	"context"
	"errors"
	"strings"
	"testing"
)

type partialNetworkProvider struct{ calls int }

func (p *partialNetworkProvider) Name() string { return "partial-network" }
func (p *partialNetworkProvider) Capabilities() inference.Capabilities {
	return inference.Capabilities{SupportsTools: true}
}
func (p *partialNetworkProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	panic("stream expected")
}
func (p *partialNetworkProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	p.calls++
	return &partialNetworkStream{}, nil
}

type partialNetworkStream struct {
	emitted bool
	started bool
}

func (s *partialNetworkStream) Close() error { return nil }
func (s *partialNetworkStream) Next() (llm.StreamEvent, bool, error) {
	if !s.started {
		s.started = true
		return llm.StreamEvent{Type: llm.EventMessageStart}, true, nil
	}
	if !s.emitted {
		s.emitted = true
		return llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: "visible-prefix"}, true, nil
	}
	return llm.StreamEvent{}, false, &llm.Error{Class: llm.ErrNetwork, Err: errors.New("fixture stream reset")}
}

func TestRoutingContractRunnerDoesNotReplayVisibleOutput(t *testing.T) {
	p := &partialNetworkProvider{}
	deps := buildDeps(p)
	sink := &captureSink{}
	_, err := New(deps).RunTurn(context.Background(), Request{Input: "fixture", ConversationID: "replay-probe", WorkDir: t.TempDir()}, sink, nil, nil)
	if err == nil {
		t.Fatal("expected network error")
	}
	var text strings.Builder
	for _, event := range sink.events {
		if event.Kind == EventToken {
			text.WriteString(event.Text)
		}
	}
	t.Logf("provider calls=%d visible output=%q", p.calls, text.String())
	if p.calls != 1 || text.String() != "visible-prefix" {
		t.Errorf("runner replayed after visible output: calls=%d output=%q", p.calls, text.String())
	}
}
