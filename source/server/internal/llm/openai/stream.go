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

	// Tool-call name/id can arrive across fragments. Strict OpenAI streaming
	// puts the full Function.Name on the first fragment for an index, but
	// llama-server serving GLM-4.5-Air (and some other OpenAI-compatible
	// servers) open the index with an empty name and stream the name — and
	// sometimes the id — in a later fragment. Emitting EventToolUseStart on
	// first sight captured ToolName="" permanently (collect.go seeds the block
	// name once and never patches it), so every such call landed with an empty
	// ToolName and was invisible to the tool loop.
	//
	// To tolerate that, we DEFER EventToolUseStart until the name is known:
	// accumulate id/name/args for the open index here, emit Start the moment
	// the name first becomes non-empty (replaying any buffered arg fragments),
	// and flush at index-change / EOF.
	openToolID      string
	openToolName    string
	openToolStarted bool          // EventToolUseStart already emitted for the open index
	openToolArgs    strings.Builder // arg fragments buffered before Start was emitted

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
			// Flush any open tool call. closeOpenTool emits a deferred Start
			// (with whatever name/args were buffered) when the name arrived but
			// the loop ended before another fragment triggered emission — the
			// common GLM-4.5-Air case where the name lands on the final tool
			// fragment.
			r.closeOpenTool()
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

			// Detect tool-index change: close the previous open tool (which may
			// still be buffered if its name never arrived — closeOpenTool emits a
			// best-effort Start in that case so args are not silently dropped),
			// then begin the new one.
			if r.openToolIdx == nil || *r.openToolIdx != idx {
				r.closeOpenTool()
				idxCopy := idx
				r.openToolIdx = &idxCopy
			}

			// Accumulate id/name across fragments. Providers may send either on a
			// fragment after the one that opened the index; keep the first
			// non-empty value we see for each.
			if tc.ID != "" && r.openToolID == "" {
				r.openToolID = tc.ID
			}
			if tc.Function.Name != "" && r.openToolName == "" {
				r.openToolName = tc.Function.Name
			}

			// Emit EventToolUseStart exactly once, as soon as the name is known.
			// Any arg fragments that arrived before the name are buffered and
			// replayed immediately after Start so ordering is preserved.
			if !r.openToolStarted && r.openToolName != "" {
				r.openToolStarted = true
				r.pending = append(r.pending, llm.StreamEvent{
					Type:      llm.EventToolUseStart,
					ToolUseID: r.openToolID,
					ToolName:  r.openToolName,
				})
				if r.openToolArgs.Len() > 0 {
					r.pending = append(r.pending, llm.StreamEvent{
						Type:      llm.EventToolUseInputDelta,
						TextDelta: r.openToolArgs.String(),
					})
					r.openToolArgs.Reset()
				}
			}

			// Argument JSON fragment goes into TextDelta (mirrors anthropic's
			// input_json_delta → EventToolUseInputDelta{TextDelta: partialJSON}).
			// If Start has not been emitted yet (name still pending), buffer the
			// fragment rather than emit an input-delta for a not-yet-started tool.
			if tc.Function.Arguments != "" {
				if r.openToolStarted {
					r.pending = append(r.pending, llm.StreamEvent{
						Type:      llm.EventToolUseInputDelta,
						TextDelta: tc.Function.Arguments,
					})
				} else {
					r.openToolArgs.WriteString(tc.Function.Arguments)
				}
			}
		}
	}
}

// closeOpenTool finalizes the currently-open tool call (if any). When the name
// arrived normally, Start was already emitted and this just queues Stop. When
// the name never arrived — a malformed stream — it still emits a best-effort
// Start (empty name) plus buffered args so nothing is silently dropped, then
// Stop; the downstream stream-guard/validation surfaces the empty name rather
// than hiding the call entirely. Resets all open-tool state.
func (r *streamReader) closeOpenTool() {
	if r.openToolIdx == nil {
		return
	}
	if !r.openToolStarted {
		r.pending = append(r.pending, llm.StreamEvent{
			Type:      llm.EventToolUseStart,
			ToolUseID: r.openToolID,
			ToolName:  r.openToolName,
		})
		if r.openToolArgs.Len() > 0 {
			r.pending = append(r.pending, llm.StreamEvent{
				Type:      llm.EventToolUseInputDelta,
				TextDelta: r.openToolArgs.String(),
			})
		}
	}
	r.pending = append(r.pending, llm.StreamEvent{Type: llm.EventToolUseStop})
	r.openToolIdx = nil
	r.openToolID = ""
	r.openToolName = ""
	r.openToolStarted = false
	r.openToolArgs.Reset()
}

func (r *streamReader) Close() error {
	r.stream.Close()
	return nil
}
