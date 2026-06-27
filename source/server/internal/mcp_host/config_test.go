package mcphost

import (
	"os"
	"path/filepath"
	"testing"
)

func TestToolNameAndDisplay(t *testing.T) {
	fq := ToolName("github", "create_issue")
	if fq != "mcp__github__create_issue" {
		t.Fatalf("ToolName = %q", fq)
	}
	if got := DisplayName(fq); got != "mcp/github/create_issue" {
		t.Fatalf("DisplayName = %q", got)
	}
	// Non-mcp names pass through unchanged.
	if got := DisplayName("Read"); got != "Read" {
		t.Fatalf("DisplayName passthrough = %q", got)
	}
}

func TestLoadConfigYAMLAndJSONImport(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mcp.yaml"), []byte(`
mcpServers:
  github:
    command: npx
    args: ["-y", "server-github"]
    env:
      TOKEN: abc
`), 0o644)
	os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(`
{"mcpServers": {"fs": {"command": "mcp-fs", "args": ["/tmp"]},
                "github": {"command": "SHOULD_NOT_WIN"}}}`), 0o644)

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Servers["github"].Command != "npx" {
		t.Fatalf("yaml should win: %q", cfg.Servers["github"].Command)
	}
	if cfg.Servers["github"].Env["TOKEN"] != "abc" {
		t.Fatalf("env not parsed")
	}
	if cfg.Servers["fs"].Command != "mcp-fs" {
		t.Fatalf("json import missing fs: %+v", cfg.Servers)
	}
}

func TestLoadConfigMissingIsEmpty(t *testing.T) {
	cfg, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 0 {
		t.Fatalf("want empty, got %+v", cfg.Servers)
	}
}
