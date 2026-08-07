package openai

import (
	"io"
	"strings"

	goopenai "github.com/sashabaranov/go-openai"

	"cercano/source/server/internal/llm"
)

// streamReader wraps a go-openai ChatCompletionStream and emits llm.StreamEvents
// following the START→DELTA→STOP contract mirroring the anthropic reader.
//
// One Recv() call may produce 0-N llm.StreamEvents (e.g. text delta + a tool-index
// change that emits ToolUseStop+ToolUseStart). A pending-event queue lets Next()
// drain them one at a time without re-calling Recv().
type streamReader struct {
	stream *goopenai.ChatCompletionStream

	// pending events to return before the next Recv()
	pending []llm.StreamEvent

	// currently open tool-call index (nil = no open tool)
	openToolIdx *int

	// captured from the final usage chunk
	inputTokens  int
	outputTokens int

	// captured from choices[0].FinishReason
	stopReason string

	// guards the one-time EventMessageStart emission
	started bool

	// reasoningBuf accumulates delta.ReasoningContent fragments. Some
	// OpenAI-compatible servers (notably llama-server serving GLM-4.5-Air) place
	// the plaintext final answer in reasoning_content and leave content empty. We
	// buffer it and, only if no normal text delta was emitted, flush it as a
	// visible text delta at EOF. emittedText tracks whether real content streamed.
	reasoningBuf strings.Builder
	emittedText  bool

	// emittedToolCall is set once any tool-call fragment is seen. A tool-use turn
	// is an action, and its reasoning is just thinking — never promote reasoning
	// to visible text when a tool call fired.
	emittedToolCall bool

	// true once we've queued the terminal EventMessageStop
	done bool

	// normalize maps vendor/transport errors into the llm.Error taxonomy;
	// go-openai is lazy about some failures, so they surface on Recv rather
	// than at stream construction.
	normalize func(error) error
}

func newStreamReader(s *goopenai.ChatCompletionStream) *streamReader {
	return &streamReader{stream: s}
}

// Next pops one event from the pending queue; refills via Recv() when empty.
// Returns (event, true, nil) while events remain, (zero, false, nil) when done.
func (r *streamReader) Next() (llm.StreamEvent, bool, error) {
	for {
		// Drain pending queue before pulling another chunk.
		if len(r.pending) > 0 {
			ev := r.pending[0]
			r.pending = r.pending[1:]
			return ev, true, nil
		}
		if r.done {
			return llm.StreamEvent{}, false, nil
		}

		chunk, err := r.stream.Recv()
		if err == io.EOF {
			// Flush any open tool call.
			if r.openToolIdx != nil {
				r.pending = append(r.pending, llm.StreamEvent{Type: llm.EventToolUseStop})
				r.openToolIdx = nil
			}
			// Recover the answer from reasoning: if no real text streamed but we
			// buffered reasoning_content, emit it now as a single visible text
			// delta before EventMessageStop.
			if !r.emittedText && !r.emittedToolCall && r.reasoningBuf.Len() > 0 {
				r.pending = append(r.pending, llm.StreamEvent{
					Type:      llm.EventTextDelta,
					TextDelta: r.reasoningBuf.String(),
				})
			}
			// Terminal event carries both token counts (OpenAI only reports usage
			// on the final chunk, so both live on EventMessageStop rather than split
			// across EventMessageStart/EventMessageStop as in the Anthropic contract).
			r.pending = append(r.pending, llm.StreamEvent{
				Type:         llm.EventMessageStop,
				StopReason:   r.stopReason,
				InputTokens:  r.inputTokens,
				OutputTokens: r.outputTokens,
			})
			r.done = true
			continue
		}
		if err != nil {
			if r.normalize != nil {
				err = r.normalize(err)
			}
			return llm.StreamEvent{}, false, err
		}

		// Emit EventMessageStart once (InputTokens unknown until end for OpenAI).
		if !r.started {
			r.started = true
			r.pending = append(r.pending, llm.StreamEvent{Type: llm.EventMessageStart})
		}

		// Capture usage from the final non-[DONE] chunk.
		if chunk.Usage != nil {
			r.inputTokens = chunk.Usage.PromptTokens
			r.outputTokens = chunk.Usage.CompletionTokens
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]

		// Track finish reason for EventMessageStop.
		if choice.FinishReason != "" {
			r.stopReason = string(choice.FinishReason)
		}

		delta := choice.Delta

		// Text delta.
		if delta.Content != "" {
			r.emittedText = true
			r.pending = append(r.pending, llm.StreamEvent{
				Type:      llm.EventTextDelta,
				TextDelta: delta.Content,
			})
		}

		// Reasoning delta: buffer only. GLM-4.5-Air (via llama-server) can put the
		// plaintext answer here with empty content. We do not emit per-delta —
		// normal content may still arrive — and flush it at EOF only if no real
		// text streamed. See reasoningBuf comment above.
		if delta.ReasoningContent != "" {
			r.reasoningBuf.WriteString(delta.ReasoningContent)
		}

		// Tool-call fragments.
		for _, tc := range delta.ToolCalls {
			r.emittedToolCall = true
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}

			// Detect tool-index change: close previous, open new.
			if r.openToolIdx == nil || *r.openToolIdx != idx {
				if r.openToolIdx != nil {
					r.pending = append(r.pending, llm.StreamEvent{Type: llm.EventToolUseStop})
				}
				idxCopy := idx
				r.openToolIdx = &idxCopy
				r.pending = append(r.pending, llm.StreamEvent{
					Type:      llm.EventToolUseStart,
					ToolUseID: tc.ID,
					ToolName:  tc.Function.Name,
				})
			}

			// Argument JSON fragment goes into TextDelta (mirrors anthropic's
			// input_json_delta → EventToolUseInputDelta{TextDelta: partialJSON}).
			if tc.Function.Arguments != "" {
				r.pending = append(r.pending, llm.StreamEvent{
					Type:      llm.EventToolUseInputDelta,
					TextDelta: tc.Function.Arguments,
				})
			}
		}
	}
}

func (r *streamReader) Close() error {
	r.stream.Close()
	return nil
}
