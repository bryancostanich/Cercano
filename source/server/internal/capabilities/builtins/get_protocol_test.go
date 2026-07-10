package builtins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/protocols"
)

func TestGetProtocolReturnsBody(t *testing.T) {
	cap := GetProtocol()
	if cap.Name() != "get_protocol" || cap.Tier() != capabilities.TierR {
		t.Fatalf("name/tier wrong: %q %q", cap.Name(), cap.Tier())
	}
	if !cap.Surfaces().Has(capabilities.SurfaceAgent) || !cap.Surfaces().Has(capabilities.SurfaceMCP) {
		t.Fatal("get_protocol should be on both surfaces")
	}
	args, _ := json.Marshal(map[string]any{"name": "design-decisions"})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Design Decision Protocol") {
		t.Fatalf("body not returned: %q", res.Text[:min(80, len(res.Text))])
	}
}

func TestGetProtocolCoversEveryBuiltinProtocol(t *testing.T) {
	cap := GetProtocol()
	for _, p := range protocols.Builtins() {
		args, _ := json.Marshal(map[string]any{"name": p.Name})
		res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
		if err != nil {
			t.Fatalf("get_protocol(%q): %v", p.Name, err)
		}
		if res.Text != p.Body {
			t.Fatalf("get_protocol(%q) drifted from protocols.Builtins", p.Name)
		}
	}
}

func TestGetProtocolUnknownLists(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"name": "nope"})
	_, err := GetProtocol().Execute(context.Background(), &capabilities.Call{Args: args})
	if err == nil {
		t.Fatal("expected error for unknown protocol")
	}
	if !strings.Contains(err.Error(), "design-decisions") {
		t.Fatal("error should list available protocol names")
	}
}
