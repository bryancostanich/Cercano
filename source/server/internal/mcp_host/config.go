package mcphost

import "strings"

// ServerConfig describes one external MCP server launched over stdio.
type ServerConfig struct {
	Command string            `yaml:"command" json:"command"`
	Args    []string          `yaml:"args" json:"args"`
	Env     map[string]string `yaml:"env" json:"env"`
}

// Config is the parsed mcp.yaml. The YAML/JSON key is "mcpServers" to match
// Claude Code's .mcp.json shape.
type Config struct {
	Servers map[string]ServerConfig `yaml:"mcpServers" json:"mcpServers"`
}

// ToolName returns the model-facing tool name: mcp__<server>__<tool>. Double
// underscore because the Anthropic API rejects "/" in tool names.
func ToolName(server, tool string) string {
	return "mcp__" + server + "__" + tool
}

// DisplayName converts a model-facing mcp__a__b name to the human form
// mcp/a/b. Names without the mcp__ prefix pass through unchanged.
func DisplayName(fqName string) string {
	if !strings.HasPrefix(fqName, "mcp__") {
		return fqName
	}
	rest := strings.TrimPrefix(fqName, "mcp__")
	parts := strings.SplitN(rest, "__", 2)
	if len(parts) != 2 {
		return fqName
	}
	return "mcp/" + parts[0] + "/" + parts[1]
}
