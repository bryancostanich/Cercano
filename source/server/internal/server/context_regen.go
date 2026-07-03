package server

import (
	"fmt"

	"cercano/source/server/pkg/proto"
)

// RegenerateContext rebuilds a conversation's derived compaction state from
// its raw turns, streaming progress to the client. The rebuild semantics live
// in compactiongen.Regenerate; this handler validates the request, forwards
// progress lines, and closes with a terminal frame carrying the before/after
// send-view token counts.
func (s *Server) RegenerateContext(req *proto.RegenerateContextRequest, stream proto.Agent_RegenerateContextServer) error {
	convID := req.GetConversationId()
	if convID == "" {
		return stream.Send(&proto.RegenerateContextProgress{Done: true, Ok: false, Error: "conversation_id is required"})
	}
	if s.compactionGen == nil {
		return stream.Send(&proto.RegenerateContextProgress{Done: true, Ok: false, Error: "compaction is not available (agent is running without a conversation store)"})
	}

	pre, post, err := s.compactionGen.Regenerate(stream.Context(), convID, func(line string) {
		_ = stream.Send(&proto.RegenerateContextProgress{Line: line})
	})
	if err != nil {
		return stream.Send(&proto.RegenerateContextProgress{Done: true, Ok: false, Error: err.Error(), PreTokens: int32(pre)})
	}
	return stream.Send(&proto.RegenerateContextProgress{
		Done:       true,
		Ok:         true,
		Line:       fmt.Sprintf("context rebuilt: ~%d → ~%d tokens", pre, post),
		PreTokens:  int32(pre),
		PostTokens: int32(post),
	})
}
