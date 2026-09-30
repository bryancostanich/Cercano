package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/pkg/proto"
)

// streamMCPControl proxies MCP tool calls from the worker back to the host over
// the RunTurn stream. The host owns every MCP subprocess, its ready-state
// machine, and its restart semantics; duplicating that ownership in the worker
// would spawn a SECOND copy of each (frequently stateful) MCP server per worker
// process. So the worker holds no connections at all — it forwards.
//
// Correlation and delivery mirror streamRuntimeControl exactly: a monotonic id
// per request, a buffered channel per pending call, and a deliver() the stream
// reader calls on response arrival.
type streamMCPControl struct {
	sndr    *sender
	next    atomic.Uint64
	mu      sync.Mutex
	pending map[string]chan *proto.McpCallResponse

	// sendFn overrides the stream send. Production leaves it nil and uses sndr;
	// tests set it to capture the request without standing up a gRPC stream.
	sendFn func(*proto.WorkerToHost)
}

// emit puts one message on the wire via the test hook when set, else the sender.
func (p *streamMCPControl) emit(m *proto.WorkerToHost) {
	if p.sendFn != nil {
		p.sendFn(m)
		return
	}
	p.sndr.send(m)
}

func newStreamMCPControl(sndr *sender) *streamMCPControl {
	return &streamMCPControl{sndr: sndr, pending: map[string]chan *proto.McpCallResponse{}}
}

// call forwards one MCP invocation to the host and blocks for the response or
// context cancellation. Host-side waiting (server warm-up, restart) happens
// where the connections live, so this sees only a completed outcome.
func (p *streamMCPControl) call(ctx context.Context, name string, args json.RawMessage) (*agenttools.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := strconv.FormatUint(p.next.Add(1), 10)
	ch := make(chan *proto.McpCallResponse, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()

	p.emit(&proto.WorkerToHost{Msg: &proto.WorkerToHost_McpRequest{McpRequest: &proto.McpCallRequest{
		Id:       id,
		Name:     name,
		ArgsJson: args,
	}}})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case response := <-ch:
		if e := response.GetError(); e != "" {
			return nil, errors.New(e)
		}
		var res agenttools.Result
		if err := json.Unmarshal(response.GetResultJson(), &res); err != nil {
			return nil, fmt.Errorf("mcp %s: decode host result: %w", name, err)
		}
		return &res, nil
	}
}

func (p *streamMCPControl) deliver(response *proto.McpCallResponse) {
	p.mu.Lock()
	ch := p.pending[response.GetId()]
	p.mu.Unlock()
	if ch != nil {
		select {
		case ch <- response:
		default:
		}
	}
}

// mcpProxyTool is the worker-side stand-in for one host MCP tool. It carries the
// SAME name, schema, permission tier and origin as the host's mcpTool, because
// all four feed the permission gate and the model-visible advertisement. In
// particular Origin() MUST report OriginMCP: agenttools.OriginOf defaults to
// OriginBuiltin for types that do not implement Originer, which would silently
// gate third-party MCP code as first-party.
type mcpProxyTool struct {
	name        string
	desc        string
	schema      json.RawMessage
	destructive bool
	ctl         *streamMCPControl
}

func newMCPProxyTool(d *proto.McpToolDescriptor, ctl *streamMCPControl) *mcpProxyTool {
	schema := json.RawMessage(d.GetSchema())
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object"}`)
	}
	return &mcpProxyTool{
		name:        d.GetName(),
		desc:        d.GetDescription(),
		schema:      schema,
		destructive: d.GetDestructive(),
		ctl:         ctl,
	}
}

func (t *mcpProxyTool) Name() string                      { return t.name }
func (t *mcpProxyTool) Description() string               { return t.desc }
func (t *mcpProxyTool) Permission() agenttools.Permission { return agenttools.PermW }
func (t *mcpProxyTool) Origin() agenttools.Origin         { return agenttools.OriginMCP }
func (t *mcpProxyTool) Destructive() bool                 { return t.destructive }
func (t *mcpProxyTool) Schema() json.RawMessage           { return t.schema }

func (t *mcpProxyTool) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	return t.ctl.call(ctx, t.name, args)
}

// registerMCPProxies builds a proxy tool per advertised descriptor and registers
// it. Returns the number registered. A descriptor whose name collides with an
// existing tool is skipped: built-ins win, matching host-side behavior where the
// built-in registry is populated before MCP servers connect.
func registerMCPProxies(reg *agenttools.Registry, descriptors []*proto.McpToolDescriptor, ctl *streamMCPControl) int {
	if reg == nil || len(descriptors) == 0 {
		return 0
	}
	n := 0
	for _, d := range descriptors {
		if d.GetName() == "" {
			continue
		}
		if err := reg.Register(newMCPProxyTool(d, ctl)); err == nil {
			n++
		}
	}
	return n
}
