package openai

import (
	"encoding/json"
	"reflect"
	"testing"

	goopenai "github.com/sashabaranov/go-openai"
)

// Characterize SDK information loss before choosing a response-decoding fix.
func TestSDKUsagePresenceProbe(t *testing.T) {
	var absent, zero goopenai.ChatCompletionResponse
	if err := json.Unmarshal([]byte(`{"model":"fake","choices":[]}`), &absent); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"model":"fake","choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`), &zero); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(absent, zero) {
		t.Fatal("SDK now preserves presence; revise normalization strategy")
	}
	var partial, reported goopenai.ChatCompletionStreamResponse
	if err := json.Unmarshal([]byte(`{"usage":{"prompt_tokens":11}}`), &partial); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"usage":{"prompt_tokens":11,"completion_tokens":0}}`), &reported); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(partial, reported) {
		t.Fatal("SDK now preserves stream field presence; revise normalization strategy")
	}
	t.Log("Chat missing usage equals reported zero; stream missing completion_tokens equals reported zero")
}
