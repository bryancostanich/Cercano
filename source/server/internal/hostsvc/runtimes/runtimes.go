// Package runtimes owns the runtime supervisor managers — meridian, local
// runtime, and MCP host — extracted from the Server god-object as part of
// Phase 2 host decomposition (Task 6).
//
// Supervisors is the interface the front door (Server) depends on for
// runtime supervisor management. The concrete service holds the managers and
// exposes them behind a stable interface so the Server does not reach into the
// managers directly.
package runtimes

import (
	"context"
	"net/url"
	"strconv"

	cfgsvc "cercano/source/server/internal/hostsvc/config"
	"cercano/source/server/internal/localruntime"
	mcphost "cercano/source/server/internal/mcp_host"
	"cercano/source/server/internal/meridian"
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
	// Meridian proxy management.
	SetupMeridian(logPath string, onStatus func(meridian.Status))
	SetMeridianMgr(m *meridian.Manager)
	SyncMeridianForProfile(prof cfg.CloudProfile)
	MeridianStatus() (meridian.Status, bool)
	StopMeridian()

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
	cfgSvc      cfgsvc.Service
	meridianMgr *meridian.Manager
	runtimeMgr  localruntime.Manager
	mcpMgr      McpManager
}

// New constructs a Supervisors service. cfgSvc may be nil (tests).
func New(cfgSvc cfgsvc.Service) Supervisors {
	return &service{cfgSvc: cfgSvc}
}

// SetupMeridian constructs a new Meridian manager, wires the status listener,
// and replaces any prior manager.
func (s *service) SetupMeridian(logPath string, onStatus func(meridian.Status)) {
	m := meridian.New(nil, logPath)
	if onStatus != nil {
		m.SetStatusListener(onStatus)
	}
	s.meridianMgr = m
}

// SetMeridianMgr injects a pre-built Meridian manager. Used in tests and
// during startup when the manager is constructed externally.
func (s *service) SetMeridianMgr(m *meridian.Manager) { s.meridianMgr = m }

// SyncMeridianForProfile starts or stops the managed Meridian proxy based on
// the active profile's Route field. No-op when the meridian manager is nil.
func (s *service) SyncMeridianForProfile(prof cfg.CloudProfile) {
	if s.meridianMgr == nil {
		return
	}
	var c cfg.Config
	if s.cfgSvc != nil {
		c = s.cfgSvc.Get()
	}
	m := prof
	if m.Route != "meridian" {
		if bp, ok := profileByName(c.CloudProfiles, c.BackupCloudProfile); ok && bp.Route == "meridian" {
			m = bp
		}
	}
	if m.Route != "meridian" {
		s.meridianMgr.Stop()
		return
	}
	port := portFromBaseURL(m.BaseURL)
	s.meridianMgr.Ensure(context.Background(), port)
}

// MeridianStatus returns the current status of the Meridian manager. The
// second return value is false when no manager has been set up.
func (s *service) MeridianStatus() (meridian.Status, bool) {
	if s.meridianMgr == nil {
		return meridian.Status{}, false
	}
	return s.meridianMgr.Status(), true
}

// StopMeridian tears down the Meridian manager. Safe when manager is nil.
func (s *service) StopMeridian() {
	if s.meridianMgr != nil {
		s.meridianMgr.Stop()
	}
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
	s.runtimeMgr.SetEndpoints(localruntime.EndpointsFromConfig(c))
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

// portFromBaseURL extracts the TCP port from a BaseURL, falling back to 3456.
func portFromBaseURL(baseURL string) int {
	const defaultPort = 3456
	if baseURL == "" {
		return defaultPort
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return defaultPort
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err == nil {
			return n
		}
	}
	return defaultPort
}

// PortFromBaseURL is the exported version of portFromBaseURL for tests.
func PortFromBaseURL(baseURL string) int { return portFromBaseURL(baseURL) }

func profileByName(profiles []cfg.CloudProfile, name string) (cfg.CloudProfile, bool) {
	for _, p := range profiles {
		if p.Name == name {
			return p, true
		}
	}
	return cfg.CloudProfile{}, false
}
