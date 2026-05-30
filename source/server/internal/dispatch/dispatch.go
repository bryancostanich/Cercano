package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"cercano/source/server/internal/engine"
)

// Loop runs an agentic tool-use conversation against an InferenceEngine.
type Loop struct {
	engine   engine.InferenceEngine
	registry *Registry
	model    string
	maxTurns int
}

// NewLoop wires a Loop with the given engine, tool registry, model name, and turn cap.
func NewLoop(eng engine.InferenceEngine, reg *Registry, model string, maxTurns int) *Loop {
	if maxTurns <= 0 {
		maxTurns = 50
	}
	return &Loop{engine: eng, registry: reg, model: model, maxTurns: maxTurns}
}

// Run starts the loop in a goroutine. It returns an event channel and a
// finalHistory accessor that becomes valid AFTER the channel is fully drained.
//
// The seed history is prepended to a new user message built from userMsg. If
// userMsg is empty, only the seed history is sent (useful when the host wants
// to continue an existing conversation without adding a new turn — rare).
func (l *Loop) Run(ctx context.Context, seed []engine.ChatMessage, userMsg string) (<-chan Event, func() []engine.ChatMessage) {
	out := make(chan Event)
	history := append([]engine.ChatMessage(nil), seed...)
	if userMsg != "" {
		history = append(history, engine.ChatMessage{Role: "user", Content: userMsg})
	}

	historyRef := &history

	go func() {
		defer close(out)

		for turn := 0; turn < l.maxTurns; turn++ {
			if ctx.Err() != nil {
				log.Printf("dispatch: turn %d aborting, ctx done before engine call", turn)
				out <- Event{Kind: EventDone, Cancelled: true}
				return
			}

			log.Printf("dispatch: turn %d starting, history=%d msgs", turn, len(*historyRef))

			req := engine.ChatRequest{
				Model:    l.model,
				Messages: *historyRef,
				Tools:    schemasAsJSON(l.registry.Schemas()),
			}
			engineStart := time.Now()
			resp, err := l.engine.ChatWithTools(ctx, req)
			engineDur := time.Since(engineStart)
			if err != nil {
				log.Printf("dispatch: turn %d engine error after %s: %v", turn, engineDur, err)
				if ctx.Err() != nil {
					out <- Event{Kind: EventDone, Cancelled: true}
					return
				}
				out <- Event{Kind: EventDone, DoneError: err.Error()}
				return
			}
			log.Printf("dispatch: turn %d engine returned in %s, content_len=%d, tool_calls=%d",
				turn, engineDur, len(resp.Content), len(resp.ToolCalls))

			if len(resp.ToolCalls) == 0 {
				log.Printf("dispatch: turn %d final text response, exiting loop", turn)
				out <- Event{Kind: EventTextChunk, Text: resp.Content}
				*historyRef = append(*historyRef, engine.ChatMessage{Role: "assistant", Content: resp.Content})
				out <- Event{Kind: EventDone}
				return
			}

			*historyRef = append(*historyRef, engine.ChatMessage{
				Role:      "assistant",
				Content:   resp.Content,
				ToolCalls: resp.ToolCalls,
			})

			for _, tc := range resp.ToolCalls {
				if ctx.Err() != nil {
					log.Printf("dispatch: turn %d aborting mid-tool-loop, ctx done", turn)
					out <- Event{Kind: EventDone, Cancelled: true}
					return
				}
				log.Printf("dispatch: turn %d running tool %q (args %d bytes)",
					turn, tc.Function.Name, len(tc.Function.Arguments))

				out <- Event{
					Kind:       EventToolCall,
					ToolCallID: tc.ID,
					ToolName:   tc.Function.Name,
					ToolArgs:   tc.Function.Arguments,
				}

				toolStart := time.Now()
				result, ok, runErr := l.runTool(ctx, tc)
				toolDur := time.Since(toolStart)
				resultText := result
				if runErr != nil {
					resultText = runErr.Error()
				}
				log.Printf("dispatch: turn %d tool %q returned in %s ok=%v result_bytes=%d",
					turn, tc.Function.Name, toolDur, ok, len(resultText))

				out <- Event{
					Kind:       EventToolResult,
					ToolCallID: tc.ID,
					ToolResult: resultText,
					ToolOK:     ok,
				}
				*historyRef = append(*historyRef, engine.ChatMessage{
					Role:       "tool",
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
					Content:    resultText,
				})
			}
		}

		log.Printf("dispatch: exceeded max turns (%d), exiting", l.maxTurns)
		out <- Event{Kind: EventDone, DoneError: fmt.Sprintf("exceeded max turns (%d)", l.maxTurns)}
	}()

	finalHistory := func() []engine.ChatMessage { return *historyRef }
	return out, finalHistory
}

// runTool looks up the tool, runs it, and returns (resultText, ok, err).
// ok=false indicates the result should be presented to the model as a failure
// (unknown tool, bad args, tool returned error). err is non-nil only when the
// tool itself failed to execute.
func (l *Loop) runTool(ctx context.Context, tc engine.ToolCall) (string, bool, error) {
	tool, ok := l.registry.Get(tc.Function.Name)
	if !ok {
		return fmt.Sprintf("tool %q not registered", tc.Function.Name), false, nil
	}
	if !json.Valid(tc.Function.Arguments) {
		return fmt.Sprintf("invalid arguments: not valid JSON: %s", string(tc.Function.Arguments)), false, nil
	}
	result, err := tool.Run(ctx, tc.Function.Arguments)
	if err != nil {
		return "", false, err
	}
	return result, true, nil
}

// schemasAsJSON wraps Registry.Schemas() in the Ollama-expected envelope.
func schemasAsJSON(in []ToolSchema) []engine.ToolSchemaJSON {
	out := make([]engine.ToolSchemaJSON, 0, len(in))
	for _, s := range in {
		out = append(out, engine.ToolSchemaJSON{
			Type: "function",
			Function: engine.ToolFunctionJSON{
				Name:        s.Name,
				Description: s.Description,
				Parameters:  s.Parameters,
			},
		})
	}
	return out
}
