package builtins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"cercano/source/server/internal/capabilities"
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
