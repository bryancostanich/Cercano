package server

import (
	"testing"

	"cercano/source/server/pkg/proto"
)

func TestMapCoprocRequestPreservesQueryThinkingPolicy(t *testing.T) {
	var s Server
	for _, disable := range []bool{false, true} {
		got := s.mapRequest(&proto.ProcessRequestRequest{Input: "question", RoutingTask: "research", DisableThinking: disable})
		if got.DisableThinking != disable || got.RoutingTask != "research" || got.Coproc {
			t.Fatalf("mapped=%+v", got)
		}
	}
}
