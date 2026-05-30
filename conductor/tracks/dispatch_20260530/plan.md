# `cercano_dispatch` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a new MCP tool `cercano_dispatch` that runs the local LLM as an autonomous agent with built-in tool-use (read_file, write_file, shell_exec, web_fetch), streams events to the host via MCP progress notifications, and is host-cancellable. No SmartRouter, no validator, no cloud escalation.

**Architecture:** A new `internal/dispatch` package owns the agentic loop; built-in tools live in `internal/dispatch/builtin/`. The `engine.InferenceEngine` interface gains one new method `ChatWithTools` implemented against Ollama's `/api/chat` tool-use protocol. A new `dispatch.Store` persists structured history (with tool turns) keyed by `conversation_id` via the existing `session.Service`, under a separate keyspace from `ConversationStore`. The MCP handler issues a progress token, drains events from the loop, and emits one MCP progress notification per event.

**Tech Stack:** Go 1.25, `net/http` for Ollama, `encoding/json` for tool args/results, `os/exec` for `shell_exec`, existing `internal/web` `Fetcher` for `web_fetch`, existing `session.Service` (Google ADK) for history persistence, existing `notifyProgress` helper (`internal/mcp/server.go:130`) for streaming.

**Spec:** [conductor/tracks/dispatch_20260530/spec.md](./spec.md)

---

## File Map

| Action | Path | Purpose |
|---|---|---|
| Create | `source/server/internal/dispatch/events.go` | `Event`, `EventKind` |
| Create | `source/server/internal/dispatch/events_test.go` | |
| Create | `source/server/internal/dispatch/tools.go` | `Tool`, `ToolSchema`, `Registry` |
| Create | `source/server/internal/dispatch/tools_test.go` | |
| Create | `source/server/internal/dispatch/builtin/read_file.go` | + `_test.go` |
| Create | `source/server/internal/dispatch/builtin/write_file.go` | + `_test.go` |
| Create | `source/server/internal/dispatch/builtin/shell_exec.go` | + `_test.go` |
| Create | `source/server/internal/dispatch/builtin/web_fetch.go` | + `_test.go` |
| Modify | `source/server/internal/engine/engine.go` | Add `ChatWithTools` method, `ChatRequest`, `ChatResponse`, `ToolCall` types |
| Modify | `source/server/internal/engine/ollama/ollama.go` | Implement `ChatWithTools` |
| Create | `source/server/internal/engine/ollama/ollama_chat_test.go` | New tests for the new method (keeps `ollama_test.go` untouched) |
| Create | `source/server/internal/dispatch/dispatch.go` | `Loop`, `Run` orchestrator |
| Create | `source/server/internal/dispatch/dispatch_test.go` | With scripted engine fake |
| Create | `source/server/internal/dispatch/store.go` | `History`, `Store` |
| Create | `source/server/internal/dispatch/store_test.go` | |
| Modify | `source/server/internal/mcp/server.go` | New `DispatchRequest`, `handleDispatch`, registration |
| Modify | `source/server/internal/mcp/server_test.go` | Handler test |
| Modify | `source/server/cmd/cercano/main.go` | Wire dispatch store + registry into the MCP server |
| Modify | `source/server/cmd/agent/main.go` | Same wiring |

---

### Task 1: `Event` types

**Files:**
- Create: `source/server/internal/dispatch/events.go`
- Create: `source/server/internal/dispatch/events_test.go`

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/dispatch/events_test.go`:

```go
package dispatch

import (
	"encoding/json"
	"testing"
)

func TestEventKindString(t *testing.T) {
	cases := map[EventKind]string{
		EventTextChunk:  "text_chunk",
		EventToolCall:   "tool_call",
		EventToolResult: "tool_result",
		EventDone:       "done",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("EventKind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

func TestEventMarshalsToJSON(t *testing.T) {
	ev := Event{
		Kind:       EventToolCall,
		ToolCallID: "tc_1",
		ToolName:   "read_file",
		ToolArgs:   json.RawMessage(`{"path":"/x"}`),
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(b), `"kind":"tool_call"`) {
		t.Errorf("expected kind:tool_call in %s", string(b))
	}
	if !contains(string(b), `"tool_name":"read_file"`) {
		t.Errorf("expected tool_name:read_file in %s", string(b))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/dispatch/ -count=1`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/dispatch/events.go`:

```go
// Package dispatch implements the cercano_dispatch agentic tool-use loop.
package dispatch

import "encoding/json"

// EventKind identifies the type of event emitted by Loop.Run.
type EventKind int

const (
	EventTextChunk EventKind = iota
	EventToolCall
	EventToolResult
	EventDone
)

func (k EventKind) String() string {
	switch k {
	case EventTextChunk:
		return "text_chunk"
	case EventToolCall:
		return "tool_call"
	case EventToolResult:
		return "tool_result"
	case EventDone:
		return "done"
	default:
		return "unknown"
	}
}

// MarshalJSON renders EventKind as its string form for downstream consumers.
func (k EventKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.String())
}

// Event is the sum-type emitted by Loop.Run.
// Only the fields relevant to Kind are populated; consumers should switch on Kind.
type Event struct {
	Kind EventKind `json:"kind"`

	// EventTextChunk
	Text string `json:"text,omitempty"`

	// EventToolCall and EventToolResult
	ToolCallID string `json:"tool_call_id,omitempty"`

	// EventToolCall
	ToolName string          `json:"tool_name,omitempty"`
	ToolArgs json.RawMessage `json:"tool_args,omitempty"`

	// EventToolResult
	ToolResult string `json:"tool_result,omitempty"`
	ToolOK     bool   `json:"tool_ok,omitempty"`

	// EventDone
	DoneError string `json:"done_error,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/dispatch/ -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/dispatch/events.go source/server/internal/dispatch/events_test.go
git commit -m "feat(dispatch): add Event sum-type and EventKind"
```

---

### Task 2: `Tool` interface and `Registry`

**Files:**
- Create: `source/server/internal/dispatch/tools.go`
- Create: `source/server/internal/dispatch/tools_test.go`

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/dispatch/tools_test.go`:

```go
package dispatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeTool struct {
	name   string
	schema ToolSchema
	runFn  func(ctx context.Context, args json.RawMessage) (string, error)
}

func (f *fakeTool) Name() string       { return f.name }
func (f *fakeTool) Schema() ToolSchema { return f.schema }
func (f *fakeTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	return f.runFn(ctx, args)
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	tool := &fakeTool{name: "x", schema: ToolSchema{Name: "x", Description: "test"}}
	if err := r.Register(tool); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Get("x")
	if !ok {
		t.Fatal("expected tool registered")
	}
	if got.Name() != "x" {
		t.Errorf("got name %q", got.Name())
	}
}

func TestRegistry_DuplicateNameErrors(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{name: "x"})
	err := r.Register(&fakeTool{name: "x"})
	if err == nil {
		t.Fatal("expected duplicate-name error")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("err = %q, want it to mention 'already registered'", err.Error())
	}
}

func TestRegistry_GetMissingReturnsFalse(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("nope"); ok {
		t.Fatal("expected ok=false for missing tool")
	}
}

func TestRegistry_Schemas(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{name: "a", schema: ToolSchema{Name: "a"}})
	r.Register(&fakeTool{name: "b", schema: ToolSchema{Name: "b"}})
	got := r.Schemas()
	if len(got) != 2 {
		t.Fatalf("got %d schemas, want 2", len(got))
	}
	names := map[string]bool{got[0].Name: true, got[1].Name: true}
	if !names["a"] || !names["b"] {
		t.Errorf("missing schema in %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/dispatch/ -run TestRegistry -count=1`
Expected: FAIL — `undefined: NewRegistry`, `undefined: ToolSchema`.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/dispatch/tools.go`:

```go
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
)

// ToolSchema describes a tool to the local LLM. Mirrors the JSON-schema shape
// expected by Ollama's /api/chat `tools` parameter.
type ToolSchema struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"` // JSON-schema object describing args
}

// Tool is one capability the local LLM can invoke during a dispatch loop.
type Tool interface {
	Name() string
	Schema() ToolSchema
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry is a lookup table of Tools by name.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register adds t to the registry; returns an error if the name is taken.
func (r *Registry) Register(t Tool) error {
	if _, exists := r.tools[t.Name()]; exists {
		return fmt.Errorf("tool %q already registered", t.Name())
	}
	r.tools[t.Name()] = t
	return nil
}

// Get returns the Tool for the given name and whether it was found.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Schemas returns a slice of ToolSchema for every registered tool.
// Order is unspecified.
func (r *Registry) Schemas() []ToolSchema {
	out := make([]ToolSchema, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Schema())
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/dispatch/ -run TestRegistry -count=1 -v`
Expected: PASS (all four subtests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/dispatch/tools.go source/server/internal/dispatch/tools_test.go
git commit -m "feat(dispatch): add Tool interface and Registry"
```

---

### Task 3: `read_file` built-in tool

**Files:**
- Create: `source/server/internal/dispatch/builtin/read_file.go`
- Create: `source/server/internal/dispatch/builtin/read_file_test.go`

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/dispatch/builtin/read_file_test.go`:

```go
package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadFile_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFile()
	args, _ := json.Marshal(map[string]string{"path": path})
	got, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestReadFile_MissingFile(t *testing.T) {
	tool := NewReadFile()
	args, _ := json.Marshal(map[string]string{"path": "/nonexistent/file/here"})
	_, err := tool.Run(context.Background(), args)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReadFile_BinaryDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin.dat")
	// NUL byte in the first 8KB triggers binary detection
	if err := os.WriteFile(path, []byte{0x01, 0x00, 0x02, 0x03}, 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFile()
	args, _ := json.Marshal(map[string]string{"path": path})
	_, err := tool.Run(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for binary file")
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("err = %q, want it to mention 'binary'", err.Error())
	}
}

func TestReadFile_PermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission test would fail")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "denied.txt")
	if err := os.WriteFile(path, []byte("nope"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0644) })
	tool := NewReadFile()
	args, _ := json.Marshal(map[string]string{"path": path})
	_, err := tool.Run(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for permission denied")
	}
}

func TestReadFile_BadArgs(t *testing.T) {
	tool := NewReadFile()
	_, err := tool.Run(context.Background(), json.RawMessage(`{"path":`))
	if err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/dispatch/builtin/ -count=1`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/dispatch/builtin/read_file.go`:

```go
// Package builtin provides the built-in tools for cercano_dispatch.
package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"cercano/source/server/internal/dispatch"
)

const binaryDetectWindow = 8 * 1024

// ReadFile reads a UTF-8 text file from disk.
type ReadFile struct{}

func NewReadFile() *ReadFile { return &ReadFile{} }

func (t *ReadFile) Name() string { return "read_file" }

func (t *ReadFile) Schema() dispatch.ToolSchema {
	return dispatch.ToolSchema{
		Name:        "read_file",
		Description: "Read the contents of a text file from disk. Returns the full file as a UTF-8 string. Errors on binary files or missing/unreadable paths.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Absolute or working-directory-relative file path to read.",
				},
			},
			"required": []string{"path"},
		},
	}
}

type readFileArgs struct {
	Path string `json:"path"`
}

func (t *ReadFile) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var a readFileArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return "", err
	}
	window := data
	if len(window) > binaryDetectWindow {
		window = window[:binaryDetectWindow]
	}
	if bytes.IndexByte(window, 0) >= 0 {
		return "", fmt.Errorf("file appears to be binary (NUL byte in first %d bytes): %s", binaryDetectWindow, a.Path)
	}
	return string(data), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/dispatch/builtin/ -run TestReadFile -count=1 -v`
Expected: PASS (5 subtests; permission test may skip on Windows/root).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/dispatch/builtin/read_file.go source/server/internal/dispatch/builtin/read_file_test.go
git commit -m "feat(dispatch/builtin): add read_file tool"
```

---

### Task 4: `write_file` built-in tool

**Files:**
- Create: `source/server/internal/dispatch/builtin/write_file.go`
- Create: `source/server/internal/dispatch/builtin/write_file_test.go`

- [ ] **Step 1: Write the failing test**

```go
package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFile_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	tool := NewWriteFile()
	args, _ := json.Marshal(map[string]any{"path": path, "content": "hello"})
	got, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "wrote 5 bytes") {
		t.Errorf("got %q", got)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "hello" {
		t.Errorf("file content = %q, want %q", string(b), "hello")
	}
}

func TestWriteFile_Overwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewWriteFile()
	args, _ := json.Marshal(map[string]any{"path": path, "content": "new"})
	if _, err := tool.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "new" {
		t.Errorf("file content = %q, want %q", string(b), "new")
	}
}

func TestWriteFile_CreateDirsTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested/deeper/out.txt")
	tool := NewWriteFile()
	args, _ := json.Marshal(map[string]any{"path": path, "content": "x", "create_dirs": true})
	if _, err := tool.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file at %s: %v", path, err)
	}
}

func TestWriteFile_CreateDirsFalseErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing/out.txt")
	tool := NewWriteFile()
	args, _ := json.Marshal(map[string]any{"path": path, "content": "x"}) // create_dirs default false
	_, err := tool.Run(context.Background(), args)
	if err == nil {
		t.Fatal("expected error when parent dir missing")
	}
}

func TestWriteFile_BadArgs(t *testing.T) {
	tool := NewWriteFile()
	_, err := tool.Run(context.Background(), json.RawMessage(`{`))
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/dispatch/builtin/ -run TestWriteFile -count=1`
Expected: FAIL — `undefined: NewWriteFile`.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/dispatch/builtin/write_file.go`:

```go
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"cercano/source/server/internal/dispatch"
)

// WriteFile writes content to a file, optionally creating parent directories.
type WriteFile struct{}

func NewWriteFile() *WriteFile { return &WriteFile{} }

func (t *WriteFile) Name() string { return "write_file" }

func (t *WriteFile) Schema() dispatch.ToolSchema {
	return dispatch.ToolSchema{
		Name:        "write_file",
		Description: "Write (or overwrite) a text file. Set create_dirs=true to auto-create missing parent directories.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path":        map[string]interface{}{"type": "string", "description": "File path to write."},
				"content":     map[string]interface{}{"type": "string", "description": "Text content to write to the file."},
				"create_dirs": map[string]interface{}{"type": "boolean", "description": "If true, create missing parent directories. Default false."},
			},
			"required": []string{"path", "content"},
		},
	}
}

type writeFileArgs struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	CreateDirs bool   `json:"create_dirs"`
}

func (t *WriteFile) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var a writeFileArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if a.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(a.Path), 0755); err != nil {
			return "", fmt.Errorf("failed to create parent dirs: %w", err)
		}
	}
	if err := os.WriteFile(a.Path, []byte(a.Content), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/dispatch/builtin/ -run TestWriteFile -count=1 -v`
Expected: PASS (5 subtests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/dispatch/builtin/write_file.go source/server/internal/dispatch/builtin/write_file_test.go
git commit -m "feat(dispatch/builtin): add write_file tool"
```

---

### Task 5: `shell_exec` built-in tool

**Files:**
- Create: `source/server/internal/dispatch/builtin/shell_exec.go`
- Create: `source/server/internal/dispatch/builtin/shell_exec_test.go`

- [ ] **Step 1: Write the failing test**

```go
package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestShellExec_ZeroExit(t *testing.T) {
	tool := NewShellExec()
	args, _ := json.Marshal(map[string]any{"command": "echo hello"})
	got, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "exit_code: 0") {
		t.Errorf("got %q, want it to contain exit_code: 0", got)
	}
	if !strings.Contains(got, "hello") {
		t.Errorf("got %q, want it to contain 'hello' from stdout", got)
	}
}

func TestShellExec_NonZeroExitIsData(t *testing.T) {
	tool := NewShellExec()
	args, _ := json.Marshal(map[string]any{"command": "exit 7"})
	got, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatalf("non-zero exit should NOT be a Go error, got: %v", err)
	}
	if !strings.Contains(got, "exit_code: 7") {
		t.Errorf("got %q, want it to contain exit_code: 7", got)
	}
}

func TestShellExec_StderrCaptured(t *testing.T) {
	tool := NewShellExec()
	args, _ := json.Marshal(map[string]any{"command": "echo boom >&2"})
	got, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("got %q, want it to contain stderr 'boom'", got)
	}
}

func TestShellExec_Timeout(t *testing.T) {
	tool := NewShellExec()
	args, _ := json.Marshal(map[string]any{"command": "sleep 10", "timeout_sec": 1})
	_, err := tool.Run(context.Background(), args)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "killed") {
		t.Errorf("err = %q, want it to mention timeout/killed", err.Error())
	}
}

func TestShellExec_Cwd(t *testing.T) {
	dir := t.TempDir()
	tool := NewShellExec()
	args, _ := json.Marshal(map[string]any{"command": "pwd", "cwd": dir})
	got, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, dir) {
		t.Errorf("got %q, want it to contain cwd %s", got, dir)
	}
}

func TestShellExec_BadArgs(t *testing.T) {
	tool := NewShellExec()
	_, err := tool.Run(context.Background(), json.RawMessage(`{`))
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/dispatch/builtin/ -run TestShellExec -count=1`
Expected: FAIL — `undefined: NewShellExec`.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/dispatch/builtin/shell_exec.go`:

```go
package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"cercano/source/server/internal/dispatch"
)

const defaultShellTimeoutSec = 60

// ShellExec runs a shell command (via sh -c) and returns combined stdout / stderr / exit_code.
type ShellExec struct{}

func NewShellExec() *ShellExec { return &ShellExec{} }

func (t *ShellExec) Name() string { return "shell_exec" }

func (t *ShellExec) Schema() dispatch.ToolSchema {
	return dispatch.ToolSchema{
		Name:        "shell_exec",
		Description: "Run a shell command via 'sh -c'. Returns exit_code, stdout, and stderr. Non-zero exit is NOT an error — it is data. Default timeout 60s.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command":     map[string]interface{}{"type": "string", "description": "The shell command to run."},
				"cwd":         map[string]interface{}{"type": "string", "description": "Optional working directory. Defaults to cercano's cwd."},
				"timeout_sec": map[string]interface{}{"type": "integer", "description": "Optional timeout in seconds. Default 60."},
			},
			"required": []string{"command"},
		},
	}
}

type shellExecArgs struct {
	Command    string `json:"command"`
	Cwd        string `json:"cwd"`
	TimeoutSec int    `json:"timeout_sec"`
}

func (t *ShellExec) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var a shellExecArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Command == "" {
		return "", fmt.Errorf("command is required")
	}
	timeout := time.Duration(a.TimeoutSec) * time.Second
	if a.TimeoutSec <= 0 {
		timeout = defaultShellTimeoutSec * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", a.Command)
	if a.Cwd != "" {
		cmd.Dir = a.Cwd
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// Non-zero exit: data, not a Go error.
			exitCode = ee.ExitCode()
			err = nil
		}
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("shell_exec timeout after %s (process killed)", timeout)
	}
	if err != nil {
		return "", fmt.Errorf("shell_exec failed: %w", err)
	}
	return fmt.Sprintf("exit_code: %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String()), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/dispatch/builtin/ -run TestShellExec -count=1 -v`
Expected: PASS (6 subtests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/dispatch/builtin/shell_exec.go source/server/internal/dispatch/builtin/shell_exec_test.go
git commit -m "feat(dispatch/builtin): add shell_exec tool"
```

---

### Task 6: `web_fetch` built-in tool

**Files:**
- Create: `source/server/internal/dispatch/builtin/web_fetch.go`
- Create: `source/server/internal/dispatch/builtin/web_fetch_test.go`

- [ ] **Step 1: Write the failing test**

```go
package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebFetch_HTMLExtracted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><h1>Hello</h1><p>World</p></body></html>`))
	}))
	defer srv.Close()
	tool := NewWebFetch()
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	got, err := tool.Run(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "World") {
		t.Errorf("got %q, want it to contain Hello and World", got)
	}
}

func TestWebFetch_Non200Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	tool := NewWebFetch()
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	_, err := tool.Run(context.Background(), args)
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestWebFetch_MalformedURL(t *testing.T) {
	tool := NewWebFetch()
	args, _ := json.Marshal(map[string]string{"url": "::not-a-url"})
	_, err := tool.Run(context.Background(), args)
	if err == nil {
		t.Fatal("expected error on malformed URL")
	}
}

func TestWebFetch_BadArgs(t *testing.T) {
	tool := NewWebFetch()
	_, err := tool.Run(context.Background(), json.RawMessage(`{`))
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/dispatch/builtin/ -run TestWebFetch -count=1`
Expected: FAIL — `undefined: NewWebFetch`.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/dispatch/builtin/web_fetch.go`:

```go
package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/dispatch"
	"cercano/source/server/internal/web"
)

// WebFetch fetches a URL and returns extracted text (reuses internal/web's HTML stripper).
type WebFetch struct {
	fetcher *web.Fetcher
}

func NewWebFetch() *WebFetch { return &WebFetch{fetcher: web.NewFetcher()} }

func (t *WebFetch) Name() string { return "web_fetch" }

func (t *WebFetch) Schema() dispatch.ToolSchema {
	return dispatch.ToolSchema{
		Name:        "web_fetch",
		Description: "Fetch a URL and return readable text (HTML stripped). Errors on network failure or non-2xx responses.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"url": map[string]interface{}{"type": "string", "description": "URL to fetch."},
			},
			"required": []string{"url"},
		},
	}
}

type webFetchArgs struct {
	URL string `json:"url"`
}

func (t *WebFetch) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var a webFetchArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if a.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	res, err := t.fetcher.Fetch(a.URL)
	if err != nil {
		return "", err
	}
	return res.Content, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/dispatch/builtin/ -run TestWebFetch -count=1 -v`
Expected: PASS (4 subtests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/dispatch/builtin/web_fetch.go source/server/internal/dispatch/builtin/web_fetch_test.go
git commit -m "feat(dispatch/builtin): add web_fetch tool"
```

---

### Task 7: Extend `engine.InferenceEngine` with `ChatWithTools`

**Files:**
- Modify: `source/server/internal/engine/engine.go`

- [ ] **Step 1: Write the failing compile-check**

There's no behavioral logic to test in the interface itself — Task 8 covers the Ollama implementation. For this task, add the types to the interface file. The "test" is that the package builds and a downstream caller (added below) can compile against the new types.

Create `source/server/internal/engine/chat_types_test.go`:

```go
package engine

import (
	"encoding/json"
	"testing"
)

func TestChatRequestJSONShape(t *testing.T) {
	req := ChatRequest{
		Model: "qwen3-coder",
		Messages: []ChatMessage{
			{Role: "user", Content: "hello"},
		},
		Tools: []ToolSchemaJSON{
			{Type: "function", Function: ToolFunctionJSON{Name: "x", Description: "y", Parameters: map[string]interface{}{"type": "object"}}},
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(b), `"model":"qwen3-coder"`) {
		t.Errorf("missing model in %s", string(b))
	}
	if !contains(string(b), `"function":`) {
		t.Errorf("missing function in %s", string(b))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/engine/ -count=1`
Expected: FAIL — `undefined: ChatRequest`, `undefined: ChatMessage`, `undefined: ToolSchemaJSON`.

- [ ] **Step 3: Add the types and extend the interface**

Replace the contents of `source/server/internal/engine/engine.go` with:

```go
package engine

import (
	"context"
	"encoding/json"
	"time"
)

// ModelInfo represents a model available on the InferenceEngine.
type ModelInfo struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

// CompletionResult holds the output of a text generation call along with token usage.
type CompletionResult struct {
	Output       string
	InputTokens  int
	OutputTokens int
}

// ChatMessage is one message in a chat conversation.
// Tool-use turns set ToolCalls (assistant) or ToolCallID + Content (tool result).
type ChatMessage struct {
	Role       string          `json:"role"` // "system" | "user" | "assistant" | "tool"
	Content    string          `json:"content,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"` // role=tool only
	Name       string          `json:"name,omitempty"`         // role=tool: tool name
}

// ToolCall is a single tool invocation requested by the assistant.
type ToolCall struct {
	ID       string         `json:"id,omitempty"`
	Function ToolCallFunc   `json:"function"`
}

type ToolCallFunc struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolSchemaJSON is the on-the-wire format Ollama expects in the chat /tools field.
type ToolSchemaJSON struct {
	Type     string           `json:"type"` // always "function"
	Function ToolFunctionJSON `json:"function"`
}

type ToolFunctionJSON struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ChatRequest is the input to ChatWithTools.
type ChatRequest struct {
	Model    string           `json:"model"`
	Messages []ChatMessage    `json:"messages"`
	Tools    []ToolSchemaJSON `json:"tools,omitempty"`
}

// ChatResponse is the output of ChatWithTools.
// If ToolCalls is non-empty, the assistant wants the loop to run them and re-call.
// Otherwise Content is the final response.
type ChatResponse struct {
	Content      string
	ToolCalls    []ToolCall
	InputTokens  int
	OutputTokens int
}

// InferenceEngine defines the interface for local text generation backends.
type InferenceEngine interface {
	Complete(ctx context.Context, model, prompt, systemPrompt string) (CompletionResult, error)
	CompleteStream(ctx context.Context, model, prompt, systemPrompt string, onToken func(string)) (CompletionResult, error)
	ChatWithTools(ctx context.Context, req ChatRequest) (ChatResponse, error)
	ListModels(ctx context.Context) ([]ModelInfo, error)
	Name() string
}

// EmbeddingService defines the interface for generating semantic embeddings.
type EmbeddingService interface {
	Embed(ctx context.Context, model, text string) ([]float64, error)
	Name() string
}

// ConfigurableEngine defines the interface for engines that support dynamic endpoint configuration and health monitoring.
type ConfigurableEngine interface {
	SetBaseURL(url string)
	GetActiveURL() string
	IsUsingFallback() bool
	StartHealthMonitor(ctx context.Context, interval time.Duration, failureThreshold int)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/engine/ -count=1 -v`
Expected: PASS for the new test. The whole project's build will FAIL because `OllamaEngine` no longer satisfies `InferenceEngine` (missing `ChatWithTools`). Task 8 fixes that.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/engine/engine.go source/server/internal/engine/chat_types_test.go
git commit -m "feat(engine): add ChatWithTools to InferenceEngine + chat types"
```

---

### Task 8: Implement `ChatWithTools` in `OllamaEngine`

**Files:**
- Modify: `source/server/internal/engine/ollama/ollama.go`
- Create: `source/server/internal/engine/ollama/ollama_chat_test.go`

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/engine/ollama/ollama_chat_test.go`:

```go
package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cercano/source/server/internal/engine"
)

func TestChatWithTools_PlainTextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("got path %s, want /api/chat", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"messages"`) {
			t.Errorf("request body missing messages: %s", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message":{"role":"assistant","content":"hi there"},"prompt_eval_count":10,"eval_count":5,"done":true}`))
	}))
	defer srv.Close()

	e := NewOllamaEngine(srv.URL)
	resp, err := e.ChatWithTools(context.Background(), engine.ChatRequest{
		Model: "qwen3-coder",
		Messages: []engine.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hi there" {
		t.Errorf("Content = %q, want %q", resp.Content, "hi there")
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.InputTokens != 10 || resp.OutputTokens != 5 {
		t.Errorf("token counts = %d/%d, want 10/5", resp.InputTokens, resp.OutputTokens)
	}
}

func TestChatWithTools_ToolCallResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"message": {
				"role": "assistant",
				"content": "",
				"tool_calls": [
					{"function": {"name": "read_file", "arguments": {"path":"/x"}}}
				]
			},
			"done": true
		}`))
	}))
	defer srv.Close()

	e := NewOllamaEngine(srv.URL)
	resp, err := e.ChatWithTools(context.Background(), engine.ChatRequest{
		Model:    "qwen3-coder",
		Messages: []engine.ChatMessage{{Role: "user", Content: "read /x"}},
		Tools:    []engine.ToolSchemaJSON{{Type: "function", Function: engine.ToolFunctionJSON{Name: "read_file"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("tool name = %q", resp.ToolCalls[0].Function.Name)
	}
	if resp.ToolCalls[0].ID == "" {
		t.Error("expected synthetic ID when Ollama omits it")
	}
	var args map[string]string
	if err := json.Unmarshal(resp.ToolCalls[0].Function.Arguments, &args); err != nil {
		t.Fatal(err)
	}
	if args["path"] != "/x" {
		t.Errorf("args = %v, want path=/x", args)
	}
}

func TestChatWithTools_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	e := NewOllamaEngine(srv.URL)
	_, err := e.ChatWithTools(context.Background(), engine.ChatRequest{Model: "x"})
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestChatWithTools_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hang until client cancels.
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	e := NewOllamaEngine(srv.URL)
	_, err := e.ChatWithTools(ctx, engine.ChatRequest{Model: "x"})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/engine/ollama/ -run TestChatWithTools -count=1`
Expected: FAIL — `OllamaEngine has no field or method ChatWithTools`.

- [ ] **Step 3: Add the implementation**

Append to `source/server/internal/engine/ollama/ollama.go`:

```go
// ChatWithTools sends a tool-use-capable chat request to Ollama's /api/chat
// endpoint. Returns the assistant message (text and/or tool_calls).
func (e *OllamaEngine) ChatWithTools(ctx context.Context, req engine.ChatRequest) (engine.ChatResponse, error) {
	url := fmt.Sprintf("%s/api/chat", e.GetActiveURL())
	payload := map[string]interface{}{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   false,
		"options":  map[string]interface{}{"num_ctx": 32768},
	}
	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
	}
	body, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return engine.ChatResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := e.Client.Do(httpReq)
	if err != nil {
		return engine.ChatResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return engine.ChatResponse{}, fmt.Errorf("ollama chat error: %s", string(b))
	}
	var chatResp struct {
		Message struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		PromptEvalCount int `json:"prompt_eval_count"`
		EvalCount       int `json:"eval_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return engine.ChatResponse{}, err
	}

	out := engine.ChatResponse{
		Content:      chatResp.Message.Content,
		InputTokens:  chatResp.PromptEvalCount,
		OutputTokens: chatResp.EvalCount,
	}
	for i, tc := range chatResp.Message.ToolCalls {
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("tc_%d", i)
		}
		// Ollama may return arguments as a JSON object (not a string); preserve the raw bytes.
		args := tc.Function.Arguments
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		out.ToolCalls = append(out.ToolCalls, engine.ToolCall{
			ID: id,
			Function: engine.ToolCallFunc{
				Name:      tc.Function.Name,
				Arguments: args,
			},
		})
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/engine/ollama/ -count=1 -v`
Expected: PASS (new and existing tests).

Then verify the whole project compiles:

Run: `cd source/server && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/engine/ollama/
git commit -m "feat(ollama): implement ChatWithTools against /api/chat"
```

---

### Task 9: `dispatch.Store` for structured history

**Files:**
- Create: `source/server/internal/dispatch/store.go`
- Create: `source/server/internal/dispatch/store_test.go`

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/dispatch/store_test.go`:

```go
package dispatch

import (
	"context"
	"encoding/json"
	"testing"

	"google.golang.org/adk/session"

	"cercano/source/server/internal/engine"
)

func TestStore_AppendAndLoad(t *testing.T) {
	svc := session.InMemoryService()
	s := NewStore(svc, 50)

	history := []engine.ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello!"},
	}
	if err := s.Save(context.Background(), "conv1", history); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(context.Background(), "conv1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	if got[1].Content != "hello!" {
		t.Errorf("got %+v", got[1])
	}
}

func TestStore_EmptyIDReturnsEmpty(t *testing.T) {
	s := NewStore(session.InMemoryService(), 50)
	got, err := s.Load(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for empty conversationID, got %+v", got)
	}
}

func TestStore_RoundTripsToolTurn(t *testing.T) {
	s := NewStore(session.InMemoryService(), 50)
	history := []engine.ChatMessage{
		{Role: "user", Content: "read /x"},
		{Role: "assistant", ToolCalls: []engine.ToolCall{
			{ID: "tc_1", Function: engine.ToolCallFunc{Name: "read_file", Arguments: json.RawMessage(`{"path":"/x"}`)}},
		}},
		{Role: "tool", ToolCallID: "tc_1", Name: "read_file", Content: "<contents>"},
		{Role: "assistant", Content: "the file says <contents>"},
	}
	if err := s.Save(context.Background(), "conv2", history); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(context.Background(), "conv2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d messages, want 4", len(got))
	}
	if len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("tool call not preserved: %+v", got[1])
	}
	if got[2].ToolCallID != "tc_1" {
		t.Errorf("tool result not preserved: %+v", got[2])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/dispatch/ -run TestStore -count=1`
Expected: FAIL — `undefined: NewStore`.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/dispatch/store.go`:

```go
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"cercano/source/server/internal/engine"
)

const (
	storeApp    = "cercano"
	storeUser   = "dispatch"
	storePrefix = "dispatch-"
)

// Store persists dispatch conversation history (structured ChatMessages) via session.Service.
// Uses a separate session-ID namespace from agent.ConversationStore so that the
// two cannot collide on a shared conversation_id.
type Store struct {
	svc      session.Service
	maxItems int
}

// NewStore returns a dispatch.Store backed by the given session service.
// maxItems caps the number of messages retained per conversation.
func NewStore(svc session.Service, maxItems int) *Store {
	return &Store{svc: svc, maxItems: maxItems}
}

// Save replaces the stored history for conversationID with messages.
// A no-op when conversationID is empty.
func (s *Store) Save(ctx context.Context, conversationID string, messages []engine.ChatMessage) error {
	if conversationID == "" {
		return nil
	}
	if len(messages) > s.maxItems {
		messages = messages[len(messages)-s.maxItems:]
	}
	payload, err := json.Marshal(messages)
	if err != nil {
		return err
	}
	sess, err := s.getOrCreate(ctx, conversationID)
	if err != nil {
		return err
	}
	ev := session.NewEvent("dispatch")
	ev.Author = "dispatch"
	// Store the full serialized history as a single event's content. Each Save
	// appends a new event; Load reads the most recent one.
	ev.LLMResponse.Content = genai.NewContentFromText(string(payload), genai.RoleModel)
	return s.svc.AppendEvent(ctx, sess, ev)
}

// Load returns the most recently saved history for conversationID, or nil if none.
func (s *Store) Load(ctx context.Context, conversationID string) ([]engine.ChatMessage, error) {
	if conversationID == "" {
		return nil, nil
	}
	sessionID := storePrefix + conversationID
	resp, err := s.svc.Get(ctx, &session.GetRequest{
		AppName:         storeApp,
		UserID:          storeUser,
		SessionID:       sessionID,
		NumRecentEvents: 1,
	})
	if err != nil {
		return nil, nil // no history yet
	}
	events := resp.Session.Events()
	if events.Len() == 0 {
		return nil, nil
	}
	var latest string
	for e := range events.All() {
		if e.LLMResponse.Content == nil {
			continue
		}
		var text string
		for _, p := range e.LLMResponse.Content.Parts {
			text += p.Text
		}
		latest = text
	}
	if latest == "" {
		return nil, nil
	}
	var out []engine.ChatMessage
	if err := json.Unmarshal([]byte(latest), &out); err != nil {
		return nil, fmt.Errorf("corrupt dispatch history for %s: %w", conversationID, err)
	}
	return out, nil
}

func (s *Store) getOrCreate(ctx context.Context, conversationID string) (session.Session, error) {
	sessionID := storePrefix + conversationID
	resp, err := s.svc.Get(ctx, &session.GetRequest{
		AppName:   storeApp,
		UserID:    storeUser,
		SessionID: sessionID,
	})
	if err == nil && resp.Session != nil {
		return resp.Session, nil
	}
	createResp, err := s.svc.Create(ctx, &session.CreateRequest{
		AppName:   storeApp,
		UserID:    storeUser,
		SessionID: sessionID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create dispatch session: %w", err)
	}
	return createResp.Session, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/dispatch/ -run TestStore -count=1 -v`
Expected: PASS (3 subtests).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/dispatch/store.go source/server/internal/dispatch/store_test.go
git commit -m "feat(dispatch): add Store for structured history persistence"
```

---

### Task 10: `dispatch.Loop` orchestrator

**Files:**
- Create: `source/server/internal/dispatch/dispatch.go`
- Create: `source/server/internal/dispatch/dispatch_test.go`

- [ ] **Step 1: Write the failing test**

Create `source/server/internal/dispatch/dispatch_test.go`:

```go
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"cercano/source/server/internal/engine"
)

type scriptedEngine struct {
	responses []engine.ChatResponse
	errs      []error
	calls     []engine.ChatRequest
}

func (s *scriptedEngine) Complete(context.Context, string, string, string) (engine.CompletionResult, error) {
	return engine.CompletionResult{}, errors.New("not used")
}
func (s *scriptedEngine) CompleteStream(context.Context, string, string, string, func(string)) (engine.CompletionResult, error) {
	return engine.CompletionResult{}, errors.New("not used")
}
func (s *scriptedEngine) ListModels(context.Context) ([]engine.ModelInfo, error) {
	return nil, errors.New("not used")
}
func (s *scriptedEngine) Name() string { return "scripted" }

func (s *scriptedEngine) ChatWithTools(_ context.Context, req engine.ChatRequest) (engine.ChatResponse, error) {
	s.calls = append(s.calls, req)
	if len(s.responses) == 0 {
		return engine.ChatResponse{}, errors.New("no scripted response remaining")
	}
	r := s.responses[0]
	s.responses = s.responses[1:]
	var e error
	if len(s.errs) > 0 {
		e = s.errs[0]
		s.errs = s.errs[1:]
	}
	return r, e
}

type echoTool struct{}

func (echoTool) Name() string { return "echo" }
func (echoTool) Schema() ToolSchema {
	return ToolSchema{Name: "echo", Description: "echo arg", Parameters: map[string]interface{}{"type": "object"}}
}
func (echoTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	return "echoed:" + string(args), nil
}

type erroringTool struct{}

func (erroringTool) Name() string { return "bad" }
func (erroringTool) Schema() ToolSchema {
	return ToolSchema{Name: "bad"}
}
func (erroringTool) Run(_ context.Context, _ json.RawMessage) (string, error) {
	return "", errors.New("boom")
}

func collect(ch <-chan Event) []Event {
	var out []Event
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func newRegistry(tools ...Tool) *Registry {
	r := NewRegistry()
	for _, t := range tools {
		r.Register(t)
	}
	return r
}

func TestLoop_PlainTextResponse(t *testing.T) {
	eng := &scriptedEngine{responses: []engine.ChatResponse{
		{Content: "hello world"},
	}}
	loop := NewLoop(eng, newRegistry(), "qwen3-coder", 50)
	ch, _ := loop.Run(context.Background(), nil, "hi")
	events := collect(ch)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (TextChunk + Done)", len(events))
	}
	if events[0].Kind != EventTextChunk || events[0].Text != "hello world" {
		t.Errorf("event[0] = %+v", events[0])
	}
	if events[1].Kind != EventDone || events[1].Cancelled || events[1].DoneError != "" {
		t.Errorf("event[1] = %+v", events[1])
	}
}

func TestLoop_SingleToolCallThenText(t *testing.T) {
	eng := &scriptedEngine{responses: []engine.ChatResponse{
		{ToolCalls: []engine.ToolCall{
			{ID: "tc_1", Function: engine.ToolCallFunc{Name: "echo", Arguments: json.RawMessage(`{"a":1}`)}},
		}},
		{Content: "done"},
	}}
	loop := NewLoop(eng, newRegistry(echoTool{}), "qwen3-coder", 50)
	ch, _ := loop.Run(context.Background(), nil, "do it")
	events := collect(ch)
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4 (ToolCall, ToolResult, TextChunk, Done): %+v", len(events), events)
	}
	if events[0].Kind != EventToolCall || events[0].ToolName != "echo" {
		t.Errorf("event[0] = %+v", events[0])
	}
	if events[1].Kind != EventToolResult || !events[1].ToolOK || !strings.Contains(events[1].ToolResult, "echoed:") {
		t.Errorf("event[1] = %+v", events[1])
	}
	if events[2].Kind != EventTextChunk || events[2].Text != "done" {
		t.Errorf("event[2] = %+v", events[2])
	}
	if events[3].Kind != EventDone {
		t.Errorf("event[3] = %+v", events[3])
	}
}

func TestLoop_ToolErrorFedBackContinues(t *testing.T) {
	eng := &scriptedEngine{responses: []engine.ChatResponse{
		{ToolCalls: []engine.ToolCall{
			{ID: "tc_1", Function: engine.ToolCallFunc{Name: "bad", Arguments: json.RawMessage(`{}`)}},
		}},
		{Content: "oh well"},
	}}
	loop := NewLoop(eng, newRegistry(erroringTool{}), "x", 50)
	ch, _ := loop.Run(context.Background(), nil, "try it")
	events := collect(ch)
	var foundFail bool
	for _, e := range events {
		if e.Kind == EventToolResult && !e.ToolOK && strings.Contains(e.ToolResult, "boom") {
			foundFail = true
		}
	}
	if !foundFail {
		t.Errorf("expected ToolResult ok=false with 'boom', got %+v", events)
	}
}

func TestLoop_UnknownToolFedBack(t *testing.T) {
	eng := &scriptedEngine{responses: []engine.ChatResponse{
		{ToolCalls: []engine.ToolCall{
			{ID: "tc_1", Function: engine.ToolCallFunc{Name: "nope", Arguments: json.RawMessage(`{}`)}},
		}},
		{Content: "ok"},
	}}
	loop := NewLoop(eng, newRegistry(), "x", 50)
	ch, _ := loop.Run(context.Background(), nil, "go")
	events := collect(ch)
	var found bool
	for _, e := range events {
		if e.Kind == EventToolResult && !e.ToolOK && strings.Contains(e.ToolResult, "not registered") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ToolResult ok=false mentioning 'not registered', got %+v", events)
	}
}

func TestLoop_InvalidArgsFedBack(t *testing.T) {
	// The echo tool itself accepts any args, so use a tool that rejects malformed args.
	// We simulate by feeding a tool that requires JSON parsing into something it can't:
	// in practice, the loop forwards args verbatim; the tool reports invalid args via err.
	eng := &scriptedEngine{responses: []engine.ChatResponse{
		{ToolCalls: []engine.ToolCall{
			{ID: "tc_1", Function: engine.ToolCallFunc{Name: "bad", Arguments: json.RawMessage(`{}`)}},
		}},
		{Content: "k"},
	}}
	loop := NewLoop(eng, newRegistry(erroringTool{}), "x", 50)
	ch, _ := loop.Run(context.Background(), nil, "")
	events := collect(ch)
	if events[1].Kind != EventToolResult || events[1].ToolOK {
		t.Errorf("expected ok=false ToolResult, got %+v", events[1])
	}
}

func TestLoop_Cancellation(t *testing.T) {
	eng := &scriptedEngine{responses: []engine.ChatResponse{
		{ToolCalls: []engine.ToolCall{
			{ID: "tc_1", Function: engine.ToolCallFunc{Name: "echo", Arguments: json.RawMessage(`{}`)}},
		}},
		// Second response would be after the tool runs — cancellation happens before that.
		{Content: "should not happen"},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	loop := NewLoop(eng, newRegistry(echoTool{}), "x", 50)
	ch, _ := loop.Run(ctx, nil, "go")

	// Read the first event (ToolCall), then cancel.
	first := <-ch
	if first.Kind != EventToolCall {
		t.Fatalf("first event kind = %s, want tool_call", first.Kind)
	}
	cancel()
	remaining := collect(ch)
	last := remaining[len(remaining)-1]
	if last.Kind != EventDone || !last.Cancelled {
		t.Errorf("last event = %+v, want Done{cancelled:true}", last)
	}

	// Wait a moment to ensure no further engine calls happened beyond what was already queued.
	time.Sleep(20 * time.Millisecond)
	if len(eng.calls) > 1 {
		t.Errorf("engine called %d times after cancel, want 1", len(eng.calls))
	}
}

func TestLoop_MaxTurnsCap(t *testing.T) {
	// Build 100 tool-call responses; loop should give up at 50.
	resp := []engine.ChatResponse{}
	for i := 0; i < 100; i++ {
		resp = append(resp, engine.ChatResponse{ToolCalls: []engine.ToolCall{
			{ID: "x", Function: engine.ToolCallFunc{Name: "echo", Arguments: json.RawMessage(`{}`)}},
		}})
	}
	eng := &scriptedEngine{responses: resp}
	loop := NewLoop(eng, newRegistry(echoTool{}), "x", 50)
	ch, _ := loop.Run(context.Background(), nil, "go")
	events := collect(ch)
	last := events[len(events)-1]
	if last.Kind != EventDone || !strings.Contains(last.DoneError, "exceeded max turns") {
		t.Errorf("last event = %+v, want Done{error: exceeded max turns}", last)
	}
}

func TestLoop_HistoryAccumulates(t *testing.T) {
	eng := &scriptedEngine{responses: []engine.ChatResponse{
		{ToolCalls: []engine.ToolCall{
			{ID: "tc_1", Function: engine.ToolCallFunc{Name: "echo", Arguments: json.RawMessage(`{}`)}},
		}},
		{Content: "done"},
	}}
	loop := NewLoop(eng, newRegistry(echoTool{}), "x", 50)
	ch, finalHist := loop.Run(context.Background(), nil, "go")
	collect(ch) // drain
	hist := finalHist()
	// Expected: user, assistant(tool_calls), tool result, assistant(text) = 4
	if len(hist) != 4 {
		t.Fatalf("history len = %d, want 4: %+v", len(hist), hist)
	}
	if hist[0].Role != "user" || hist[1].Role != "assistant" || hist[2].Role != "tool" || hist[3].Role != "assistant" {
		t.Errorf("roles = %s/%s/%s/%s", hist[0].Role, hist[1].Role, hist[2].Role, hist[3].Role)
	}
	if len(hist[1].ToolCalls) != 1 {
		t.Errorf("assistant tool_calls = %+v", hist[1])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/dispatch/ -run TestLoop -count=1`
Expected: FAIL — `undefined: NewLoop`.

- [ ] **Step 3: Write implementation**

Create `source/server/internal/dispatch/dispatch.go`:

```go
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"

	"cercano/source/server/internal/engine"
)

// Loop runs an agentic tool-use conversation against an InferenceEngine.
type Loop struct {
	engine   engine.InferenceEngine
	registry *Registry
	model    string
	maxTurns int
}

// NewLoop wires a Loop with the given engine, tool registry, model name, and turn cap.
func NewLoop(eng engine.InferenceEngine, reg *Registry, model string, maxTurns int) *Loop {
	if maxTurns <= 0 {
		maxTurns = 50
	}
	return &Loop{engine: eng, registry: reg, model: model, maxTurns: maxTurns}
}

// Run starts the loop in a goroutine. It returns an event channel and a
// finalHistory accessor that becomes valid AFTER the channel is fully drained.
//
// The seed history is prepended to a new user message built from userMsg. If
// userMsg is empty, only the seed history is sent (useful when the host wants
// to continue an existing conversation without adding a new turn — rare).
func (l *Loop) Run(ctx context.Context, seed []engine.ChatMessage, userMsg string) (<-chan Event, func() []engine.ChatMessage) {
	out := make(chan Event, 8)
	history := append([]engine.ChatMessage(nil), seed...)
	if userMsg != "" {
		history = append(history, engine.ChatMessage{Role: "user", Content: userMsg})
	}

	historyRef := &history

	go func() {
		defer close(out)

		for turn := 0; turn < l.maxTurns; turn++ {
			if ctx.Err() != nil {
				out <- Event{Kind: EventDone, Cancelled: true}
				return
			}

			req := engine.ChatRequest{
				Model:    l.model,
				Messages: *historyRef,
				Tools:    schemasAsJSON(l.registry.Schemas()),
			}
			resp, err := l.engine.ChatWithTools(ctx, req)
			if err != nil {
				if ctx.Err() != nil {
					out <- Event{Kind: EventDone, Cancelled: true}
					return
				}
				out <- Event{Kind: EventDone, DoneError: err.Error()}
				return
			}

			if len(resp.ToolCalls) == 0 {
				// Plain assistant text → final response.
				out <- Event{Kind: EventTextChunk, Text: resp.Content}
				*historyRef = append(*historyRef, engine.ChatMessage{Role: "assistant", Content: resp.Content})
				out <- Event{Kind: EventDone}
				return
			}

			// Persist the assistant-with-tool-calls turn.
			*historyRef = append(*historyRef, engine.ChatMessage{
				Role:      "assistant",
				Content:   resp.Content, // may be empty when model only emitted tool calls
				ToolCalls: resp.ToolCalls,
			})

			for _, tc := range resp.ToolCalls {
				if ctx.Err() != nil {
					out <- Event{Kind: EventDone, Cancelled: true}
					return
				}
				out <- Event{
					Kind:       EventToolCall,
					ToolCallID: tc.ID,
					ToolName:   tc.Function.Name,
					ToolArgs:   tc.Function.Arguments,
				}

				result, ok, runErr := l.runTool(ctx, tc)
				resultText := result
				if runErr != nil {
					resultText = runErr.Error()
				}
				out <- Event{
					Kind:       EventToolResult,
					ToolCallID: tc.ID,
					ToolResult: resultText,
					ToolOK:     ok,
				}
				*historyRef = append(*historyRef, engine.ChatMessage{
					Role:       "tool",
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
					Content:    resultText,
				})
			}
		}

		out <- Event{Kind: EventDone, DoneError: fmt.Sprintf("exceeded max turns (%d)", l.maxTurns)}
	}()

	finalHistory := func() []engine.ChatMessage { return *historyRef }
	return out, finalHistory
}

// runTool looks up the tool, runs it, and returns (resultText, ok, err).
// ok=false indicates the result should be presented to the model as a failure
// (unknown tool, bad args, tool returned error). err is non-nil only when the
// tool itself failed to execute (separates "tool failed" from "loop failed").
func (l *Loop) runTool(ctx context.Context, tc engine.ToolCall) (string, bool, error) {
	tool, ok := l.registry.Get(tc.Function.Name)
	if !ok {
		return fmt.Sprintf("tool %q not registered", tc.Function.Name), false, nil
	}
	// Validate that arguments parse as JSON.
	if !json.Valid(tc.Function.Arguments) {
		return fmt.Sprintf("invalid arguments: not valid JSON: %s", string(tc.Function.Arguments)), false, nil
	}
	result, err := tool.Run(ctx, tc.Function.Arguments)
	if err != nil {
		return "", false, err
	}
	return result, true, nil
}

// schemasAsJSON wraps Registry.Schemas() in the Ollama-expected envelope.
func schemasAsJSON(in []ToolSchema) []engine.ToolSchemaJSON {
	out := make([]engine.ToolSchemaJSON, 0, len(in))
	for _, s := range in {
		out = append(out, engine.ToolSchemaJSON{
			Type: "function",
			Function: engine.ToolFunctionJSON{
				Name:        s.Name,
				Description: s.Description,
				Parameters:  s.Parameters,
			},
		})
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/dispatch/ -count=1 -v`
Expected: PASS (all loop tests + tools/events/store from prior tasks).

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/dispatch/dispatch.go source/server/internal/dispatch/dispatch_test.go
git commit -m "feat(dispatch): add Loop orchestrator for agentic tool-use"
```

---

### Task 11: Register `cercano_dispatch` MCP tool

**Files:**
- Modify: `source/server/internal/mcp/server.go`
- Modify: `source/server/internal/mcp/server_test.go`

- [ ] **Step 1: Write the failing handler test**

Open `source/server/internal/mcp/server_test.go`. Following the existing patterns there (which use a fake gRPC client and the registered tool), add a new test. Read the file first to identify the existing test infrastructure (server constructor, fake transport). Then add:

```go
func TestHandleDispatch_TextOnlyResponse(t *testing.T) {
	// Build a Server with a fake engine that returns plain text immediately.
	// Assert: tool returns text in result, no MCP errors.
	eng := &fakeEngineForDispatch{
		responses: []engine.ChatResponse{{Content: "hello back"}},
	}
	srv := newServerWithDispatch(t, eng, /* model: */ "qwen3-coder")

	resp, _, err := srv.handleDispatch(context.Background(), &gomcp.CallToolRequest{}, DispatchRequest{
		Prompt: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	textOut := resultText(resp)
	if !strings.Contains(textOut, "hello back") {
		t.Errorf("result = %q, want it to contain 'hello back'", textOut)
	}
	if !strings.Contains(textOut, `"turns":1`) {
		t.Errorf("result = %q, want it to contain turns:1 summary", textOut)
	}
}

func TestHandleDispatch_StreamsEventsAsProgress(t *testing.T) {
	eng := &fakeEngineForDispatch{
		responses: []engine.ChatResponse{
			{ToolCalls: []engine.ToolCall{{ID: "tc_1", Function: engine.ToolCallFunc{Name: "echo", Arguments: json.RawMessage(`{}`)}}}},
			{Content: "done"},
		},
	}
	srv, progressCh := newServerWithDispatchAndProgressSink(t, eng, "qwen3-coder")
	go func() {
		defer close(progressCh)
		_, _, _ = srv.handleDispatch(context.Background(), makeReqWithProgressToken("ptok"), DispatchRequest{Prompt: "go"})
	}()
	var seen []string
	for note := range progressCh {
		seen = append(seen, note.Message)
	}
	// Expect at least: tool_call, tool_result, text_chunk, done.
	wantSubs := []string{`"kind":"tool_call"`, `"kind":"tool_result"`, `"kind":"text_chunk"`, `"kind":"done"`}
	for _, sub := range wantSubs {
		var found bool
		for _, s := range seen {
			if strings.Contains(s, sub) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("progress events missing %s; got %v", sub, seen)
		}
	}
}
```

(The two helpers — `newServerWithDispatch`, `newServerWithDispatchAndProgressSink`, `resultText`, `makeReqWithProgressToken`, `fakeEngineForDispatch` — must be implemented next to these tests, modeled on the patterns already present in `server_test.go`. Read what's there first; reuse the existing fake transport and tool-registration helpers wherever possible.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd source/server && go test ./internal/mcp/ -run TestHandleDispatch -count=1`
Expected: FAIL — `undefined: DispatchRequest`, `undefined: handleDispatch`.

- [ ] **Step 3: Add the schema, handler, registration, and the helpers**

Open `source/server/internal/mcp/server.go`. Add the request schema near the other `*Request` definitions (after `DeepResearchRequest`):

```go
// DispatchRequest is the input schema for the cercano_dispatch tool.
type DispatchRequest struct {
	Prompt         string `json:"prompt" jsonschema:"The task / instruction for the local LLM."`
	System         string `json:"system,omitempty" jsonschema:"Optional system message override. Defaults to the built-in dispatch system prompt."`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"Conversation ID for multi-turn dispatch across calls."`
	cloudTokenFields
}
```

Add the tool registration in `registerTools()` (alongside the other `gomcp.AddTool` calls):

```go
gomcp.AddTool(s.mcpServer, &gomcp.Tool{
    Name:        "cercano_dispatch",
    Description: "Dispatch a task to Cercano's local LLM as an autonomous agent with full tool-use capability (read_file, write_file, shell_exec, web_fetch). Runs an agentic loop locally — the model can read code, run commands, fetch URLs, and edit files until it decides the task is done. Streams events as progress notifications so you can see what's happening; cancel any time. No cloud calls, no validator loop, no SmartRouter — raw local dispatch under your control. Multi-turn via conversation_id.",
}, s.handleDispatch)
```

The `Server` struct needs three new fields. Find the existing struct declaration and add:

```go
dispatchLoop     *dispatch.Loop      // shared loop (engine + registry + model)
dispatchStore    *dispatch.Store     // history persistence
dispatchModelFor func() string       // resolves current model at call time (live config)
```

The constructor for `Server` (find `NewServer` or similar) takes a new functional option:

```go
// WithDispatch wires the cercano_dispatch tool. modelResolver is called at each
// request to pick up live config changes (e.g., user switching local_model).
func WithDispatch(loop *dispatch.Loop, store *dispatch.Store, modelResolver func() string) Option {
    return func(s *Server) {
        s.dispatchLoop = loop
        s.dispatchStore = store
        s.dispatchModelFor = modelResolver
    }
}
```

Add the handler at the bottom of `server.go`:

```go
// handleDispatch processes a cercano_dispatch tool call.
func (s *Server) handleDispatch(ctx context.Context, request *gomcp.CallToolRequest, args DispatchRequest) (*gomcp.CallToolResult, any, error) {
	if result, ok := s.checkDegraded(); ok {
		return result, nil, nil
	}
	if s.dispatchLoop == nil || s.dispatchStore == nil {
		return nil, nil, fmt.Errorf("dispatch is not configured on this server")
	}

	// Load any existing history for this conversation.
	hist, err := s.dispatchStore.Load(ctx, args.ConversationID)
	if err != nil {
		return nil, nil, fmt.Errorf("load history: %w", err)
	}

	// Prepend system message if absent and a non-empty override was provided.
	if args.System != "" {
		hasSystem := len(hist) > 0 && hist[0].Role == "system"
		if !hasSystem {
			hist = append([]engine.ChatMessage{{Role: "system", Content: args.System}}, hist...)
		}
	}

	startTime := time.Now().UnixNano()

	eventCh, finalHistory := s.dispatchLoop.Run(ctx, hist, args.Prompt)

	var (
		finalText      string
		turns          int
		toolCallsMade  int
		cancelled      bool
		doneErr        string
	)
	for ev := range eventCh {
		// Stream each event as a progress notification.
		if body, mErr := json.Marshal(ev); mErr == nil {
			notifyProgress(ctx, request, string(body), 0, 0)
		}
		switch ev.Kind {
		case dispatch.EventTextChunk:
			finalText += ev.Text
			turns++
		case dispatch.EventToolCall:
			toolCallsMade++
		case dispatch.EventDone:
			cancelled = ev.Cancelled
			doneErr = ev.DoneError
		}
	}

	// Persist updated history (best-effort).
	if persistErr := s.dispatchStore.Save(ctx, args.ConversationID, finalHistory()); persistErr != nil {
		// Don't fail the call — the host already got the result.
		fmt.Fprintf(os.Stderr, "dispatch: failed to persist history for %q: %v\n", args.ConversationID, persistErr)
	}

	// Record usage telemetry (mirrors the pattern in handleLocal).
	s.recordUsage("cercano_dispatch", args.cloudTokenFields, startTime)

	if doneErr != "" {
		return nil, nil, fmt.Errorf("dispatch failed: %s", doneErr)
	}

	resultBody := map[string]interface{}{
		"text": finalText,
		"summary": map[string]interface{}{
			"turns":           turns,
			"tool_calls_made": toolCallsMade,
			"cancelled":       cancelled,
		},
	}
	b, _ := json.MarshalIndent(resultBody, "", "  ")
	return &gomcp.CallToolResult{
		Content: []gomcp.Content{&gomcp.TextContent{Text: string(b)}},
	}, nil, nil
}
```

Add the necessary imports at the top of `server.go`:

```go
import (
    // ...existing...
    "cercano/source/server/internal/dispatch"
    "cercano/source/server/internal/engine"
)
```

Then implement the test helpers in `server_test.go` next to the tests. Reuse the `fakeGRPCClient` / fake transport already present where applicable; `fakeEngineForDispatch` mirrors `scriptedEngine` from `dispatch_test.go` (copy or shared-helper as the existing test file's conventions allow). `newServerWithDispatch` constructs a `Server` with `WithDispatch(NewLoop(eng, builtinRegistry(), model, 50), NewStore(session.InMemoryService(), 100), func() string { return model })`.

`builtinRegistry()` is a small helper:

```go
func builtinRegistry() *dispatch.Registry {
	r := dispatch.NewRegistry()
	r.Register(builtin.NewReadFile())
	r.Register(builtin.NewWriteFile())
	r.Register(builtin.NewShellExec())
	r.Register(builtin.NewWebFetch())
	return r
}
```

…but for the *handler test* you want a controlled engine, so the test registers only the `echo`-style fake tool from the loop test (factor it out into a shared `testutil` if convenient).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd source/server && go test ./internal/mcp/ -run TestHandleDispatch -count=1 -v`
Expected: PASS (both subtests).

Then verify the whole project still compiles and the full test suite passes:

```bash
cd source/server && go build ./...
cd source/server && go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/mcp/
git commit -m "feat(mcp): add cercano_dispatch tool with progress streaming"
```

---

### Task 12: Wire dispatch into both binaries

**Files:**
- Modify: `source/server/cmd/cercano/main.go`
- Modify: `source/server/cmd/agent/main.go`

- [ ] **Step 1: Read current wiring**

Open both `main.go` files. Identify the existing `mcp.NewServer(...)` (or equivalent) call where the MCP server is constructed. Identify where the Ollama engine and `sessionSvc` are already instantiated (Task investigation already confirmed Ollama engine is built early; `sessionSvc := session.InMemoryService()` exists by line ~90 in `cmd/cercano/main.go`).

- [ ] **Step 2: Add dispatch wiring**

In `source/server/cmd/cercano/main.go`, after the existing `sessionSvc := session.InMemoryService()` line, add:

```go
// Wire cercano_dispatch.
dispatchRegistry := dispatch.NewRegistry()
dispatchRegistry.Register(builtin.NewReadFile())
dispatchRegistry.Register(builtin.NewWriteFile())
dispatchRegistry.Register(builtin.NewShellExec())
dispatchRegistry.Register(builtin.NewWebFetch())
dispatchLoop := dispatch.NewLoop(ollamaEng, dispatchRegistry, cfg.LocalModel, 50)
dispatchStore := dispatch.NewStore(sessionSvc, 200)
modelResolver := func() string { return cfg.LocalModel }
```

Then where `mcp.NewServer(...)` is called, append the `mcp.WithDispatch(dispatchLoop, dispatchStore, modelResolver)` option.

Add to the imports:

```go
"cercano/source/server/internal/dispatch"
"cercano/source/server/internal/dispatch/builtin"
```

- [ ] **Step 3: Apply the same change to `source/server/cmd/agent/main.go`**

Mirror the additions exactly.

- [ ] **Step 4: Build and test**

```bash
cd source/server && go build ./...
cd source/server && go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/cmd/cercano/main.go source/server/cmd/agent/main.go
git commit -m "feat(cmd): wire dispatch loop and store into both binaries"
```

---

### Task 13: End-to-end smoke verification

**Files:** none (manual checks).

- [ ] **Step 1: Build the binary**

```bash
cd source/server && make build
```

- [ ] **Step 2: Confirm `cercano_dispatch` is registered**

Use the gRPC reflection or a quick MCP probe to list tools. Expected: `cercano_dispatch` appears alongside the existing `cercano_*` tools with the description from Task 11.

- [ ] **Step 3: Drive a simple two-turn task against a real Ollama instance**

(Requires Ollama running with a tool-use-capable model like `qwen3-coder`.)

In a temp working dir:

```bash
mkdir -p /tmp/cerc-dispatch-smoke && cd /tmp/cerc-dispatch-smoke
echo "the file says hello" > /tmp/cerc-dispatch-smoke/note.txt
```

Invoke `cercano_dispatch` via the MCP interface with:

```json
{"prompt": "Read /tmp/cerc-dispatch-smoke/note.txt and tell me what it contains."}
```

Expected progress events (in order):
1. `tool_call` with `tool_name: "read_file"` and args `{"path":"/tmp/cerc-dispatch-smoke/note.txt"}`.
2. `tool_result` with `tool_ok: true` and content `"the file says hello\n"`.
3. `text_chunk` containing the assistant's summary.
4. `done` with no error, `cancelled: false`.

Final tool result includes `text` referring to the file content, and `summary.tool_calls_made >= 1`.

- [ ] **Step 4: Verify cancellation**

Invoke `cercano_dispatch` with `{"prompt": "Run the command sleep 30 and tell me the exit code."}`. After 2 seconds, send the MCP `notifications/cancelled` for the request. Expected: a `done{cancelled: true}` event arrives within ~1s, the MCP tool result reflects `summary.cancelled: true`, and the cercano process has no lingering `sleep` child.

- [ ] **Step 5: Verify multi-turn continuation**

Call once with `{"prompt": "What's in /tmp/cerc-dispatch-smoke/note.txt?", "conversation_id": "smoke-1"}`. Then call again with `{"prompt": "Now write 'plus more' to /tmp/cerc-dispatch-smoke/append.txt.", "conversation_id": "smoke-1"}` (same ID). Expected: the second call has prior history available (asserted by the model referring to the earlier read if relevant) and `write_file` runs to create the new file.

```bash
cat /tmp/cerc-dispatch-smoke/append.txt   # expected: plus more
```

- [ ] **Step 6: Commit any docs touch-ups**

If the project README mentions `cercano_local` in a "Local Co-Processor Tools" table, add `cercano_dispatch` to the same table. Otherwise no docs change is required for v1.

```bash
git add -A
git commit -m "docs: mention cercano_dispatch in README tools table" --allow-empty
```

---

## Self-Review

**1. Spec coverage:**

| Spec section | Task(s) |
|---|---|
| MCP tool surface + request schema | T11 |
| `dispatch.Loop` + tool-use orchestration | T10 |
| Per-call flow / streaming events | T10 (loop), T11 (MCP bridge) |
| Built-in tools (read_file, write_file, shell_exec, web_fetch) | T3, T4, T5, T6 |
| Engine `ChatWithTools` interface | T7, T8 |
| Conversation history (structured, separate keyspace) | T9 |
| Cancellation | T10 (loop), T8 (engine HTTP), T11 (handler propagation), T13 (smoke) |
| 50-turn cap | T10 |
| Tool errors fed back, loop continues | T10 |
| Unknown tool / invalid args fed back | T10 |
| Multi-turn via conversation_id | T11 (load+save), T13 (smoke) |
| Wiring into binaries | T12 |

No gaps.

**2. Placeholder scan:** None. Each step has executable code, exact commands, or specific instructions for adapting to existing test infrastructure (Task 11's helpers, where the existing patterns in `server_test.go` are the source of truth — that's pragmatic, not a placeholder).

**3. Type consistency:**

- `Event`, `EventKind`, `EventTextChunk/ToolCall/ToolResult/Done` — defined T1, used T10, T11. Match.
- `Tool`, `ToolSchema`, `Registry`, `NewRegistry` — T2, used T3–T6, T10, T12.
- `engine.ChatRequest`, `engine.ChatResponse`, `engine.ChatMessage`, `engine.ToolCall`, `engine.ToolCallFunc`, `engine.ToolSchemaJSON`, `engine.ToolFunctionJSON` — T7, used T8, T9, T10, T11.
- `engine.InferenceEngine.ChatWithTools` — T7 (interface), T8 (impl), T10 (consumer).
- `dispatch.Store`, `NewStore`, `Save`, `Load` — T9, used T11, T12.
- `dispatch.Loop`, `NewLoop`, `Run` — T10, used T11, T12.
- `dispatch.builtin.NewReadFile/NewWriteFile/NewShellExec/NewWebFetch` — T3–T6, used T11, T12.
- MCP: `DispatchRequest`, `handleDispatch`, `WithDispatch`, `dispatchLoop`/`dispatchStore`/`dispatchModelFor` fields — all T11.

Consistent.
