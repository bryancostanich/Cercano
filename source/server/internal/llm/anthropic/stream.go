package anthropic

import (
	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"cercano/source/server/internal/llm"
)

type streamReader struct {
	stream    *ssestream.Stream[sdk.MessageStreamEventUnion]
	blockKind map[int64]string
	// normalize maps vendor/transport errors into the llm.Error taxonomy; the
	// SDK is lazy, so even request-time failures (auth, quota 429) surface
	// here on the first read rather than at StreamChat.
	normalize func(error) error
}

func (s *streamReader) Next() (llm.StreamEvent, bool, error) {
	for s.stream.Next() {
		raw := s.stream.Current()
		ev, emit := s.convert(raw)
		if emit {
			return ev, true, nil
		}
	}
	if err := s.stream.Err(); err != nil {
		if s.normalize != nil {
			err = s.normalize(err)
		}
		return llm.StreamEvent{}, false, err
	}
	return llm.StreamEvent{}, false, nil
}

func (s *streamReader) Close() error { return s.stream.Close() }

func (s *streamReader) convert(raw sdk.MessageStreamEventUnion) (llm.StreamEvent, bool) {
	switch raw.Type {
	case "message_start":
		return llm.StreamEvent{Type: llm.EventMessageStart, InputTokens: int(raw.Message.Usage.InputTokens)}, true
	case "content_block_start":
		cb := raw.ContentBlock
		s.blockKind[raw.Index] = cb.Type
		if cb.Type == "tool_use" {
			return llm.StreamEvent{
				Type:      llm.EventToolUseStart,
				ToolUseID: cb.ID,
				ToolName:  cb.Name,
			}, true
		}
		return llm.StreamEvent{}, false
	case "content_block_delta":
		switch raw.Delta.Type {
		case "text_delta":
			return llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: raw.Delta.Text}, true
		case "input_json_delta":
			return llm.StreamEvent{Type: llm.EventToolUseInputDelta, TextDelta: raw.Delta.PartialJSON}, true
		}
		return llm.StreamEvent{}, false
	case "content_block_stop":
		kind := s.blockKind[raw.Index]
		delete(s.blockKind, raw.Index)
		if kind == "tool_use" {
			return llm.StreamEvent{Type: llm.EventToolUseStop}, true
		}
		return llm.StreamEvent{}, false
	case "message_delta":
		return llm.StreamEvent{Type: llm.EventMessageStop, StopReason: string(raw.Delta.StopReason), OutputTokens: int(raw.Usage.OutputTokens)}, true
	case "message_stop":
		return llm.StreamEvent{Type: llm.EventMessageStop}, true
	}
	return llm.StreamEvent{}, false
}
