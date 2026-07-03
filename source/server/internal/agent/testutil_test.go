package agent

import (
	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
	"cercano/source/server/internal/capabilities/agentadapter"
	"cercano/source/server/internal/capabilities/builtins"
)

// testDefaultRegistry builds an *agenttools.Registry via the capability
// registry, mirroring what the server does at startup. Uses empty Services
// because the file/git builtins used in agent tests need no cloud providers.
func testDefaultRegistry() *agenttools.Registry {
	capReg := capabilities.NewRegistry(capabilities.Services{})
	builtins.Register(capReg)
	return agentadapter.BuildAgentRegistry(capReg, builtins.AgentAliases(), builtins.CapabilitySynonyms())
}
