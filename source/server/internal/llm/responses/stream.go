package responses

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"cercano/source/server/internal/llm"
)

// streamReader parses the Responses SSE event stream into llm.StreamEvents. SSE
// frames are separated by a blank line; we read each "data:" payload, JSON-decode
// it, and dispatch on its "type" field. Reasoning encrypted_content arrives on the
// reasoning item's output_item.done and is surfaced as EventReasoning in stream
// order, so collectStream assembles a BlockReasoning before the function_call.
type streamReader struct {
	rc      io.ReadCloser
	br      *bufio.Reader
	pending []llm.StreamEvent
	done    bool
}

func newStreamReader(rc io.ReadCloser) *streamReader {
	return &streamReader{rc: rc, br: bufio.NewReader(rc)}
}

type streamEnvelope struct {
	Type     string      `json:"type"`
	Delta    string      `json:"delta"`
	Item     *streamItem `json:"item"`
	Response *response   `json:"response"`
	// Error fields of a top-level "error" event: unlike "response.failed"
	// (which nests under response.error), the error event carries
	// code/message/param at the top of the frame.
	Message string    `json:"message"`
	Code    string    `json:"code"`
	Param   string    `json:"param"`
	Error   *apiError `json:"error"`
}

type streamItem struct {
	Type             string `json:"type"`
	CallID           string `json:"call_id"`
	Name             string `json:"name"`
	ID               string `json:"id"`
	EncryptedContent string `json:"encrypted_content"`
}

func (s *streamReader) Next() (llm.StreamEvent, bool, error) {
	for len(s.pending) == 0 {
		if s.done {
			return llm.StreamEvent{}, false, nil
		}
		data, err := s.readFrame()
		if err == io.EOF {
			s.done = true
			if data == "" {
				return llm.StreamEvent{}, false, nil
			}
		} else if err != nil {
			return llm.StreamEvent{}, false, err
		}
		if data == "" {
			continue
		}
		s.dispatch(data)
	}
	ev := s.pending[0]
	s.pending = s.pending[1:]
	return ev, true, nil
}

// readFrame reads lines until a blank line, returning the concatenated "data:"
// payload for one SSE event. Returns io.EOF when the stream ends.
func (s *streamReader) readFrame() (string, error) {
	var data strings.Builder
	for {
		line, err := s.br.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" { // end of frame (or leading blank)
			if err != nil {
				return data.String(), err
			}
			if data.Len() > 0 {
				return data.String(), nil
			}
			continue // skip leading blank lines between frames
		}
		if strings.HasPrefix(trimmed, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")))
		}
		// "event:" and other field lines are ignored; type lives in the JSON.
		if err != nil {
			return data.String(), err
		}
	}
}

func (s *streamReader) dispatch(data string) {
	if data == "[DONE]" {
		s.done = true
		return
	}
	var env streamEnvelope
	if err := json.Unmarshal([]byte(data), &env); err != nil {
		return // ignore unparseable frames
	}
	switch env.Type {
	case "response.created":
		s.pending = append(s.pending, llm.StreamEvent{Type: llm.EventMessageStart})
	case "response.output_text.delta":
		s.pending = append(s.pending, llm.StreamEvent{Type: llm.EventTextDelta, TextDelta: env.Delta})
	case "response.output_item.added":
		if env.Item != nil && env.Item.Type == "function_call" {
			s.pending = append(s.pending, llm.StreamEvent{Type: llm.EventToolUseStart, ToolUseID: env.Item.CallID, ToolName: env.Item.Name})
		}
	case "response.function_call_arguments.delta":
		s.pending = append(s.pending, llm.StreamEvent{Type: llm.EventToolUseInputDelta, TextDelta: env.Delta})
	case "response.output_item.done":
		if env.Item == nil {
			return
		}
		switch env.Item.Type {
		case "function_call":
			s.pending = append(s.pending, llm.StreamEvent{Type: llm.EventToolUseStop})
		case "reasoning":
			s.pending = append(s.pending, llm.StreamEvent{Type: llm.EventReasoning, ReasoningID: env.Item.ID, ReasoningData: env.Item.EncryptedContent})
		}
	case "response.completed":
		ev := llm.StreamEvent{Type: llm.EventMessageStop}
		if env.Response != nil {
			ev.StopReason = env.Response.Status
			if env.Response.Usage != nil {
				ev.InputTokens = env.Response.Usage.InputTokens
				ev.OutputTokens = env.Response.Usage.OutputTokens
			}
		}
		s.pending = append(s.pending, ev)
	case "response.failed", "response.error", "error":
		s.pending = append(s.pending, llm.StreamEvent{Type: llm.EventError, ErrText: streamErrorMessage(env, data)})
	}
}

// streamErrorMessage extracts the most specific error text from an error-ish
// stream frame. "response.failed" nests the error under response.error; a
// top-level "error" event carries message/code at the frame root; anything
// else falls back to the raw frame (truncated) — a bare "responses stream
// error" is undiagnosable and has already cost debugging sessions.
func streamErrorMessage(env streamEnvelope, raw string) string {
	if env.Response != nil && env.Response.Error != nil && env.Response.Error.Message != "" {
		return env.Response.Error.Message
	}
	if env.Error != nil && env.Error.Message != "" {
		return env.Error.Message
	}
	if env.Message != "" {
		if env.Code != "" {
			return env.Code + ": " + env.Message
		}
		return env.Message
	}
	snippet := strings.TrimSpace(raw)
	if len(snippet) > 200 {
		snippet = snippet[:200] + "…"
	}
	if snippet == "" {
		return "responses stream error"
	}
	return "responses stream error: " + snippet
}

func (s *streamReader) Close() error { return s.rc.Close() }
