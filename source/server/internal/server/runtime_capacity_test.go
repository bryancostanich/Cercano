package server

import (
	"cercano/source/server/internal/localruntime"
	"cercano/source/server/pkg/proto"
	wire "google.golang.org/protobuf/proto"
	"testing"
	"time"
)

func TestRuntimeCapacityProtoRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	p := mapRuntimeInstance(localruntime.InstanceRecord{ID: "generation", ModelID: "model", Runtime: "llama_server", State: localruntime.InstanceRunning, Context: localruntime.ContextCapacity{PlannedTokens: 65536, PlannedSource: "ram_profile", ConfirmedTokens: 32768, ConfirmedAt: now}})
	data, err := wire.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded proto.RuntimeInstance
	if err := wire.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.GetId() != "generation" || decoded.GetModelId() != "model" || decoded.GetPlannedContextTokens() != 65536 || decoded.GetPlannedContextSource() != "ram_profile" || decoded.GetConfirmedContextTokens() != 32768 || decoded.GetContextConfirmedAt() != formatRuntimeTime(now) {
		t.Fatalf("lost capacity evidence: %v", &decoded)
	}
}
