package worker

import (
	"cercano/source/server/internal/modelmetadata"
	pb "cercano/source/server/pkg/proto"
	"google.golang.org/protobuf/proto"
	"reflect"
	"testing"
)

func TestModelMetadataWireRoundTrip(t *testing.T) {
	id := modelmetadata.Identity{Provider: "deepinfra", BaseURL: "https://api.deepinfra.com/v1/openai", Route: "direct", Model: "fixture/model"}
	snapshot := modelmetadata.Snapshot{{Identity: id, Evidence: modelmetadata.Evidence{ContextWindow: 262144, Vision: modelmetadata.VisionSupported}}}
	other := id
	other.BaseURL = "https://other.example"
	snapshot = append(snapshot, modelmetadata.Entry{Identity: other, Evidence: modelmetadata.Evidence{ContextWindow: 8192, Vision: modelmetadata.VisionUnsupported}})
	wire := &pb.ConfigSnapshot{ModelMetadata: MarshalModelMetadata(snapshot)}
	data, err := proto.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	decoded := &pb.ConfigSnapshot{}
	if err := proto.Unmarshal(data, decoded); err != nil {
		t.Fatal(err)
	}
	got := UnmarshalModelMetadata(decoded.ModelMetadata)
	if !reflect.DeepEqual(got, snapshot) {
		t.Fatalf("roundtrip got %+v want %+v", got, snapshot)
	}
	// Returned values do not share mutable evidence with the serialized payload.
	decoded.ModelMetadata[0].ContextWindow = 1
	if got[0].Evidence.ContextWindow != 262144 {
		t.Fatal("snapshot mutated through wire")
	}
}

func TestModelMetadataWireUnknownAndInvalid(t *testing.T) {
	if got := UnmarshalModelMetadata(nil); len(got) != 0 {
		t.Fatal(got)
	}
	got := UnmarshalModelMetadata([]*pb.ModelMetadataEntry{nil, {Model: "fixture", ContextWindow: -1, Vision: pb.ModelMetadataEntry_Vision(99)}})
	if len(got) != 1 || got[0].Evidence.ContextWindow != 0 || got[0].Evidence.Vision != modelmetadata.VisionUnknown {
		t.Fatalf("invalid evidence granted capability: %+v", got)
	}
}
