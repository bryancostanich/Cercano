package worker

import (
	"testing"

	"cercano/source/server/internal/llm"
	wire "cercano/source/server/pkg/proto"

	"google.golang.org/protobuf/proto"
)

func TestQueryThinkingPolicySurvivesWorkerWire(t *testing.T) {
	for _, disable := range []bool{false, true} {
		request, err := MarshalChatRequest(llm.ChatRequest{Model: "model", MaxTokens: 4096, DisableThinking: disable})
		if err != nil {
			t.Fatal(err)
		}
		data, err := proto.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		var received wire.LLMChatRequest
		if err := proto.Unmarshal(data, &received); err != nil {
			t.Fatal(err)
		}
		got, err := UnmarshalChatRequest(&received)
		if err != nil {
			t.Fatal(err)
		}
		if got.DisableThinking != disable || got.MaxTokens != 4096 {
			t.Fatalf("round trip: %+v", got)
		}
	}
}
