package openai

import (
	"bytes"
	"encoding/json"
)

// ReasoningEvidence describes wire presence, not the model's internal thinking.
// Counts contain no reasoning text. Empty and null chunks can coexist with text.
type ReasoningEvidence struct {
	State       string `json:"state"` // absent, null, empty, nonempty
	NullChunks  int    `json:"null_chunks"`
	EmptyChunks int    `json:"empty_chunks"`
	TextChunks  int    `json:"text_chunks"`
	Bytes       int    `json:"bytes"`
}

// Called only after parseDiagnosticSSE has validated this bounded body. Keep
// presence inspection separate from concatenation, which intentionally maps
// missing/null/empty strings to the same replay payload.
func diagnosticReasoningEvidence(body []byte) ReasoningEvidence {
	e := ReasoningEvidence{State: "absent"}
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[5:])
		if bytes.Equal(data, []byte("[DONE]")) {
			break
		}
		var event struct {
			Choices []struct {
				Delta map[string]json.RawMessage `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal(data, &event) != nil {
			continue
		}
		for _, c := range event.Choices {
			raw, ok := c.Delta["reasoning_content"]
			if !ok {
				continue
			}
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				e.NullChunks++
				continue
			}
			var text string
			if json.Unmarshal(raw, &text) != nil {
				continue
			}
			if text == "" {
				e.EmptyChunks++
			} else {
				e.TextChunks++
				e.Bytes += len(text)
			}
		}
	}
	if e.NullChunks > 0 {
		e.State = "null"
	}
	if e.EmptyChunks > 0 {
		e.State = "empty"
	}
	if e.TextChunks > 0 {
		e.State = "nonempty"
	}
	return e
}
