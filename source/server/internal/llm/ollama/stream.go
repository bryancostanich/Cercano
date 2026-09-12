package ollama

import (
	"context"
	"encoding/json"

	api "github.com/ollama/ollama/api"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/usage"
)

type streamReader struct {
	usage  llm.TokenUsage
	done   chan struct{}
	ch     chan llm.StreamEvent
	cancel context.CancelFunc
	err    error
}

func (s *streamReader) Next() (llm.StreamEvent, bool, error) {
	ev, ok := <-s.ch
	if !ok {
		return llm.StreamEvent{Usage: s.usage}, false, s.err
	}
	return ev, true, nil
}

func (s *streamReader) Close() error {
	s.cancel()
	return nil
}

func (c *Client) StreamChat(ctx context.Context, req ChatRequest) (llm.StreamReader, error) {
	cctx, cancel := context.WithCancel(ctx)
	ch := make(chan llm.StreamEvent, 16)
	r := &streamReader{ch: ch, cancel: cancel, done: make(chan struct{})}

	msgs := []api.Message{}
	if req.System != "" {
		msgs = append(msgs, api.Message{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		om, err := messageToOllama(ctx, m)
		if err != nil {
			cancel()
			return nil, err
		}
		msgs = append(msgs, om)
	}
	freq := &api.ChatRequest{
		Model:    req.Model,
		Messages: msgs,
		Stream:   boolPtr(true),
		Tools:    toolsToOllama(req.Tools),
	}

	a := usage.StartAttempt(ctx, c.Name(), freq.Model)
	send := func(ev llm.StreamEvent) bool {
		select {
		case ch <- ev:
			return true
		case <-cctx.Done():
			return false
		}
	}
	go func() {
		defer close(r.done)
		defer close(ch)
		if !send(llm.StreamEvent{Type: llm.EventMessageStart}) {
			r.err = cctx.Err()
			return
		}
		actualModel := ""
		err := c.api.Chat(cctx, freq, func(resp api.ChatResponse) error {
			next := normalizedUsage(resp)
			r.usage = r.usage.Merge(next)
			if next != (llm.TokenUsage{}) || resp.Model != actualModel {
				actualModel = resp.Model
				a.Observe(next, &llm.ServingRoute{Provider: c.Name(), Model: resp.Model, Destination: "local"})
			}
			if resp.Message.Content != "" {
				if !send(llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: resp.Message.Content}) {
					return cctx.Err()
				}
			}
			for _, tc := range resp.Message.ToolCalls {
				raw, _ := json.Marshal(tc.Function.Arguments)
				if !send(llm.StreamEvent{Type: llm.EventToolUseStart, ToolName: tc.Function.Name, ToolInputRaw: raw}) {
					return cctx.Err()
				}
				if !send(llm.StreamEvent{Type: llm.EventToolUseStop}) {
					return cctx.Err()
				}
			}
			if resp.Done {
				if !send(llm.StreamEvent{Type: llm.EventMessageStop, StopReason: resp.DoneReason, InputTokens: resp.PromptEvalCount, OutputTokens: resp.EvalCount, Usage: r.usage}) {
					return cctx.Err()
				}
			}
			return nil
		})
		r.err = err
	}()
	return a.TrackStream(r), nil
}
