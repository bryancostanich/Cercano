package capabilities

import "cercano/source/server/internal/capabilities/mcpadapter"

// mcpCatalogSource is set by the builtins package via init to avoid an import
// cycle (builtins imports capabilities). See builtins.init.
var mcpCatalogSource func() []mcpadapter.CapMeta

// RegisterMCPCatalogSource is called from builtins init.
func RegisterMCPCatalogSource(f func() []mcpadapter.CapMeta) { mcpCatalogSource = f }

// MCPCatalog returns the metadata for every mcp-surface capability.
// Returns nil if the builtins package has not been imported.
func MCPCatalog() []mcpadapter.CapMeta {
	if mcpCatalogSource == nil {
		return nil
	}
	return mcpCatalogSource()
}
