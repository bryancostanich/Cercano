# Locus Mode Implementation Plan (Core)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Locus Mode (`cloud_only`|`cloud_primary`|`local_primary`|`local_only`) the single source of truth for whether the agent's **main LLM** runs on cloud or local, with bidirectional fallback for `*_primary`, hard-fail for `*_only`, and always-visible routing (no silent degradation).

**Architecture:** A new pure `internal/locus` package resolves a mode to a preferred/fallback **tier** (local|cloud). The gRPC server maps tiers to its wired `llm.Provider`s (cloud = Anthropic, local = Ollama — newly wired), drives the native tool-loop with the resolved provider, and emits the existing `RouteSelected` frame (plus a `Progress` notice on fallback). `locus_mode` is added to config + the `UpdateConfig`/`GetConfig` RPCs + the CLI and VS Code control surfaces.

**Tech Stack:** Go, gRPC (protoc-gen-go v1.36.11), `internal/llm` provider interface, bubbletea CLI.

## Scope

**In scope (this plan):** config/proto/RPC plumbing for `locus_mode`; the `internal/locus` policy; wiring a local tool-capable `llm.Provider`; main-LLM provider selection + fallback + visibility in the native tool-loop; CLI `/locus` + config-editor row; VS Code setting; default `local_primary`.

**Deferred to a follow-up plan (NOT here):** (1) co-processor tier honoring locus — making `cercano_summarize/extract/classify/explain` route to cloud under Cloud Only (today they call `ProcessRequest{DirectLocal:true}` → local). (2) Formally shelving the SmartRouter in the legacy `ProcessRequest`/`ProcessRequestStream` path. These are separable; the design doc (`docs/agent/locus-mode.md`) covers them as the full vision.

## Global Constraints

- Module path `cercano/source/server`. Run Go commands from `source/server/`. Build: `go build ./...`. Test: `go test ./<pkg>/ -count=1`.
- Mode string values are exactly `cloud_only`, `cloud_primary`, `local_primary`, `local_only`. Default `local_primary`.
- **No silent degradation:** whenever the tier actually serving differs from the mode's preferred tier, emit a visible notice AND reflect the true tier in `RouteSelected`/routing metadata.
- `*_only` modes must NEVER cross tiers — return a clear error instead.
- Commit messages must NOT contain the word "Claude" anywhere (user rule).
- Cloud tool-loop is Anthropic-only for V1 (matches current wiring).

---

### Task 1: `internal/locus` package — mode + tier resolution

**Files:**
- Create: `source/server/internal/locus/locus.go`
- Test: `source/server/internal/locus/locus_test.go`

**Interfaces:**
- Produces:
  - `type Mode string`; consts `CloudOnly`, `CloudPrimary`, `LocalPrimary`, `LocalOnly` (values `"cloud_only"`, `"cloud_primary"`, `"local_primary"`, `"local_only"`).
  - `const DefaultMode = LocalPrimary`
  - `type Tier int`; consts `TierLocal`, `TierCloud`; `func (Tier) String() string`
  - `type Resolution struct { Preferred Tier; Fallback Tier; CrossAllowed bool }`
  - `func (m Mode) Main() Resolution`
  - `func ParseMode(s string) (Mode, error)` (also accepts `""` → DefaultMode)

- [ ] **Step 1: Write failing tests**

Create `locus_test.go`:

```go
package locus

import "testing"

func TestParseMode(t *testing.T) {
	cases := map[string]Mode{
		"":              LocalPrimary,
		"local_primary": LocalPrimary,
		"cloud_only":    CloudOnly,
		"cloud_primary": CloudPrimary,
		"local_only":    LocalOnly,
	}
	for in, want := range cases {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseMode("nonsense"); err == nil {
		t.Error("expected error for invalid mode")
	}
}

func TestMainResolution(t *testing.T) {
	cases := []struct {
		mode  Mode
		pref  Tier
		fall  Tier
		cross bool
	}{
		{CloudOnly, TierCloud, TierCloud, false},
		{CloudPrimary, TierCloud, TierLocal, true},
		{LocalPrimary, TierLocal, TierCloud, true},
		{LocalOnly, TierLocal, TierLocal, false},
	}
	for _, c := range cases {
		r := c.mode.Main()
		if r.Preferred != c.pref || r.Fallback != c.fall || r.CrossAllowed != c.cross {
			t.Errorf("%s.Main() = %+v; want pref=%v fall=%v cross=%v", c.mode, r, c.pref, c.fall, c.cross)
		}
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

Run: `go test ./internal/locus/ -count=1`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Implement `locus.go`**

Create `locus.go`:

```go
// Package locus is the single source of truth for whether work runs on the
// local or cloud tier. It resolves a Mode into a preferred/fallback Tier; the
// caller maps Tiers to concrete providers and enforces availability.
package locus

import "fmt"

type Mode string

const (
	CloudOnly    Mode = "cloud_only"
	CloudPrimary Mode = "cloud_primary"
	LocalPrimary Mode = "local_primary"
	LocalOnly    Mode = "local_only"
)

// DefaultMode preserves Cercano's local-first intent.
const DefaultMode = LocalPrimary

type Tier int

const (
	TierLocal Tier = iota
	TierCloud
)

func (t Tier) String() string {
	if t == TierCloud {
		return "cloud"
	}
	return "local"
}

// Resolution describes how to serve a request for one work tier: the Preferred
// provider tier, the Fallback tier to use if Preferred can't serve, and whether
// crossing to the Fallback is allowed at all (false for the *_only modes).
type Resolution struct {
	Preferred    Tier
	Fallback     Tier
	CrossAllowed bool
}

// Main resolves the tier policy for the agent's main LLM.
func (m Mode) Main() Resolution {
	switch m {
	case CloudOnly:
		return Resolution{TierCloud, TierCloud, false}
	case CloudPrimary:
		return Resolution{TierCloud, TierLocal, true}
	case LocalOnly:
		return Resolution{TierLocal, TierLocal, false}
	case LocalPrimary:
		fallthrough
	default:
		return Resolution{TierLocal, TierCloud, true}
	}
}

// ParseMode validates a config string. Empty resolves to DefaultMode.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case "":
		return DefaultMode, nil
	case CloudOnly, CloudPrimary, LocalPrimary, LocalOnly:
		return Mode(s), nil
	default:
		return "", fmt.Errorf("invalid locus_mode %q (want cloud_only|cloud_primary|local_primary|local_only)", s)
	}
}
```

- [ ] **Step 4: Run tests, verify pass**

Run: `go test ./internal/locus/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/locus/
git commit -m "feat(locus): mode + tier resolution policy"
```

---

### Task 2: Config — `locus_mode` field + default

**Files:**
- Modify: `source/server/pkg/config/config.go`
- Test: `source/server/pkg/config/config_test.go` (create if absent)

**Interfaces:**
- Produces: `Config.LocusMode string` (yaml `locus_mode`), defaulted to `"local_primary"` in `Defaults()`.

- [ ] **Step 1: Write failing test**

Append to `config_test.go` (create with `package config` + imports if it does not exist):

```go
func TestDefaultsLocusMode(t *testing.T) {
	if got := Defaults().LocusMode; got != "local_primary" {
		t.Errorf("Defaults().LocusMode = %q; want local_primary", got)
	}
}
```

- [ ] **Step 2: Run test, verify it fails**

Run: `go test ./pkg/config/ -run LocusMode -count=1`
Expected: FAIL — `LocusMode` undefined.

- [ ] **Step 3: Add the field**

In `config.go` `Config` struct, after `CloudBaseURL`:

```go
	CloudBaseURL string `yaml:"cloud_base_url"`
	LocusMode    string `yaml:"locus_mode"` // cloud_only|cloud_primary|local_primary|local_only
```

- [ ] **Step 4: Default it**

In `Defaults()`, add to the returned `Config{...}` literal:

```go
		LocusMode: "local_primary",
```

- [ ] **Step 5: Run test, verify pass**

Run: `go test ./pkg/config/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add source/server/pkg/config/
git commit -m "feat(config): add locus_mode with local_primary default"
```

---

### Task 3: Proto + RPC + agentclient — plumb `locus_mode`

**Files:**
- Modify: `source/proto/agent.proto`
- Regenerate: `source/server/pkg/proto/agent.pb.go`, `agent_grpc.pb.go`
- Modify: `source/server/internal/server/server.go` (`UpdateConfig`, `GetConfig`)
- Modify: `source/server/pkg/agentclient/client.go` (`Config`, `ConfigUpdate`, `GetConfig`, `UpdateConfig`)

**Interfaces:**
- Produces (generated): `UpdateConfigRequest.LocusMode`, `GetConfigResponse.LocusMode`.
- Produces (agentclient): `Config.LocusMode`, `ConfigUpdate.LocusMode`.

- [ ] **Step 1: Add proto fields**

In `source/proto/agent.proto`, `UpdateConfigRequest` (after `local_runtime = 7;`):

```proto
  string locus_mode = 8;     // cloud_only|cloud_primary|local_primary|local_only
```

`GetConfigResponse` (after `local_runtime = 10;`):

```proto
  string locus_mode = 11;
```

- [ ] **Step 2: Regenerate proto**

From repo root, with plugins installed (`go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11` and `…/grpc/cmd/protoc-gen-go-grpc@latest`, then `export PATH="$PATH:$(go env GOPATH)/bin"`):

```bash
protoc --proto_path=. \
  --go_out=source/server --go_opt=module=cercano/source/server \
  --go-grpc_out=source/server --go-grpc_opt=module=cercano/source/server \
  source/proto/agent.proto
```

Verify: `grep -c 'GetLocusMode' source/server/pkg/proto/agent.pb.go` returns ≥ 2.

- [ ] **Step 3: Apply + persist in `UpdateConfig`**

In `server.go` `UpdateConfig`, after the `local_runtime` block (ends ~line 162), add validation + change tracking:

```go
	if req.LocusMode != "" {
		if _, err := locus.ParseMode(req.LocusMode); err != nil {
			return &proto.UpdateConfigResponse{Success: false, Message: err.Error()}, nil
		}
		changes = append(changes, fmt.Sprintf("locus_mode=%s", req.LocusMode))
		fmt.Printf("UpdateConfig: Locus mode set to %s\n", req.LocusMode)
	}
```

In the persistence block (after `if req.CloudBaseUrl != "" { ... }`, ~line 235):

```go
	if req.LocusMode != "" {
		s.currentConfig.LocusMode = req.LocusMode
	}
```

Add the import `"cercano/source/server/internal/locus"` to `server.go`.

- [ ] **Step 4: Report in `GetConfig`**

In `server.go` `GetConfig`, add to the returned `&proto.GetConfigResponse{...}`:

```go
		LocusMode:      s.currentConfig.LocusMode,
```

- [ ] **Step 5: Plumb agentclient**

In `pkg/agentclient/client.go`:
- Add `LocusMode string` to the `Config` struct (after `Port`).
- Add `LocusMode string` to the `ConfigUpdate` struct (after `CloudBaseURL`).
- In `GetConfig`, map `LocusMode: resp.GetLocusMode(),`.
- In `UpdateConfig`, set `LocusMode: u.LocusMode,` in the `&proto.UpdateConfigRequest{...}`.

- [ ] **Step 6: Build**

Run: `cd source/server && go build ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add source/proto/agent.proto source/server/pkg/proto/ source/server/internal/server/server.go source/server/pkg/agentclient/client.go
git commit -m "feat(proto,rpc): plumb locus_mode through config RPCs + client"
```

---

### Task 4: Wire a local tool-capable provider + server tier→provider resolver

**Files:**
- Modify: `source/server/internal/server/server.go` (field, setter, resolver)
- Modify: `source/server/cmd/cercano/main.go` (construct + wire Ollama llm client)
- Test: `source/server/internal/server/locus_resolve_test.go` (create)

**Interfaces:**
- Consumes: `locus.Mode.Main()`, `llm.Provider`.
- Produces:
  - `Server.localLLMProvider llm.Provider` field
  - `func (s *Server) SetLocalLLMProvider(p llm.Provider)`
  - `func (s *Server) resolveMainProvider() (p llm.Provider, isCloud, fellBack bool, err error)`

- [ ] **Step 1: Write failing test**

Create `internal/server/locus_resolve_test.go`:

```go
package server

import (
	"testing"

	"cercano/source/server/internal/llm"
	"cercano/source/server/pkg/config"
)

type stubProv struct{ name string }

func (s stubProv) Name() string                { return s.name }
func (s stubProv) Capabilities() llm.Capabilities { return llm.Capabilities{SupportsTools: true} }
func (s stubProv) Chat(any, any) (any, any)     { panic("unused") } // replaced below

func TestResolveMainProvider(t *testing.T) {
	cloud := stubProv{"anthropic"}
	local := stubProv{"ollama"}

	mk := func(mode string, c, l llm.Provider) *Server {
		s := &Server{currentConfig: config.Config{LocusMode: mode}}
		s.cloudLLMProvider = c
		s.localLLMProvider = l
		return s
	}

	// Local Primary, both available → local, not fallback.
	if p, isCloud, fb, err := mk("local_primary", cloud, local).resolveMainProvider(); err != nil || isCloud || fb || p.Name() != "ollama" {
		t.Errorf("local_primary both: %v isCloud=%v fb=%v err=%v", p, isCloud, fb, err)
	}
	// Local Primary, local missing → fall back to cloud.
	if p, isCloud, fb, err := mk("local_primary", cloud, nil).resolveMainProvider(); err != nil || !isCloud || !fb || p.Name() != "anthropic" {
		t.Errorf("local_primary fallback: %v isCloud=%v fb=%v err=%v", p, isCloud, fb, err)
	}
	// Cloud Only, cloud missing → hard error (no cross).
	if _, _, _, err := mk("cloud_only", nil, local).resolveMainProvider(); err == nil {
		t.Error("cloud_only with no cloud should error")
	}
	// Local Only, local missing → hard error (no cross even though cloud present).
	if _, _, _, err := mk("local_only", cloud, nil).resolveMainProvider(); err == nil {
		t.Error("local_only with no local should error")
	}
}
```

NOTE: the `stubProv` must satisfy `llm.Provider`. Replace the placeholder `Chat`/add `StreamChat` to match the real interface signatures (`Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error)` and `StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error)`) — both can `panic("unused")` since `resolveMainProvider` only calls `Name()`/`Capabilities()`. Read `internal/llm/provider.go` for the exact `ChatRequest`/`ChatResponse`/`StreamReader` types and write the two methods accordingly.

- [ ] **Step 2: Run test, verify it fails**

Run: `go test ./internal/server/ -run ResolveMainProvider -count=1`
Expected: FAIL — `localLLMProvider`/`resolveMainProvider` undefined.

- [ ] **Step 3: Add field + setter**

In `server.go` `Server` struct, after `cloudLLMProvider llm.Provider`:

```go
	localLLMProvider    llm.Provider // native-tool-loop local provider (Ollama)
```

After `SetCloudLLMProvider`:

```go
// SetLocalLLMProvider attaches the native-tool-calling local provider (Ollama).
func (s *Server) SetLocalLLMProvider(p llm.Provider) { s.localLLMProvider = p }
```

- [ ] **Step 4: Implement `resolveMainProvider`**

Add to `server.go`:

```go
// resolveMainProvider picks the llm.Provider for the main tool-loop per the
// active Locus Mode. Returns the provider, whether it's the cloud tier, whether
// this is a fallback (preferred tier unavailable), or an error when the mode
// forbids crossing and the required tier has no provider wired.
func (s *Server) resolveMainProvider() (llm.Provider, bool, bool, error) {
	mode, _ := locus.ParseMode(s.currentConfig.LocusMode)
	res := mode.Main()

	provForTier := func(t locus.Tier) llm.Provider {
		if t == locus.TierCloud {
			return s.cloudLLMProvider
		}
		return s.localLLMProvider
	}

	if p := provForTier(res.Preferred); p != nil {
		return p, res.Preferred == locus.TierCloud, false, nil
	}
	if res.CrossAllowed {
		if p := provForTier(res.Fallback); p != nil {
			return p, res.Fallback == locus.TierCloud, true, nil
		}
	}
	return nil, false, false, fmt.Errorf(
		"locus mode %q: no %s provider available (and fallback not permitted)",
		mode, res.Preferred)
}
```

Ensure `server.go` imports `"cercano/source/server/internal/locus"` (added in Task 3).

- [ ] **Step 5: Wire the Ollama llm client in main.go**

In `cmd/cercano/main.go`, near the Anthropic wiring (~line 240), add an unconditional local provider wire (the local tier should always be available when Ollama is reachable):

```go
	// Native tool-loop local provider (Ollama). Wired unconditionally so Local
	// modes can drive the tool-calling loop; availability is enforced per turn.
	srv.SetLocalLLMProvider(ollamallm.NewClient(ollamallm.Config{
		BaseURL: cfg.OllamaURL,
		Model:   cfg.LocalModel,
	}))
```

Add the import with an alias to avoid colliding with the existing `engine/ollama` import:

```go
	ollamallm "cercano/source/server/internal/llm/ollama"
```

- [ ] **Step 6: Run test + build**

Run: `go test ./internal/server/ -run ResolveMainProvider -count=1 && go build ./...`
Expected: PASS + BUILD OK.

- [ ] **Step 7: Commit**

```bash
git add source/server/internal/server/ source/server/cmd/cercano/main.go
git commit -m "feat(server): wire local llm provider + locus main-tier resolver"
```

---

### Task 5: Dispatch + tool-loop use the resolved provider

**Files:**
- Modify: `source/server/internal/server/server.go` (`StreamProcessRequest` gate + `streamProcessRequestWithToolLoop`)

**Interfaces:**
- Consumes: `resolveMainProvider()`.

- [ ] **Step 1: Update the dispatch gate**

In `StreamProcessRequest` (~line 727), the native loop should run whenever *either* tool provider is wired (so Local modes use it too):

```go
	if (s.cloudLLMProvider != nil || s.localLLMProvider != nil) && s.toolRegistry != nil {
		return s.streamProcessRequestWithToolLoop(req, stream)
	}
```

- [ ] **Step 2: Resolve the provider inside the tool-loop**

In `streamProcessRequestWithToolLoop`, replace the hardcoded cloud announce + `RunToolLoop` provider (the `RouteSelected` block at ~859 and the `agent.RunToolLoop` call at ~868). First resolve:

```go
	provider, isCloud, fellBack, err := s.resolveMainProvider()
	if err != nil {
		// *_only mode with its tier unavailable — hard fail, no silent cross.
		return stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_FinalResponse{
				FinalResponse: &proto.ProcessRequestResponse{Output: "Locus: " + err.Error()},
			},
		})
	}
	if fellBack {
		stream.Send(&proto.StreamProcessResponse{
			Payload: &proto.StreamProcessResponse_Progress{
				Progress: &proto.ProgressUpdate{
					Message: fmt.Sprintf("⚠ preferred tier unavailable — falling back to %s (%s)", provider.Name(), s.mainModelFor(isCloud)),
				},
			},
		})
	}
```

- [ ] **Step 3: Emit the true route + drive the loop with the resolved provider**

Replace the `RouteSelected` block to reflect the resolved tier, and the `RunToolLoop` input + final metadata to use `provider`:

```go
	stream.Send(&proto.StreamProcessResponse{
		Payload: &proto.StreamProcessResponse_RouteSelected{
			RouteSelected: &proto.RouteSelected{
				Model:   s.mainModelFor(isCloud),
				IsCloud: isCloud,
			},
		},
	})

	result, err := agent.RunToolLoop(ctx, agent.ToolLoopInput{
		Provider:            provider,
		Registry:            s.toolRegistry,
		Permissions:         s.permStore,
		UserInput:           req.GetInput(),
		Model:               s.mainModelFor(isCloud),
		EventSink:           sink,
		PermissionRequester: requester,
	})
```

And the final-response metadata `ModelName: provider.Name(),` (replace `s.cloudLLMProvider.Name()`).

- [ ] **Step 4: Add the model-name helper**

Add to `server.go`:

```go
// mainModelFor returns the configured model name for the served tier.
func (s *Server) mainModelFor(isCloud bool) string {
	if isCloud {
		return s.currentConfig.CloudModel
	}
	return s.currentConfig.LocalModel
}
```

Also update `persistToolLoopTurns`’ `EnsureConversation` model arg if it referenced `s.currentConfig.CloudModel` — leave the persisted model as-is (cosmetic); not required for this task.

- [ ] **Step 5: Build**

Run: `cd source/server && go build ./... && go test ./internal/server/ -count=1`
Expected: BUILD OK + existing server tests pass.

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/server/server.go
git commit -m "feat(server): tool-loop honors locus mode + announces true route"
```

---

### Task 6: Runtime-error fallback for `*_primary` modes

**Files:**
- Modify: `source/server/internal/server/server.go` (`streamProcessRequestWithToolLoop`)

**Interfaces:**
- Consumes: `resolveMainProvider()`, `locus.ParseMode`.

- [ ] **Step 1: Add a fallback-on-error retry around `RunToolLoop`**

Replace the `if err != nil { return fmt.Errorf("tool loop error: %w", err) }` that follows `RunToolLoop` with a guarded retry: if the preferred (non-fallback) provider errored, the mode allows crossing, and a fallback provider is wired, re-run once on the fallback with a visible notice.

```go
	if err != nil {
		mode, _ := locus.ParseMode(s.currentConfig.LocusMode)
		res := mode.Main()
		fbProv := s.cloudLLMProvider
		fbCloud := true
		if res.Fallback == locus.TierLocal {
			fbProv, fbCloud = s.localLLMProvider, false
		}
		if !fellBack && res.CrossAllowed && fbProv != nil {
			stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_Progress{
					Progress: &proto.ProgressUpdate{
						Message: fmt.Sprintf("⚠ %s failed (%v) — retrying on %s", provider.Name(), err, fbProv.Name()),
					},
				},
			})
			stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_RouteSelected{
					RouteSelected: &proto.RouteSelected{Model: s.mainModelFor(fbCloud), IsCloud: fbCloud},
				},
			})
			provider = fbProv
			result, err = agent.RunToolLoop(ctx, agent.ToolLoopInput{
				Provider:            fbProv,
				Registry:            s.toolRegistry,
				Permissions:         s.permStore,
				UserInput:           req.GetInput(),
				Model:               s.mainModelFor(fbCloud),
				EventSink:           sink,
				PermissionRequester: requester,
			})
		}
		if err != nil {
			return fmt.Errorf("tool loop error: %w", err)
		}
	}
```

(`result` and `provider` are the variables declared earlier in the function; this reassigns them.)

- [ ] **Step 2: Build**

Run: `cd source/server && go build ./... && go test ./internal/server/ -count=1`
Expected: BUILD OK + pass.

- [ ] **Step 3: Manual smoke (local tool-loop)**

With Ollama running and `locus_mode: local_only`, run `go run ./cmd/cercano`, send "list the files here". Confirm: a `local` engine badge, the loop calls a tool, and no cloud is used. Then set `cloud_only` with no Anthropic config and confirm a clear "no cloud provider available" message instead of a silent local run. (If the Ollama `llm` client can't drive tools against the configured model, capture the error — this is the known risk that Local modes depend on local tool-calling.)

- [ ] **Step 4: Commit**

```bash
git add source/server/internal/server/server.go
git commit -m "feat(server): bidirectional tool-loop fallback for primary modes"
```

---

### Task 7: CLI — `/locus` command + config-editor row

**Files:**
- Create: `source/clients/cli/internal/slash/locus.go`
- Modify: `source/clients/cli/internal/ui/model.go` (register)
- Modify: `source/clients/cli/internal/ui/config_editor.go` (row + saveSingle)
- Test: `source/clients/cli/internal/slash/locus_test.go`

**Interfaces:**
- Consumes: `agentclient.Client.GetConfig/UpdateConfig`, `agentclient.ConfigUpdate.LocusMode`.

- [ ] **Step 1: Write a failing test for argument validation**

Create `locus_test.go`:

```go
package slash

import "testing"

func TestLocusValidatesMode(t *testing.T) {
	if validLocusMode("cloud_primary") != true {
		t.Error("cloud_primary should be valid")
	}
	if validLocusMode("nope") != false {
		t.Error("nope should be invalid")
	}
}
```

- [ ] **Step 2: Run test, verify it fails**

Run: `go test ./internal/slash/ -run Locus -count=1` (from `source/clients/cli`)
Expected: FAIL — `validLocusMode` undefined.

- [ ] **Step 3: Implement `/locus`**

Create `source/clients/cli/internal/slash/locus.go` (mirrors `RegisterConfig` style in `config.go`):

```go
package slash

import (
	"context"
	"strings"
	"time"

	"cercano/source/server/pkg/agentclient"
)

var locusModes = map[string]bool{
	"cloud_only": true, "cloud_primary": true, "local_primary": true, "local_only": true,
}

func validLocusMode(s string) bool { return locusModes[s] }

// RegisterLocus wires /locus: view current mode, or set it.
func RegisterLocus(r *Registry, c *agentclient.Client) {
	r.Register(Command{
		Name: "locus",
		Help: "View or set Locus Mode. Usage: /locus [cloud_only|cloud_primary|local_primary|local_only].",
		Handler: func(args []string) Result {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if len(args) == 0 {
				cfg, err := c.GetConfig(ctx)
				if err != nil {
					return Result{Kind: ResultText, Text: "locus: " + err.Error()}
				}
				mode := cfg.LocusMode
				if mode == "" {
					mode = "local_primary"
				}
				return Result{Kind: ResultText, Text: "locus mode: " + mode}
			}
			mode := strings.ToLower(args[0])
			if !validLocusMode(mode) {
				return Result{Kind: ResultText, Text: "invalid mode (want cloud_only|cloud_primary|local_primary|local_only)"}
			}
			msg, err := c.UpdateConfig(ctx, agentclient.ConfigUpdate{LocusMode: mode})
			if err != nil {
				return Result{Kind: ResultText, Text: "locus update failed: " + err.Error()}
			}
			return Result{Kind: ResultText, Text: msg}
		},
	})
}
```

- [ ] **Step 4: Register it**

In `source/clients/cli/internal/ui/model.go`, in the slash-registration block (after `slash.RegisterRuntime(reg)`):

```go
	slash.RegisterLocus(reg, ag)
```

- [ ] **Step 5: Add a config-editor row + save case**

In `config_editor.go`, add a row to the editable set (after `cloud-base-url`):

```go
		{Key: "locus-mode", Label: "locus-mode", Value: cfg.LocusMode, Editable: true, Hint: "cloud_only | cloud_primary | local_primary | local_only"},
```

In `saveSingle`'s switch, add:

```go
	case "locus-mode":
		u.LocusMode = value
```

- [ ] **Step 6: Run test + build**

Run (from `source/clients/cli`): `go test ./internal/slash/ -run Locus -count=1 && go build ./...`
Expected: PASS + BUILD OK.

- [ ] **Step 7: Commit**

```bash
git add source/clients/cli/internal/slash/locus.go source/clients/cli/internal/slash/locus_test.go source/clients/cli/internal/ui/model.go source/clients/cli/internal/ui/config_editor.go
git commit -m "feat(cli): /locus command + config-editor row"
```

---

### Task 8: VS Code — `cercano.locusMode` setting

**Files:**
- Modify: `source/clients/vscode/package.json`

- [ ] **Step 1: Add the setting**

In `contributes.configuration.properties`, add (after `cercano.provider`):

```json
        "cercano.locusMode": {
          "type": "string",
          "enum": ["cloud_only", "cloud_primary", "local_primary", "local_only"],
          "default": "local_primary",
          "description": "Locus Mode: controls whether the main LLM and co-processor tasks run on cloud or local models."
        },
```

- [ ] **Step 2: Validate JSON**

Run: `cd source/clients/vscode && node -e "JSON.parse(require('fs').readFileSync('package.json','utf8'))" && echo JSON_OK`
Expected: `JSON_OK`.

- [ ] **Step 3: Commit**

```bash
git add source/clients/vscode/package.json
git commit -m "feat(vscode): add cercano.locusMode setting"
```

---

## Self-Review

**Spec coverage (against `docs/agent/locus-mode.md`):**
- Four modes + default `local_primary` → Tasks 1, 2. ✓
- `locus.Policy` single source of truth → Task 1 (`locus` package) + Task 4 (`resolveMainProvider`). ✓
- Main-LLM tier honors locus → Tasks 4–5. ✓
- Bidirectional fallback for `*_primary`; hard-fail for `*_only` → Task 4 (availability) + Task 6 (runtime error). ✓
- No silent degradation (notice + `RouteSelected`) → Tasks 5–6. ✓
- Control surface: config / RPC / CLI `/locus` / VS Code / default → Tasks 2, 3, 7, 8. ✓
- **Deferred (documented, not in this plan):** co-processor tier honoring locus; SmartRouter shelving in the legacy path. These remain in the design doc as the full vision and need a follow-up plan.

**Placeholder scan:** No TBD/TODO. The one "read the interface and write the two methods" note (Task 4 Step 1 `stubProv`) is an explicit, bounded instruction with the exact method signatures given.

**Type consistency:** `Mode`, `Tier`, `Resolution{Preferred,Fallback,CrossAllowed}`, `Main()`, `ParseMode` are used identically across Tasks 1, 4, 6. `resolveMainProvider() (llm.Provider, bool, bool, error)` signature matches its test and call sites. `mainModelFor(isCloud bool)` consistent in Tasks 5–6. `ConfigUpdate.LocusMode` / `Config.LocusMode` consistent across Tasks 3 and 7.

**Known risk:** Local modes depend on the Ollama `internal/llm/ollama` client actually driving the native tool-loop (`SupportsTools: true`). Task 6 Step 3 verifies this manually; if it can't drive tools against the configured model, Local modes need that provider hardened (out of scope here — would be a fast-follow).
