package ollama

import (
	"context"
	"encoding/json"

	api "github.com/ollama/ollama/api"

	"cercano/source/server/internal/llm"
)

type streamReader struct {
	ch     chan llm.StreamEvent
	cancel context.CancelFunc
	err    error
}

func (s *streamReader) Next() (llm.StreamEvent, bool, error) {
	ev, ok := <-s.ch
	if !ok {
		return llm.StreamEvent{}, false, s.err
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
	r := &streamReader{ch: ch, cancel: cancel}

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

	go func() {
		defer close(ch)
		// Open the message frame before any content. collect.go's stream-guard
		// drops every delta that arrives before message_start, so without this
		// the entire Ollama response is discarded. Mirrors the anthropic/openai
		// adapters, which emit message_start up front.
		ch <- llm.StreamEvent{Type: llm.EventMessageStart}
		err := c.api.Chat(cctx, freq, func(resp api.ChatResponse) error {
			if resp.Message.Content != "" {
				ch <- llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: resp.Message.Content}
			}
			for _, tc := range resp.Message.ToolCalls {
				raw, _ := json.Marshal(tc.Function.Arguments)
				ch <- llm.StreamEvent{Type: llm.EventToolUseStart, ToolName: tc.Function.Name, ToolInputRaw: raw}
				ch <- llm.StreamEvent{Type: llm.EventToolUseStop}
			}
			if resp.Done {
				ch <- llm.StreamEvent{Type: llm.EventMessageStop, StopReason: resp.DoneReason}
			}
			return nil
		})
		r.err = err
	}()
	return r, nil
}
