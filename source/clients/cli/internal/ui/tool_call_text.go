package ui

import (
	"encoding/json"
	"strings"
)

// looksLikeRawToolCallText identifies OpenAI-compatible tool-call JSON that a
// local runtime may incorrectly stream as assistant text before also emitting
// structured tool-use events. When the structured event arrives, the tool row is
// the canonical UI; keeping the raw JSON produces duplicate, unformatted tool
// output in sub-agent tabs.
func looksLikeRawToolCallText(s, toolName string) bool {
	s = strings.TrimSpace(s)
	if s == "" || !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return false
	}
	var calls []struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Function  struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal([]byte(s), &calls); err != nil || len(calls) == 0 {
		return false
	}
	for _, call := range calls {
		name := call.Name
		args := call.Arguments
		if name == "" && call.Function.Name != "" {
			name = call.Function.Name
			args = call.Function.Arguments
		}
		if name == "" || len(args) == 0 {
			return false
		}
		if toolName != "" && name != toolName {
			return false
		}
	}
	return true
}
