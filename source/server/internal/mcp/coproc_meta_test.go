package mcp

import (
	"testing"

	"cercano/source/server/pkg/proto"
)

func TestCoprocMeta(t *testing.T) {
	resp := &proto.ProcessRequestResponse{
		RoutingMetadata: &proto.RoutingMetadata{ModelName: "anthropic", IsCloud: true},
		Notice:          "ran on cloud",
	}
	m := coprocMeta(resp)
	if m.Model != "anthropic" || m.Tier != "cloud" || m.Notice != "ran on cloud" {
		t.Errorf("coprocMeta = %+v", m)
	}
	local := coprocMeta(&proto.ProcessRequestResponse{
		RoutingMetadata: &proto.RoutingMetadata{ModelName: "ollama", IsCloud: false},
	})
	if local.Tier != "local" || local.Model != "ollama" {
		t.Errorf("coprocMeta local = %+v", local)
	}
}

func TestCoprocMeta_NilResp(t *testing.T) {
	m := coprocMeta(nil)
	if m.Tier != "local" {
		t.Errorf("expected local tier for nil resp, got %q", m.Tier)
	}
}
