package capabilities

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTierToPermission(t *testing.T) {
	cases := map[Tier]string{TierR: "R", TierW: "W", TierX: "X"}
	for tier, want := range cases {
		if got := string(tier.ToPermission()); got != want {
			t.Fatalf("Tier %q -> %q, want %q", tier, got, want)
		}
	}
}

func TestSurfaceHas(t *testing.T) {
	both := SurfaceAgent | SurfaceMCP
	if !both.Has(SurfaceAgent) || !both.Has(SurfaceMCP) {
		t.Fatal("both should contain agent and mcp")
	}
	if SurfaceAgent.Has(SurfaceMCP) {
		t.Fatal("agent-only must not contain mcp")
	}
}

func TestNewTextResultTruncates(t *testing.T) {
	big := strings.Repeat("a", 40*1024)
	r := NewTextResult(big)
	if !r.Truncated {
		t.Fatal("expected truncation over 32 KiB")
	}
	if r.Type != ResultText {
		t.Fatalf("type = %q, want text", r.Type)
	}
}

func TestLLMContentSerializesRows(t *testing.T) {
	r := NewRowsResult([]map[string]any{{"k": "v"}})
	if !strings.Contains(r.LLMContent(), `"k":"v"`) {
		t.Fatalf("rows not serialized: %q", r.LLMContent())
	}
	_ = json.RawMessage(nil)
}
