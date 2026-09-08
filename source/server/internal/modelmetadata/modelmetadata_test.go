package modelmetadata

import "testing"

func TestSnapshotIdentityIsolation(t *testing.T) {
	id := Identity{Provider: "deepinfra", BaseURL: "https://api.deepinfra.com/v1/openai", Route: "direct", Model: "org/model"}
	s := Snapshot{{Identity: id, Evidence: Evidence{ContextWindow: 262144, Vision: VisionSupported}}}
	if got, ok := s.Lookup(id); !ok || got.ContextWindow != 262144 || got.Vision != VisionSupported {
		t.Fatalf("lookup = %+v, %v", got, ok)
	}
	for _, field := range []string{"provider", "endpoint", "route", "model"} {
		other := id
		switch field {
		case "provider":
			other.Provider = "other"
		case "endpoint":
			other.BaseURL = "https://other.example"
		case "route":
			other.Route = "subscription"
		case "model":
			other.Model = "other/model"
		}
		if got, ok := s.Lookup(other); ok || got.ContextWindow != 0 || got.Vision == VisionSupported {
			t.Fatalf("%s leaked: %+v, %v", field, got, ok)
		}
	}
}

func TestSnapshotInvalidEvidence(t *testing.T) {
	id := Identity{Provider: "deepinfra", Model: "fixture"}
	for _, capacity := range []int{-1, 0, 8192, 1048576} {
		for _, vision := range []Vision{"", "invalid", VisionUnknown, VisionSupported, VisionUnsupported} {
			s := Snapshot{{Identity: id, Evidence: Evidence{ContextWindow: capacity, Vision: vision}}}
			got, ok := s.Lookup(id)
			if !ok {
				t.Fatal("lost entry")
			}
			if got.ContextWindow < 0 {
				t.Fatalf("negative capacity: %+v", got)
			}
			if capacity > 0 && got.ContextWindow != capacity {
				t.Fatalf("changed capacity: %+v", got)
			}
			if vision != VisionSupported && vision != VisionUnsupported && got.Vision != VisionUnknown {
				t.Fatalf("invalid vision not unknown: %+v", got)
			}
		}
	}
}
