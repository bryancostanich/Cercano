// Package runtimes owns the runtime supervisor managers — local runtime and
// MCP host — extracted from the Server god-object as part of Phase 2 host
// decomposition (Task 6).
//
// Supervisors is the interface the front door (Server) depends on for runtime
// supervisor management. The concrete service holds the managers and exposes
// them behind a stable interface so the Server does not reach into the
// managers directly.
package runtimes

import (
	"context"

	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/localruntime"
	mcphost "cercano/source/server/internal/mcp_host"
	"cercano/source/server/internal/openmodels"
	cfg "cercano/source/server/pkg/config"
)

// McpManager is the subset of *mcphost.Manager the RPC handlers use.
// Mirrors the definition in internal/server so the runtimes package does
// not import the server package (which would create a cycle).
type McpManager interface {
	List() []mcphost.ServerStatus
	Add(ctx context.Context, name string, cfg mcphost.ServerConfig) error
	Remove(ctx context.Context, name string) error
	Restart(ctx context.Context, name string) error
}

// Supervisors is the interface the front door (Server) depends on for
// runtime supervisor management.
type Supervisors interface {
	// Local runtime manager.
	SetRuntimeManager(m localruntime.Manager)
	RuntimeManager() localruntime.Manager
	ApplyRuntimeEndpoints(c cfg.Config)
	RefreshRuntimeEndpoints()

	// MCP host manager.
	SetMcpManager(m McpManager)
	McpManager() McpManager
}

type service struct {
	cfgSvc     cfgsvc.Service
	openModels *openmodels.Resolver
	runtimeMgr localruntime.Manager
	mcpMgr     McpManager
}

// New constructs a Supervisors service. cfgSvc and openModels may be nil
// (tests); when openModels is nil the endpoint inventory omits the effective
// open models (display-only).
func New(cfgSvc cfgsvc.Service, openModels *openmodels.Resolver) Supervisors {
	return &service{cfgSvc: cfgSvc, openModels: openModels}
}

// SetRuntimeManager wires the local runtime manager and immediately pushes
// runtime endpoints from the current config.
func (s *service) SetRuntimeManager(m localruntime.Manager) {
	s.runtimeMgr = m
	s.RefreshRuntimeEndpoints()
}

// RuntimeManager returns the local runtime manager (may be nil).
func (s *service) RuntimeManager() localruntime.Manager { return s.runtimeMgr }

// ApplyRuntimeEndpoints derives and pushes runtime endpoints from the given
// config snapshot.
func (s *service) ApplyRuntimeEndpoints(c cfg.Config) {
	if s.runtimeMgr == nil {
		return
	}
	var chat, embed string
	if s.openModels != nil {
		chat = s.openModels.ChatModel()
		embed = s.openModels.EmbeddingModel()
	}
	s.runtimeMgr.SetEndpoints(localruntime.EndpointsFromConfig(c, chat, embed))
}

// RefreshRuntimeEndpoints snapshots the current config and pushes endpoints.
func (s *service) RefreshRuntimeEndpoints() {
	if s.runtimeMgr == nil || s.cfgSvc == nil {
		return
	}
	s.ApplyRuntimeEndpoints(s.cfgSvc.Get())
}

// SetMcpManager wires the MCP host manager.
func (s *service) SetMcpManager(m McpManager) { s.mcpMgr = m }

// McpManager returns the MCP host manager (may be nil).
func (s *service) McpManager() McpManager { return s.mcpMgr }
