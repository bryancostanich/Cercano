package server

import (
	"testing"

	"cercano/source/server/pkg/proto"
)

func TestProtoUpdate_NewFields(t *testing.T) {
	res := &proto.ProcessRequestResponse{
		Output: "some output",
		// These fields do not exist yet and will cause a compilation error
		FileChanges: []*proto.FileChange{
			{
				Path:    "test.txt",
				Content: "new content",
				Action:  proto.FileAction_UPDATE,
			},
		},
		RoutingMetadata: &proto.RoutingMetadata{
			ModelName:  "gpt-4",
			Confidence: 0.95,
		},
	}

	if res.Output != "some output" {
		t.Errorf("Expected output, got %s", res.Output)
	}

	if len(res.FileChanges) != 1 {
		t.Errorf("Expected 1 file change, got %d", len(res.FileChanges))
	}

	if res.RoutingMetadata.ModelName != "gpt-4" {
		t.Errorf("Expected model name gpt-4, got %s", res.RoutingMetadata.ModelName)
	}
}
