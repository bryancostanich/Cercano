package agentclient

import (
	"cercano/source/server/pkg/proto"
	"testing"
	"time"
)

func TestRuntimeCapacityClientMapping(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	got := mapRuntimeInstance(&proto.RuntimeInstance{Id: "generation", ModelId: "model", PlannedContextTokens: 65536, PlannedContextSource: "gguf", ConfirmedContextTokens: 32768, ContextConfirmedAt: now.Format(time.RFC3339)})
	if got.ID != "generation" || got.ModelID != "model" || got.PlannedContextTokens != 65536 || got.PlannedContextSource != "gguf" || got.ConfirmedContextTokens != 32768 || !got.ContextConfirmedAt.Equal(now) {
		t.Fatalf("lost capacity evidence: %+v", got)
	}
}
