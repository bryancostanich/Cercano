# Unified Capability Architecture + Migration — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every Cercano capability a single implementation behind one `Capability` interface and registry, exposed to the standalone agent loop and to the MCP plugin surface through two thin adapters — migrating all existing tools onto it and retiring `dispatch.Tool`.

**Architecture:** A new `internal/capabilities` package holds the `Capability` interface, a `Services` dependency container, and a `Registry`. An **agent adapter** wraps any capability as the existing `agenttools.Tool` so `toolloop.go` is untouched; an **MCP adapter** registers each capability as `cercano_<name>` and forwards to one new generic gRPC RPC, `InvokeCapability`, served by the agent. The 15 built-ins, the 4 dispatch builtins, and the 8 co-processor MCP handlers all move onto the registry.

**Tech Stack:** Go 1.21+, two Go modules (`cercano/source/server`, `cercano/source/clients/cli`), gRPC + protobuf (protoc-gen-go v1.36.11), `modelcontextprotocol/go-sdk` (`gomcp`), Ollama engine, anthropic-sdk-go.

## Global Constraints

- Behavior-preserving: the standalone loop and existing MCP tools must behave exactly as before migration. Pin behavior with tests before cutover.
- Permission tiers are `R`/`W`/`X`; the standalone loop gates W/X, the plugin surface does not gate (host owns gating).
- Standalone display names stay Claude-style (`Read`, `Write`, `Edit`, `Bash`, `LS`, `Glob`, `Grep`); MCP names are `cercano_<canonical>`. One implementation per capability.
- Keep the gRPC server-comm architecture. The MCP surface reaches capabilities via the new generic `InvokeCapability` RPC, never by removing the transport.
- Commit messages must not contain the word "Claude" anywhere (including trailers).
- `go test ./...` green in `source/server` after every task; `source/clients/cli` green after any task that touches it.
- Build both binaries with `make build` (server) before declaring a phase done.

---

## Phase 0 — Baseline

### Task 0: Verify a clean baseline

**Files:** none (verification only)

- [ ] **Step 1: Build the server**

Run: `cd source/server && make build`
Expected: builds `bin/cercano` with no errors.

- [ ] **Step 2: Run the server test suite**

Run: `cd source/server && go test ./... -count=1`
Expected: PASS (note any pre-existing failures; if any fail, STOP and report before changing code).

- [ ] **Step 3: Run the CLI test suite**

Run: `cd source/clients/cli && go test ./... -count=1`
Expected: PASS.

---

## Phase 1 — Capability core (`internal/capabilities/`)

### Task 1: Capability types

**Files:**
- Create: `source/server/internal/capabilities/capability.go`
- Test: `source/server/internal/capabilities/capability_test.go`

**Interfaces:**
- Produces: `Tier` (`TierR`/`TierW`/`TierX`), `Surface` (`SurfaceAgent`/`SurfaceMCP`, `Has` method), `Schema` (= `json.RawMessage`), `Result` (fields mirror `agenttools.Result`) + `NewTextResult`/`NewRowsResult`/`(*Result).LLMContent`, `Call`, and the `Capability` interface. `Tier.ToPermission()` maps to `agenttools.Permission` strings (`"R"/"W"/"X"`).

- [ ] **Step 1: Write the failing test**

```go
package capabilities

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTierToPermission(t *testing.T) {
	cases := map[Tier]string{TierR: "R", TierW: "W", TierX: "X"}
	for tier, want := range cases {
		if got := string(tier.ToPermission()); got != want {
			t.Fatalf("Tier %q -> %q, want %q", tier, got, want)
		}
	}
}

func TestSurfaceHas(t *testing.T) {
	both := SurfaceAgent | SurfaceMCP
	if !both.Has(SurfaceAgent) || !both.Has(SurfaceMCP) {
		t.Fatal("both should contain agent and mcp")
	}
	if SurfaceAgent.Has(SurfaceMCP) {
		t.Fatal("agent-only must not contain mcp")
	}
}

func TestNewTextResultTruncates(t *testing.T) {
	big := strings.Repeat("a", 40*1024)
	r := NewTextResult(big)
	if !r.Truncated {
		t.Fatal("expected truncation over 32 KiB")
	}
	if r.Type != ResultText {
		t.Fatalf("type = %q, want text", r.Type)
	}
}

func TestLLMContentSerializesRows(t *testing.T) {
	r := NewRowsResult([]map[string]any{{"k": "v"}})
	if !strings.Contains(r.LLMContent(), `"k":"v"`) {
		t.Fatalf("rows not serialized: %q", r.LLMContent())
	}
	_ = json.RawMessage(nil)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/capabilities/ -run TestTier -v`
Expected: FAIL — package/types do not compile yet.

- [ ] **Step 3: Write the implementation**

```go
// Package capabilities defines the single Capability interface and registry
// that both the standalone agent loop (via agentadapter) and the MCP plugin
// surface (via mcpadapter + the InvokeCapability RPC) consume. One capability
// is implemented once and exposed on both surfaces — no duplicated logic.
package capabilities

import (
	"context"
	"encoding/json"

	"cercano/source/server/internal/agenttools"
)

// Tier tags how risky a capability's effects are; drives standalone confirm gating.
type Tier string

const (
	TierR Tier = "R" // read-only, runs silently
	TierW Tier = "W" // writes, confirm before applying
	TierX Tier = "X" // destructive, always confirm
)

// ToPermission maps a Tier to the agenttools.Permission used by the loop's gate.
func (t Tier) ToPermission() agenttools.Permission {
	switch t {
	case TierW:
		return agenttools.PermW
	case TierX:
		return agenttools.PermX
	default:
		return agenttools.PermR
	}
}

// Surface is a bitmask of the places a capability is exposed.
type Surface uint8

const (
	SurfaceAgent Surface = 1 << iota // standalone agent loop
	SurfaceMCP                       // MCP plugin
)

// Has reports whether s includes every bit in want.
func (s Surface) Has(want Surface) bool { return s&want == want }

// Schema is a JSON Schema document describing a capability's parameters.
type Schema = json.RawMessage

// ResultType tells clients how to render a Result.
type ResultType string

const (
	ResultRows ResultType = "rows"
	ResultText ResultType = "text"
	ResultJSON ResultType = "json"
)

// Result is the canonical output shape. Mirrors agenttools.Result so the agent
// adapter converts by field copy.
type Result struct {
	Type      ResultType       `json:"type"`
	Rows      []map[string]any `json:"rows,omitempty"`
	Text      string           `json:"text,omitempty"`
	JSON      json.RawMessage  `json:"json,omitempty"`
	Truncated bool             `json:"truncated,omitempty"`
	Note      string           `json:"note,omitempty"`
	Detail    string           `json:"detail,omitempty"`
}

// LLMContent renders the result as the text the model receives.
func (r *Result) LLMContent() string {
	var body string
	switch r.Type {
	case ResultRows:
		if b, err := json.Marshal(r.Rows); err == nil {
			body = string(b)
		}
	case ResultJSON:
		body = string(r.JSON)
	default:
		body = r.Text
	}
	if r.Note != "" {
		if body != "" {
			body += "\n"
		}
		body += "(" + r.Note + ")"
	}
	return body
}

// NewRowsResult applies the 200-row truncation policy.
func NewRowsResult(rows []map[string]any) *Result {
	r := &Result{Type: ResultRows}
	const maxRows = 200
	if len(rows) > maxRows {
		r.Rows = rows[:maxRows]
		r.Truncated = true
		r.Note = "showed first 200 rows; refine the query for more"
		return r
	}
	r.Rows = rows
	return r
}

// NewTextResult applies the 32 KiB byte-cap truncation policy.
func NewTextResult(text string) *Result {
	r := &Result{Type: ResultText}
	const maxBytes = 32 * 1024
	if len(text) > maxBytes {
		cut := maxBytes
		for cut > 0 && (text[cut]&0xC0) == 0x80 {
			cut--
		}
		r.Text = text[:cut] + "\n… (truncated)"
		r.Truncated = true
		r.Note = "showed first 32 KiB; refine to get more"
		return r
	}
	r.Text = text
	return r
}

// Call is the per-invocation context an adapter constructs for each call.
type Call struct {
	Args           json.RawMessage
	WorkDir        string
	ConversationID string
	// RequestPermission lets a capability ask for confirmation mid-execute.
	// The agent surface wires it to the loop gate; the MCP surface passes an
	// allow-all (the host gates). Most capabilities never call it.
	RequestPermission func(ctx context.Context, reason string) (bool, error)
	// Emit streams progress events back to the surface. Nil-safe — capabilities
	// must tolerate a nil Emit.
	Emit func(note string)
}

// Capability is the single implementation surface for a thing Cercano can do.
type Capability interface {
	Name() string        // canonical snake_case id
	Description() string
	Tier() Tier
	Schema() Schema
	Surfaces() Surface
	Execute(ctx context.Context, call *Call) (*Result, error)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/capabilities/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/capabilities/capability.go source/server/internal/capabilities/capability_test.go
git commit -m "feat(capabilities): Capability interface, Tier/Surface/Result core types"
```

### Task 2: Services container

**Files:**
- Create: `source/server/internal/capabilities/services.go`
- Test: `source/server/internal/capabilities/services_test.go`

**Interfaces:**
- Consumes: `llm.Provider` (`internal/llm`), `engine.InferenceEngine` (`internal/engine`), `config.Config` (`pkg/config`), `conversation.Store` (`internal/conversation`), `projectctx.Loader` (`internal/context`).
- Produces: `Services` struct + `(Services).MainProvider(isCloud bool) llm.Provider` + `RunCoproc` function-typed field (used in Phase 5).

- [ ] **Step 1: Write the failing test**

```go
package capabilities

import "testing"

func TestMainProviderSelectsByFlag(t *testing.T) {
	cloud := stubProvider{name: "cloud"}
	local := stubProvider{name: "local"}
	s := Services{CloudProvider: cloud, LocalProvider: local}
	if s.MainProvider(true).Name() != "cloud" {
		t.Fatal("isCloud=true should pick cloud provider")
	}
	if s.MainProvider(false).Name() != "local" {
		t.Fatal("isCloud=false should pick local provider")
	}
}

func TestMainProviderFallsBackToLocalWhenNoCloud(t *testing.T) {
	local := stubProvider{name: "local"}
	s := Services{LocalProvider: local}
	if s.MainProvider(true).Name() != "local" {
		t.Fatal("nil cloud should fall back to local")
	}
}
```

Add the stub to the test file:

```go
import (
	"context"
	"cercano/source/server/internal/llm"
)

type stubProvider struct{ name string }

func (p stubProvider) Name() string                  { return p.name }
func (p stubProvider) Capabilities() llm.Capabilities { return llm.Capabilities{} }
func (p stubProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (p stubProvider) StreamChat(context.Context, llm.ChatRequest) (llm.StreamReader, error) {
	return nil, nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/capabilities/ -run TestMainProvider -v`
Expected: FAIL — `Services` undefined.

- [ ] **Step 3: Write the implementation**

```go
package capabilities

import (
	"context"

	projectctx "cercano/source/server/internal/context"
	"cercano/source/server/internal/conversation"
	"cercano/source/server/internal/engine"
	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
)

// Services holds the static collaborators a capability may need. Injected once
// when the registry is built. There is no ProviderSet type — the agent holds
// cloud + local providers as two discrete fields, so Services mirrors that.
type Services struct {
	CloudProvider llm.Provider // may be nil (local-only deployments)
	LocalProvider llm.Provider
	Engine        engine.InferenceEngine
	Config        *config.Config
	Conversations conversation.Store
	ProjectCtx    *projectctx.Loader

	// RunCoproc runs a co-processor prompt through the agent's local pipeline
	// (the equivalent of ProcessRequest with Coproc=true) and returns the
	// model output. Set by the agent server when it builds the registry; used
	// by the co-processor capabilities (Phase 5). May be nil in tests.
	RunCoproc func(ctx context.Context, prompt, projectDir string) (string, error)
}

// MainProvider returns the provider for a turn: cloud when isCloud and a cloud
// provider is configured, otherwise local.
func (s Services) MainProvider(isCloud bool) llm.Provider {
	if isCloud && s.CloudProvider != nil {
		return s.CloudProvider
	}
	return s.LocalProvider
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/capabilities/ -run TestMainProvider -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/capabilities/services.go source/server/internal/capabilities/services_test.go
git commit -m "feat(capabilities): Services dependency container + MainProvider"
```

### Task 3: Registry

**Files:**
- Create: `source/server/internal/capabilities/registry.go`
- Test: `source/server/internal/capabilities/registry_test.go`

**Interfaces:**
- Produces: `Registry`, `NewRegistry(Services) *Registry`, `(*Registry).Register(Capability) error`, `MustRegister`, `Get(name) (Capability, bool)`, `All() []Capability`, `ForSurface(Surface) []Capability`, `Services() Services`.

- [ ] **Step 1: Write the failing test**

```go
package capabilities

import (
	"context"
	"testing"
)

type fakeCap struct {
	name string
	surf Surface
}

func (c fakeCap) Name() string        { return c.name }
func (c fakeCap) Description() string  { return "fake " + c.name }
func (c fakeCap) Tier() Tier           { return TierR }
func (c fakeCap) Schema() Schema       { return Schema(`{"type":"object"}`) }
func (c fakeCap) Surfaces() Surface    { return c.surf }
func (c fakeCap) Execute(context.Context, *Call) (*Result, error) {
	return NewTextResult("ok"), nil
}

func TestRegistryRegisterGet(t *testing.T) {
	r := NewRegistry(Services{})
	if err := r.Register(fakeCap{name: "a", surf: SurfaceAgent | SurfaceMCP}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(fakeCap{name: "a"}); err == nil {
		t.Fatal("duplicate name should error")
	}
	got, ok := r.Get("a")
	if !ok || got.Name() != "a" {
		t.Fatal("Get failed")
	}
}

func TestRegistryForSurface(t *testing.T) {
	r := NewRegistry(Services{})
	r.MustRegister(fakeCap{name: "agentonly", surf: SurfaceAgent})
	r.MustRegister(fakeCap{name: "both", surf: SurfaceAgent | SurfaceMCP})
	if got := len(r.ForSurface(SurfaceMCP)); got != 1 {
		t.Fatalf("ForSurface(MCP) = %d, want 1", got)
	}
	if got := len(r.ForSurface(SurfaceAgent)); got != 2 {
		t.Fatalf("ForSurface(Agent) = %d, want 2", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/capabilities/ -run TestRegistry -v`
Expected: FAIL — `NewRegistry` undefined.

- [ ] **Step 3: Write the implementation**

```go
package capabilities

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Registry holds capabilities and the Services they share. Thread-safe.
type Registry struct {
	mu    sync.RWMutex
	svc   Services
	items map[string]Capability
}

// NewRegistry returns an empty Registry bound to svc.
func NewRegistry(svc Services) *Registry {
	return &Registry{svc: svc, items: map[string]Capability{}}
}

// Services returns the injected dependency container.
func (r *Registry) Services() Services { return r.svc }

// Register adds a capability; errors on empty or duplicate name.
func (r *Registry) Register(c Capability) error {
	if c == nil {
		return errors.New("capabilities: nil Capability")
	}
	name := c.Name()
	if name == "" {
		return errors.New("capabilities: empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[name]; ok {
		return fmt.Errorf("capabilities: duplicate name %q", name)
	}
	r.items[name] = c
	return nil
}

// MustRegister panics on error — for startup wiring.
func (r *Registry) MustRegister(c Capability) {
	if err := r.Register(c); err != nil {
		panic(err)
	}
}

// Get looks up a capability by canonical name.
func (r *Registry) Get(name string) (Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.items[name]
	return c, ok
}

// All returns every capability, sorted by name.
func (r *Registry) All() []Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Capability, 0, len(r.items))
	for _, c := range r.items {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// ForSurface returns capabilities exposed on the given surface, sorted by name.
func (r *Registry) ForSurface(s Surface) []Capability {
	out := make([]Capability, 0)
	for _, c := range r.All() {
		if c.Surfaces().Has(s) {
			out = append(out, c)
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/capabilities/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/capabilities/registry.go source/server/internal/capabilities/registry_test.go
git commit -m "feat(capabilities): Registry with Services and per-surface filtering"
```

---

## Phase 2 — Agent adapter

### Task 4: Wrap a capability as `agenttools.Tool`

**Files:**
- Create: `source/server/internal/capabilities/agentadapter/adapter.go`
- Test: `source/server/internal/capabilities/agentadapter/adapter_test.go`

**Interfaces:**
- Consumes: `capabilities.Capability`, `capabilities.Registry`, `agenttools.Tool`/`Registry`/`Permission`/`Result`.
- Produces: `AliasMap` (`map[string]string` canonical→display), `AsTool(cap capabilities.Capability, display string) agenttools.Tool`, `BuildAgentRegistry(reg *capabilities.Registry, aliases AliasMap) *agenttools.Registry`.

- [ ] **Step 1: Write the failing test**

```go
package agentadapter

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
)

type echoCap struct{}

func (echoCap) Name() string       { return "read_file" }
func (echoCap) Description() string { return "echo" }
func (echoCap) Tier() capabilities.Tier { return capabilities.TierR }
func (echoCap) Schema() capabilities.Schema { return capabilities.Schema(`{"type":"object"}`) }
func (echoCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent }
func (echoCap) Execute(_ context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	return capabilities.NewTextResult("hello " + string(call.Args)), nil
}

func TestAsToolAppliesAliasAndTier(t *testing.T) {
	tool := AsTool(echoCap{}, "Read")
	if tool.Name() != "Read" {
		t.Fatalf("display name = %q, want Read", tool.Name())
	}
	if tool.Permission() != agenttools.PermR {
		t.Fatalf("permission = %q, want R", tool.Permission())
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Type != agenttools.ResultText || res.Text == "" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestBuildAgentRegistryUsesAgentSurfaceOnly(t *testing.T) {
	reg := capabilities.NewRegistry(capabilities.Services{})
	reg.MustRegister(echoCap{}) // agent-only
	ar := BuildAgentRegistry(reg, AliasMap{"read_file": "Read"})
	if _, ok := ar.Get("Read"); !ok {
		t.Fatal("expected Read in agent registry")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/capabilities/agentadapter/ -v`
Expected: FAIL — `AsTool` undefined.

- [ ] **Step 3: Write the implementation**

```go
// Package agentadapter exposes capabilities to the standalone agent loop by
// wrapping each Capability as an agenttools.Tool, so internal/agent/toolloop.go
// is untouched.
package agentadapter

import (
	"context"
	"encoding/json"

	"cercano/source/server/internal/agenttools"
	"cercano/source/server/internal/capabilities"
)

// AliasMap maps a capability's canonical name to its standalone display name
// (e.g. "read_file" -> "Read"). Missing entries default to the canonical name.
type AliasMap map[string]string

func (m AliasMap) display(canonical string) string {
	if d, ok := m[canonical]; ok && d != "" {
		return d
	}
	return canonical
}

// capTool adapts a Capability to agenttools.Tool.
type capTool struct {
	cap     capabilities.Capability
	display string
}

// AsTool wraps cap as an agenttools.Tool using the given display name.
func AsTool(cap capabilities.Capability, display string) agenttools.Tool {
	if display == "" {
		display = cap.Name()
	}
	return capTool{cap: cap, display: display}
}

func (t capTool) Name() string                 { return t.display }
func (t capTool) Description() string          { return t.cap.Description() }
func (t capTool) Permission() agenttools.Permission { return t.cap.Tier().ToPermission() }
func (t capTool) Schema() json.RawMessage      { return json.RawMessage(t.cap.Schema()) }

func (t capTool) Execute(ctx context.Context, args json.RawMessage) (*agenttools.Result, error) {
	call := &capabilities.Call{
		Args: args,
		// The loop has already gated W/X before calling Execute, and emits its
		// own events around execution, so allow-all + no-op here is correct and
		// behavior-preserving for the migrated tools.
		RequestPermission: func(context.Context, string) (bool, error) { return true, nil },
		Emit:              func(string) {},
	}
	res, err := t.cap.Execute(ctx, call)
	if err != nil {
		return nil, err
	}
	return toAgentResult(res), nil
}

func toAgentResult(r *capabilities.Result) *agenttools.Result {
	return &agenttools.Result{
		Type:      agenttools.ResultType(r.Type),
		Rows:      r.Rows,
		Text:      r.Text,
		JSON:      r.JSON,
		Truncated: r.Truncated,
		Note:      r.Note,
		Detail:    r.Detail,
	}
}

// BuildAgentRegistry constructs an agenttools.Registry from the agent-surface
// capabilities in reg, applying the alias map for display names.
func BuildAgentRegistry(reg *capabilities.Registry, aliases AliasMap) *agenttools.Registry {
	ar := agenttools.NewRegistry()
	for _, c := range reg.ForSurface(capabilities.SurfaceAgent) {
		ar.MustRegister(AsTool(c, aliases.display(c.Name())))
	}
	return ar
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/capabilities/agentadapter/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/capabilities/agentadapter/
git commit -m "feat(capabilities): agent adapter wrapping capabilities as agenttools.Tool"
```

---

## Phase 3 — Port the 15 built-ins to capabilities

Each existing tool in `internal/agenttools/` is reimplemented as a `Capability` in
`internal/capabilities/builtins/`, keeping its exact arg schema, behavior, and
truncation. The capability's `Name()` is the **canonical** snake_case id; the display
name comes from the alias map (Task 6 cutover). `Execute` reads args from
`call.Args` instead of a raw `json.RawMessage` parameter; everything else is copied
verbatim from the source tool, swapping `agenttools.NewTextResult`/`NewRowsResult`/
`Result` for the `capabilities` equivalents and helper funcs (`countLabel`, `lineCount`,
`looksBinary`, etc.) copied alongside into the builtins package.

**Canonical name + display + tier + surface for every tool:**

| Source (agenttools) | Canonical name | Display | Tier | Surfaces |
|---|---|---|---|---|
| `Read` | `read_file` | `Read` | R | agent+mcp |
| `LS` | `list_dir` | `LS` | R | agent+mcp |
| `stat_file` | `stat_file` | `stat_file` | R | agent+mcp |
| `Glob` | `glob` | `Glob` | R | agent+mcp |
| `Grep` | `grep` | `Grep` | R | agent+mcp |
| `git_status` | `git_status` | `git_status` | R | agent+mcp |
| `git_log` | `git_log` | `git_log` | R | agent+mcp |
| `Write` | `write_file` | `Write` | W | agent+mcp |
| `Edit` | `edit_file` | `Edit` | W | agent+mcp |
| `Bash` (run) | `run_command` | `Bash` | W | agent+mcp |
| `git_add` | `git_add` | `git_add` | W | agent+mcp |
| `git_commit` | `git_commit` | `git_commit` | W | agent+mcp |
| `rm_file` | `rm_file` | `rm_file` | X | agent+mcp |
| `git_push` | `git_push` | `git_push` | X | agent+mcp |
| `git_reset_hard` | `git_reset_hard` | `git_reset_hard` | X | agent+mcp |

> Confirm the source canonical names for `Write`/`Edit`/`Bash`/git/`rm` by reading
> `fs_write.go`, `run.go`, `fs_destructive.go`, `git_read.go`, `git_write.go` — match each
> tool's current `Name()` for the **display** column and use the snake_case **canonical**
> column above for the capability id.

### Task 5: Port the R-tier filesystem capabilities (worked example)

**Files:**
- Create: `source/server/internal/capabilities/builtins/fs_read.go`
- Create: `source/server/internal/capabilities/builtins/detail.go` (copy `countLabel`/`lineCount`/`isUTF8Boundary`/`isSymlink`/`looksBinary`/`selectLines` helpers used by the ported tools)
- Test: `source/server/internal/capabilities/builtins/fs_read_test.go`

**Interfaces:**
- Produces: `ReadFile() capabilities.Capability`, `ListDir()`, `StatFile()`, `Glob()` — capability constructors.

- [ ] **Step 1: Write the failing test**

```go
package builtins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestReadFileCapability(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cap := ReadFile()
	if cap.Name() != "read_file" || cap.Tier() != capabilities.TierR {
		t.Fatalf("name/tier wrong: %q %q", cap.Name(), cap.Tier())
	}
	args, _ := json.Marshal(map[string]any{"path": p})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "line1\nline2\n" {
		t.Fatalf("content = %q", res.Text)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/capabilities/builtins/ -run TestReadFile -v`
Expected: FAIL — `ReadFile` undefined.

- [ ] **Step 3: Write the implementation**

Port `internal/agenttools/fs_read.go` verbatim, changing only: package name to `builtins`; each tool struct's methods to the `Capability` interface (`Tier()`/`Surfaces()` added, `Permission()` removed, `Execute(ctx, call *capabilities.Call)` reading `call.Args`); `Name()` returns the **canonical** id; results use `capabilities.NewTextResult`/`NewRowsResult`/`Result`. Example for the read tool (apply the same transform to `listDirTool`, `statFileTool`, `globTool`):

```go
package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"cercano/source/server/internal/capabilities"
)

type readFileCap struct{}

// ReadFile constructs the read_file capability (display "Read").
func ReadFile() capabilities.Capability { return readFileCap{} }

func (readFileCap) Name() string                  { return "read_file" }
func (readFileCap) Tier() capabilities.Tier        { return capabilities.TierR }
func (readFileCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (readFileCap) Description() string {
	return "Read a UTF-8 text file from disk. Returns the file contents, capped at 32 KiB. Refuses binary files. Args: {path: string, start?: int, end?: int}."
}
func (readFileCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path":  {"type": "string", "description": "Absolute or relative file path."},
			"start": {"type": "integer", "minimum": 1, "description": "Optional 1-indexed first line."},
			"end":   {"type": "integer", "minimum": 1, "description": "Optional 1-indexed last line, inclusive."}
		}
	}`)
}

type readFileArgs struct {
	Path  string `json:"path"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

func (readFileCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a readFileArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("read_file: parse args: %w", err)
	}
	if a.Path == "" {
		return nil, errors.New("read_file: path is required")
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return nil, fmt.Errorf("read_file: %w", err)
	}
	if looksBinary(data) {
		return nil, fmt.Errorf("read_file: %s appears to be binary; refusing to read", a.Path)
	}
	text := string(data)
	if a.Start > 0 || a.End > 0 {
		text = selectLines(text, a.Start, a.End)
	}
	res := capabilities.NewTextResult(text)
	res.Detail = countLabel(lineCount(text), "line", "lines")
	return res, nil
}
```

Copy `looksBinary`, `selectLines`, `isSymlink`, and the `countLabel`/`lineCount`/
`isUTF8Boundary` helpers (from `agenttools/fs_read.go` and `agenttools/detail.go`) into
`builtins/detail.go`. Port `listDirTool`→`listDirCap` (`list_dir`), `statFileTool`→
`statFileCap` (`stat_file`), `globTool`→`globCap` (`glob`) with the same transform.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/capabilities/builtins/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/capabilities/builtins/fs_read.go source/server/internal/capabilities/builtins/detail.go source/server/internal/capabilities/builtins/fs_read_test.go
git commit -m "feat(capabilities): port R-tier fs capabilities (read_file/list_dir/stat_file/glob)"
```

### Tasks 5b–5g: Port the remaining built-ins (one task + commit each)

Apply the **same mechanical transform** from Task 5 to each source file. For each: create the builtins file, copy any private helpers it uses, write a unit test mirroring the source's existing test, run it green, commit. Source → target mapping:

- **Task 5b — grep:** `agenttools/grep.go` → `builtins/grep.go` (`grep`, R). Keep the `rg`-with-fallback logic and the truncation Note. Commit: `feat(capabilities): port grep capability`.
- **Task 5c — run_command:** `agenttools/run.go` → `builtins/run.go` (`run_command`, W). Keep the 16 KiB output cap and timeout handling. Commit: `feat(capabilities): port run_command capability`.
- **Task 5d — fs_write:** `agenttools/fs_write.go` → `builtins/fs_write.go` (`write_file` W, `edit_file` W). Keep `edit_file`'s ambiguous/zero/no-op match refusal. Commit: `feat(capabilities): port write_file/edit_file capabilities`.
- **Task 5e — fs_destructive:** `agenttools/fs_destructive.go` → `builtins/fs_destructive.go` (`rm_file` X; if `mv_file` exists, port it as W). Preserve any `Destructive()` reporting by giving the X capabilities a `Tier() == TierX` (the adapter maps X→destructive display in Task 6 follow-up if needed). Commit: `feat(capabilities): port rm_file (and mv_file) capabilities`.
- **Task 5f — git_read:** `agenttools/git_read.go` → `builtins/git_read.go` (`git_status` R, `git_log` R). Commit: `feat(capabilities): port git read capabilities`.
- **Task 5g — git_write:** `agenttools/git_write.go` → `builtins/git_write.go` (`git_add` W, `git_commit` W, `git_push` X with `--force-with-lease`, `git_reset_hard` X). Commit: `feat(capabilities): port git write/destructive capabilities`.

After 5g, add the builtins registry constructor:

**Files:** Create `source/server/internal/capabilities/builtins/builtins.go`

```go
package builtins

import "cercano/source/server/internal/capabilities"

// Register adds every built-in capability to reg.
func Register(reg *capabilities.Registry) {
	// R-tier
	reg.MustRegister(ReadFile())
	reg.MustRegister(ListDir())
	reg.MustRegister(StatFile())
	reg.MustRegister(Glob())
	reg.MustRegister(Grep())
	reg.MustRegister(GitStatus())
	reg.MustRegister(GitLog())
	// W-tier
	reg.MustRegister(WriteFile())
	reg.MustRegister(EditFile())
	reg.MustRegister(RunCommand())
	reg.MustRegister(GitAdd())
	reg.MustRegister(GitCommit())
	// X-tier
	reg.MustRegister(RmFile())
	reg.MustRegister(GitPush())
	reg.MustRegister(GitResetHard())
}

// AgentAliases maps canonical capability names to standalone display names.
func AgentAliases() map[string]string {
	return map[string]string{
		"read_file":   "Read",
		"list_dir":    "LS",
		"glob":        "Glob",
		"grep":        "Grep",
		"write_file":  "Write",
		"edit_file":   "Edit",
		"run_command": "Bash",
		// stat_file, git_*, rm_file keep their canonical names as display names.
	}
}
```

Commit: `feat(capabilities): builtins registry + agent alias map`.

### Task 6: Cut the server over to the capability registry; delete old tool impls

**Files:**
- Modify: `source/server/internal/server/server.go` (wherever `s.toolRegistry` is set — the `SetToolRegistry` path / `agenttools.DefaultRegistry()` call site)
- Modify: `source/server/cmd/cercano/main.go` (where `DefaultRegistry()` is currently called, if any)
- Delete: `source/server/internal/agenttools/{fs_read.go,fs_write.go,fs_destructive.go,git_read.go,git_write.go,grep.go,run.go,detail.go,builtins.go}` and their `_test.go` files
- Keep: `source/server/internal/agenttools/{tool.go,registry.go,catalog.go}` (the interface + registry + catalog the adapter targets)

**Interfaces:**
- Consumes: `capabilities.NewRegistry`, `builtins.Register`, `builtins.AgentAliases`, `agentadapter.BuildAgentRegistry`.

- [ ] **Step 1: Find the current wiring**

Run: `cd source/server && grep -rn "DefaultRegistry\|toolRegistry\|SetToolRegistry" cmd/ internal/server/`
Expected: shows where `agenttools.DefaultRegistry()` builds the registry handed to the server.

- [ ] **Step 2: Build the capability registry at that site**

Replace the `agenttools.DefaultRegistry()` construction with (adjust variable names to the call site; `svc` is built from the server's existing fields):

```go
capReg := capabilities.NewRegistry(capabilities.Services{
	CloudProvider: s.cloudLLMProvider,
	LocalProvider: s.localLLMProvider,
	Config:        &s.currentConfig,
	ProjectCtx:    s.contextLoader,
	// Engine/Conversations/RunCoproc wired in Phase 5; nil-safe until then.
})
builtins.Register(capReg)
agentReg := agentadapter.BuildAgentRegistry(capReg, builtins.AgentAliases())
s.SetToolRegistry(agentReg)
// retain capReg on the server for Phase 5 (InvokeCapability + MCP adapter):
s.capRegistry = capReg
```

Add a `capRegistry *capabilities.Registry` field to `server.Server` and a `SetToolRegistry` call that mirrors the existing one. If `DefaultRegistry()` is called in `main.go` rather than the server, move this construction there and pass `capReg` into the server via a setter.

- [ ] **Step 3: Delete the migrated tool files**

```bash
cd source/server
git rm internal/agenttools/fs_read.go internal/agenttools/fs_write.go internal/agenttools/fs_destructive.go internal/agenttools/git_read.go internal/agenttools/git_write.go internal/agenttools/grep.go internal/agenttools/run.go internal/agenttools/detail.go internal/agenttools/builtins.go
git rm internal/agenttools/agenttools_test.go internal/agenttools/wx_test.go internal/agenttools/detail_test.go internal/agenttools/detail_wiring_test.go internal/agenttools/llmcontent_test.go internal/agenttools/ls_path_test.go internal/agenttools/run_note_test.go internal/agenttools/catalog_test.go
```

> Before deleting each `_test.go`, port any behavior assertion it makes that isn't already
> covered by the new `builtins` tests into the corresponding `builtins/*_test.go`. The
> deletion must not drop coverage.

- [ ] **Step 4: Build and run the full suite**

Run: `cd source/server && make build && go test ./... -count=1`
Expected: builds; PASS. The standalone loop now serves the migrated capabilities under their Claude-style display names.

- [ ] **Step 5: Manually verify the loop still calls tools**

Run: `cd source/server && bin/cercano agent &` then in another shell `cd source/clients/cli && go run . ` and issue "list the files in this directory".
Expected: `LS`/`Read` tool calls render and succeed exactly as before.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: serve standalone tools from capability registry; remove duplicated agenttools impls"
```

---

## Phase 4 — Dispatch consolidation (retire `dispatch.Tool`)

### Task 7: Drive the dispatch loop from the capability registry

**Files:**
- Modify: `source/server/internal/dispatch/dispatch.go` (`Loop` consumes capabilities)
- Modify: `source/server/cmd/cercano/main.go` (the `runMCPMode` dispatch wiring, lines ~1176-1186)
- Delete: `source/server/internal/dispatch/tools.go` (the `dispatch.Tool` interface + dispatch `Registry`) and `source/server/internal/dispatch/builtin/` (all four builtins + tests)
- Test: `source/server/internal/dispatch/dispatch_test.go` (update)

**Interfaces:**
- Consumes: `capabilities.Registry`, `capabilities.Capability`.
- Produces: `NewLoop(eng engine.InferenceEngine, reg *capabilities.Registry, names []string, model string, maxTurns int) *Loop` (signature change: capability registry + an allow-list of capability names instead of a `*dispatch.Registry`).

- [ ] **Step 1: Write the failing test**

```go
package dispatch

// Update the existing dispatch_test.go: build a capabilities.Registry with a
// fake capability, construct NewLoop with the new signature, and assert the
// loop advertises that capability's schema and runs it. Replace any use of
// dispatch.NewRegistry/dispatch.Tool with capabilities equivalents.
```

(Write the concrete test against the new `NewLoop` signature, mirroring the existing dispatch loop test's seed/turn assertions but sourcing tools from a `capabilities.Registry`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/dispatch/ -v`
Expected: FAIL — new `NewLoop` signature undefined.

- [ ] **Step 3: Change the Loop to consume capabilities**

In `dispatch.go`, replace the `registry *Registry` field with `reg *capabilities.Registry` plus `names []string` (the subset). Replace `schemasAsJSON(l.registry.Schemas())` with a builder that, for each name in `l.names`, looks up the capability and emits `engine.ToolSchemaJSON{Type:"function", Function:{Name:cap.Name(), Description:cap.Description(), Parameters: <parsed cap.Schema()>}}`. Replace `runTool` to call `cap.Execute(ctx, &capabilities.Call{Args: tc.Function.Arguments})` and return `res.LLMContent()`. Parse `cap.Schema()` (JSON bytes) into `map[string]interface{}` for the `Parameters` field.

- [ ] **Step 4: Rewire main.go and delete dispatch builtins**

In `runMCPMode`, replace:

```go
dispatchReg := dispatch.NewRegistry()
_ = dispatchReg.Register(builtin.NewReadFile())
_ = dispatchReg.Register(builtin.NewWriteFile())
_ = dispatchReg.Register(builtin.NewShellExec())
_ = dispatchReg.Register(builtin.NewWebFetch())
dispatchLoop := dispatch.NewLoop(dispatchEng, dispatchReg, cfg.LocalModel, 50)
```

with a capability registry built from builtins (plus the fetch capability from Phase 5 once it lands; until then use `run_command` for shell and omit web_fetch or keep it as a temporary builtin):

```go
capReg := capabilities.NewRegistry(capabilities.Services{Engine: dispatchEng})
builtins.Register(capReg)
dispatchLoop := dispatch.NewLoop(dispatchEng, capReg,
	[]string{"read_file", "write_file", "run_command"}, cfg.LocalModel, 50)
```

Then delete the dispatch builtins and the `dispatch.Tool`/`Registry`:

```bash
cd source/server
git rm -r internal/dispatch/builtin
git rm internal/dispatch/tools.go internal/dispatch/tools_test.go
```

> `web_fetch` becomes the `fetch` capability in Phase 5; add `"fetch"` to the dispatch
> name list in that phase. Until then dispatch loses raw web_fetch — note this in the
> commit body.

- [ ] **Step 5: Build and test**

Run: `cd source/server && make build && go test ./... -count=1`
Expected: builds; PASS.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(dispatch): drive loop from capability registry; retire dispatch.Tool"
```

---

## Phase 5 — MCP surface via `InvokeCapability`

### Task 8: Add the `InvokeCapability` RPC to the proto and regenerate

**Files:**
- Modify: `source/proto/agent.proto`
- Regenerate: `source/server/pkg/proto/agent.pb.go`, `agent_grpc.pb.go`

- [ ] **Step 1: Add the RPC + messages**

In `source/proto/agent.proto`, add to the `Agent` service:

```proto
rpc InvokeCapability(InvokeCapabilityRequest) returns (InvokeCapabilityResponse);
```

and the messages:

```proto
message InvokeCapabilityRequest {
  string name = 1;
  bytes args_json = 2;
  string work_dir = 3;
}

message InvokeCapabilityResponse {
  bytes result_json = 1;
  bool is_error = 2;
  string error = 3;
}
```

- [ ] **Step 2: Regenerate bindings**

Run from the repo root (matches the existing `option go_package` → `pkg/proto`, using the installed protoc-gen-go v1.36.11):

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano/.claude/worktrees/agent-capabilities
protoc \
  --go_out=. --go_opt=module=cercano/source/server \
  --go-grpc_out=. --go-grpc_opt=module=cercano/source/server \
  source/proto/agent.proto
```

Expected: `git diff --stat source/server/pkg/proto/` shows only additive changes (the new RPC + messages).

- [ ] **Step 3: Build**

Run: `cd source/server && go build ./...`
Expected: builds (the new `InvokeCapability` server method is required by the interface next task; if the build fails on `UnimplementedAgentServer`, that's expected until Task 9).

- [ ] **Step 4: Commit**

```bash
git add source/proto/agent.proto source/server/pkg/proto/
git commit -m "proto: add InvokeCapability RPC"
```

### Task 9: Implement the `InvokeCapability` server handler

**Files:**
- Create: `source/server/internal/server/invoke_capability.go`
- Test: `source/server/internal/server/invoke_capability_test.go`

**Interfaces:**
- Consumes: `s.capRegistry` (set in Task 6), `proto.InvokeCapabilityRequest/Response`, `capabilities.Call`.
- Produces: `(*Server).InvokeCapability(ctx, *proto.InvokeCapabilityRequest) (*proto.InvokeCapabilityResponse, error)`.

- [ ] **Step 1: Write the failing test**

```go
package server

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/pkg/proto"
)

func TestInvokeCapabilityRunsRegisteredCap(t *testing.T) {
	reg := capabilities.NewRegistry(capabilities.Services{})
	reg.MustRegister(testEchoCap{})
	s := &Server{capRegistry: reg}
	args, _ := json.Marshal(map[string]any{"v": "hi"})
	resp, err := s.InvokeCapability(context.Background(), &proto.InvokeCapabilityRequest{
		Name: "echo", ArgsJson: args,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	var got capabilities.Result
	if err := json.Unmarshal(resp.ResultJson, &got); err != nil {
		t.Fatal(err)
	}
	if got.Text == "" {
		t.Fatalf("empty result: %+v", got)
	}
}

func TestInvokeCapabilityUnknownName(t *testing.T) {
	s := &Server{capRegistry: capabilities.NewRegistry(capabilities.Services{})}
	resp, err := s.InvokeCapability(context.Background(), &proto.InvokeCapabilityRequest{Name: "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.IsError {
		t.Fatal("expected is_error for unknown capability")
	}
}

// testEchoCap is a minimal capability for the handler test.
type testEchoCap struct{}

func (testEchoCap) Name() string                  { return "echo" }
func (testEchoCap) Description() string            { return "echo" }
func (testEchoCap) Tier() capabilities.Tier        { return capabilities.TierR }
func (testEchoCap) Schema() capabilities.Schema    { return capabilities.Schema(`{"type":"object"}`) }
func (testEchoCap) Surfaces() capabilities.Surface { return capabilities.SurfaceMCP }
func (testEchoCap) Execute(_ context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	return capabilities.NewTextResult("echo " + string(call.Args)), nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/server/ -run TestInvokeCapability -v`
Expected: FAIL — `InvokeCapability` method undefined.

- [ ] **Step 3: Write the handler**

```go
package server

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/capabilities"
	"cercano/source/server/pkg/proto"
)

// InvokeCapability resolves a capability by canonical name and runs it. Used by
// the MCP adapter so every cercano_<name> tool forwards to one generic RPC.
func (s *Server) InvokeCapability(ctx context.Context, req *proto.InvokeCapabilityRequest) (*proto.InvokeCapabilityResponse, error) {
	if s.capRegistry == nil {
		return &proto.InvokeCapabilityResponse{IsError: true, Error: "capability registry not initialized"}, nil
	}
	cap, ok := s.capRegistry.Get(req.GetName())
	if !ok {
		return &proto.InvokeCapabilityResponse{IsError: true, Error: fmt.Sprintf("unknown capability %q", req.GetName())}, nil
	}
	call := &capabilities.Call{
		Args:    req.GetArgsJson(),
		WorkDir: req.GetWorkDir(),
		// Host gates; capability does not self-gate over the MCP surface.
		RequestPermission: func(context.Context, string) (bool, error) { return true, nil },
		Emit:              func(string) {},
	}
	res, err := cap.Execute(ctx, call)
	if err != nil {
		return &proto.InvokeCapabilityResponse{IsError: true, Error: err.Error()}, nil
	}
	out, err := json.Marshal(res)
	if err != nil {
		return &proto.InvokeCapabilityResponse{IsError: true, Error: err.Error()}, nil
	}
	return &proto.InvokeCapabilityResponse{ResultJson: out}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/server/ -run TestInvokeCapability -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/server/invoke_capability.go source/server/internal/server/invoke_capability_test.go
git commit -m "feat(server): InvokeCapability RPC handler over the shared registry"
```

### Task 10: MCP adapter — register `cercano_<name>` forwarders

**Files:**
- Create: `source/server/internal/capabilities/mcpadapter/adapter.go`
- Modify: `source/server/internal/mcp/server.go` (call the adapter from `registerTools`; the `Server` already holds `grpcClient proto.AgentClient`)
- Test: `source/server/internal/capabilities/mcpadapter/adapter_test.go`

**Interfaces:**
- Consumes: `capabilities.Capability` (for name/description/schema metadata — the adapter does not need a live registry, only the list of mcp-surface capabilities' metadata), `proto.AgentClient`, `gomcp`.
- Produces: `RegisterCapabilities(srv *gomcp.Server, client proto.AgentClient, caps []CapMeta)` where `CapMeta{Name, Description string; Schema json.RawMessage}`.

> Note: the MCP process has no `capabilities.Registry` (it only has a gRPC client). So the
> adapter is given a static list of capability metadata (name/description/schema) to
> advertise, and forwards execution to the agent via `InvokeCapability`. Generate that
> metadata list in the agent and expose it — simplest: hardcode the mcp-surface metadata
> list in a small `capabilities/catalog.go` `MCPCatalog() []mcpadapter.CapMeta` built from
> the same builtins constructors (call each constructor and read `Name/Description/Schema/
> Surfaces`), and import it where the MCP server is constructed (it's the same process in
> embedded mode; for external mode, fetch via a `ListCapabilities` RPC — out of scope here,
> embedded covers the default).

- [ ] **Step 1: Write the failing test**

```go
package mcpadapter

import (
	"encoding/json"
	"testing"
)

func TestCapMetaToolName(t *testing.T) {
	m := CapMeta{Name: "read_file", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)}
	if ToolName(m) != "cercano_read_file" {
		t.Fatalf("tool name = %q", ToolName(m))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/capabilities/mcpadapter/ -v`
Expected: FAIL — `CapMeta`/`ToolName` undefined.

- [ ] **Step 3: Write the adapter**

```go
// Package mcpadapter exposes capabilities on the MCP plugin surface. Each
// capability becomes a cercano_<name> tool whose handler forwards execution to
// the agent's InvokeCapability RPC.
package mcpadapter

import (
	"context"
	"encoding/json"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"cercano/source/server/pkg/proto"
)

// CapMeta is the metadata the MCP surface needs to advertise a capability.
type CapMeta struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// ToolName returns the MCP tool name for a capability.
func ToolName(m CapMeta) string { return "cercano_" + m.Name }

// RegisterCapabilities advertises each capability as an MCP tool that forwards
// to InvokeCapability over the gRPC client.
func RegisterCapabilities(srv *gomcp.Server, client proto.AgentClient, caps []CapMeta) {
	for _, m := range caps {
		m := m
		gomcp.AddTool(srv, &gomcp.Tool{
			Name:        ToolName(m),
			Description: m.Description,
		}, func(ctx context.Context, req *gomcp.CallToolRequest, args json.RawMessage) (*gomcp.CallToolResult, any, error) {
			resp, err := client.InvokeCapability(ctx, &proto.InvokeCapabilityRequest{
				Name:     m.Name,
				ArgsJson: args,
			})
			if err != nil {
				return nil, nil, err
			}
			if resp.IsError {
				return &gomcp.CallToolResult{
					IsError: true,
					Content: []gomcp.Content{&gomcp.TextContent{Text: resp.Error}},
				}, nil, nil
			}
			return &gomcp.CallToolResult{
				Content: []gomcp.Content{&gomcp.TextContent{Text: string(resp.ResultJson)}},
			}, nil, nil
		})
	}
}
```

> Verify the `gomcp.AddTool` generic signature accepts a `json.RawMessage` args type; if
> the SDK requires a struct, define `type rawArgs = json.RawMessage` or a passthrough
> struct with a single `json.RawMessage` field and adjust. Check against `handleSummarize`'s
> existing `gomcp.AddTool` usage in `internal/mcp/server.go`.

- [ ] **Step 4: Wire it into the MCP server**

In `internal/mcp/server.go` `registerTools`, after the existing control-plane tools, add:

```go
mcpadapter.RegisterCapabilities(s.mcpServer, s.grpcClient, capabilities.MCPCatalog())
```

and add `capabilities.MCPCatalog()` in `internal/capabilities/catalog.go`:

```go
package capabilities

import "cercano/source/server/internal/capabilities/mcpadapter"

// mcpCatalogSource is set by the builtins package via init to avoid an import
// cycle (builtins imports capabilities). See builtins.RegisterMCPCatalog.
var mcpCatalogSource func() []mcpadapter.CapMeta

// RegisterMCPCatalogSource is called from builtins init.
func RegisterMCPCatalogSource(f func() []mcpadapter.CapMeta) { mcpCatalogSource = f }

// MCPCatalog returns the metadata for every mcp-surface capability.
func MCPCatalog() []mcpadapter.CapMeta {
	if mcpCatalogSource == nil {
		return nil
	}
	return mcpCatalogSource()
}
```

In `builtins/builtins.go` add an `init()` that registers the source by iterating the
builtin constructors and filtering `Surfaces().Has(SurfaceMCP)`:

```go
func init() {
	capabilities.RegisterMCPCatalogSource(func() []mcpadapter.CapMeta {
		reg := capabilities.NewRegistry(capabilities.Services{})
		Register(reg)
		var out []mcpadapter.CapMeta
		for _, c := range reg.ForSurface(capabilities.SurfaceMCP) {
			out = append(out, mcpadapter.CapMeta{Name: c.Name(), Description: c.Description(), Schema: c.Schema()})
		}
		return out
	})
}
```

- [ ] **Step 5: Build and test**

Run: `cd source/server && make build && go test ./... -count=1`
Expected: builds; PASS.

- [ ] **Step 6: Manually verify MCP tools appear**

Run: `cd source/server && bin/cercano --mcp` and from a connected host (or `mcp` inspector) list tools.
Expected: `cercano_read_file`, `cercano_grep`, etc. appear and dispatch through `InvokeCapability`.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(capabilities): MCP adapter forwarding cercano_<name> tools to InvokeCapability"
```

---

## Phase 6 — Co-processor capabilities

### Task 11: Port the co-processor handlers to capabilities

**Files:**
- Create: `source/server/internal/capabilities/builtins/coproc.go` (summarize, extract, classify, explain)
- Create: `source/server/internal/capabilities/builtins/web.go` (fetch, research) — port from `internal/web/` callers
- Modify: `internal/server/server.go` — set `Services.RunCoproc` when building `capReg` (Task 6 site)
- Modify: `internal/mcp/server.go` — remove the now-duplicated `handleSummarize`/`handleExtract`/`handleClassify`/`handleExplain`/`handleFetch`/`handleResearch`/`handleDocument`/`handleDeepResearch` registrations (the capabilities replace them); keep control-plane handlers
- Test: `source/server/internal/capabilities/builtins/coproc_test.go`

**Interfaces:**
- Consumes: `Services.RunCoproc(ctx, prompt, projectDir)`.
- Produces: `Summarize()`, `Extract()`, `Classify()`, `Explain()`, `Fetch()`, `Research()` capability constructors (all `SurfaceMCP`; tier R; `Summarize/Extract/Classify/Explain` also `SurfaceAgent` so Cercano's own loop can use them).

- [ ] **Step 1: Wire `RunCoproc` on the server**

At the Task 6 registry-construction site, set:

```go
Services{
	// ...existing fields...
	RunCoproc: func(ctx context.Context, prompt, projectDir string) (string, error) {
		// Mirror the old handler path: ProcessRequest with Coproc=true via the
		// in-process agent. Reuse the existing internal method the gRPC
		// ProcessRequest handler calls (find it: grep ProcessRequest in
		// internal/server/), passing Coproc=true.
		return s.runCoprocPrompt(ctx, prompt, projectDir)
	},
}
```

Add `runCoprocPrompt` to `server.go` extracting the body the existing `ProcessRequest` gRPC handler uses for `Coproc: true` (so behavior matches the old MCP path exactly).

- [ ] **Step 2: Write the failing test (summarize)**

```go
package builtins

import (
	"context"
	"encoding/json"
	"testing"

	"cercano/source/server/internal/capabilities"
)

func TestSummarizeUsesRunCoproc(t *testing.T) {
	var gotPrompt string
	svc := capabilities.Services{
		RunCoproc: func(_ context.Context, prompt, _ string) (string, error) {
			gotPrompt = prompt
			return "SUMMARY", nil
		},
	}
	reg := capabilities.NewRegistry(svc)
	Register(reg)
	cap, _ := reg.Get("summarize")
	args, _ := json.Marshal(map[string]any{"text": "long text", "max_length": "brief"})
	res, err := cap.Execute(context.Background(), &capabilities.Call{Args: args})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "SUMMARY" {
		t.Fatalf("result = %q", res.Text)
	}
	if gotPrompt == "" {
		t.Fatal("expected a prompt passed to RunCoproc")
	}
}
```

> Capabilities reach `Services` via the registry. Add a small change so a capability can
> read `Services`: either (a) pass `Services` into each constructor (`Summarize(svc)`), or
> (b) add `Services` to `Call` (set by both adapters from `registry.Services()`). Choose
> (b): add `Svc Services` to `Call`, set it in `agentadapter` (from the registry) and in
> the `InvokeCapability` handler (from `s.capRegistry.Services()`); update Task 1's `Call`
> and the Task 4 / Task 9 construction accordingly. Re-run Phase 1–2 tests after the change.

- [ ] **Step 3: Run test to verify it fails**

Run: `cd source/server && go test ./internal/capabilities/builtins/ -run TestSummarize -v`
Expected: FAIL — `summarize` not registered.

- [ ] **Step 4: Implement the co-processor capabilities**

Port each handler's prompt-building (from `internal/mcp/server.go` `handleSummarize` etc.) into a capability whose `Execute` builds the same prompt and calls `call.Svc.RunCoproc(ctx, prompt, projectDir)`. For `fetch`/`research`, call the existing `internal/web/` functions directly (they're local already) rather than `RunCoproc`. Register all in `builtins.Register`. Example (summarize):

```go
type summarizeCap struct{}

func Summarize() capabilities.Capability { return summarizeCap{} }

func (summarizeCap) Name() string                  { return "summarize" }
func (summarizeCap) Tier() capabilities.Tier        { return capabilities.TierR }
func (summarizeCap) Surfaces() capabilities.Surface { return capabilities.SurfaceAgent | capabilities.SurfaceMCP }
func (summarizeCap) Description() string {
	return "Summarize text or a file using local AI. Returns a concise summary without sending the full content to the cloud."
}
func (summarizeCap) Schema() capabilities.Schema {
	return capabilities.Schema(`{"type":"object","properties":{
		"text":{"type":"string"},"file_path":{"type":"string"},
		"max_length":{"type":"string","enum":["brief","medium","detailed"]},
		"project_dir":{"type":"string"}}}`)
}

type summarizeArgs struct {
	Text       string `json:"text"`
	FilePath   string `json:"file_path"`
	MaxLength  string `json:"max_length"`
	ProjectDir string `json:"project_dir"`
}

func (summarizeCap) Execute(ctx context.Context, call *capabilities.Call) (*capabilities.Result, error) {
	var a summarizeArgs
	if err := json.Unmarshal(call.Args, &a); err != nil {
		return nil, fmt.Errorf("summarize: parse args: %w", err)
	}
	content := a.Text
	if a.FilePath != "" {
		data, err := os.ReadFile(a.FilePath)
		if err != nil {
			return nil, fmt.Errorf("summarize: read %q: %w", a.FilePath, err)
		}
		content = string(data)
	}
	if content == "" {
		return nil, errors.New("summarize: provide either 'text' or 'file_path'")
	}
	length := "one paragraph"
	switch a.MaxLength {
	case "brief":
		length = "1-2 sentences"
	case "detailed":
		length = "multiple paragraphs covering all key points"
	}
	prompt := fmt.Sprintf("Summarize the following text in %s. Focus on the most important information. Output only the summary, no preamble.\n\nText to summarize:\n%s", length, content)
	out, err := call.Svc.RunCoproc(ctx, prompt, a.ProjectDir)
	if err != nil {
		return nil, err
	}
	return capabilities.NewTextResult(out), nil
}
```

Implement `extract`, `classify`, `explain` the same way (port their prompt strings verbatim from the existing handlers). For `document`/`deep_research`, port their existing local pipelines into capabilities or, if too large for this phase, leave their MCP handlers in place and note them as a follow-up (they are not duplicated elsewhere, so deferring them does not violate the no-duplication goal).

- [ ] **Step 5: Remove the duplicated MCP handlers**

In `internal/mcp/server.go` delete the `gomcp.AddTool` registrations for the migrated co-processor tools (summarize/extract/classify/explain/fetch/research) — they are now served by the MCP adapter via `InvokeCapability`. Delete the corresponding `handleXxx` methods and request structs. Keep control-plane handlers (`local`, `models`, `config`, `skills`, `stats`, `submit_usage`, `init`, `dispatch`, and any deferred `document`/`deep_research`).

- [ ] **Step 6: Build and test**

Run: `cd source/server && make build && go test ./... -count=1`
Expected: builds; PASS.

- [ ] **Step 7: Manually verify a co-processor tool over MCP**

Run: `cd source/server && bin/cercano --mcp`, then from a host call `cercano_summarize` with sample text.
Expected: returns a summary identical in behavior to before migration.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "feat(capabilities): port co-processor tools (summarize/extract/classify/explain/fetch/research) to capabilities"
```

---

## Self-Review

- **Spec coverage:** Capability interface (Task 1), Services (Task 2), Registry (Task 3), agent adapter (Task 4), naming/aliases (Task 5g, Task 6), permission tiers (Task 1 `ToPermission` + adapter), full migration of 15 built-ins (Tasks 5–5g, cutover Task 6), dispatch consolidation + retire `dispatch.Tool` (Task 7), `InvokeCapability` RPC (Tasks 8–9), MCP adapter (Task 10), co-processor migration (Task 11), control-plane tools left as-is (Task 11 step 5). All spec sections map to a task.
- **Deferred (noted, not duplication):** `document` and `deep_research` may stay as MCP handlers if their pipelines are too large for Task 11 — flagged inline; they are MCP-only, so leaving them does not reintroduce duplication.
- **Open follow-up baked into a task:** capabilities reach `Services` via `Call.Svc`. To avoid rework, add `Svc Services` to the `Call` struct in **Task 2** (right after `Services` is defined — it cannot go in Task 1 because `Services` does not exist yet), and set it in the agent adapter (Task 4, from `registry.Services()`) and the `InvokeCapability` handler (Task 9, from `s.capRegistry.Services()`). Task 11 step 2 restates this as a safety net for anyone who skipped ahead.
- **Type consistency:** `Tier`/`TierR/W/X`, `Surface`/`SurfaceAgent/MCP`, `Result`, `Call`, `Registry`, `NewRegistry`, `ForSurface`, `AsTool`, `BuildAgentRegistry`, `CapMeta`, `ToolName`, `InvokeCapability` used consistently across tasks.
- **Behavior preservation:** Task 0 baseline; per-tool tests ported before deletion (Task 6 step 3); full suite gate after every cutover; manual verifications at Tasks 6, 10, 11.
</content>
