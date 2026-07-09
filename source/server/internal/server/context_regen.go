package server

import (
	"cercano/source/server/pkg/proto"
)

// RegenerateContext implements proto.AgentServer — delegates to persistSvc.
func (s *Server) RegenerateContext(req *proto.RegenerateContextRequest, stream proto.Agent_RegenerateContextServer) error {
	return s.persistSvc.RegenerateContext(req, stream)
}
