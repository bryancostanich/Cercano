// source/server/internal/mcp_host/manager.go
package mcphost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"cercano/source/server/internal/agenttools"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

// ServerState describes the lifecycle state of a hosted MCP server.
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

// serverHandle tracks the live state of one hosted server.
type serverHandle struct {
	name    string
	cfg     ServerConfig
	mu      sync.Mutex
	conn    *conn
	state   ServerState
	err     string
	tools   []string      // registered tool names (for unregister on remove/restart)
	readyCh chan struct{}  // closed once state reaches Ready or Failed
}

// Manager connects to external MCP servers, lists their tools, and registers
// them into an agenttools.Registry. Each server lifecycle is independent.
type Manager struct {
	reg      *agenttools.Registry
	dir      string
	callWait time.Duration
	mu       sync.Mutex
	servers  map[string]*serverHandle
	dialFn   func(ctx context.Context, cfg ServerConfig) (*conn, error)
}

// New creates a Manager. dir is the config directory (mcp.yaml lives there).
// callWait is how long a tool call blocks waiting for the server to become ready.
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

// fail transitions h to StateFailed and closes readyCh exactly once.
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
