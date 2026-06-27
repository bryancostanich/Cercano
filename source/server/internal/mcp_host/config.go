package mcphost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

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

// LoadConfig reads <dir>/mcp.yaml as the canonical config and, if present,
// imports <dir>/mcp.json (Claude Code shape). On key collision YAML wins.
// Missing files are not an error — they yield an empty Config.
func LoadConfig(dir string) (Config, error) {
	out := Config{Servers: map[string]ServerConfig{}}

	if data, err := os.ReadFile(filepath.Join(dir, "mcp.json")); err == nil {
		var c Config
		if err := json.Unmarshal(data, &c); err != nil {
			return out, err
		}
		for k, v := range c.Servers {
			out.Servers[k] = v
		}
	} else if !os.IsNotExist(err) {
		return out, err
	}

	if data, err := os.ReadFile(filepath.Join(dir, "mcp.yaml")); err == nil {
		var c Config
		if err := yaml.Unmarshal(data, &c); err != nil {
			return out, err
		}
		for k, v := range c.Servers { // YAML overrides JSON
			out.Servers[k] = v
		}
	} else if !os.IsNotExist(err) {
		return out, err
	}

	return out, nil
}
