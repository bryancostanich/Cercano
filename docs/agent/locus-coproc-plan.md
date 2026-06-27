# Locus Co-processor Tier — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Cercano's one-shot co-processor model calls (summarize, extract, classify, explain, document, research internals) follow Locus Mode instead of always running local, with the same fallback/hard-fail rules as the main tier and structured per-call routing metadata.

**Architecture:** Add `locus.Coproc()` (sibling of `Main()`). The **agent** resolves the co-proc tier on a new `Coproc` request flag, picks local/cloud from its existing provider map (`router.GetModelProviders()`), applies bidirectional fallback for `*_primary` / hard-fail for `*_only`, and reports the served tier+model via `RoutingMetadata` + `Notice`. MCP handlers swap `DirectLocal:true` → `Coproc:true` and pass that metadata into each tool's structured output.

**Tech Stack:** Go, gRPC (protoc-gen-go v1.36.11), the `internal/locus` policy, the `gomcp` MCP SDK.

## Global Constraints

- Module path `cercano/source/server`. Server Go commands run from `source/server/`.
- Mode values exactly `cloud_only|cloud_primary|local_primary|local_only`; default `local_primary`.
- Co-proc tier per mode: Cloud Only → cloud (hard-fail); Cloud Primary → local (→cloud); Local Primary → local (→cloud); Local Only → local (hard-fail). **`Coproc()` == `Main()` except Cloud Primary, which is local-preferred.**
- `*_only` modes never cross tiers — return an error (surfaced as an MCP tool error).
- No silent degradation: the served tier + model are reported in `RoutingMetadata`; a fallback also sets a human-readable `Notice`. The client decides how to surface it.
- The cloud co-proc provider is the agent's existing `CloudModel` provider; it is **absent** when its `Name() == "NONE"` (the `AbsentCloudProvider` sentinel).
- Commit messages must NOT contain the word "Claude".
- Build gate: `go build ./...`. Test gate: `go test ./<pkg>/ -count=1`.

---

### Task 1: `locus.Coproc()` resolution

**Files:**
- Modify: `source/server/internal/locus/locus.go`
- Test: `source/server/internal/locus/locus_test.go`

**Interfaces:**
- Consumes: existing `Mode`, `Tier`, `Resolution`.
- Produces: `func (m Mode) Coproc() Resolution`.

- [ ] **Step 1: Write failing test**

Append to `locus_test.go`:

```go
func TestCoprocResolution(t *testing.T) {
	cases := []struct {
		mode  Mode
		pref  Tier
		fall  Tier
		cross bool
	}{
		{CloudOnly, TierCloud, TierCloud, false},
		{CloudPrimary, TierLocal, TierCloud, true}, // differs from Main(): local-preferred
		{LocalPrimary, TierLocal, TierCloud, true},
		{LocalOnly, TierLocal, TierLocal, false},
	}
	for _, c := range cases {
		r := c.mode.Coproc()
		if r.Preferred != c.pref || r.Fallback != c.fall || r.CrossAllowed != c.cross {
			t.Errorf("%s.Coproc() = %+v; want pref=%v fall=%v cross=%v", c.mode, r, c.pref, c.fall, c.cross)
		}
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/locus/ -run Coproc -count=1`
Expected: FAIL — `Coproc` undefined.

- [ ] **Step 3: Implement**

In `locus.go`, after `Main()`:

```go
// Coproc resolves the tier policy for one-shot co-processor work (summarize,
// extract, classify, explain, …). Identical to Main except Cloud Primary, which
// keeps grunt work local while the main LLM runs on cloud.
func (m Mode) Coproc() Resolution {
	if m == CloudPrimary {
		return Resolution{TierLocal, TierCloud, true}
	}
	return m.Main()
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/locus/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/locus/
git commit -m "feat(locus): Coproc tier resolution"
```

---

### Task 2: Proto + request/response plumbing for the co-proc signal

**Files:**
- Modify: `source/proto/agent.proto`
- Regenerate: `source/server/pkg/proto/agent.pb.go`, `agent_grpc.pb.go`
- Modify: `source/server/internal/agent/router.go` (`Request`, `RoutingMetadata`)
- Modify: `source/server/internal/server/server.go` (`mapRequest`, `mapResponse`)

**Interfaces:**
- Produces: `ProcessRequestRequest.Coproc` (proto, field 8), `RoutingMetadata.IsCloud` (proto field 6 + Go field), `agent.Request.Coproc bool`, `agent.RoutingMetadata.IsCloud bool`.

- [ ] **Step 1: Edit proto**

In `agent.proto`, in `message ProcessRequestRequest`, after `string model_override = 7;`:

```proto
  bool coproc = 8; // Route per Locus Mode's co-processor tier (local/cloud)
```

In `message RoutingMetadata`, after `bool is_fallback = 5;`:

```proto
  bool is_cloud = 6; // Locus: the request was served by the cloud tier
```

- [ ] **Step 2: Regenerate**

From repo root, plugins installed (`go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11`, `…/grpc/cmd/protoc-gen-go-grpc@latest`, `export PATH="$PATH:$(go env GOPATH)/bin"`):

```bash
protoc --proto_path=. \
  --go_out=source/server --go_opt=module=cercano/source/server \
  --go-grpc_out=source/server --go-grpc_opt=module=cercano/source/server \
  source/proto/agent.proto
```

Verify: `grep -c 'GetCoproc\|GetIsCloud' source/server/pkg/proto/agent.pb.go` ≥ 2.

- [ ] **Step 3: Add Go struct fields**

In `router.go` `Request` struct, after `ModelOverride string`:

```go
	Coproc         bool   // Route per Locus Mode's co-processor tier (local/cloud)
```

In `router.go` `RoutingMetadata` struct, after `Escalated bool`:

```go
	IsCloud    bool
```

- [ ] **Step 4: Plumb through mapping**

In `server.go` `mapRequest`, add to the returned `&agent.Request{...}`:

```go
		Coproc:         req.Coproc,
```

In `server.go` `mapResponse`, add to the `rm := &proto.RoutingMetadata{...}` literal:

```go
		IsCloud:    response.RoutingMetadata.IsCloud,
```

- [ ] **Step 5: Build**

Run: `cd source/server && go build ./...`
Expected: PASS (fields unused so far — Task 4 uses them).

- [ ] **Step 6: Commit**

```bash
git add source/proto/agent.proto source/server/pkg/proto/ source/server/internal/agent/router.go source/server/internal/server/server.go
git commit -m "feat(proto,agent): coproc request flag + is_cloud routing metadata"
```

---

### Task 3: Give the agent the live Locus mode

**Files:**
- Modify: `source/server/internal/agent/agent.go` (field, setter, getter)
- Modify: `source/server/internal/server/server.go` (`LocusMode()` accessor)
- Modify: `source/server/cmd/cercano/main.go` (wire the getter)

**Interfaces:**
- Produces: `agent.Agent.SetLocusModeGetter(func() string)`, `agent.Agent.currentLocusMode() string`, `server.Server.LocusMode() string`.

- [ ] **Step 1: Add the agent field + setter + getter**

In `agent.go`, add to the `Agent` struct (after `recap RecapScheduler`):

```go
	locusMode func() string // live getter for the configured Locus Mode
```

Add near the other `With*`/`Set*` helpers:

```go
// SetLocusModeGetter wires a live getter for the active Locus Mode so co-proc
// routing reflects runtime UpdateConfig changes. Nil getter → DefaultMode.
func (a *Agent) SetLocusModeGetter(f func() string) { a.locusMode = f }

func (a *Agent) currentLocusMode() string {
	if a.locusMode == nil {
		return string(locus.DefaultMode)
	}
	return a.locusMode()
}
```

Add the import `"cercano/source/server/internal/locus"` to `agent.go`.

- [ ] **Step 2: Add the server accessor**

In `server.go`, near `SetConfigPersistence`:

```go
// LocusMode returns the currently configured Locus Mode (live; reflects
// UpdateConfig). Used by the agent for co-processor tier resolution.
func (s *Server) LocusMode() string { return s.currentConfig.LocusMode }
```

- [ ] **Step 3: Wire it in main.go**

In `cmd/cercano/main.go`, after `srv := server.NewServer(...)` and `srv.SetConfigPersistence(...)` are both called, add:

```go
	orchestrator.SetLocusModeGetter(srv.LocusMode)
```

(`orchestrator` is the `*agent.Agent`; `srv` is the `*server.Server`. Find their exact variable names in this file — earlier they were `orchestrator` and `srv`.)

- [ ] **Step 4: Build**

Run: `cd source/server && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/agent/agent.go source/server/internal/server/server.go source/server/cmd/cercano/main.go
git commit -m "feat(agent): live locus-mode getter for co-proc routing"
```

---

### Task 4: Co-processor resolution branch in the agent

**Files:**
- Modify: `source/server/internal/agent/agent.go` (`ProcessRequest` + new `processCoproc`)
- Test: `source/server/internal/agent/coproc_test.go` (create)

**Interfaces:**
- Consumes: `Request.Coproc`, `currentLocusMode()`, `locus.Coproc()`, `router.GetModelProviders()`, `ModelProvider.Process`/`Name`.
- Produces: co-proc routing behavior; `RoutingMetadata.IsCloud` + `Notice` set.

- [ ] **Step 1: Write failing test**

Create `coproc_test.go`:

```go
package agent

import (
	"context"
	"testing"
)

// fakeProvider is a minimal ModelProvider for routing tests.
type fakeProvider struct {
	name string
	out  string
}

func (f *fakeProvider) Process(ctx context.Context, req *Request) (*Response, error) {
	return &Response{Output: f.out, InputTokens: 1, OutputTokens: 1}, nil
}
func (f *fakeProvider) ProcessStream(ctx context.Context, req *Request, onToken TokenFunc) (*Response, error) {
	return f.Process(ctx, req)
}
func (f *fakeProvider) Name() string { return f.name }

// fakeRouter returns a fixed provider map.
type fakeCoprocRouter struct{ providers map[string]ModelProvider }

func (r *fakeCoprocRouter) ClassifyIntent(req *Request) (Intent, error)        { return IntentChat, nil }
func (r *fakeCoprocRouter) SelectProvider(req *Request, i Intent) (ModelProvider, error) {
	return r.providers["LocalModel"], nil
}
func (r *fakeCoprocRouter) GetModelProviders() map[string]ModelProvider { return r.providers }

func newCoprocAgent(mode, localName, cloudName string) *Agent {
	provs := map[string]ModelProvider{"LocalModel": &fakeProvider{name: localName, out: "local-out"}}
	provs["CloudModel"] = &fakeProvider{name: cloudName, out: "cloud-out"}
	a := NewAgent(&fakeCoprocRouter{providers: provs}, nil)
	a.SetLocusModeGetter(func() string { return mode })
	return a
}

func TestCoprocRoutesPerMode(t *testing.T) {
	ctx := context.Background()

	// local_primary → local
	if r, err := newCoprocAgent("local_primary", "ollama", "anthropic").
		ProcessRequest(ctx, &Request{Input: "x", Coproc: true}); err != nil || r.RoutingMetadata.ModelName != "ollama" || r.RoutingMetadata.IsCloud {
		t.Errorf("local_primary coproc: %+v err=%v", r.RoutingMetadata, err)
	}
	// cloud_only → cloud
	if r, err := newCoprocAgent("cloud_only", "ollama", "anthropic").
		ProcessRequest(ctx, &Request{Input: "x", Coproc: true}); err != nil || r.RoutingMetadata.ModelName != "anthropic" || !r.RoutingMetadata.IsCloud {
		t.Errorf("cloud_only coproc: %+v err=%v", r.RoutingMetadata, err)
	}
}

func TestCoprocCloudOnlyHardFailsWhenAbsent(t *testing.T) {
	// CloudModel is the absent sentinel (Name "NONE").
	a := newCoprocAgent("cloud_only", "ollama", "NONE")
	if _, err := a.ProcessRequest(context.Background(), &Request{Input: "x", Coproc: true}); err == nil {
		t.Error("cloud_only with absent cloud should error, not fall back to local")
	}
}

func TestCoprocCloudPrimaryFallsBackToLocal(t *testing.T) {
	// Cloud Primary co-proc is local-preferred; even with no cloud it runs local.
	a := newCoprocAgent("cloud_primary", "ollama", "NONE")
	r, err := a.ProcessRequest(context.Background(), &Request{Input: "x", Coproc: true})
	if err != nil || r.RoutingMetadata.ModelName != "ollama" || r.RoutingMetadata.IsCloud {
		t.Errorf("cloud_primary coproc: %+v err=%v", r.RoutingMetadata, err)
	}
}
```

NOTE: confirm the `Router` interface method set and `TokenFunc` name by reading `router.go` (the fake must satisfy `Router`; `ModelProvider` requires `Process`, `ProcessStream`, `Name` — match the real interface exactly). Adjust the fakes' method signatures to the real ones before running.

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/agent/ -run Coproc -count=1`
Expected: FAIL — co-proc branch not implemented (cloud_only routes local / no IsCloud).

- [ ] **Step 3: Implement the branch**

In `agent.go` `ProcessRequest`, at the very top of the function body (before the `DirectLocal` branch), add:

```go
	if req.Coproc {
		return a.processCoproc(ctx, req)
	}
```

Add the method (next to `ProcessRequest`):

```go
// processCoproc serves a one-shot co-processor request on the tier chosen by
// the active Locus Mode. Bidirectional fallback for *_primary; hard error for
// *_only when its tier is unavailable. Sets IsCloud + a Notice on fallback.
func (a *Agent) processCoproc(ctx context.Context, req *Request) (*Response, error) {
	augmentedInput, originalInput := a.loadHistory(ctx, req)

	mode, _ := locus.ParseMode(a.currentLocusMode())
	res := mode.Coproc()
	providers := a.router.GetModelProviders()

	pick := func(t locus.Tier) ModelProvider {
		if t == locus.TierCloud {
			if cp := providers["CloudModel"]; cp != nil && cp.Name() != "NONE" {
				return cp
			}
			return nil
		}
		if lp := providers["LocalModel"]; lp != nil {
			return lp
		}
		return nil
	}

	prov := pick(res.Preferred)
	isCloud := res.Preferred == locus.TierCloud
	fellBack := false
	if prov == nil && res.CrossAllowed {
		prov = pick(res.Fallback)
		isCloud = res.Fallback == locus.TierCloud
		fellBack = true
	}
	if prov == nil {
		return nil, fmt.Errorf("locus mode %q: no %s provider available for co-processor work", mode, res.Preferred)
	}

	out, err := prov.Process(ctx, &Request{Input: augmentedInput, ModelOverride: req.ModelOverride})
	if err != nil {
		return nil, err
	}
	modelName := prov.Name()
	if req.ModelOverride != "" {
		modelName = req.ModelOverride
	}
	out.RoutingMetadata = RoutingMetadata{ModelName: modelName, Confidence: 1.0, IsCloud: isCloud}
	if fellBack {
		out.Notice = fmt.Sprintf("locus: preferred co-processor tier unavailable — ran on %s (%s)", res.Fallback, modelName)
	}
	a.storeConversationTurn(ctx, req.ConversationID, originalInput, augmentedInput, out)
	return out, nil
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/agent/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/agent/
git commit -m "feat(agent): co-processor tier routing per locus mode"
```

---

### Task 5: MCP co-processor handlers honor locus + emit structured metadata

**Files:**
- Modify: `source/server/internal/mcp/server.go`
- Test: `source/server/internal/mcp/coproc_meta_test.go` (create)

**Interfaces:**
- Consumes: `proto.ProcessRequestResponse.RoutingMetadata` (`GetIsCloud`, `GetModelName`), `.GetNotice()`.
- Produces: `CoprocMeta` struct + `coprocMeta(resp)` helper; co-proc tools route via `Coproc:true` and return `CoprocMeta` as their structured output.

> **Note:** this file is under active edit. Do NOT trust line numbers — `grep -n 'DirectLocal: true' source/server/internal/mcp/server.go` to find the current co-proc call sites.

- [ ] **Step 1: Write failing test for the helper**

Create `coproc_meta_test.go`:

```go
package mcp

import (
	"testing"

	"cercano/source/server/pkg/proto"
)

func TestCoprocMeta(t *testing.T) {
	resp := &proto.ProcessRequestResponse{
		RoutingMetadata: &proto.RoutingMetadata{ModelName: "anthropic", IsCloud: true},
		Notice:          "ran on cloud",
	}
	m := coprocMeta(resp)
	if m.Model != "anthropic" || m.Tier != "cloud" || m.Notice != "ran on cloud" {
		t.Errorf("coprocMeta = %+v", m)
	}
	local := coprocMeta(&proto.ProcessRequestResponse{
		RoutingMetadata: &proto.RoutingMetadata{ModelName: "ollama", IsCloud: false},
	})
	if local.Tier != "local" || local.Model != "ollama" {
		t.Errorf("coprocMeta local = %+v", local)
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/mcp/ -run CoprocMeta -count=1`
Expected: FAIL — `coprocMeta`/`CoprocMeta` undefined.

- [ ] **Step 3: Add the struct + helper**

In `server.go` (mcp package), add:

```go
// CoprocMeta is the structured routing metadata returned alongside a
// co-processor tool's text result. Clients (CLI, host agents) decide how to
// surface it; Tier is "local" or "cloud".
type CoprocMeta struct {
	Model  string `json:"model"`
	Tier   string `json:"tier"`
	Notice string `json:"notice,omitempty"`
}

func coprocMeta(resp *proto.ProcessRequestResponse) CoprocMeta {
	tier := "local"
	if resp.GetRoutingMetadata().GetIsCloud() {
		tier = "cloud"
	}
	return CoprocMeta{
		Model:  resp.GetRoutingMetadata().GetModelName(),
		Tier:   tier,
		Notice: resp.GetNotice(),
	}
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/mcp/ -run CoprocMeta -count=1`
Expected: PASS.

- [ ] **Step 5: Swap the co-proc call sites + return the metadata**

For each co-processor handler — `handleSummarize`, `handleExtract`, `handleClassify`, `handleExplain`, and `handleDocument` if present — make two edits:

1. In the `s.grpcClient.ProcessRequest(ctx, &proto.ProcessRequestRequest{...})` call, replace `DirectLocal: true,` with `Coproc: true,`.
2. Change the handler's return so the structured-output (`any`) value carries the metadata. Where it currently returns e.g. `return s.maybeNudge(args.ProjectDir, result), nil, nil`, change the middle value to `coprocMeta(resp)`:

```go
	return s.maybeNudge(args.ProjectDir, result), coprocMeta(resp), nil
```

(Handlers that return `return result, nil, nil` become `return result, coprocMeta(resp), nil`.) Leave any handler whose semantics are explicitly "force local" (e.g. `cercano_local`, the named local tool) on `DirectLocal: true` — see Open items; default is to NOT change `cercano_local`.

- [ ] **Step 6: Build + full mcp tests**

Run: `cd source/server && go build ./... && go test ./internal/mcp/ -count=1`
Expected: PASS (existing mcp tests must still pass; if a test asserted a tool's structured output was nil, update it to expect `CoprocMeta`).

- [ ] **Step 7: Commit**

```bash
git add source/server/internal/mcp/server.go source/server/internal/mcp/coproc_meta_test.go
git commit -m "feat(mcp): co-proc tools honor locus + return routing metadata"
```

---

### Task 6: Research/deep-research internals + docs

**Files:**
- Modify: `source/server/internal/mcp/server.go` (the `grpcModelCaller` / research internal `ProcessRequest` calls)
- Modify: `docs/agent/locus-coproc.md` (resolve the two open items)

**Interfaces:**
- Consumes: `Request.Coproc`.

- [ ] **Step 1: Route the research internal model calls per locus**

`grep -n 'DirectLocal: true' source/server/internal/mcp/server.go` for the remaining sites inside the research / deep-research paths (the `grpcModelCaller` `Process`-style calls, ~lines 985/1007/1131/1243 historically). For each that is a co-processor model call, replace `DirectLocal: true,` with `Coproc: true,`. Where a call also sets `ModelOverride: g.modelOverride` (the `use_model` override), **keep it** — `model_override` still applies on top of the locus-resolved tier.

- [ ] **Step 2: Build + test**

Run: `cd source/server && go build ./... && go test ./internal/mcp/ -count=1`
Expected: PASS.

- [ ] **Step 3: Resolve the doc open items**

In `docs/agent/locus-coproc.md`, under "Open items / risks", convert the two bullets to decisions:
- **`use_model` precedence:** an explicit `use_model` sets the model name but the request still runs on the locus-resolved tier (`model_override` + `coproc` both set).
- **Cost under Cloud Only:** note that `deep_research` fans many internal model calls; under Cloud Only they all run on cloud — intended.
- **`cercano_local`:** stays explicitly local (the named force-local tool / escape hatch); it does not follow locus.

- [ ] **Step 4: Commit**

```bash
git add source/server/internal/mcp/server.go docs/agent/locus-coproc.md
git commit -m "feat(mcp): research internals honor locus; finalize coproc decisions"
```

---

## Self-Review

**Spec coverage (against `docs/agent/locus-coproc.md`):**
- All one-shot model calls honor locus → Tasks 5 (core tools) + 6 (research internals). `cercano_local` kept local by decision (Task 6 doc). ✓
- `locus.Coproc()` table → Task 1. ✓
- Agent-side resolution + fallback + hard-fail → Task 4. ✓
- Structured metadata (tier, model, notice), client decides surfacing → Tasks 2 (is_cloud), 4 (sets it), 5 (`CoprocMeta` in structured output). ✓
- Cloud served by existing `CloudModel` provider one-shot → Task 4. ✓
- Live mode (runtime UpdateConfig) → Task 3. ✓
- SmartRouter retired from co-proc path → Task 4 (the `Coproc` branch bypasses `ClassifyIntent`/`SelectProvider` entirely; `DirectLocal` path unchanged for explicit-local callers). ✓

**Placeholder scan:** No TBD/TODO. Two "read the real interface and match signatures" notes (Task 4 fakes, Task 5 grep for call sites) are explicit, bounded instructions — necessary because the `Router`/`ModelProvider` interfaces and the actively-edited `mcp/server.go` must be matched against live code.

**Type consistency:** `Coproc()`, `Resolution{Preferred,Fallback,CrossAllowed}`, `Tier` consistent across Tasks 1/4. `Request.Coproc`, `RoutingMetadata.IsCloud` defined in Task 2, used in Task 4, surfaced in Task 5. `CoprocMeta{Model,Tier,Notice}` + `coprocMeta(resp)` consistent in Task 5. `currentLocusMode()`/`SetLocusModeGetter`/`LocusMode()` consistent across Task 3 → 4.

**Risk:** `mcp/server.go` is under active edit by the user; Tasks 5–6 deliberately use grep-for-call-sites instead of line numbers. If the co-proc handler return shape has changed (e.g. already returns structured output), merge `coprocMeta(resp)` into the existing structured value rather than replacing it.
