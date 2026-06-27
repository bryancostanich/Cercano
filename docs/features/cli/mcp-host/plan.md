# MCP Host Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the standalone Cercano agent host external MCP servers — connect to them, register their tools into the shared tool registry so the model can call them in chat, gate them confirm-by-default, and manage them from the CLI via `/mcp`.

**Architecture:** A new `internal/mcp_host` package owns server lifecycle (background connect, supervise, restart) and wraps each external tool as an `agenttools.Tool` that proxies `tools/call` over stdio. Tools register into the *existing* `agenttools.Registry`, so the tool loop, `/tools`, and the LLM catalog are unchanged. The only behavioral change outside the new package is the permission gate: MCP tools confirm even in `permissive` mode unless allowlisted in `permissions.yaml`.

**Tech Stack:** Go 1.21+, `github.com/modelcontextprotocol/go-sdk v1.3.1` (already vendored — client + in-memory transport), gRPC/protobuf, `gopkg.in/yaml.v3`, Bubble Tea (CLI).

## Global Constraints

- **Module boundary:** server code lives in module `cercano/source/server`; CLI in `cercano/source/clients/cli`. CLI talks to the agent only through `pkg/` (`proto`, `agentclient`). No agent logic in the CLI.
- **Tool name to the model:** `mcp__<server>__<tool>` (double underscore). The Anthropic API rejects `/` in tool names. `mcp/<server>/<tool>` is display-only, derived in the CLI.
- **Trust:** MCP tools are untrusted third-party code. They confirm by default even in `permissive` mode. A `permissions.yaml` allowlist promotes to silent; `bypass` skips everything. Hints never lower the bar.
- **Servers are global** (machine-wide), declared in `~/.config/cercano/mcp.yaml`. No per-project config in this slice.
- **Boot is non-blocking.** A slow/dead server never blocks the agent or unrelated tools. A call to a not-ready server blocks only that call, up to a timeout.
- **stdio transport only.**
- **Commit messages:** never include the name "Claude" anywhere (per user global rules). End-of-message trailers are not required for this repo.
- **Build/test:** server — `cd source/server && go test ./... -count=1`; CLI — `cd source/clients/cli && go test ./... -count=1`.

---

## File Structure

**New — `source/server/internal/mcp_host/` (package `mcphost`):**
- `config.go` — `mcp.yaml` schema + loader, `.mcp.json` import, name helpers
- `config_test.go`
- `client.go` — one stdio MCP client connection; connect / list / call / close
- `client_test.go`
- `tool.go` — `mcpTool` (implements `agenttools.Tool` + `Origin()`); `tools/call` proxy + result mapping
- `tool_test.go`
- `manager.go` — lifecycle: background connect, per-server state, register/unregister into the shared registry, readiness wait, add/remove/restart
- `manager_test.go`

**Modified — server:**
- `internal/agenttools/tool.go` — add `Origin` type + `Originer` optional interface + `OriginOf` helper
- `internal/agent/permissions.go` — add `GateDecisionForMCP`, `mcpAllow` storage, `IsMCPAllowed`, `AddMCPAllow`
- `internal/agent/permissions_test.go` — gate + allowlist tests
- `internal/agent/pending.go` — `Decision{Allow,Persist}`; `Wait`→`Decision`; `Resolve(id, Decision)`
- `internal/agent/toolloop.go` — gate call uses `GateDecisionForMCP` with origin + allowlist
- `internal/server/server.go` — `McpManager` interface + `SetMcpManager`; host RPC handlers; requester persists MCP always-allow; `AllowToolCall` carries persist
- `source/proto/agent.proto` — `persist` on `AllowToolCallRequest`; 4 host RPCs + messages
- `pkg/agentclient/client.go` — client methods for the 4 host RPCs + `AllowToolCall` persist
- `cmd/cercano/main.go` — load mcp config, build manager, `Start` in background, `SetMcpManager`

**Modified — CLI:**
- `internal/slash/registry.go` (or a new `internal/slash/mcp.go`) — `/mcp` command
- `internal/ui/model.go` — `[a]lways allow` confirm key for MCP tools; `mcp__a__b`→`mcp/a/b` display

---

## Phase A — Config loader

### Task A1: mcp.yaml schema + name helpers

**Files:**
- Create: `source/server/internal/mcp_host/config.go`
- Test: `source/server/internal/mcp_host/config_test.go`

**Interfaces:**
- Produces: `type ServerConfig struct { Command string; Args []string; Env map[string]string }`; `type Config struct { Servers map[string]ServerConfig }`; `func ToolName(server, tool string) string`; `func DisplayName(fqName string) string`.

- [ ] **Step 1: Write the failing test**

```go
// source/server/internal/mcp_host/config_test.go
package mcphost

import "testing"

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/mcp_host/ -run TestToolNameAndDisplay -v`
Expected: FAIL (package/functions do not exist).

- [ ] **Step 3: Write minimal implementation**

```go
// source/server/internal/mcp_host/config.go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/mcp_host/ -run TestToolNameAndDisplay -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/mcp_host/config.go source/server/internal/mcp_host/config_test.go
git commit -m "feat(mcphost): server config schema + tool name helpers"
```

### Task A2: LoadConfig with .mcp.json import

**Files:**
- Modify: `source/server/internal/mcp_host/config.go`
- Test: `source/server/internal/mcp_host/config_test.go`

**Interfaces:**
- Produces: `func LoadConfig(dir string) (Config, error)` — reads `<dir>/mcp.yaml`; if `<dir>/mcp.json` exists, merges it in (YAML wins on key collision). Missing files yield an empty Config, not an error.

- [ ] **Step 1: Write the failing test**

```go
// append to config_test.go
import (
	"os"
	"path/filepath"
)

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/mcp_host/ -run TestLoadConfig -v`
Expected: FAIL (`LoadConfig` undefined).

- [ ] **Step 3: Write minimal implementation**

```go
// append to config.go
import (
	"encoding/json"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/mcp_host/ -run TestLoadConfig -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/mcp_host/config.go source/server/internal/mcp_host/config_test.go
git commit -m "feat(mcphost): load mcp.yaml with .mcp.json import"
```

---

## Phase B — MCP client connection

### Task B1: connect, list, call, close over a transport

**Files:**
- Create: `source/server/internal/mcp_host/client.go`
- Test: `source/server/internal/mcp_host/client_test.go`

**Interfaces:**
- Produces: `type remoteTool struct { Name, Description string; Schema json.RawMessage; Destructive bool }`; `type conn struct { sess *mcp.ClientSession }`; `func dial(ctx context.Context, t mcp.Transport) (*conn, error)`; `func (c *conn) listTools(ctx context.Context) ([]remoteTool, error)`; `func (c *conn) call(ctx context.Context, tool string, args json.RawMessage) (string, bool, error)` (text, isError, transportErr); `func (c *conn) close() error`.
- Consumes: go-sdk `mcp` package.

- [ ] **Step 1: Write the failing test** (uses the in-memory transport pair + a real in-process MCP server)

```go
// source/server/internal/mcp_host/client_test.go
package mcphost

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoIn struct {
	Text string `json:"text" jsonschema:"the text to echo"`
}

func startTestServer(t *testing.T, ctx context.Context) mcp.Transport {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "echo", Description: "echoes text"},
		func(ctx context.Context, req *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "echo:" + in.Text}},
			}, nil, nil
		})
	serverT, clientT := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(ctx, serverT) }()
	return clientT
}

func TestConnListCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientT := startTestServer(t, ctx)

	c, err := dial(ctx, clientT)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()

	tools, err := c.listTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}

	text, isErr, err := c.call(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if text != "echo:hi" {
		t.Fatalf("call result = %q", text)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/mcp_host/ -run TestConnListCall -v`
Expected: FAIL (`dial` undefined).

- [ ] **Step 3: Write minimal implementation**

```go
// source/server/internal/mcp_host/client.go
package mcphost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// remoteTool is a tool as advertised by an external MCP server.
type remoteTool struct {
	Name        string
	Description string
	Schema      json.RawMessage
	Destructive bool
}

// conn is one live MCP client session over a transport.
type conn struct {
	sess *mcp.ClientSession
}

// dial connects an MCP client over the given transport and completes the
// initialize handshake.
func dial(ctx context.Context, t mcp.Transport) (*conn, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "cercano", Version: "0.1.0"}, nil)
	sess, err := client.Connect(ctx, t, nil)
	if err != nil {
		return nil, err
	}
	return &conn{sess: sess}, nil
}

// listTools enumerates the server's advertised tools, marshaling each input
// schema back to raw JSON for the agent's tool catalog.
func (c *conn) listTools(ctx context.Context) ([]remoteTool, error) {
	res, err := c.sess.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]remoteTool, 0, len(res.Tools))
	for _, t := range res.Tools {
		schema, err := json.Marshal(t.InputSchema)
		if err != nil {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		destructive := false
		if t.Annotations != nil && t.Annotations.DestructiveHint != nil {
			destructive = *t.Annotations.DestructiveHint
		}
		out = append(out, remoteTool{
			Name:        t.Name,
			Description: t.Description,
			Schema:      schema,
			Destructive: destructive,
		})
	}
	return out, nil
}

// call invokes a tool. Returns (text, isToolError, transportError). A tool-level
// error (isToolError) carries its message in text; a transport error means the
// call never completed.
func (c *conn) call(ctx context.Context, tool string, args json.RawMessage) (string, bool, error) {
	var arguments any
	if len(args) > 0 {
		arguments = args
	}
	res, err := c.sess.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: arguments})
	if err != nil {
		return "", false, err
	}
	return flattenContent(res.Content), res.IsError, nil
}

func (c *conn) close() error {
	if c.sess == nil {
		return nil
	}
	return c.sess.Close()
}

// flattenContent concatenates the text parts of an MCP tool result. Non-text
// content (images, resources) is ignored in v1.
func flattenContent(content []mcp.Content) string {
	var b strings.Builder
	for _, part := range content {
		if tc, ok := part.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String()
}

var errNoSession = errors.New("mcp: no live session")
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/mcp_host/ -run TestConnListCall -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/mcp_host/client.go source/server/internal/mcp_host/client_test.go
git commit -m "feat(mcphost): stdio MCP client — connect, list, call"
```

---

## Phase C — Tool origin + mcpTool adapter

### Task C1: add Origin to agenttools (optional interface)

**Files:**
- Modify: `source/server/internal/agenttools/tool.go`
- Test: `source/server/internal/agenttools/agenttools_test.go`

**Interfaces:**
- Produces: `type Origin string`; `const OriginBuiltin Origin = "builtin"`, `OriginMCP Origin = "mcp"`; `type Originer interface { Origin() Origin }`; `func OriginOf(t Tool) Origin`.

- [ ] **Step 1: Write the failing test**

```go
// append to source/server/internal/agenttools/agenttools_test.go
func TestOriginOfDefaultsBuiltin(t *testing.T) {
	if got := OriginOf(ReadFile()); got != OriginBuiltin {
		t.Fatalf("builtin tool origin = %q, want builtin", got)
	}
}

type fakeMCP struct{ readFileTool }

func (fakeMCP) Origin() Origin { return OriginMCP }

func TestOriginOfHonorsOptionalInterface(t *testing.T) {
	if got := OriginOf(fakeMCP{}); got != OriginMCP {
		t.Fatalf("origin = %q, want mcp", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/agenttools/ -run TestOriginOf -v`
Expected: FAIL (`Origin`/`OriginOf` undefined).

- [ ] **Step 3: Write minimal implementation**

```go
// append to source/server/internal/agenttools/tool.go

// Origin tags where a Tool came from. Built-ins are first-party; MCP tools are
// third-party code hosted from an external server and are gated more strictly.
type Origin string

const (
	OriginBuiltin Origin = "builtin"
	OriginMCP     Origin = "mcp"
)

// Originer is an optional interface a Tool may implement to declare its origin.
// Tools that do not implement it are treated as built-in, so the 15 first-party
// tools need no changes.
type Originer interface {
	Origin() Origin
}

// OriginOf returns a tool's origin, defaulting to OriginBuiltin when the tool
// does not implement Originer.
func OriginOf(t Tool) Origin {
	if o, ok := t.(Originer); ok {
		return o.Origin()
	}
	return OriginBuiltin
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/agenttools/ -run TestOriginOf -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/agenttools/tool.go source/server/internal/agenttools/agenttools_test.go
git commit -m "feat(agenttools): optional Origin interface (builtin default, mcp opt-in)"
```

### Task C2: mcpTool adapter

**Files:**
- Create: `source/server/internal/mcp_host/tool.go`
- Test: `source/server/internal/mcp_host/tool_test.go`

**Interfaces:**
- Consumes: `remoteTool` (B1); `agenttools.Tool`, `agenttools.PermW`, `agenttools.OriginMCP`, `agenttools.NewTextResult` (C1 + existing); `ToolName` (A1).
- Produces: `type readyFunc func(ctx context.Context) (*conn, error)`; `func newMCPTool(server string, rt remoteTool, ready readyFunc) *mcpTool`. `*mcpTool` implements `agenttools.Tool` + `Origin()`.

- [ ] **Step 1: Write the failing test**

```go
// source/server/internal/mcp_host/tool_test.go
package mcphost

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"cercano/source/server/internal/agenttools"
)

func TestMCPToolMetadata(t *testing.T) {
	rt := remoteTool{Name: "create_issue", Description: "make an issue",
		Schema: json.RawMessage(`{"type":"object"}`)}
	tl := newMCPTool("github", rt, nil)

	if tl.Name() != "mcp__github__create_issue" {
		t.Fatalf("name = %q", tl.Name())
	}
	if tl.Permission() != agenttools.PermW {
		t.Fatalf("permission = %q, want W", tl.Permission())
	}
	if agenttools.OriginOf(tl) != agenttools.OriginMCP {
		t.Fatalf("origin not mcp")
	}
	if string(tl.Schema()) != `{"type":"object"}` {
		t.Fatalf("schema = %s", tl.Schema())
	}
}

func TestMCPToolExecuteProxies(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientT := startTestServer(t, ctx) // from client_test.go
	c, err := dial(ctx, clientT)
	if err != nil {
		t.Fatal(err)
	}
	ready := func(ctx context.Context) (*conn, error) { return c, nil }

	tl := newMCPTool("test", remoteTool{Name: "echo"}, ready)
	res, err := tl.Execute(ctx, json.RawMessage(`{"text":"yo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "echo:yo" {
		t.Fatalf("text = %q", res.Text)
	}
}

func TestMCPToolExecuteUnavailable(t *testing.T) {
	ready := func(ctx context.Context) (*conn, error) {
		return nil, errors.New("warming")
	}
	tl := newMCPTool("github", remoteTool{Name: "x"}, ready)
	_, err := tl.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("want error when server unavailable")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/mcp_host/ -run TestMCPTool -v`
Expected: FAIL (`newMCPTool` undefined).

- [ ] **Step 3: Write minimal implementation**

```go
// source/server/internal/mcp_host/tool.go
package mcphost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"cercano/source/server/internal/agenttools"
)

// readyFunc resolves a live connection to the tool's server, blocking until the
// server is ready or returning an error if it is unavailable.
type readyFunc func(ctx context.Context) (*conn, error)

// mcpTool adapts one external MCP tool to the agent's Tool interface. Every MCP
// tool is PermW so it routes through the permission gate (R-tier tools bypass
// the gate entirely); the gate then forces a confirm-by-default for MCP origin
// unless allowlisted.
type mcpTool struct {
	server      string
	tool        string
	fqName      string
	desc        string
	schema      json.RawMessage
	destructive bool
	ready       readyFunc
}

func newMCPTool(server string, rt remoteTool, ready readyFunc) *mcpTool {
	schema := rt.Schema
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object"}`)
	}
	return &mcpTool{
		server:      server,
		tool:        rt.Name,
		fqName:      ToolName(server, rt.Name),
		desc:        rt.Description,
		schema:      schema,
		destructive: rt.Destructive,
		ready:       ready,
	}
}

func (t *mcpTool) Name() string                    { return t.fqName }
func (t *mcpTool) Description() string              { return t.desc }
func (t *mcpTool) Permission() agenttools.Permission { return agenttools.PermW }
func (t *mcpTool) Origin() agenttools.Origin        { return agenttools.OriginMCP }
func (t *mcpTool) Schema() json.RawMessage          { return t.schema }

func (t *mcpTool) Execute(ctx context.Context, raw json.RawMessage) (*agenttools.Result, error) {
	c, err := t.ready(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q unavailable — /mcp restart %s", t.server, t.server)
	}
	text, isToolErr, callErr := c.call(ctx, t.tool, raw)
	if callErr != nil {
		return nil, fmt.Errorf("mcp %s: %w", t.fqName, callErr)
	}
	if isToolErr {
		if text == "" {
			text = "tool reported an error"
		}
		return nil, errors.New(text)
	}
	res := agenttools.NewTextResult(text)
	res.Detail = "mcp"
	return res, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/mcp_host/ -run TestMCPTool -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/mcp_host/tool.go source/server/internal/mcp_host/tool_test.go
git commit -m "feat(mcphost): mcpTool adapter proxying tools/call"
```

---

## Phase D — Manager (lifecycle + registration)

### Task D1: manager — background connect, register, status, readiness wait

**Files:**
- Create: `source/server/internal/mcp_host/manager.go`
- Test: `source/server/internal/mcp_host/manager_test.go`

**Interfaces:**
- Consumes: `Config`/`ServerConfig` (A), `dial`/`conn` (B), `newMCPTool` (C2), `agenttools.Registry`.
- Produces:
  - `type ServerState string` with `StateWarming`, `StateReady`, `StateFailed`.
  - `type ServerStatus struct { Name string; State ServerState; ToolCount int; Err string }`.
  - `type Manager struct { ... }`.
  - `func New(reg *agenttools.Registry, dir string, callWait time.Duration) *Manager`.
  - `func (m *Manager) startServer(ctx context.Context, name string, cfg ServerConfig)` — connect+list+register, used by Start/Add/Restart.
  - `func (m *Manager) List() []ServerStatus`.
  - For testing seams: a `dialFn func(ctx, ServerConfig) (*conn, error)` field defaulting to a stdio dial, so tests inject an in-memory connection.

Because spawning real subprocesses in unit tests is brittle, `Manager` dials through an injectable `dialFn`. The default builds a `mcp.CommandTransport`; tests substitute an in-memory transport.

- [ ] **Step 1: Write the failing test**

```go
// source/server/internal/mcp_host/manager_test.go
package mcphost

import (
	"context"
	"testing"
	"time"

	"cercano/source/server/internal/agenttools"
)

func TestManagerRegistersToolsOnStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := agenttools.NewRegistry()
	m := New(reg, t.TempDir(), time.Second)
	// Inject in-memory dial: ignore cfg, connect to a live test server.
	m.dialFn = func(ctx context.Context, _ ServerConfig) (*conn, error) {
		return dial(ctx, startTestServer(t, ctx))
	}

	m.startServer(ctx, "test", ServerConfig{Command: "ignored"})

	if _, ok := reg.Get("mcp__test__echo"); !ok {
		t.Fatalf("echo tool not registered; have %d tools", len(reg.All()))
	}
	st := m.List()
	if len(st) != 1 || st[0].State != StateReady || st[0].ToolCount != 1 {
		t.Fatalf("status = %+v", st)
	}
}

func TestManagerMarksFailedOnDialError(t *testing.T) {
	reg := agenttools.NewRegistry()
	m := New(reg, t.TempDir(), time.Second)
	m.dialFn = func(ctx context.Context, _ ServerConfig) (*conn, error) {
		return nil, context.DeadlineExceeded
	}
	m.startServer(context.Background(), "broken", ServerConfig{Command: "x"})

	st := m.List()
	if len(st) != 1 || st[0].State != StateFailed {
		t.Fatalf("want failed, got %+v", st)
	}
	if len(reg.All()) != 0 {
		t.Fatalf("failed server must register no tools")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/mcp_host/ -run TestManager -v`
Expected: FAIL (`New`/`Manager` undefined).

- [ ] **Step 3: Write minimal implementation**

```go
// source/server/internal/mcp_host/manager.go
package mcphost

import (
	"context"
	"os/exec"
	"sync"
	"time"

	"cercano/source/server/internal/agenttools"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ServerState string

const (
	StateWarming ServerState = "warming"
	StateReady   ServerState = "ready"
	StateFailed  ServerState = "failed"
)

// ServerStatus is a point-in-time view of one hosted server.
type ServerStatus struct {
	Name      string
	State     ServerState
	ToolCount int
	Err       string
}

type serverHandle struct {
	name    string
	cfg     ServerConfig
	mu      sync.Mutex
	conn    *conn
	state   ServerState
	err     string
	tools   []string // registered fully-qualified tool names
	readyCh chan struct{}
}

// Manager owns the lifecycle of all hosted MCP servers and registers their
// tools into the shared agent tool registry.
type Manager struct {
	reg      *agenttools.Registry
	dir      string
	callWait time.Duration

	mu      sync.Mutex
	servers map[string]*serverHandle

	// dialFn is the connection seam; the default spawns the configured command
	// over stdio. Tests override it with an in-memory transport.
	dialFn func(ctx context.Context, cfg ServerConfig) (*conn, error)
}

func New(reg *agenttools.Registry, dir string, callWait time.Duration) *Manager {
	m := &Manager{
		reg:      reg,
		dir:      dir,
		callWait: callWait,
		servers:  map[string]*serverHandle{},
	}
	m.dialFn = m.stdioDial
	return m
}

// stdioDial launches the configured command and connects over stdin/stdout.
func (m *Manager) stdioDial(ctx context.Context, cfg ServerConfig) (*conn, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	if len(cfg.Env) > 0 {
		env := cmd.Environ()
		for k, v := range cfg.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	return dial(ctx, &mcp.CommandTransport{Command: cmd})
}

// startServer connects to one server, lists its tools, and registers them.
// Synchronous: callers that want background warm-up invoke it in a goroutine
// (see Start). A failed connect marks the server failed and registers nothing.
func (m *Manager) startServer(ctx context.Context, name string, cfg ServerConfig) {
	h := &serverHandle{name: name, cfg: cfg, state: StateWarming, readyCh: make(chan struct{})}
	m.mu.Lock()
	m.servers[name] = h
	m.mu.Unlock()

	c, err := m.dialFn(ctx, cfg)
	if err != nil {
		h.fail(err)
		return
	}
	tools, err := c.listTools(ctx)
	if err != nil {
		_ = c.close()
		h.fail(err)
		return
	}

	h.mu.Lock()
	h.conn = c
	h.state = StateReady
	for _, rt := range tools {
		tl := newMCPTool(name, rt, h.ready(m.callWait))
		if err := m.reg.Register(tl); err == nil {
			h.tools = append(h.tools, tl.Name())
		}
	}
	close(h.readyCh)
	h.mu.Unlock()
}

func (h *serverHandle) fail(err error) {
	h.mu.Lock()
	h.state = StateFailed
	h.err = err.Error()
	close(h.readyCh)
	h.mu.Unlock()
}

// ready returns a readyFunc that blocks until this server is ready (or fails /
// times out). In the common path the server is already ready and it returns
// immediately; the wait covers an in-flight reconnect.
func (h *serverHandle) ready(wait time.Duration) readyFunc {
	return func(ctx context.Context) (*conn, error) {
		h.mu.Lock()
		if h.state == StateReady && h.conn != nil {
			c := h.conn
			h.mu.Unlock()
			return c, nil
		}
		ch := h.readyCh
		h.mu.Unlock()

		select {
		case <-ch:
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.state == StateReady && h.conn != nil {
				return h.conn, nil
			}
			return nil, errNoSession
		case <-time.After(wait):
			return nil, errNoSession
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// List returns a snapshot of every hosted server's status.
func (m *Manager) List() []ServerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ServerStatus, 0, len(m.servers))
	for _, h := range m.servers {
		h.mu.Lock()
		out = append(out, ServerStatus{Name: h.name, State: h.state, ToolCount: len(h.tools), Err: h.err})
		h.mu.Unlock()
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/mcp_host/ -run TestManager -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/mcp_host/manager.go source/server/internal/mcp_host/manager_test.go
git commit -m "feat(mcphost): manager connects, registers tools, tracks state"
```

### Task D2: Start (background warm-all) + Add / Remove / Restart

**Files:**
- Modify: `source/server/internal/mcp_host/manager.go`
- Test: `source/server/internal/mcp_host/manager_test.go`

**Interfaces:**
- Produces: `func (m *Manager) Start(ctx context.Context)`; `func (m *Manager) Add(ctx, name string, cfg ServerConfig) error`; `func (m *Manager) Remove(ctx, name string) error`; `func (m *Manager) Restart(ctx, name string) error`. Add/Remove persist to `<dir>/mcp.yaml`.

- [ ] **Step 1: Write the failing test**

```go
// append to manager_test.go
func TestManagerRestartReregisters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := agenttools.NewRegistry()
	m := New(reg, t.TempDir(), time.Second)
	m.dialFn = func(ctx context.Context, _ ServerConfig) (*conn, error) {
		return dial(ctx, startTestServer(t, ctx))
	}
	m.startServer(ctx, "test", ServerConfig{Command: "x"})
	if _, ok := reg.Get("mcp__test__echo"); !ok {
		t.Fatal("precondition: echo registered")
	}
	if err := m.Restart(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("mcp__test__echo"); !ok {
		t.Fatal("echo should be re-registered after restart")
	}
}

func TestManagerRemoveUnregisters(t *testing.T) {
	ctx := context.Background()
	reg := agenttools.NewRegistry()
	m := New(reg, t.TempDir(), time.Second)
	m.dialFn = func(ctx context.Context, _ ServerConfig) (*conn, error) {
		return dial(ctx, startTestServer(t, ctx))
	}
	m.startServer(ctx, "test", ServerConfig{Command: "x"})
	if err := m.Remove(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("mcp__test__echo"); ok {
		t.Fatal("echo should be gone after remove")
	}
	if len(m.List()) != 0 {
		t.Fatal("server should be dropped from status")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/mcp_host/ -run "TestManagerRestart|TestManagerRemove" -v`
Expected: FAIL (`Restart`/`Remove` undefined).

- [ ] **Step 3: Write minimal implementation**

```go
// append to manager.go
import (
	"os"            // add to import block
	"path/filepath" // add to import block

	"gopkg.in/yaml.v3" // add to import block
)

// Start connects to every configured server in the background. Returns
// immediately; tools appear in the registry as each server finishes listing.
func (m *Manager) Start(ctx context.Context) {
	cfg, _ := LoadConfig(m.dir)
	for name, sc := range cfg.Servers {
		name, sc := name, sc
		go m.startServer(ctx, name, sc)
	}
}

// Add connects a new server now and persists it to mcp.yaml.
func (m *Manager) Add(ctx context.Context, name string, cfg ServerConfig) error {
	m.startServer(ctx, name, cfg)
	return m.persistAdd(name, cfg)
}

// Remove stops a server, unregisters its tools, and drops it from mcp.yaml.
func (m *Manager) Remove(ctx context.Context, name string) error {
	m.mu.Lock()
	h := m.servers[name]
	delete(m.servers, name)
	m.mu.Unlock()
	if h != nil {
		m.teardown(h)
	}
	return m.persistRemove(name)
}

// Restart stops a server (keeping its config) and reconnects it.
func (m *Manager) Restart(ctx context.Context, name string) error {
	m.mu.Lock()
	h := m.servers[name]
	m.mu.Unlock()
	if h == nil {
		return os.ErrNotExist
	}
	cfg := h.cfg
	m.teardown(h)
	m.mu.Lock()
	delete(m.servers, name)
	m.mu.Unlock()
	m.startServer(ctx, name, cfg)
	return nil
}

// teardown unregisters a server's tools and closes its connection.
func (m *Manager) teardown(h *serverHandle) {
	h.mu.Lock()
	for _, name := range h.tools {
		m.reg.Unregister(name)
	}
	h.tools = nil
	c := h.conn
	h.conn = nil
	h.mu.Unlock()
	if c != nil {
		_ = c.close()
	}
}

func (m *Manager) persistAdd(name string, cfg ServerConfig) error {
	c, _ := LoadConfig(m.dir)
	if c.Servers == nil {
		c.Servers = map[string]ServerConfig{}
	}
	c.Servers[name] = cfg
	return m.writeYAML(c)
}

func (m *Manager) persistRemove(name string) error {
	c, _ := LoadConfig(m.dir)
	delete(c.Servers, name)
	return m.writeYAML(c)
}

func (m *Manager) writeYAML(c Config) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.dir, "mcp.yaml"), data, 0o644)
}
```

This task depends on `Registry.Unregister`, added next.

- [ ] **Step 2b: Add `Registry.Unregister` (the test won't compile without it)**

Add to `source/server/internal/agenttools/registry.go`:

```go
// Unregister removes a Tool by name. No-op if absent. Used by the MCP host to
// drop a server's tools on remove/restart.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}
```

- [ ] **Step 3: Run the tests** (re-run from Step 2)

Run: `cd source/server && go test ./internal/mcp_host/ ./internal/agenttools/ -count=1`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add source/server/internal/mcp_host/manager.go source/server/internal/mcp_host/manager_test.go source/server/internal/agenttools/registry.go
git commit -m "feat(mcphost): Start/Add/Remove/Restart + registry Unregister"
```

---

## Phase E — Permission gate, allowlist, persist

### Task E1: allowlist storage + MCP gate decision

**Files:**
- Modify: `source/server/internal/agent/permissions.go`
- Test: `source/server/internal/agent/permissions_test.go`

**Interfaces:**
- Produces:
  - `func GateDecisionForMCP(mode PermissionMode, tier llm.Permission, isMCP, allowlisted bool) bool`.
  - `func (s *PermissionStore) IsMCPAllowed(name string) bool`.
  - `func (s *PermissionStore) AddMCPAllow(pattern string) error`.
  - `permsFile` gains `MCPAllow []string yaml:"mcp_allow"`.

- [ ] **Step 1: Write the failing test**

```go
// append to source/server/internal/agent/permissions_test.go
import "path/filepath" // if not already imported

func TestGateDecisionForMCP(t *testing.T) {
	cases := []struct {
		mode        PermissionMode
		isMCP       bool
		allowlisted bool
		want        bool // true == confirm required
	}{
		// Built-in W in permissive: silent (unchanged behavior).
		{ModePermissive, false, false, false},
		// MCP in permissive, not allowlisted: confirm.
		{ModePermissive, true, false, true},
		// MCP in permissive, allowlisted: silent.
		{ModePermissive, true, true, false},
		// MCP under bypass: silent.
		{ModeBypass, true, false, false},
		// MCP under strict, not allowlisted: confirm.
		{ModeStrict, true, false, true},
	}
	for _, c := range cases {
		got := GateDecisionForMCP(c.mode, llm.PermW, c.isMCP, c.allowlisted)
		if got != c.want {
			t.Errorf("mode=%s mcp=%v allow=%v: got %v want %v",
				c.mode, c.isMCP, c.allowlisted, got, c.want)
		}
	}
}

func TestMCPAllowlistGlob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permissions.yaml")
	s, _ := LoadPermissionStore(path)
	if err := s.AddMCPAllow("mcp__github__*"); err != nil {
		t.Fatal(err)
	}
	if !s.IsMCPAllowed("mcp__github__create_issue") {
		t.Fatal("glob should match")
	}
	if s.IsMCPAllowed("mcp__gitlab__create_issue") {
		t.Fatal("glob should not match other server")
	}
	// Persists across reload, preserving mode.
	s.SetMode(ModeStrict)
	s2, _ := LoadPermissionStore(path)
	if !s2.IsMCPAllowed("mcp__github__create_issue") {
		t.Fatal("allowlist not persisted")
	}
	if s2.Mode() != ModeStrict {
		t.Fatal("mode clobbered by allowlist write")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/agent/ -run "TestGateDecisionForMCP|TestMCPAllowlistGlob" -v`
Expected: FAIL (functions undefined).

- [ ] **Step 3: Write minimal implementation**

Replace `permsFile`, add the allowlist field/methods, and refactor writes through `persistLocked`:

```go
// in permissions.go — replace the permsFile type
type permsFile struct {
	Mode     string   `yaml:"mode"`
	MCPAllow []string `yaml:"mcp_allow"`
}

// add mcpAllow to the struct
type PermissionStore struct {
	mu       sync.Mutex
	path     string
	mode     PermissionMode
	mcpAllow []string
}
```

Update `LoadPermissionStore` to populate `mcpAllow`:

```go
	if f.Mode != "" {
		if m, err := ParseMode(f.Mode); err == nil {
			s.mode = m
		}
	}
	s.mcpAllow = f.MCPAllow
	return s, nil
```

Replace `SetMode` body with a shared writer:

```go
func (s *PermissionStore) SetMode(m PermissionMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = m
	return s.persistLocked()
}

// persistLocked writes mode + allowlist. Caller holds s.mu.
func (s *PermissionStore) persistLocked() error {
	data, _ := yaml.Marshal(permsFile{Mode: string(s.mode), MCPAllow: s.mcpAllow})
	return os.WriteFile(s.path, data, 0o644)
}

// AddMCPAllow appends a glob pattern (e.g. "mcp__github__*") to the silent
// allowlist and persists it. Idempotent.
func (s *PermissionStore) AddMCPAllow(pattern string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.mcpAllow {
		if p == pattern {
			return nil
		}
	}
	s.mcpAllow = append(s.mcpAllow, pattern)
	return s.persistLocked()
}

// IsMCPAllowed reports whether an mcp__server__tool name matches any allowlist
// pattern. Re-reads the file so hand-edits take effect live, mirroring Mode().
func (s *PermissionStore) IsMCPAllowed(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if data, err := os.ReadFile(s.path); err == nil {
		var f permsFile
		if yaml.Unmarshal(data, &f) == nil {
			s.mcpAllow = f.MCPAllow
		}
	}
	for _, pat := range s.mcpAllow {
		if ok, _ := path.Match(pat, name); ok {
			return true
		}
	}
	return false
}

// GateDecisionForMCP extends GateDecision with MCP origin. MCP tools are
// untrusted third-party code: they confirm by default even in permissive mode,
// unless allowlisted. Bypass still skips everything.
func GateDecisionForMCP(mode PermissionMode, tier llm.Permission, isMCP, allowlisted bool) bool {
	if mode == ModeBypass {
		return false
	}
	if isMCP {
		return !allowlisted
	}
	return GateDecision(mode, tier)
}
```

Add `"path"` to the imports in `permissions.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/agent/ -run "TestGateDecisionForMCP|TestMCPAllowlistGlob" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/agent/permissions.go source/server/internal/agent/permissions_test.go
git commit -m "feat(agent): MCP allowlist + confirm-by-default gate decision"
```

### Task E2: wire the MCP gate into the tool loop

**Files:**
- Modify: `source/server/internal/agent/toolloop.go`
- Test: `source/server/internal/agent/toolloop_test.go`

**Interfaces:**
- Consumes: `GateDecisionForMCP`, `IsMCPAllowed` (E1); `agenttools.OriginOf`, `agenttools.OriginMCP` (C1).

- [ ] **Step 1: Write the failing test** (an MCP-origin tool forces a permission request even in permissive)

```go
// append to toolloop_test.go — assumes existing fakeProvider / fakePerms helpers.
// A minimal MCP-origin tool that records whether it executed.
type fakeMCPTool struct {
	executed bool
}

func (*fakeMCPTool) Name() string                       { return "mcp__x__do" }
func (*fakeMCPTool) Description() string                 { return "d" }
func (*fakeMCPTool) Permission() agenttools.Permission   { return agenttools.PermW }
func (*fakeMCPTool) Origin() agenttools.Origin           { return agenttools.OriginMCP }
func (*fakeMCPTool) Schema() json.RawMessage             { return json.RawMessage(`{"type":"object"}`) }
func (m *fakeMCPTool) Execute(context.Context, json.RawMessage) (*agenttools.Result, error) {
	m.executed = true
	return agenttools.NewTextResult("ok"), nil
}

func TestToolLoopMCPConfirmsInPermissive(t *testing.T) {
	reg := agenttools.NewRegistry()
	reg.MustRegister(&fakeMCPTool{})

	// Provider that asks to call mcp__x__do once, then stops.
	prov := newScriptedProvider(t, "mcp__x__do", `{}`) // see existing scripted helper
	perms := newPermStore(t, ModePermissive)           // existing helper

	denied := false
	_, err := RunToolLoop(context.Background(), ToolLoopInput{
		Provider: prov, Registry: reg, Permissions: perms, UserInput: "go",
		PermissionRequester: func(context.Context, string, string, json.RawMessage, llm.Permission) (bool, error) {
			denied = true // a confirm was requested
			return false, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !denied {
		t.Fatal("MCP tool must trigger a permission request in permissive mode")
	}
}
```

> Note for the implementer: match `newScriptedProvider` / `newPermStore` to the existing test helpers in `toolloop_test.go`; the assertion that matters is that `PermissionRequester` is invoked for an MCP-origin W tool under `ModePermissive`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/agent/ -run TestToolLoopMCPConfirmsInPermissive -v`
Expected: FAIL — currently `GateDecision(ModePermissive, PermW)` is `false`, so no confirm is requested and `denied` stays false.

- [ ] **Step 3: Write minimal implementation**

In `toolloop.go`, replace the gate check at the top of the `wxCalls` loop:

```go
	for _, pc := range wxCalls {
		isMCP := agenttools.OriginOf(pc.tool) == agenttools.OriginMCP
		allowlisted := in.Permissions != nil && in.Permissions.IsMCPAllowed(pc.block.ToolName)
		if GateDecisionForMCP(mode, pc.tier, isMCP, allowlisted) {
			// ... existing PermissionRequester block unchanged ...
```

(Only the `if GateDecision(mode, pc.tier)` line changes to the three lines above.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/agent/ -count=1`
Expected: PASS (new test + existing loop tests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/agent/toolloop.go source/server/internal/agent/toolloop_test.go
git commit -m "feat(agent): tool loop confirms MCP tools unless allowlisted"
```

### Task E3: persist decision (`Decision{Allow,Persist}`) through pending + RPC

**Files:**
- Modify: `source/server/internal/agent/pending.go`
- Modify: `source/server/internal/server/server.go` (AllowToolCall/DenyToolCall handlers + requester closure)
- Modify: `source/proto/agent.proto` (`persist` field) + regen
- Test: `source/server/internal/agent/pending_test.go`

**Interfaces:**
- Produces: `type Decision struct { Allow, Persist bool }`; `func (p *PendingDecisions) Wait(ctx, id) (Decision, error)`; `func (p *PendingDecisions) Resolve(id string, d Decision) bool`.

- [ ] **Step 1: Read the current pending.go**

Run: `sed -n '1,60p' source/server/internal/agent/pending.go` — note current `Wait` returns `(bool, error)` and `Resolve(id string, allow bool) bool`, backed by a `map[string]chan bool`.

- [ ] **Step 2: Write the failing test**

```go
// source/server/internal/agent/pending_test.go (add or extend)
func TestPendingCarriesPersist(t *testing.T) {
	p := NewPendingDecisions()
	go func() { p.Resolve("t1", Decision{Allow: true, Persist: true}) }()
	d, err := p.Wait(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allow || !d.Persist {
		t.Fatalf("decision = %+v", d)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd source/server && go test ./internal/agent/ -run TestPendingCarriesPersist -v`
Expected: FAIL (`Decision` undefined; signatures mismatch).

- [ ] **Step 4: Implement — change the channel payload to `Decision`**

Edit `pending.go`: change the internal map to `map[string]chan Decision`, and:

```go
type Decision struct {
	Allow   bool
	Persist bool
}

func (p *PendingDecisions) Wait(ctx context.Context, toolUseID string) (Decision, error) {
	p.mu.Lock()
	ch := make(chan Decision, 1)
	p.waiters[toolUseID] = ch
	p.mu.Unlock()
	select {
	case d := <-ch:
		return d, nil
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.waiters, toolUseID)
		p.mu.Unlock()
		return Decision{}, ctx.Err()
	}
}

func (p *PendingDecisions) Resolve(toolUseID string, d Decision) bool {
	p.mu.Lock()
	ch, ok := p.waiters[toolUseID]
	delete(p.waiters, toolUseID)
	p.mu.Unlock()
	if !ok {
		return false
	}
	ch <- d
	return true
}
```

(Rename the field to `waiters` if the current field name differs; keep the existing `NewPendingDecisions` initializer in sync.)

- [ ] **Step 5: Update the proto** — add `persist` to `AllowToolCallRequest`

In `source/proto/agent.proto`:

```proto
message AllowToolCallRequest  { string tool_use_id = 1; bool persist = 2; }
```

Regenerate (protoc per `source/server/README.md`):

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
protoc --go_out=. --go_opt=module=cercano/source/server \
       --go-grpc_out=. --go-grpc_opt=module=cercano/source/server \
       --proto_path=source/proto source/proto/agent.proto
```

> If the generated files land outside `source/server/pkg/proto/`, match the invocation already used to produce the current `pkg/proto/*.pb.go` (same `protoc` v7.34.1 toolchain). Verify with `git diff --stat source/server/pkg/proto`.

- [ ] **Step 6: Update server handlers + requester** (`internal/server/server.go`)

```go
// AllowToolCall — carry persist into the decision.
func (s *Server) AllowToolCall(ctx context.Context, req *proto.AllowToolCallRequest) (*proto.AllowToolCallResponse, error) {
	if s.pendingDecisions == nil {
		return &proto.AllowToolCallResponse{Ok: false}, nil
	}
	ok := s.pendingDecisions.Resolve(req.GetToolUseId(), agent.Decision{Allow: true, Persist: req.GetPersist()})
	return &proto.AllowToolCallResponse{Ok: ok}, nil
}

// DenyToolCall
	ok := s.pendingDecisions.Resolve(req.GetToolUseId(), agent.Decision{Allow: false})
```

In the streaming `requester` closure (around server.go:1135), replace the final `return s.pendingDecisions.Wait(ctx, toolUseID)` with:

```go
	d, err := s.pendingDecisions.Wait(ctx, toolUseID)
	if err != nil {
		return false, err
	}
	if d.Allow && d.Persist && s.permStore != nil {
		if tool, ok := s.toolRegistry.Get(name); ok && agenttools.OriginOf(tool) == agenttools.OriginMCP {
			_ = s.permStore.AddMCPAllow(name)
		}
	}
	return d.Allow, nil
```

Ensure `agenttools` is imported in server.go (it already is — used by `SetToolRegistry`).

- [ ] **Step 7: Run the full server module test**

Run: `cd source/server && go build ./... && go test ./internal/agent/ ./internal/server/ -count=1`
Expected: PASS. (Existing tests calling `Resolve(id, true)` / consuming `Wait`'s bool must be updated to the new `Decision` signature — fix any compile errors in `*_test.go` by wrapping in `Decision{Allow: ...}` and reading `d.Allow`.)

- [ ] **Step 8: Commit**

```bash
git add source/server/internal/agent/pending.go source/server/internal/agent/pending_test.go source/proto/agent.proto source/server/pkg/proto/ source/server/internal/server/server.go
git commit -m "feat(agent): persist always-allow for MCP tools through the decision path"
```

---

## Phase F — Host RPCs + client methods

### Task F1: proto messages + service RPCs

**Files:**
- Modify: `source/proto/agent.proto` + regen (`pkg/proto/*.pb.go`)

**Interfaces:**
- Produces RPCs `ListMcpServers`, `AddMcpServer`, `RemoveMcpServer`, `RestartMcpServer` and their messages.

- [ ] **Step 1: Add to the `service Agent { ... }` block**

```proto
  // ---- MCP host ----
  rpc ListMcpServers (ListMcpServersRequest) returns (ListMcpServersResponse) {}
  rpc AddMcpServer (AddMcpServerRequest) returns (AddMcpServerResponse) {}
  rpc RemoveMcpServer (RemoveMcpServerRequest) returns (RemoveMcpServerResponse) {}
  rpc RestartMcpServer (RestartMcpServerRequest) returns (RestartMcpServerResponse) {}
```

- [ ] **Step 2: Add the messages** (near the other tool messages)

```proto
message McpServerInfo {
  string name       = 1;
  string state      = 2; // "warming" | "ready" | "failed"
  int32  tool_count = 3;
  string error      = 4;
}
message ListMcpServersRequest  {}
message ListMcpServersResponse { repeated McpServerInfo servers = 1; }

message AddMcpServerRequest {
  string name              = 1;
  string command           = 2;
  repeated string args      = 3;
  map<string, string> env  = 4;
}
message AddMcpServerResponse { bool ok = 1; string error = 2; }

message RemoveMcpServerRequest  { string name = 1; }
message RemoveMcpServerResponse { bool ok = 1; string error = 2; }

message RestartMcpServerRequest  { string name = 1; }
message RestartMcpServerResponse { bool ok = 1; string error = 2; int32 tool_count = 3; }
```

- [ ] **Step 3: Regenerate + verify build**

Run:
```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
protoc --go_out=. --go_opt=module=cercano/source/server \
       --go-grpc_out=. --go-grpc_opt=module=cercano/source/server \
       --proto_path=source/proto source/proto/agent.proto
cd source/server && go build ./...
```
Expected: builds (new methods now exist on the generated server interface; server.go won't satisfy it until F2 — so this step builds `pkg/proto` only). Build just the proto package: `go build ./pkg/proto/`.

- [ ] **Step 4: Commit**

```bash
git add source/proto/agent.proto source/server/pkg/proto/
git commit -m "feat(proto): MCP host RPCs (list/add/remove/restart)"
```

### Task F2: server handlers backed by a `McpManager` interface

**Files:**
- Modify: `source/server/internal/server/server.go`
- Test: `source/server/internal/server/mcp_host_test.go`

**Interfaces:**
- Produces: `type McpManager interface { List() []mcphost.ServerStatus; Add(context.Context, string, mcphost.ServerConfig) error; Remove(context.Context, string) error; Restart(context.Context, string) error }`; `func (s *Server) SetMcpManager(m McpManager)`; the four RPC handlers.
- Consumes: F1 generated types; `mcphost` package.

> `*mcphost.Manager` satisfies `McpManager` structurally. The interface lets tests inject a fake.

- [ ] **Step 1: Write the failing test** (fake manager)

```go
// source/server/internal/server/mcp_host_test.go
package server

import (
	"context"
	"testing"

	"cercano/source/server/internal/mcp_host"
	"cercano/source/server/pkg/proto"
)

type fakeMgr struct {
	added   string
	removed string
}

func (f *fakeMgr) List() []mcphost.ServerStatus {
	return []mcphost.ServerStatus{{Name: "github", State: mcphost.StateReady, ToolCount: 3}}
}
func (f *fakeMgr) Add(_ context.Context, name string, _ mcphost.ServerConfig) error {
	f.added = name
	return nil
}
func (f *fakeMgr) Remove(_ context.Context, name string) error { f.removed = name; return nil }
func (f *fakeMgr) Restart(_ context.Context, _ string) error   { return nil }

func TestListAndAddMcpServers(t *testing.T) {
	s := &Server{}
	s.SetMcpManager(&fakeMgr{})

	resp, err := s.ListMcpServers(context.Background(), &proto.ListMcpServersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Servers) != 1 || resp.Servers[0].Name != "github" || resp.Servers[0].State != "ready" {
		t.Fatalf("list = %+v", resp.Servers)
	}

	fm := &fakeMgr{}
	s.SetMcpManager(fm)
	if _, err := s.AddMcpServer(context.Background(), &proto.AddMcpServerRequest{
		Name: "fs", Command: "mcp-fs", Args: []string{"/tmp"},
	}); err != nil {
		t.Fatal(err)
	}
	if fm.added != "fs" {
		t.Fatalf("Add not called with fs: %q", fm.added)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/server/ -run TestListAndAddMcpServers -v`
Expected: FAIL (`SetMcpManager`/handlers undefined).

- [ ] **Step 3: Write minimal implementation** (add to server.go)

```go
import mcphost "cercano/source/server/internal/mcp_host" // add to import block

// McpManager is the subset of *mcphost.Manager the RPC handlers use. An
// interface so tests can inject a fake.
type McpManager interface {
	List() []mcphost.ServerStatus
	Add(ctx context.Context, name string, cfg mcphost.ServerConfig) error
	Remove(ctx context.Context, name string) error
	Restart(ctx context.Context, name string) error
}

// add field `mcpManager McpManager` to the Server struct.

func (s *Server) SetMcpManager(m McpManager) { s.mcpManager = m }

func (s *Server) ListMcpServers(ctx context.Context, _ *proto.ListMcpServersRequest) (*proto.ListMcpServersResponse, error) {
	out := &proto.ListMcpServersResponse{}
	if s.mcpManager == nil {
		return out, nil
	}
	for _, st := range s.mcpManager.List() {
		out.Servers = append(out.Servers, &proto.McpServerInfo{
			Name: st.Name, State: string(st.State), ToolCount: int32(st.ToolCount), Error: st.Err,
		})
	}
	return out, nil
}

func (s *Server) AddMcpServer(ctx context.Context, req *proto.AddMcpServerRequest) (*proto.AddMcpServerResponse, error) {
	if s.mcpManager == nil {
		return &proto.AddMcpServerResponse{Ok: false, Error: "mcp host not enabled"}, nil
	}
	err := s.mcpManager.Add(ctx, req.GetName(), mcphost.ServerConfig{
		Command: req.GetCommand(), Args: req.GetArgs(), Env: req.GetEnv(),
	})
	if err != nil {
		return &proto.AddMcpServerResponse{Ok: false, Error: err.Error()}, nil
	}
	return &proto.AddMcpServerResponse{Ok: true}, nil
}

func (s *Server) RemoveMcpServer(ctx context.Context, req *proto.RemoveMcpServerRequest) (*proto.RemoveMcpServerResponse, error) {
	if s.mcpManager == nil {
		return &proto.RemoveMcpServerResponse{Ok: false, Error: "mcp host not enabled"}, nil
	}
	if err := s.mcpManager.Remove(ctx, req.GetName()); err != nil {
		return &proto.RemoveMcpServerResponse{Ok: false, Error: err.Error()}, nil
	}
	return &proto.RemoveMcpServerResponse{Ok: true}, nil
}

func (s *Server) RestartMcpServer(ctx context.Context, req *proto.RestartMcpServerRequest) (*proto.RestartMcpServerResponse, error) {
	if s.mcpManager == nil {
		return &proto.RestartMcpServerResponse{Ok: false, Error: "mcp host not enabled"}, nil
	}
	if err := s.mcpManager.Restart(ctx, req.GetName()); err != nil {
		return &proto.RestartMcpServerResponse{Ok: false, Error: err.Error()}, nil
	}
	return &proto.RestartMcpServerResponse{Ok: true}, nil
}
```

- [ ] **Step 4: Run test + full build**

Run: `cd source/server && go build ./... && go test ./internal/server/ -run TestListAndAddMcpServers -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/server/server.go source/server/internal/server/mcp_host_test.go
git commit -m "feat(server): MCP host RPC handlers behind McpManager interface"
```

### Task F3: agentclient methods

**Files:**
- Modify: `source/server/pkg/agentclient/client.go`
- Test: `source/server/pkg/agentclient/client_test.go` (if a harness exists; otherwise compile-only)

**Interfaces:**
- Produces: `type McpServer struct { Name, State string; ToolCount int; Err string }`; `func (c *Client) ListMcpServers(ctx) ([]McpServer, error)`; `func (c *Client) AddMcpServer(ctx, name, command string, args []string, env map[string]string) error`; `func (c *Client) RemoveMcpServer(ctx, name string) error`; `func (c *Client) RestartMcpServer(ctx, name string) error`; and a persist-aware allow: `func (c *Client) AllowToolCallPersist(ctx, toolUseID string, persist bool) error`.

- [ ] **Step 1: Implement** (mirrors existing client method style)

```go
// append to client.go
type McpServer struct {
	Name      string
	State     string
	ToolCount int
	Err       string
}

func (c *Client) ListMcpServers(ctx context.Context) ([]McpServer, error) {
	resp, err := c.agent.ListMcpServers(ctx, &proto.ListMcpServersRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]McpServer, 0, len(resp.GetServers()))
	for _, s := range resp.GetServers() {
		out = append(out, McpServer{Name: s.GetName(), State: s.GetState(),
			ToolCount: int(s.GetToolCount()), Err: s.GetError()})
	}
	return out, nil
}

func (c *Client) AddMcpServer(ctx context.Context, name, command string, args []string, env map[string]string) error {
	resp, err := c.agent.AddMcpServer(ctx, &proto.AddMcpServerRequest{
		Name: name, Command: command, Args: args, Env: env})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetError())
	}
	return nil
}

func (c *Client) RemoveMcpServer(ctx context.Context, name string) error {
	resp, err := c.agent.RemoveMcpServer(ctx, &proto.RemoveMcpServerRequest{Name: name})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetError())
	}
	return nil
}

func (c *Client) RestartMcpServer(ctx context.Context, name string) error {
	resp, err := c.agent.RestartMcpServer(ctx, &proto.RestartMcpServerRequest{Name: name})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetError())
	}
	return nil
}

// AllowToolCallPersist approves a paused tool call and, when persist is true,
// asks the agent to allowlist it for silent future runs.
func (c *Client) AllowToolCallPersist(ctx context.Context, toolUseID string, persist bool) error {
	_, err := c.agent.AllowToolCall(ctx, &proto.AllowToolCallRequest{ToolUseId: toolUseID, Persist: persist})
	return err
}
```

Ensure `fmt` is imported (it is, used elsewhere).

- [ ] **Step 2: Build**

Run: `cd source/server && go build ./...`
Expected: builds.

- [ ] **Step 3: Commit**

```bash
git add source/server/pkg/agentclient/client.go
git commit -m "feat(agentclient): MCP host client methods + persist allow"
```

---

## Phase G — Boot wiring

### Task G1: construct + start the manager in cmd/cercano

**Files:**
- Modify: `source/server/cmd/cercano/main.go`

**Interfaces:**
- Consumes: `mcphost.New`, `Manager.Start`, `srv.SetMcpManager` (F2), the existing registry + `config.DefaultPath()`.

- [ ] **Step 1: Read the wiring site**

Run: `sed -n '250,270p' source/server/cmd/cercano/main.go` — confirm `srv.SetToolRegistry(agenttools.DefaultRegistry())` (line ~255) and `permsPath := filepath.Join(filepath.Dir(config.DefaultPath()), ...)`.

- [ ] **Step 2: Implement** — capture the registry, build the manager, start it in the background

Replace the single registry line with:

```go
	reg := agenttools.DefaultRegistry()
	srv.SetToolRegistry(reg)

	// MCP host: load global servers and connect them in the background. Tools
	// register into `reg` as each server lists; a slow/dead server never blocks
	// boot (a call to a not-ready server blocks only that call — see mcphost).
	cfgDir := filepath.Dir(config.DefaultPath())
	mcpMgr := mcphost.New(reg, cfgDir, 10*time.Second)
	srv.SetMcpManager(mcpMgr)
	mcpMgr.Start(context.Background())
```

Add imports: `mcphost "cercano/source/server/internal/mcp_host"`, and ensure `time` and `context` are imported (both already are in main.go).

- [ ] **Step 3: Build + run the agent, sanity-check**

Run:
```bash
cd source/server && go build -o bin/cercano ./cmd/cercano/
go test ./... -count=1
```
Expected: builds; full server suite passes.

- [ ] **Step 4: Commit**

```bash
git add source/server/cmd/cercano/main.go
git commit -m "feat(cercano): boot the MCP host manager (background warm)"
```

---

## Phase H — CLI surface

### Task H1: `/mcp` slash command

**Files:**
- Create: `source/clients/cli/internal/slash/mcp.go`
- Modify: wherever slash commands are registered (search for an existing `Register(Command{Name: "tools"...})` site)
- Test: `source/clients/cli/internal/slash/mcp_test.go`

**Interfaces:**
- Consumes: `agentclient.Client` MCP methods (F3); the slash `Command` shape (read `registry.go` for `Command` fields — `Name`, `Help`, and the handler signature).

- [ ] **Step 1: Read the Command shape + an existing command**

Run: `sed -n '1,60p' source/clients/cli/internal/slash/registry.go` and grep an existing command registration (e.g. `grep -rn "Name: \"tools\"\|Name: \"models\"" source/clients/cli/internal`). Mirror that handler signature exactly in the steps below (the plan uses `Run func(args []string) Result` as a placeholder — replace with the real signature).

- [ ] **Step 2: Write the failing test** (parse/dispatch of `/mcp` subcommands)

```go
// source/clients/cli/internal/slash/mcp_test.go
package slash

import "testing"

func TestParseMcpSubcommand(t *testing.T) {
	sub, rest := parseMcp([]string{"add", "github", "npx", "-y", "server-github"})
	if sub != "add" {
		t.Fatalf("sub = %q", sub)
	}
	if len(rest) != 4 || rest[0] != "github" || rest[1] != "npx" {
		t.Fatalf("rest = %v", rest)
	}
	// Bare /mcp defaults to list.
	sub, _ = parseMcp(nil)
	if sub != "list" {
		t.Fatalf("default sub = %q", sub)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/slash/ -run TestParseMcpSubcommand -v`
Expected: FAIL (`parseMcp` undefined).

- [ ] **Step 4: Implement** the parser + command registration

```go
// source/clients/cli/internal/slash/mcp.go
package slash

// parseMcp splits /mcp arguments into a subcommand and its remaining args.
// Bare `/mcp` means `list`.
func parseMcp(args []string) (string, []string) {
	if len(args) == 0 {
		return "list", nil
	}
	return args[0], args[1:]
}
```

Then register a `/mcp` command alongside the others, dispatching:
- `list` → `client.ListMcpServers`, render a `render.Table` with columns `Server | State | Tools | Error` (transpose-safe; max 4 columns — uses the existing Table primitive).
- `add <name> <command> [args…]` → `client.AddMcpServer(ctx, name, command, args, nil)`.
- `remove <name>` → `client.RemoveMcpServer`.
- `restart <name>` → `client.RestartMcpServer`.
- unknown sub → help line.

Match the existing handler signature and Table construction from the `/tools` command. Keep this handler thin: call the client, format the result, return.

- [ ] **Step 5: Run test + build**

Run: `cd source/clients/cli && go test ./internal/slash/ -count=1 && go build .`
Expected: PASS + builds.

- [ ] **Step 6: Commit**

```bash
git add source/clients/cli/internal/slash/mcp.go source/clients/cli/internal/slash/mcp_test.go
git commit -m "feat(cli): /mcp list|add|remove|restart command"
```

### Task H2: `[a]lways allow` confirm key for MCP tools + display name

**Files:**
- Modify: `source/clients/cli/internal/ui/model.go`
- Test: `source/clients/cli/internal/ui/confirm_test.go`

**Interfaces:**
- Consumes: `client.AllowToolCallPersist` (F3); `pendingToolCall` (existing); the confirm-prompt render + `toolConfirm` (model.go:1695–1781).

The CLI decides whether to offer `[a]` by inspecting the tool name prefix — `mcp__` — so no proto change is needed for the display affordance (the authoritative gate is server-side).

- [ ] **Step 1: Write the failing test**

```go
// append to source/clients/cli/internal/ui/confirm_test.go
func TestMCPConfirmOffersAlwaysAllow(t *testing.T) {
	tc := &pendingToolCall{Name: "mcp__github__create_issue", Permission: "W", ToolUseID: "t1"}
	c := toolConfirm(tc)
	if _, ok := c.extras["a"]; !ok {
		t.Fatal("MCP tool confirm should expose an [a]lways-allow key")
	}
}

func TestBuiltinConfirmHasNoAlwaysAllow(t *testing.T) {
	tc := &pendingToolCall{Name: "Write", Permission: "W", ToolUseID: "t2"}
	c := toolConfirm(tc)
	if _, ok := c.extras["a"]; ok {
		t.Fatal("built-in confirm must not expose always-allow")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/clients/cli && go test ./internal/ui/ -run "AlwaysAllow" -v`
Expected: FAIL (no `a` key).

- [ ] **Step 3: Implement** — add the `[a]` extra in `toolConfirm` only for MCP tools

In `toolConfirm` (model.go), after building `extras`, conditionally add the key:

```go
import "strings" // if not already imported

	// MCP tools (mcp__server__tool) are confirm-by-default; offer always-allow,
	// which persists a silent allowlist rule server-side.
	if strings.HasPrefix(tc.Name, "mcp__") {
		cr.extras["a"] = func(m Model) (Model, tea.Cmd) {
			m.pendingConfirm = nil
			m.chat.AppendEntry(&Entry{Role: RoleSystem,
				Content: m.styles.Accent.Render("✓ always-allowed — running…")})
			m.refreshViewport()
			if tc.ToolUseID != "" {
				ag, id := m.agent, tc.ToolUseID
				if ag != nil {
					go func() { _ = ag.AllowToolCallPersist(context.Background(), id, true) }()
				}
			}
			return m, nil
		}
	}
```

(Where `cr` is the `*confirmRequest` being returned; refactor `toolConfirm` to build it into a variable `cr` first, then return `cr`.)

- [ ] **Step 4: Add the `[a]` hint to the prompt render** (model.go `renderConfirmPrompt`)

After the `]iff` segment, append (only for MCP tools):

```go
	if strings.HasPrefix(p.Name, "mcp__") {
		out += m.styles.Muted.Render(" / [") +
			m.styles.Accent.Render("a") +
			m.styles.Muted.Render("]lways")
	}
	return out
```

(Restructure `renderConfirmPrompt` to build into `out` then return, mirroring the existing concatenation.)

- [ ] **Step 5: Show MCP tools by display name** — where the confirm summary or tool-call entry prints `p.Name`, convert MCP names to `mcp/server/tool`. Add a small CLI helper:

```go
// in model.go or a ui helper file
func displayToolName(name string) string {
	if !strings.HasPrefix(name, "mcp__") {
		return name
	}
	rest := strings.TrimPrefix(name, "mcp__")
	if i := strings.Index(rest, "__"); i >= 0 {
		return "mcp/" + rest[:i] + "/" + rest[i+2:]
	}
	return name
}
```

Use `displayToolName(p.Name)` in `renderConfirmPrompt`'s `summary` line.

- [ ] **Step 6: Run tests + build**

Run: `cd source/clients/cli && go test ./internal/ui/ -count=1 && go build .`
Expected: PASS + builds.

- [ ] **Step 7: Commit**

```bash
git add source/clients/cli/internal/ui/model.go source/clients/cli/internal/ui/confirm_test.go
git commit -m "feat(cli): always-allow key + mcp/ display for hosted tools"
```

---

## Phase I — Integration smoke + docs

### Task I1: end-to-end smoke against a stub MCP server

**Files:**
- Create: `source/server/internal/mcp_host/integration_test.go`

- [ ] **Step 1: Write the test** — manager + registry + gate, exercised together through the tool registry (no real subprocess; in-memory dial)

```go
// source/server/internal/mcp_host/integration_test.go
package mcphost

import (
	"context"
	"testing"
	"time"

	"cercano/source/server/internal/agenttools"
)

func TestEndToEndRegisterAndCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := agenttools.NewRegistry()
	m := New(reg, t.TempDir(), 2*time.Second)
	m.dialFn = func(ctx context.Context, _ ServerConfig) (*conn, error) {
		return dial(ctx, startTestServer(t, ctx))
	}
	m.startServer(ctx, "test", ServerConfig{Command: "x"})

	tl, ok := reg.Get("mcp__test__echo")
	if !ok {
		t.Fatal("tool not registered")
	}
	if agenttools.OriginOf(tl) != agenttools.OriginMCP {
		t.Fatal("origin must be mcp (gate relies on it)")
	}
	res, err := tl.Execute(ctx, []byte(`{"text":"e2e"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "echo:e2e" {
		t.Fatalf("text = %q", res.Text)
	}
}
```

- [ ] **Step 2: Run**

Run: `cd source/server && go test ./internal/mcp_host/ -count=1`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add source/server/internal/mcp_host/integration_test.go
git commit -m "test(mcphost): end-to-end register + call through the registry"
```

### Task I2: update feature docs + status

**Files:**
- Modify: `docs/features/cli/README.md` (mark phases 6 + 15 done; close the `/tools` MCP note)
- Modify: `docs/agent/README.md` (Slash commands table: add `/mcp`; remove the "MCP host not added" caveats if present)
- Modify: `docs/features/cli/mcp-host/design.md` (Status header → "Built")

- [ ] **Step 1:** In `docs/features/cli/README.md`, flip Phase 6 to ✅ (tasks 6.1–6.5), Phase 15 to ✅ (15.1 MCP tools now in `/tools`; 15.2 `/mcp` shipped), and update the "Plan-track features still outstanding" rollup to drop MCP host + `/mcp` UI. Keep the no-schedule rule — no dates.

- [ ] **Step 2:** In `docs/agent/README.md`, add `/mcp` to the slash-commands table (`/mcp | list/add/remove/restart hosted MCP servers`).

- [ ] **Step 3:** In `docs/features/cli/mcp-host/design.md`, change the Status line to `> Status: Built — phases 6 + 15.`

- [ ] **Step 4: Commit**

```bash
git add docs/features/cli/README.md docs/agent/README.md docs/features/cli/mcp-host/design.md
git commit -m "docs(cli): mark MCP host + /mcp shipped"
```

---

## Self-Review

**Spec coverage:**
- Runtime hosts external servers → Phases B, C, D, G. ✓
- Tools register into shared registry, loop unchanged → C, D1, E2. ✓
- Confirm-by-default + allowlist + bypass → E1, E2. ✓
- Interactive always-allow → E3, H2. ✓
- `mcp__server__tool` naming, `mcp/` display → A1, H2. ✓
- mcp.yaml canonical + .mcp.json import → A2. ✓
- Non-blocking boot, per-call per-server block → D1 (`ready`), G1 (`Start` in goroutines). ✓
- Global only, no per-project → config dir is `~/.config/cercano` (G1). ✓
- `/mcp list|add|remove|restart` + `/tools` shows MCP → H1, plus `/tools` works free (existing ListTools over the registry). ✓
- Host RPCs → F1, F2, F3. ✓
- Tests: client (B), tool (C2), manager (D), gate/allowlist (E1), loop gating (E2), persist (E3), RPC (F2), CLI (H1, H2), e2e (I1). ✓

**Placeholder scan:** No "TBD"/"implement later". The only deferred numeric (per-call wait) is bound to `10*time.Second` (G1) / `2*time.Second` (tests). The CLI handler-signature and `confirmRequest` field details are flagged as "read the existing code and match" rather than guessed — these are real integration points the implementer must mirror, not placeholders for logic.

**Type consistency:**
- `Decision{Allow,Persist}` used identically in E3 (pending, server, tests). ✓
- `ServerConfig`/`ServerStatus`/`ServerState` names consistent across A, D, F2, F3. ✓
- `OriginOf`/`OriginMCP` consistent C1 → C2 → E2 → I1. ✓
- `ToolName`/`DisplayName` (server) vs `displayToolName` (CLI, separate module — cannot import server internal). Intentional duplication across the module boundary; both produce `mcp/server/tool`. ✓
- `GateDecisionForMCP(mode, tier, isMCP, allowlisted)` signature consistent E1 → E2. ✓

## Notes for the implementer

- **Module boundary:** the CLI cannot import `source/server/internal/*`. The `displayToolName` helper is deliberately re-implemented CLI-side (H2). Everything else the CLI needs comes through `agentclient` (F3).
- **protoc:** this repo regenerates `pkg/proto/*.pb.go` by hand (no Makefile target). Use the same toolchain that produced the current files (`protoc` v7.34.1, `protoc-gen-go` v1.36.11, `protoc-gen-go-grpc` v1.6.2). Verify the regen only adds the new symbols: `git diff --stat source/server/pkg/proto`.
- **Existing test fallout from E3:** changing `PendingDecisions.Wait/Resolve` signatures will break any existing test/call that used the old `bool` forms — grep `Resolve(` and `.Wait(` under `internal/` and update to `Decision{...}` / `d.Allow`.
- **Manager subprocess tests:** never spawn real `npx`/servers in unit tests — always go through the injected `dialFn` + in-memory transport (`startTestServer`).
