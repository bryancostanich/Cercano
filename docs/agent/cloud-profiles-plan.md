# Multi-Cloud Provider Profiles — Implementation Plan (foundation)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store multiple named cloud provider profiles (metadata in `config.yaml`, secrets in the OS keychain), select an active one at runtime, and drive **one** cloud system — both the native tool-loop and the co-processor `CloudModel` — from the active profile via a provider factory, retiring the langchaingo cloud path.

**Architecture:** `CloudProfile{Name,Flavor,BaseURL,Model}` in config; an `internal/secrets` keychain wrapper (`99designs/keyring`); an `internal/cloudfactory.BuildCloudProvider` mapping a profile (+key) to an `llm.Provider` (only `messages`→anthropic today); an `agent` adapter wrapping any `llm.Provider` as an `agent.ModelProvider`. Startup + runtime "set active" wires the active profile's `llm.Provider` into `SetCloudLLMProvider` and (adapter-wrapped) into the router's `CloudModel`.

**Tech Stack:** Go, gRPC (protoc-gen-go v1.36.11), `github.com/99designs/keyring`, the `internal/llm` provider interface.

## Global Constraints

- Module path `cercano/source/server`; server Go commands from `source/server/`.
- `CloudProfile` fields: `Name, Flavor, BaseURL, Model` (yaml `name|flavor|base_url|model`). **No key field** — keys live in the keychain under `service="cercano", account=<profile-name>`.
- Flavor values: `messages` (Anthropic, the only one that resolves now); `chat_completions`, `responses`, `bedrock` are reserved and must error as `flavor %q not yet supported`.
- Default active flavor inference for migration: `cloud_provider=="anthropic"` → `messages`; anything else → `""` (metadata-only).
- One cloud system: the active profile feeds both `SetCloudLLMProvider` (native) and the router/coordinator `CloudModel` (via the adapter). Langchaingo cloud construction is removed.
- Keychain-only for now; on keychain failure, behave like today's absent-cloud sentinel (agent runs, cloud absent) — do not crash.
- Commit messages must NOT contain the word "Claude".
- Build gate `go build ./...`; test gate `go test ./<pkg>/ -count=1`.

---

### Task 1: Config — `CloudProfile` model, fields, migration

**Files:**
- Modify: `source/server/pkg/config/config.go`
- Test: `source/server/pkg/config/config_test.go`

**Interfaces:**
- Produces: `config.CloudProfile{Name,Flavor,BaseURL,Model string}`, `Config.CloudProfiles []CloudProfile`, `Config.ActiveCloudProfile string`, and a migration in `Load` that synthesizes a `default` profile from legacy `cloud_*` fields.

- [ ] **Step 1: Write failing test**

Append to `config_test.go`:

```go
func TestMigrateLegacyCloudToProfile(t *testing.T) {
	// A config with only the legacy single-cloud fields set migrates to one
	// "default" profile + active selection on Load. We exercise the helper
	// directly to avoid file IO.
	cfg := Config{CloudProvider: "anthropic", CloudModel: "claude-sonnet-4-6", CloudBaseURL: "http://x"}
	migrateCloudProfiles(&cfg)
	if len(cfg.CloudProfiles) != 1 || cfg.ActiveCloudProfile != "default" {
		t.Fatalf("profiles=%+v active=%q", cfg.CloudProfiles, cfg.ActiveCloudProfile)
	}
	p := cfg.CloudProfiles[0]
	if p.Name != "default" || p.Flavor != "messages" || p.Model != "claude-sonnet-4-6" || p.BaseURL != "http://x" {
		t.Errorf("profile = %+v", p)
	}
}

func TestMigrateNoLegacyNoProfiles(t *testing.T) {
	cfg := Config{} // nothing set
	migrateCloudProfiles(&cfg)
	if len(cfg.CloudProfiles) != 0 || cfg.ActiveCloudProfile != "" {
		t.Errorf("expected no migration, got %+v / %q", cfg.CloudProfiles, cfg.ActiveCloudProfile)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	cfg := Config{CloudProvider: "anthropic", CloudProfiles: []CloudProfile{{Name: "x", Flavor: "messages"}}}
	migrateCloudProfiles(&cfg)
	if len(cfg.CloudProfiles) != 1 || cfg.CloudProfiles[0].Name != "x" {
		t.Errorf("should not overwrite existing profiles: %+v", cfg.CloudProfiles)
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./pkg/config/ -run Migrate -count=1`
Expected: FAIL — `CloudProfile`/`migrateCloudProfiles` undefined.

- [ ] **Step 3: Add the struct + fields**

In `config.go`, add the struct (near `Config`):

```go
// CloudProfile is one named cloud provider configuration. The API key is NOT
// stored here — it lives in the OS keychain keyed by Name.
type CloudProfile struct {
	Name    string `yaml:"name"`
	Flavor  string `yaml:"flavor"` // messages | chat_completions | responses | bedrock
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
}
```

Add to the `Config` struct (after `CloudBaseURL`):

```go
	CloudProfiles      []CloudProfile `yaml:"cloud_profiles"`
	ActiveCloudProfile string         `yaml:"active_cloud_profile"`
```

- [ ] **Step 4: Add the migration helper + call it from `Load`**

Add:

```go
// migrateCloudProfiles synthesizes a "default" profile from the legacy
// single-cloud fields when no profiles exist yet. Metadata only — the inline
// cloud_api_key is relocated to the keychain by the startup wiring, not here
// (config has no keychain dependency). No-op if profiles already exist or no
// legacy cloud is configured.
func migrateCloudProfiles(cfg *Config) {
	if len(cfg.CloudProfiles) > 0 || cfg.CloudProvider == "" {
		return
	}
	flavor := ""
	if cfg.CloudProvider == "anthropic" {
		flavor = "messages"
	}
	cfg.CloudProfiles = []CloudProfile{{
		Name:    "default",
		Flavor:  flavor,
		BaseURL: cfg.CloudBaseURL,
		Model:   cfg.CloudModel,
	}}
	cfg.ActiveCloudProfile = "default"
}
```

In `Load`, call `migrateCloudProfiles(&cfg)` after the file + env merge, just before `return cfg, nil`. (Read `Load` to place it after the existing field population.)

- [ ] **Step 5: Run, verify pass; build**

Run: `go test ./pkg/config/ -count=1 && go build ./...`
Expected: PASS + build OK.

- [ ] **Step 6: Commit**

```bash
git add source/server/pkg/config/
git commit -m "feat(config): cloud profiles model + legacy migration"
```

---

### Task 2: `internal/secrets` — keychain wrapper

**Files:**
- Create: `source/server/internal/secrets/secrets.go`
- Test: `source/server/internal/secrets/secrets_test.go`
- Modify: `source/server/go.mod` / `go.sum` (add `github.com/99designs/keyring`)

**Interfaces:**
- Produces: `secrets.Store` interface (`Get(profile)(string,error)`, `Set(profile,key string) error`, `Delete(profile string) error`), `secrets.OpenKeychain() (Store, error)`, and `secrets.NewMemory()` for tests.

- [ ] **Step 1: Add the dependency**

Run from `source/server/`:
```bash
go get github.com/99designs/keyring@latest
```

- [ ] **Step 2: Write failing test**

Create `secrets_test.go`:

```go
package secrets

import "testing"

func TestMemoryStoreRoundTrip(t *testing.T) {
	s := NewMemory()
	if err := s.Set("openai", "sk-123"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("openai")
	if err != nil || got != "sk-123" {
		t.Fatalf("get = %q, %v", got, err)
	}
	if err := s.Delete("openai"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("openai"); err == nil {
		t.Error("expected error after delete")
	}
}
```

- [ ] **Step 3: Run, verify fail**

Run: `go test ./internal/secrets/ -count=1`
Expected: FAIL — package/`NewMemory` undefined.

- [ ] **Step 4: Implement**

Create `secrets.go`:

```go
// Package secrets stores cloud provider API keys in the OS keychain (macOS
// Keychain, Windows Credential Manager, Linux Secret Service) via 99designs/
// keyring, keyed by profile name under a single service. A headless/Docker
// fallback is deferred; OpenKeychain returns an error there and callers treat
// cloud as absent.
package secrets

import (
	"fmt"
	"sync"

	"github.com/99designs/keyring"
)

const service = "cercano"

// Store reads/writes a profile's API key.
type Store interface {
	Get(profile string) (string, error)
	Set(profile, key string) error
	Delete(profile string) error
}

type keyringStore struct{ kr keyring.Keyring }

// OpenKeychain opens the OS keychain backend. Errors on headless systems with
// no secret service (deferred fallback).
func OpenKeychain() (Store, error) {
	kr, err := keyring.Open(keyring.Config{
		ServiceName:              service,
		KeychainTrustApplication: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open keychain: %w", err)
	}
	return &keyringStore{kr: kr}, nil
}

func (s *keyringStore) Get(profile string) (string, error) {
	item, err := s.kr.Get(profile)
	if err != nil {
		return "", err
	}
	return string(item.Data), nil
}

func (s *keyringStore) Set(profile, key string) error {
	return s.kr.Set(keyring.Item{Key: profile, Data: []byte(key)})
}

func (s *keyringStore) Delete(profile string) error { return s.kr.Remove(profile) }

// memoryStore is an in-process Store for tests.
type memoryStore struct {
	mu sync.Mutex
	m  map[string]string
}

func NewMemory() Store { return &memoryStore{m: map[string]string{}} }

func (s *memoryStore) Get(profile string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[profile]
	if !ok {
		return "", fmt.Errorf("no secret for %q", profile)
	}
	return v, nil
}
func (s *memoryStore) Set(profile, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[profile] = key
	return nil
}
func (s *memoryStore) Delete(profile string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, profile)
	return nil
}
```

- [ ] **Step 5: Run, verify pass; build**

Run: `go test ./internal/secrets/ -count=1 && go build ./...`
Expected: PASS + build OK. (If `keyring.Config` field names differ in the resolved version, read the package's `Config`/`Item` types and adjust — the fields used are `ServiceName`, `KeychainTrustApplication`, `Item{Key,Data}`.)

- [ ] **Step 6: Commit**

```bash
git add source/server/internal/secrets/ source/server/go.mod source/server/go.sum
git commit -m "feat(secrets): OS keychain store for profile API keys"
```

---

### Task 3: `internal/cloudfactory` — profile → `llm.Provider`

**Files:**
- Create: `source/server/internal/cloudfactory/factory.go`
- Test: `source/server/internal/cloudfactory/factory_test.go`

**Interfaces:**
- Consumes: `config.CloudProfile`, `llm.Provider`, `anthropic.NewClient`.
- Produces: flavor consts (`FlavorMessages = "messages"`, …) and `func BuildCloudProvider(p config.CloudProfile, apiKey string) (llm.Provider, error)`.

- [ ] **Step 1: Write failing test**

Create `factory_test.go`:

```go
package cloudfactory

import (
	"testing"

	"cercano/source/server/pkg/config"
)

func TestBuildMessagesProvider(t *testing.T) {
	p, err := BuildCloudProvider(config.CloudProfile{Name: "c", Flavor: "messages", Model: "claude-x"}, "sk")
	if err != nil || p == nil || p.Name() != "anthropic" {
		t.Fatalf("messages → %v, %v", p, err)
	}
}

func TestBuildUnsupportedFlavor(t *testing.T) {
	if _, err := BuildCloudProvider(config.CloudProfile{Name: "c", Flavor: "chat_completions"}, "sk"); err == nil {
		t.Error("chat_completions should be unsupported in the foundation")
	}
	if _, err := BuildCloudProvider(config.CloudProfile{Name: "c", Flavor: ""}, "sk"); err == nil {
		t.Error("empty flavor should error")
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/cloudfactory/ -count=1`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Create `factory.go`:

```go
// Package cloudfactory builds an llm.Provider from a cloud profile. It is the
// single extension point for new wire-format flavors: each later sub-project
// adds one case.
package cloudfactory

import (
	"fmt"

	"cercano/source/server/internal/llm"
	"cercano/source/server/internal/llm/anthropic"
	"cercano/source/server/pkg/config"
)

const (
	FlavorMessages        = "messages"
	FlavorChatCompletions = "chat_completions"
	FlavorResponses       = "responses"
	FlavorBedrock         = "bedrock"
)

// BuildCloudProvider maps a profile (+ its key) to an llm.Provider. Only the
// messages (Anthropic) flavor is implemented in the foundation.
func BuildCloudProvider(p config.CloudProfile, apiKey string) (llm.Provider, error) {
	switch p.Flavor {
	case FlavorMessages:
		return anthropic.NewClient(anthropic.Config{
			BaseURL: p.BaseURL,
			APIKey:  apiKey,
			Model:   p.Model,
		}), nil
	default:
		return nil, fmt.Errorf("flavor %q not yet supported", p.Flavor)
	}
}
```

- [ ] **Step 4: Run, verify pass; build**

Run: `go test ./internal/cloudfactory/ -count=1 && go build ./...`
Expected: PASS + build OK.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/cloudfactory/
git commit -m "feat(cloudfactory): profile → llm.Provider (messages)"
```

---

### Task 4: `agent` adapter — `llm.Provider` as `ModelProvider`

**Files:**
- Create: `source/server/internal/agent/llm_adapter.go`
- Test: `source/server/internal/agent/llm_adapter_test.go`

**Interfaces:**
- Consumes: `llm.Provider`, `llm.ChatRequest`, `llm.Message`, `llm.Block`, agent `Request`/`Response`/`ModelProvider`.
- Produces: `func NewLLMModelProvider(p llm.Provider, model string) ModelProvider`.

- [ ] **Step 1: Write failing test**

Create `llm_adapter_test.go`:

```go
package agent

import (
	"context"
	"testing"

	"cercano/source/server/internal/llm"
)

type fakeLLM struct{ name string }

func (f *fakeLLM) Name() string                  { return f.name }
func (f *fakeLLM) Capabilities() llm.Capabilities { return llm.Capabilities{} }
func (f *fakeLLM) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{
		Blocks:      []llm.Block{{Type: llm.BlockText, Text: "echo:" + req.Messages[0].Blocks[0].Text}},
		InputTokens: 3, OutputTokens: 4,
	}, nil
}
func (f *fakeLLM) StreamChat(ctx context.Context, req llm.ChatRequest) (llm.StreamReader, error) {
	panic("unused")
}

func TestLLMModelProviderProcess(t *testing.T) {
	mp := NewLLMModelProvider(&fakeLLM{name: "openai"}, "gpt-5")
	if mp.Name() != "openai" {
		t.Errorf("name = %q", mp.Name())
	}
	resp, err := mp.Process(context.Background(), &Request{Input: "hi"})
	if err != nil || resp.Output != "echo:hi" || resp.OutputTokens != 4 {
		t.Fatalf("resp = %+v err=%v", resp, err)
	}
}
```

(Confirm `llm.StreamReader`'s exact name from `internal/llm/stream.go`; match the signature.)

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/agent/ -run LLMModelProvider -count=1`
Expected: FAIL — `NewLLMModelProvider` undefined.

- [ ] **Step 3: Implement**

Create `llm_adapter.go`:

```go
package agent

import (
	"context"
	"strings"

	"cercano/source/server/internal/llm"
)

// llmModelProvider adapts an llm.Provider (native tool-calling interface) to the
// legacy agent.ModelProvider interface, so a single cloud profile can serve both
// the tool-loop and the co-processor CloudModel. Process runs a one-shot Chat
// (no tools) and concatenates text blocks.
type llmModelProvider struct {
	p     llm.Provider
	model string
}

// NewLLMModelProvider wraps an llm.Provider as a ModelProvider.
func NewLLMModelProvider(p llm.Provider, model string) ModelProvider {
	return &llmModelProvider{p: p, model: model}
}

func (a *llmModelProvider) Name() string { return a.p.Name() }

func (a *llmModelProvider) Process(ctx context.Context, req *Request) (*Response, error) {
	model := a.model
	if req.ModelOverride != "" {
		model = req.ModelOverride
	}
	resp, err := a.p.Chat(ctx, llm.ChatRequest{
		Model:    model,
		Messages: []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockText, Text: req.Input}}}},
	})
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	for _, b := range resp.Blocks {
		if b.Type == llm.BlockText {
			sb.WriteString(b.Text)
		}
	}
	return &Response{
		Output:          sb.String(),
		InputTokens:     resp.InputTokens,
		OutputTokens:    resp.OutputTokens,
		RoutingMetadata: RoutingMetadata{ModelName: a.p.Name()},
	}, nil
}
```

- [ ] **Step 4: Run, verify pass; build**

Run: `go test ./internal/agent/ -count=1 && go build ./...`
Expected: PASS + build OK.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/agent/llm_adapter.go source/server/internal/agent/llm_adapter_test.go
git commit -m "feat(agent): adapt llm.Provider as ModelProvider for co-proc cloud"
```

---

### Task 5: Proto + RPCs + agentclient — profile management

**Files:**
- Modify: `source/proto/agent.proto`; regenerate `source/server/pkg/proto/*.pb.go`
- Modify: `source/server/pkg/agentclient/client.go`

**Interfaces:**
- Produces RPCs: `GetCloudProfiles() → {repeated CloudProfileInfo, string active}`; `SetActiveCloudProfile(name) → {ok,error}`; `SetCloudProfileKey(name, key) → {ok,error}`. `CloudProfileInfo{name, flavor, base_url, model, bool has_key}`.

- [ ] **Step 1: Edit proto**

In `agent.proto`, add messages + RPCs (place RPCs in the `Agent` service):

```proto
  rpc GetCloudProfiles (GetCloudProfilesRequest) returns (GetCloudProfilesResponse) {}
  rpc SetActiveCloudProfile (SetActiveCloudProfileRequest) returns (SetActiveCloudProfileResponse) {}
  rpc SetCloudProfileKey (SetCloudProfileKeyRequest) returns (SetCloudProfileKeyResponse) {}
```

```proto
message CloudProfileInfo {
  string name = 1;
  string flavor = 2;
  string base_url = 3;
  string model = 4;
  bool   has_key = 5; // a key exists in the keychain for this profile
}
message GetCloudProfilesRequest {}
message GetCloudProfilesResponse {
  repeated CloudProfileInfo profiles = 1;
  string active = 2;
}
message SetActiveCloudProfileRequest  { string name = 1; }
message SetActiveCloudProfileResponse { bool ok = 1; string error = 2; }
message SetCloudProfileKeyRequest  { string name = 1; string api_key = 2; }
message SetCloudProfileKeyResponse { bool ok = 1; string error = 2; }
```

- [ ] **Step 2: Regenerate**

From repo root (plugins installed; `export PATH="$PATH:$(go env GOPATH)/bin"`):

```bash
protoc --proto_path=. \
  --go_out=source/server --go_opt=module=cercano/source/server \
  --go-grpc_out=source/server --go-grpc_opt=module=cercano/source/server \
  source/proto/agent.proto
```

Verify `grep -c 'GetCloudProfiles\|SetActiveCloudProfile\|SetCloudProfileKey' source/server/pkg/proto/agent_grpc.pb.go` ≥ 3.

- [ ] **Step 3: agentclient methods**

In `pkg/agentclient/client.go`, add a `CloudProfileInfo` struct (mirroring the proto, with `HasKey bool`) and three methods — `GetCloudProfiles(ctx) ([]CloudProfileInfo, string, error)`, `SetActiveCloudProfile(ctx, name) error`, `SetCloudProfileKey(ctx, name, key) error` — following the existing method patterns in that file (e.g. `GetConfig`/`UpdateConfig`). Return an error built from `resp.GetError()` when `!resp.GetOk()`.

- [ ] **Step 4: Build**

Run: `cd source/server && go build ./...`
Expected: PASS (server doesn't implement the RPCs yet — `UnimplementedAgentServer` covers it; Task 6 implements them).

- [ ] **Step 5: Commit**

```bash
git add source/proto/agent.proto source/server/pkg/proto/ source/server/pkg/agentclient/client.go
git commit -m "feat(proto): cloud profile management RPCs + client"
```

---

### Task 6: Server — implement profile RPCs + `rebuildCloud`

**Files:**
- Modify: `source/server/internal/server/server.go`
- Test: `source/server/internal/server/cloud_profiles_test.go`

> **Note:** `server.go` is large and actively edited. Read the current `Server` struct + `UpdateConfig` cloud-rebuild block (search `wantCloudRebuild`, `SetCloudLLMProvider`, `s.router.SetCloudProvider`) before editing.

**Interfaces:**
- Consumes: `secrets.Store`, `cloudfactory.BuildCloudProvider`, `agent.NewLLMModelProvider`, `config.CloudProfile`, `s.router.SetCloudProvider`, `s.coordinator.SetCloudProvider`, `s.SetCloudLLMProvider`.
- Produces: `Server.secrets secrets.Store` field + `SetSecrets(secrets.Store)`; `(*Server).rebuildCloud() error`; the three RPC handlers; a `activeProfile()` helper.

- [ ] **Step 1: Add the field + setter + helpers**

Add `secrets secrets.Store` to the `Server` struct, plus:

```go
func (s *Server) SetSecrets(st secrets.Store) { s.secrets = st }

// activeProfile returns the configured active cloud profile, or false if none.
func (s *Server) activeProfile() (config.CloudProfile, bool) {
	for _, p := range s.currentConfig.CloudProfiles {
		if p.Name == s.currentConfig.ActiveCloudProfile {
			return p, true
		}
	}
	return config.CloudProfile{}, false
}

// rebuildCloud resolves the active profile + its key and rewires BOTH the native
// tool-loop cloud provider and the router/coordinator CloudModel. On any failure
// (no active profile, no key, unsupported flavor, keychain down) it clears the
// native cloud provider and installs the absent-cloud sentinel — the agent keeps
// running with cloud absent.
func (s *Server) rebuildCloud() error {
	p, ok := s.activeProfile()
	if !ok {
		s.SetCloudLLMProvider(nil)
		s.router.SetCloudProvider(legacymodels.NewAbsentCloudProvider("no active cloud profile"))
		return fmt.Errorf("no active cloud profile")
	}
	key := ""
	if s.secrets != nil {
		if k, err := s.secrets.Get(p.Name); err == nil {
			key = k
		}
	}
	prov, err := cloudfactory.BuildCloudProvider(p, key)
	if err != nil {
		s.SetCloudLLMProvider(nil)
		s.router.SetCloudProvider(legacymodels.NewAbsentCloudProvider(err.Error()))
		return err
	}
	s.SetCloudLLMProvider(prov)
	mp := agent.NewLLMModelProvider(prov, p.Model)
	s.router.SetCloudProvider(mp)
	if s.coordinator != nil {
		s.coordinator.SetCloudProvider(mp)
	}
	s.currentConfig.CloudModel = p.Model // keep CloudModel reporting consistent
	return nil
}
```

Add imports: `cercano/source/server/internal/cloudfactory`, `cercano/source/server/internal/secrets`. (`agent`, `legacymodels`, `config` already imported.)

- [ ] **Step 2: Implement the three RPC handlers**

```go
func (s *Server) GetCloudProfiles(ctx context.Context, req *proto.GetCloudProfilesRequest) (*proto.GetCloudProfilesResponse, error) {
	out := &proto.GetCloudProfilesResponse{Active: s.currentConfig.ActiveCloudProfile}
	for _, p := range s.currentConfig.CloudProfiles {
		hasKey := false
		if s.secrets != nil {
			if _, err := s.secrets.Get(p.Name); err == nil {
				hasKey = true
			}
		}
		out.Profiles = append(out.Profiles, &proto.CloudProfileInfo{
			Name: p.Name, Flavor: p.Flavor, BaseUrl: p.BaseURL, Model: p.Model, HasKey: hasKey,
		})
	}
	return out, nil
}

func (s *Server) SetActiveCloudProfile(ctx context.Context, req *proto.SetActiveCloudProfileRequest) (*proto.SetActiveCloudProfileResponse, error) {
	if _, ok := profileByName(s.currentConfig.CloudProfiles, req.GetName()); !ok {
		return &proto.SetActiveCloudProfileResponse{Ok: false, Error: fmt.Sprintf("no profile %q", req.GetName())}, nil
	}
	s.currentConfig.ActiveCloudProfile = req.GetName()
	if err := s.rebuildCloud(); err != nil {
		// active is set, but the provider couldn't be built — report it, keep going.
		s.persistConfig()
		return &proto.SetActiveCloudProfileResponse{Ok: false, Error: err.Error()}, nil
	}
	s.persistConfig()
	return &proto.SetActiveCloudProfileResponse{Ok: true}, nil
}

func (s *Server) SetCloudProfileKey(ctx context.Context, req *proto.SetCloudProfileKeyRequest) (*proto.SetCloudProfileKeyResponse, error) {
	if s.secrets == nil {
		return &proto.SetCloudProfileKeyResponse{Ok: false, Error: "keychain unavailable"}, nil
	}
	if _, ok := profileByName(s.currentConfig.CloudProfiles, req.GetName()); !ok {
		return &proto.SetCloudProfileKeyResponse{Ok: false, Error: fmt.Sprintf("no profile %q", req.GetName())}, nil
	}
	if err := s.secrets.Set(req.GetName(), req.GetApiKey()); err != nil {
		return &proto.SetCloudProfileKeyResponse{Ok: false, Error: err.Error()}, nil
	}
	// If the key belongs to the active profile, rebuild so it takes effect now.
	if req.GetName() == s.currentConfig.ActiveCloudProfile {
		_ = s.rebuildCloud()
	}
	return &proto.SetCloudProfileKeyResponse{Ok: true}, nil
}
```

Add a small helper `profileByName([]config.CloudProfile, string) (config.CloudProfile, bool)` and a `persistConfig()` that does `if s.configPath != "" { _ = config.Save(s.currentConfig, s.configPath) }` (or reuse the existing save path used by `UpdateConfig` — read it and match).

- [ ] **Step 3: Write the test**

Create `cloud_profiles_test.go` — construct a `Server` with `currentConfig` holding two profiles (one `messages`, one `chat_completions`) + a `secrets.NewMemory()` store, then:
- `GetCloudProfiles` returns both, `has_key` true only after `Set`.
- `SetActiveCloudProfile("messages-one")` after a key is set → `Ok` and `s.cloudLLMProvider != nil`.
- `SetActiveCloudProfile("the-chat_completions-one")` → `Ok:false` with the unsupported-flavor error, and cloud goes absent.
- `SetActiveCloudProfile("nope")` → `Ok:false`.

Use a real `anthropic.NewClient` (it constructs without network) for the messages profile; `rebuildCloud` only builds, doesn't call the network.

- [ ] **Step 4: Run + build**

Run: `cd source/server && go test ./internal/server/ -run CloudProfile -count=1 && go build ./...`
Expected: PASS + build OK.

- [ ] **Step 5: Commit**

```bash
git add source/server/internal/server/server.go source/server/internal/server/cloud_profiles_test.go
git commit -m "feat(server): cloud profile RPCs + rebuildCloud (one cloud system)"
```

---

### Task 7: Startup wiring + key relocation; retire langchaingo cloud

**Files:**
- Modify: `source/server/cmd/cercano/main.go`

> **Note:** `main.go` is actively edited. Read the current cloud-wiring block (search `legacymodels.NewCloudModelProvider`, `canConfigureCloud`, `srv.SetCloudLLMProvider`, `cloudFactory`) before editing.

- [ ] **Step 1: Open the keychain + wire secrets**

After `srv := server.NewServer(...)` (and config persistence), open the keychain and attach it; tolerate failure (cloud just stays absent):

```go
	if store, err := secrets.OpenKeychain(); err == nil {
		srv.SetSecrets(store)
	} else {
		fmt.Fprintf(os.Stderr, "[WARN] keychain unavailable (%v) — cloud profiles can't load keys; fallback deferred.\n", err)
	}
```

- [ ] **Step 2: One-time key relocation (legacy → keychain)**

After secrets is set, if the legacy inline `cfg.CloudAPIKey` is non-empty and the active profile has no key yet, move it into the keychain and blank the yaml field:

```go
	if cfg.CloudAPIKey != "" && cfg.ActiveCloudProfile != "" {
		if store, err := secrets.OpenKeychain(); err == nil {
			if _, gerr := store.Get(cfg.ActiveCloudProfile); gerr != nil {
				if serr := store.Set(cfg.ActiveCloudProfile, cfg.CloudAPIKey); serr == nil {
					cfg.CloudAPIKey = ""
					_ = config.Save(cfg, config.DefaultPath())
				}
			}
		}
	}
```

- [ ] **Step 3: Replace the cloud provider construction with `rebuildCloud`**

Remove the `legacymodels.NewCloudModelProvider(...)`-based `cloudProvider` construction for the cloud tier (keep `legacymodels.NewAbsentCloudProvider` available as the sentinel, and the `LocalModelProvider` untouched). The agent/router still needs an initial `cloudProvider` to construct `NewAgent`/`NewLazyRouter`; pass the **absent sentinel** initially, then let `srv.rebuildCloud()` install the real one:

- Keep `cloudProvider = legacymodels.NewAbsentCloudProvider("pending profile resolution")` as the initial value handed to `NewLazyRouter`/`coordinator`.
- Delete the anthropic-only `if canConfigureCloud && strings.EqualFold(cfg.CloudProvider,"anthropic") { srv.SetCloudLLMProvider(...) }` block.
- After secrets + relocation, call `srv.RebuildCloud()` (export `rebuildCloud` as `RebuildCloud` for the cmd to call, or add `func (s *Server) RebuildCloud() error { return s.rebuildCloud() }`). Log on error but continue.
- The `cloudFactory` closure (langchaingo) can stay for now ONLY if still referenced by `UpdateConfig`; otherwise remove it. Grep `s.cloudFactory` — if `UpdateConfig`'s `wantCloudRebuild` block still uses it, leave that path but note it's superseded (the profile path is authoritative). Prefer: leave `UpdateConfig`'s legacy cloud block intact this task (don't break it); it simply becomes redundant.

- [ ] **Step 4: Build + run**

Run: `cd source/server && go build ./...`
Expected: PASS. Then run `go run ./cmd/cercano` against an existing config with an Anthropic cloud configured; confirm via logs that the active profile resolved and (if a key is in the keychain or was relocated) the cloud provider built. Confirm the conversation still reaches cloud in a Cloud mode.

- [ ] **Step 5: Commit**

```bash
git add source/server/cmd/cercano/main.go source/server/internal/server/server.go
git commit -m "feat(cli): wire cloud profiles at startup; relocate legacy key to keychain"
```

---

### Task 8: CLI — `/cloud` profile management

**Files:**
- Modify/Create: `source/clients/cli/internal/slash/cloud.go` (or wherever `/cloud` lives — grep `RegisterCloud`/`"cloud"`)
- Test: alongside, a small handler test if the existing slash tests have a pattern

> **Note:** CLI is module `cercano/source/clients/cli`; build/test from there.

- [ ] **Step 1: Implement `/cloud` subcommands**

Extend (or add) the `/cloud` command with:
- `/cloud` or `/cloud list` → `GetCloudProfiles`, print each `name  flavor  model  [active]  [key✓/key✗]`.
- `/cloud use <name>` → `SetActiveCloudProfile`; print the returned message/error.
- `/cloud key <name> <api-key>` → `SetCloudProfileKey`; print ok/error. (Mask the key in any echo.)

Mirror the existing slash-command structure (read `internal/slash/config.go`'s `RegisterConfig` for the `Command{Name,Help,Handler}` + `agentclient` call pattern). Register it in `internal/ui/model.go` if it's a new command.

- [ ] **Step 2: Build + test**

Run (from `source/clients/cli`): `go build ./... && go test ./internal/slash/ -count=1`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add source/clients/cli/internal/slash/ source/clients/cli/internal/ui/model.go
git commit -m "feat(cli): /cloud profile list/use/key management"
```

---

### Task 9: Docs

**Files:**
- Modify: `docs/agent/cloud-profiles.md` (Status → implemented; note any deviations)
- Modify: `docs/agent/README.md` if it documents cloud setup

- [ ] **Step 1: Update status + usage**

Flip the Status line to implemented-for-the-foundation, and add a short "Using profiles" section (the `/cloud list|use|key` commands + the `cloud_profiles`/`active_cloud_profile` yaml). Note the deferred items unchanged.

- [ ] **Step 2: Commit**

```bash
git add docs/agent/cloud-profiles.md docs/agent/README.md
git commit -m "docs(agent): cloud profiles usage + status"
```

---

## Self-Review

**Spec coverage (against `docs/agent/cloud-profiles.md`):**
- §1 config model + migration → Task 1. ✓
- §2 keychain secrets (99designs/keyring) → Task 2. ✓
- §3 provider factory (messages→anthropic; others error) → Task 3. ✓
- §4 unify: adapter + active profile feeds native AND CloudModel; retire langchaingo → Task 4 (adapter) + Task 6 (`rebuildCloud` wires both) + Task 7 (startup uses rebuild, drops anthropic-only block). ✓
- §5 runtime management (list/active/key) → Task 5 (proto/client) + Task 6 (handlers) + Task 8 (CLI). ✓
- §6 migration + key relocation → Task 1 (metadata) + Task 7 (key → keychain, blank yaml). ✓
- §7 error handling (keychain down / key missing / unsupported flavor → absent-cloud, no crash) → Task 6 `rebuildCloud` + Task 7 tolerant wiring. ✓
- §8 testing → Tasks 1–6 each ship tests. ✓

**Placeholder scan:** No TBD/TODO. The "read current code before editing" notes on Tasks 5–8 are bounded (named search anchors), required because `server.go`/`main.go`/the CLI are actively edited; exact code is given for all new units (Tasks 1–4, 6's helpers).

**Type consistency:** `CloudProfile{Name,Flavor,BaseURL,Model}`, `secrets.Store` (Get/Set/Delete), `cloudfactory.BuildCloudProvider(profile,key)`, `agent.NewLLMModelProvider(p,model)`, `rebuildCloud`, and the three RPCs are referenced consistently across tasks. Flavor consts (`FlavorMessages="messages"`) match the config migration's `"messages"` literal and the factory switch.

**Risk:** `99designs/keyring`'s exact `Config`/`Item` field names may differ by version (Task 2 Step 5 says to adjust against the resolved package). `server.go`/`main.go` cloud wiring is volatile — Tasks 6–7 anchor on search terms, not line numbers. The `UpdateConfig` legacy cloud-rebuild block is intentionally left intact in Task 7 (redundant but not broken); fully removing it is a follow-up once the profile path is proven.
