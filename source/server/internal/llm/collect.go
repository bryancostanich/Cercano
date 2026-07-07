package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// CollectStream consumes a StreamReader into a ChatResponse. onText, when
// non-nil, is called with each text delta as it arrives so callers can stream
// assistant prose to the client live (callers that only need the final
// message pass nil and read the fully-buffered result).
//
// Text deltas concatenate into BlockText; tool_use_input_delta events
// concatenate partial JSON into BlockToolUse.ToolInput. A framing guard (see
// docs/bugs/2026-07-04-user-message-tear.md) drops stream content that arrives
// outside a message_start/message_stop window, and a second message_start
// discards the prior partial message — a proxy/session-resume hiccup can
// replay stale content otherwise.
//
// Lives in the llm package (not the agent tool loop) so provider adapters can
// reuse it: the ChatGPT-account codex backend requires streaming, so
// responses.Chat aggregates its stream through here.
func CollectStream(ctx context.Context, rdr StreamReader, onText func(string)) (ChatResponse, error) {
	var (
		out          ChatResponse
		currentText  strings.Builder
		currentTool  *Block
		toolArgsBuf  strings.Builder
		accepting    bool
		started      bool
		droppedBytes int
	)
	flushText := func() {
		if currentText.Len() > 0 {
			out.Blocks = append(out.Blocks, Block{Type: BlockText, Text: currentText.String()})
			currentText.Reset()
		}
	}
	flushTool := func() {
		if currentTool != nil {
			if toolArgsBuf.Len() > 0 {
				raw := toolArgsBuf.String()
				if json.Valid([]byte(raw)) {
					currentTool.ToolInput = json.RawMessage(raw)
				} else {
					// Invalid input kept raw would poison the turn: persistence
					// fails (RawMessage refuses to marshal) and replaying the
					// block corrupts every later request body. Wrap it in a
					// valid envelope; the loop turns it into a clear error
					// tool_result the model can react to.
					wrapped, _ := json.Marshal(map[string]string{MalformedToolInputKey: raw})
					currentTool.ToolInput = json.RawMessage(wrapped)
					fmt.Fprintf(os.Stderr, "[stream-guard] tool_use %q input is not valid JSON (%d bytes) — wrapped for safe replay\n", currentTool.ToolName, len(raw))
				}
			} else if currentTool.ToolInput == nil {
				currentTool.ToolInput = json.RawMessage("{}")
			}
			out.Blocks = append(out.Blocks, *currentTool)
			currentTool = nil
			toolArgsBuf.Reset()
		}
	}
	for {
		ev, ok, err := rdr.Next()
		if err != nil {
			return out, err
		}
		if !ok {
			break
		}
		switch ev.Type {
		case EventTextDelta:
			if !accepting {
				if droppedBytes == 0 {
					fmt.Fprintf(os.Stderr, "[stream-guard] dropping stream content outside message framing (before message_start / after message_stop)\n")
				}
				droppedBytes += len(ev.TextDelta)
				break
			}
			if currentTool != nil {
				flushTool()
			}
			currentText.WriteString(ev.TextDelta)
			if onText != nil {
				onText(ev.TextDelta)
			}
		case EventToolUseStart:
			if !accepting {
				if droppedBytes == 0 {
					fmt.Fprintf(os.Stderr, "[stream-guard] dropping stream content outside message framing (before message_start / after message_stop)\n")
				}
				droppedBytes += len(ev.ToolName)
				break
			}
			flushText()
			flushTool()
			currentTool = &Block{
				Type:      BlockToolUse,
				ToolUseID: ev.ToolUseID,
				ToolName:  ev.ToolName,
			}
			// Some providers deliver the whole input on the start event
			// (Ollama's tool_calls arrive complete) instead of as
			// input-delta events. Seed the buffer so flushTool sees it.
			if len(ev.ToolInputRaw) > 0 {
				toolArgsBuf.Write(ev.ToolInputRaw)
			}
		case EventToolUseInputDelta:
			if !accepting {
				break
			}
			toolArgsBuf.WriteString(ev.TextDelta)
		case EventToolUseStop:
			if !accepting {
				break
			}
			flushTool()
		case EventReasoning:
			if !accepting {
				break
			}
			flushText()
			flushTool()
			out.Blocks = append(out.Blocks, Block{
				Type:          BlockReasoning,
				ReasoningID:   ev.ReasoningID,
				ReasoningData: ev.ReasoningData,
			})
		case EventMessageStart:
			if started {
				// One response == one message. A second message_start means the
				// stream restarted under us — keep only the newest message.
				fmt.Fprintf(os.Stderr, "[stream-guard] message_start while already accumulating — discarding prior partial message\n")
				currentText.Reset()
				currentTool = nil
				toolArgsBuf.Reset()
				out.Blocks = nil
			}
			started = true
			accepting = true
			out.InputTokens = ev.InputTokens
		case EventMessageStop:
			flushText()
			flushTool()
			accepting = false
			if ev.StopReason != "" {
				out.StopReason = ev.StopReason
			}
			if ev.InputTokens > 0 {
				out.InputTokens = ev.InputTokens
			}
			if ev.OutputTokens > 0 {
				out.OutputTokens = ev.OutputTokens
			}
		case EventError:
			return out, fmt.Errorf("stream error: %s", ev.ErrText)
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
	}
	flushText()
	flushTool()
	return out, nil
}
