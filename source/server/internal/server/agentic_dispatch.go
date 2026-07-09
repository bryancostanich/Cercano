package server

import (
	"context"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/dispatch"
)

// runAgenticDispatch is the front-door delegator for the tool-catalog service's
// agentic runner. It is installed onto the dispatch.Engine by SetDispatchEngine
// (via toolSvc.SetEngine), and is kept here as a thin wrapper so tests in
// package server can still call srv.runAgenticDispatch directly.
func (s *Server) runAgenticDispatch(ctx context.Context, spec dispatch.Spec, sel dispatch.Selection, model string) (dispatch.Result, error) {
	return s.toolSvc.RunAgenticDispatch(ctx, spec, sel, model)
}

// grantedRegistry is a thin delegator for tests in package server that call
// srv.grantedRegistry directly. The real logic lives in hostsvc/tools.Service.
func (s *Server) grantedRegistry(tools []string) (*agenttools.Registry, []string, []string, error) {
	return s.toolSvc.GrantedRegistry(tools)
}
