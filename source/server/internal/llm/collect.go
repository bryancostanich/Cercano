package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
		out            ChatResponse
		currentText    strings.Builder
		currentTool    *Block
		toolArgsBuf    strings.Builder
		accepting      bool
		started        bool
		droppedBytes   int
		droppedContent strings.Builder
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
				droppedContent.WriteString(ev.TextDelta)
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
				droppedContent.WriteString("[tool_use:" + ev.ToolName + "]")
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
				recordStreamAnomaly(ctx, "message_start_discard", streamAnomalySummary(out.Blocks, currentText.String()))
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
	if droppedContent.Len() > 0 {
		recordStreamAnomaly(ctx, "outside_framing", droppedContent.String())
	}
	return out, nil
}

// streamAnomalySummary renders a discarded partial message (text + tool_use
// blocks) into one string for the anomaly log — the fabricated/replayed content
// the guard dropped.
func streamAnomalySummary(blocks []Block, partialText string) string {
	var b strings.Builder
	for _, bl := range blocks {
		switch bl.Type {
		case BlockText:
			b.WriteString(bl.Text)
		case BlockToolUse:
			b.WriteString("[tool_use:" + bl.ToolName + " " + string(bl.ToolInput) + "]")
		}
	}
	b.WriteString(partialText)
	return b.String()
}

// recordStreamAnomaly appends a structured record of a stream-guard catch (a
// resume-replay / fabricated-turn fingerprint) to
// ~/.config/cercano/stream-anomalies.jsonl, so every occurrence across all
// conversations is captured with its content + conversation id — a reviewable
// trail instead of ad-hoc incident hunting. Best-effort; never affects the stream.
func recordStreamAnomaly(ctx context.Context, reason, content string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	rec := map[string]any{
		"ts":           time.Now().Unix(),
		"conversation": SessionIDFromContext(ctx),
		"reason":       reason,
		"bytes":        len(content),
		"content":      content,
	}
	blob, err := json.Marshal(rec)
	if err != nil {
		return
	}
	fh, err := os.OpenFile(filepath.Join(home, ".config", "cercano", "stream-anomalies.jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer fh.Close()
	_, _ = fh.Write(append(blob, '\n'))
}
