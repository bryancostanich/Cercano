# LLM Provider Wireup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the legacy flat "Cloud" settings section with a Cloud Providers section — a vertical list of configured profiles + known-provider templates, each with status, and an inline detail editor to create / edit / activate / delete a profile — backed by new server CRUD RPCs.

**Architecture:** Server side adds two RPCs (`UpsertCloudProfile`, `RemoveCloudProfile`) over the existing profile config + keychain model. CLI adds one new form widget (`RowField`), a static provider-preset table, and a settings-page section that merges presets with the live profile list and routes detail-editor commits to the RPCs. Keys go to the OS keychain via the existing `SetCloudProfileKey`; metadata persists to `config.yaml`.

**Tech Stack:** Go; gRPC + protobuf (`protoc`, `protoc-gen-go`, `protoc-gen-go-grpc`); Bubble Tea v2 / Lipgloss v2 (`charm.land/...`); `gopkg.in/yaml.v3`; two Go modules (`source/server`, `source/clients/cli`).

## Global Constraints

- Two separate Go modules. Server work: `cd source/server`. CLI work: `cd source/clients/cli`. Build/test each in its own module.
- Server build/test: `go build ./...` and `go test ./... -count=1` from `source/server`.
- CLI build/test: `go build -o bin/cercano-cli .` and `go test ./... -count=1` from `source/clients/cli`.
- API keys are NEVER written to `config.yaml` — they live only in the OS keychain, keyed by profile name. No new field carries a key into config or into `CloudProfileInfo`.
- `internal/form` MUST NOT import `internal/ui` (import cycle). Widgets depend only on `theme` + charm libs.
- Provider preset base URLs (exact, verbatim):
  - `anthropic` — flavor `messages` — base URL `""` (default endpoint)
  - `gemini` — flavor `chat_completions`, backend `gemini` — `https://generativelanguage.googleapis.com/v1beta/openai`
  - `openai` — flavor `chat_completions`, backend `openai` — `https://api.openai.com/v1`
  - `groq` — flavor `chat_completions`, backend `groq` — `https://api.groq.com/openai/v1`
  - `deepinfra` — flavor `chat_completions`, backend `""` — `https://api.deepinfra.com/v1/openai`
  - `together` — flavor `chat_completions`, backend `""` — `https://api.together.xyz/v1`
  - `openrouter` — flavor `chat_completions`, backend `""` — `https://openrouter.ai/api/v1`
  - `deepseek` — flavor `chat_completions`, backend `""` — `https://api.deepseek.com`
  - `bedrock` — flavor `bedrock` — base URL `""` (coming soon)
  - `openai-responses` — flavor `responses` — `https://api.openai.com/v1` (coming soon)
- Label tiers: verified (no label) = `anthropic`, `gemini`. `(untested)` = `openai`, `groq`, `deepinfra`, `together`, `openrouter`, `deepseek`. `(coming soon)` = `bedrock`, `openai-responses`; their `[ activate ]` button is disabled.
- A profile's `name` is fixed after creation (rename is out of scope — deferred).
- `flavor`/`backend` are read-only in the detail editor for known templates; editable only for `other`.
- Commit messages must not contain the word "Claude" anywhere. Do not run `git push`.

---

### Task 1: Proto — profile CRUD messages + RPCs

**Files:**
- Modify: `source/proto/agent.proto` (service block ~line 117-119; messages ~line 648-663)
- Regenerate: `source/server/pkg/proto/agent.pb.go`, `source/server/pkg/proto/agent_grpc.pb.go`

**Interfaces:**
- Produces: proto messages `UpsertCloudProfileRequest{Name, Flavor, Backend, BaseUrl, Model string}`, `UpsertCloudProfileResponse{Ok bool, Error string}`, `RemoveCloudProfileRequest{Name string}`, `RemoveCloudProfileResponse{Ok bool, Error string}`; service methods `UpsertCloudProfile`, `RemoveCloudProfile`; `CloudProfileInfo` gains `Backend string` (field 6, accessor `GetBackend()`).

- [ ] **Step 1: Add the `backend` field to `CloudProfileInfo` and the new messages**

In `source/proto/agent.proto`, replace the `CloudProfileInfo` message and append the new messages (the block currently ends at line 663):

```proto
// Cloud profile management messages.
message CloudProfileInfo {
  string name = 1;
  string flavor = 2;
  string base_url = 3;
  string model = 4;
  bool   has_key = 5; // a key exists in the keychain for this profile
  string backend = 6; // chat_completions quirks selector (openai|gemini|groq|…)
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
message UpsertCloudProfileRequest {
  string name = 1;
  string flavor = 2;
  string backend = 3;
  string base_url = 4;
  string model = 5;
}
message UpsertCloudProfileResponse { bool ok = 1; string error = 2; }
message RemoveCloudProfileRequest  { string name = 1; }
message RemoveCloudProfileResponse { bool ok = 1; string error = 2; }
```

- [ ] **Step 2: Add the RPC methods to the service**

In `source/proto/agent.proto`, after the existing `SetCloudProfileKey` rpc line (line 119), add:

```proto
  rpc UpsertCloudProfile (UpsertCloudProfileRequest) returns (UpsertCloudProfileResponse) {}
  rpc RemoveCloudProfile (RemoveCloudProfileRequest) returns (RemoveCloudProfileResponse) {}
```

- [ ] **Step 3: Install codegen plugins (one-time) and regenerate**

The plugins are not on PATH (`protoc` is, at `/opt/homebrew/bin/protoc`). Install them, then regenerate from the repo root:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
export PATH="$PATH:$(go env GOPATH)/bin"
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
protoc -I source/proto \
  --go_out=source/server --go_opt=module=cercano/source/server \
  --go-grpc_out=source/server --go-grpc_opt=module=cercano/source/server \
  source/proto/agent.proto
```

Expected: `source/server/pkg/proto/agent.pb.go` and `agent_grpc.pb.go` are rewritten with the new types/methods. (A differing `protoc`/plugin version in the file header is fine.)

- [ ] **Step 4: Verify the generated code compiles and exposes the new surface**

```bash
cd source/server && go build ./pkg/proto/
```
Expected: builds clean. Confirm the new symbols exist:
```bash
grep -c "UpsertCloudProfileRequest\|RemoveCloudProfileRequest\|func (x \*CloudProfileInfo) GetBackend" pkg/proto/agent.pb.go
```
Expected: a count ≥ 3.

- [ ] **Step 5: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/proto/agent.proto source/server/pkg/proto/agent.pb.go source/server/pkg/proto/agent_grpc.pb.go
git commit -m "feat(proto): cloud profile upsert/remove RPCs + backend field"
```

---

### Task 2: Server — Upsert/Remove handlers + Backend in GetCloudProfiles

**Files:**
- Modify: `source/server/internal/server/server.go` (add two handlers near the existing profile handlers ~line 252-299; add `Backend` to the `GetCloudProfiles` mapping at line 261-263)
- Test: `source/server/internal/server/cloud_profiles_test.go` (extend; harness `newTestServer()` + `secrets.NewMemory()` already there)

**Interfaces:**
- Consumes: `proto.UpsertCloudProfileRequest/Response`, `proto.RemoveCloudProfileRequest/Response` (Task 1); existing `config.CloudProfile`, `s.currentConfig.CloudProfiles`, `profileByName`, `s.persistConfig`, `s.rebuildCloud`, `s.secrets`.
- Produces: `(*Server) UpsertCloudProfile(ctx, *proto.UpsertCloudProfileRequest) (*proto.UpsertCloudProfileResponse, error)`; `(*Server) RemoveCloudProfile(ctx, *proto.RemoveCloudProfileRequest) (*proto.RemoveCloudProfileResponse, error)`. `GetCloudProfiles` response now sets `Backend`.

- [ ] **Step 1: Write the failing tests**

Append to `source/server/internal/server/cloud_profiles_test.go`:

```go
func TestUpsertCloudProfileCreatesAndUpdates(t *testing.T) {
	s, _ := newTestServer()
	// Create a new profile.
	resp, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "openai", Flavor: "chat_completions", Backend: "openai",
		BaseUrl: "https://api.openai.com/v1", Model: "gpt-x",
	})
	if err != nil {
		t.Fatalf("UpsertCloudProfile: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("want Ok, got error: %s", resp.Error)
	}
	p, ok := profileByName(s.currentConfig.CloudProfiles, "openai")
	if !ok {
		t.Fatal("profile openai was not added")
	}
	if p.Backend != "openai" || p.BaseURL != "https://api.openai.com/v1" || p.Model != "gpt-x" {
		t.Fatalf("created profile wrong: %+v", p)
	}
	// Update the same name in place (no duplicate row).
	if _, err := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "openai", Flavor: "chat_completions", Backend: "openai",
		BaseUrl: "https://api.openai.com/v1", Model: "gpt-y",
	}); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, pr := range s.currentConfig.CloudProfiles {
		if pr.Name == "openai" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("update should not duplicate; got %d openai rows", count)
	}
	p2, _ := profileByName(s.currentConfig.CloudProfiles, "openai")
	if p2.Model != "gpt-y" {
		t.Fatalf("update did not change model: %+v", p2)
	}
}

func TestUpsertCloudProfileValidation(t *testing.T) {
	s, _ := newTestServer()
	// Empty name rejected.
	if resp, _ := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "", Flavor: "messages",
	}); resp.Ok {
		t.Error("empty name should be rejected")
	}
	// chat_completions requires a base_url.
	if resp, _ := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "x", Flavor: "chat_completions", BaseUrl: "",
	}); resp.Ok {
		t.Error("chat_completions without base_url should be rejected")
	}
	// Unknown flavor rejected.
	if resp, _ := s.UpsertCloudProfile(context.Background(), &proto.UpsertCloudProfileRequest{
		Name: "x", Flavor: "bogus",
	}); resp.Ok {
		t.Error("unknown flavor should be rejected")
	}
}

func TestRemoveCloudProfileDropsRowAndKeyAndActive(t *testing.T) {
	s, _ := newTestServer()
	if err := s.secrets.Set("messages-one", "sk-test"); err != nil {
		t.Fatal(err)
	}
	s.currentConfig.ActiveCloudProfile = "messages-one"
	resp, err := s.RemoveCloudProfile(context.Background(), &proto.RemoveCloudProfileRequest{Name: "messages-one"})
	if err != nil {
		t.Fatalf("RemoveCloudProfile: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("want Ok, got: %s", resp.Error)
	}
	if _, ok := profileByName(s.currentConfig.CloudProfiles, "messages-one"); ok {
		t.Error("profile should be gone")
	}
	if _, err := s.secrets.Get("messages-one"); err == nil {
		t.Error("key should be deleted from keychain")
	}
	if s.currentConfig.ActiveCloudProfile != "" {
		t.Errorf("active should be cleared, got %q", s.currentConfig.ActiveCloudProfile)
	}
}

func TestGetCloudProfilesReportsBackend(t *testing.T) {
	s, _ := newTestServer()
	s.currentConfig.CloudProfiles = append(s.currentConfig.CloudProfiles,
		config.CloudProfile{Name: "g", Flavor: "chat_completions", Backend: "gemini", BaseURL: "u", Model: "m"})
	resp, _ := s.GetCloudProfiles(context.Background(), &proto.GetCloudProfilesRequest{})
	var found bool
	for _, p := range resp.Profiles {
		if p.Name == "g" {
			found = true
			if p.Backend != "gemini" {
				t.Errorf("Backend = %q, want gemini", p.Backend)
			}
		}
	}
	if !found {
		t.Fatal("profile g not returned")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd source/server && go test ./internal/server/ -run 'Upsert|RemoveCloudProfile|GetCloudProfilesReportsBackend' -count=1
```
Expected: FAIL — `s.UpsertCloudProfile` / `s.RemoveCloudProfile` undefined, and `p.Backend` unset.

- [ ] **Step 3: Add `Backend` to the GetCloudProfiles mapping**

In `source/server/internal/server/server.go`, in `GetCloudProfiles` (line 261-263), add `Backend`:

```go
		out.Profiles = append(out.Profiles, &proto.CloudProfileInfo{
			Name: p.Name, Flavor: p.Flavor, BaseUrl: p.BaseURL, Model: p.Model, HasKey: hasKey, Backend: p.Backend,
		})
```

- [ ] **Step 4: Add the two handlers**

In `source/server/internal/server/server.go`, after `SetCloudProfileKey` (ends line 299), add. Use the `cloudfactory` flavor constants (already imported in this package) for validation:

```go
// knownFlavor reports whether the flavor is a recognized enum value (whether or
// not it is implemented yet — coming-soon flavors are storable but won't activate).
func knownFlavor(f string) bool {
	switch f {
	case cloudfactory.FlavorMessages, cloudfactory.FlavorChatCompletions,
		cloudfactory.FlavorResponses, cloudfactory.FlavorBedrock:
		return true
	}
	return false
}

// UpsertCloudProfile implements proto.AgentServer — creates or updates a profile's
// metadata (the API key is managed separately via SetCloudProfileKey).
func (s *Server) UpsertCloudProfile(ctx context.Context, req *proto.UpsertCloudProfileRequest) (*proto.UpsertCloudProfileResponse, error) {
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return &proto.UpsertCloudProfileResponse{Ok: false, Error: "profile name is required"}, nil
	}
	if !knownFlavor(req.GetFlavor()) {
		return &proto.UpsertCloudProfileResponse{Ok: false, Error: fmt.Sprintf("unknown flavor %q", req.GetFlavor())}, nil
	}
	if req.GetFlavor() == cloudfactory.FlavorChatCompletions && strings.TrimSpace(req.GetBaseUrl()) == "" {
		return &proto.UpsertCloudProfileResponse{Ok: false, Error: "base_url is required for chat_completions"}, nil
	}
	np := config.CloudProfile{
		Name: name, Flavor: req.GetFlavor(), Backend: req.GetBackend(),
		BaseURL: req.GetBaseUrl(), Model: req.GetModel(),
	}
	replaced := false
	for i, p := range s.currentConfig.CloudProfiles {
		if p.Name == name {
			s.currentConfig.CloudProfiles[i] = np
			replaced = true
			break
		}
	}
	if !replaced {
		s.currentConfig.CloudProfiles = append(s.currentConfig.CloudProfiles, np)
	}
	// If this is the active profile, rebuild so metadata changes take effect now.
	if name == s.currentConfig.ActiveCloudProfile {
		_ = s.rebuildCloud()
	}
	s.persistConfig()
	return &proto.UpsertCloudProfileResponse{Ok: true}, nil
}

// RemoveCloudProfile implements proto.AgentServer — deletes a profile and its
// keychain key. Clears the active profile (→ absent cloud) if it was active.
func (s *Server) RemoveCloudProfile(ctx context.Context, req *proto.RemoveCloudProfileRequest) (*proto.RemoveCloudProfileResponse, error) {
	name := req.GetName()
	if _, ok := profileByName(s.currentConfig.CloudProfiles, name); !ok {
		return &proto.RemoveCloudProfileResponse{Ok: false, Error: fmt.Sprintf("no profile %q", name)}, nil
	}
	kept := s.currentConfig.CloudProfiles[:0]
	for _, p := range s.currentConfig.CloudProfiles {
		if p.Name != name {
			kept = append(kept, p)
		}
	}
	s.currentConfig.CloudProfiles = kept
	if s.secrets != nil {
		_ = s.secrets.Delete(name) // best-effort; missing key is not an error
	}
	if s.currentConfig.ActiveCloudProfile == name {
		s.currentConfig.ActiveCloudProfile = ""
		s.installAbsentCloud("active cloud profile removed")
	}
	s.persistConfig()
	return &proto.RemoveCloudProfileResponse{Ok: true}, nil
}
```

Note: `strings` is already imported in `server.go` (used elsewhere). If `go build` reports it missing, add `"strings"` to the import block.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
cd source/server && go test ./internal/server/ -run 'Upsert|RemoveCloudProfile|GetCloudProfilesReportsBackend' -count=1
```
Expected: PASS. Then full package: `go test ./internal/server/ -count=1` → PASS.

- [ ] **Step 6: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/server/internal/server/server.go source/server/internal/server/cloud_profiles_test.go
git commit -m "feat(server): cloud profile upsert/remove handlers + report backend"
```

---

### Task 3: agentclient — Upsert/Remove wrappers + Backend on CloudProfileInfo

**Files:**
- Modify: `source/server/pkg/agentclient/client.go` (`CloudProfileInfo` struct ~line 1130-1136; `GetCloudProfiles` mapping ~line 1147-1153; add two methods after `SetCloudProfileKey` ~line 1180)

**Interfaces:**
- Consumes: `proto.UpsertCloudProfileRequest`, `proto.RemoveCloudProfileRequest` (Task 1); `c.agent` (a `proto.AgentClient`).
- Produces: `CloudProfileInfo` gains `Backend string`; `(*Client) UpsertCloudProfile(ctx context.Context, p CloudProfileInfo) error`; `(*Client) RemoveCloudProfile(ctx context.Context, name string) error`. These are the methods the CLI settings page (Task 7) calls.

Note: `agentclient` has no test file in the repo and its existing profile wrappers (`GetCloudProfiles`, `SetActiveCloudProfile`, `SetCloudProfileKey`) are untested thin proto mappers. Per that established convention, these two wrappers are verified by compilation; their behavior is exercised by the server handler tests (Task 2) over the same proto types. Do not stand up a new gRPC test harness for them.

- [ ] **Step 1: Add `Backend` to `CloudProfileInfo` and its mapping**

In `source/server/pkg/agentclient/client.go`, add the field to the struct (after `HasKey`):

```go
// CloudProfileInfo is a point-in-time view of one cloud profile.
type CloudProfileInfo struct {
	Name    string
	Flavor  string
	BaseURL string
	Model   string
	HasKey  bool // a key exists in the keychain for this profile
	Backend string
}
```

And in `GetCloudProfiles`, set it in the append (the loop at line 1147):

```go
		out = append(out, CloudProfileInfo{
			Name:    p.GetName(),
			Flavor:  p.GetFlavor(),
			BaseURL: p.GetBaseUrl(),
			Model:   p.GetModel(),
			HasKey:  p.GetHasKey(),
			Backend: p.GetBackend(),
		})
```

- [ ] **Step 2: Add the two wrapper methods**

After `SetCloudProfileKey` (ends ~line 1180), add:

```go
// UpsertCloudProfile creates or updates a cloud profile's metadata.
func (c *Client) UpsertCloudProfile(ctx context.Context, p CloudProfileInfo) error {
	resp, err := c.agent.UpsertCloudProfile(ctx, &proto.UpsertCloudProfileRequest{
		Name: p.Name, Flavor: p.Flavor, Backend: p.Backend, BaseUrl: p.BaseURL, Model: p.Model,
	})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetError())
	}
	return nil
}

// RemoveCloudProfile deletes a cloud profile and its keychain key.
func (c *Client) RemoveCloudProfile(ctx context.Context, name string) error {
	resp, err := c.agent.RemoveCloudProfile(ctx, &proto.RemoveCloudProfileRequest{Name: name})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetError())
	}
	return nil
}
```

- [ ] **Step 3: Verify both modules build**

```bash
cd source/server && go build ./...
cd ../clients/cli && go build ./...
```
Expected: both build clean.

- [ ] **Step 4: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/server/pkg/agentclient/client.go
git commit -m "feat(agentclient): cloud profile upsert/remove wrappers + backend field"
```

---

### Task 4: form — RowField widget

**Files:**
- Create: `source/clients/cli/internal/form/row_field.go`
- Test: `source/clients/cli/internal/form/row_field_test.go`

**Interfaces:**
- Consumes: the `Field` interface (`field.go`); `theme.Styles`; `tea.KeyPressMsg`.
- Produces: `RowField` implementing `Field`. Constructor `NewRow(key, label, annotation string, accent bool) *RowField`. `Key()`=key, `Label()`=label, `Display()`=annotation, `Editing()`=false. Enter/space commits the sentinel `RowSelect` ("select"). `View` renders the annotation (e.g. `✓ key   (active)` or `(untested)`); when `accent` is true the label-side dot/marker styling is highlighted. Used by the settings Cloud section (Task 6) as one list row.

- [ ] **Step 1: Write the failing test**

Create `source/clients/cli/internal/form/row_field_test.go`:

```go
package form

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestRowFieldActivateCommitsSelect(t *testing.T) {
	r := NewRow("cloud-row:template:openai", "openai", "(untested)", false)
	if r.Editing() {
		t.Fatal("RowField is never in editing mode")
	}
	cmd, committed, val := r.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_ = cmd
	if !committed || val != RowSelect {
		t.Fatalf("enter should commit RowSelect; committed=%v val=%q", committed, val)
	}
}

func TestRowFieldViewShowsAnnotation(t *testing.T) {
	_, s := testStyles()
	r := NewRow("cloud-row:profile:my", "my", "✓ key", false)
	out := r.View(false, 30, s)
	if !strings.Contains(out, "✓ key") {
		t.Fatalf("View should render the annotation; got %q", out)
	}
}

func TestRowFieldNonActivateDoesNotCommit(t *testing.T) {
	r := NewRow("k", "l", "", false)
	if _, committed, _ := r.Update(tea.KeyPressMsg{Code: tea.KeyLeft}); committed {
		t.Fatal("left arrow must not commit")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd source/clients/cli && go test ./internal/form/ -run RowField -count=1
```
Expected: FAIL — `NewRow` / `RowSelect` undefined.

- [ ] **Step 3: Implement the widget**

Create `source/clients/cli/internal/form/row_field.go`:

```go
package form

import (
	tea "charm.land/bubbletea/v2"

	"cercano/source/clients/cli/internal/theme"
)

// RowSelect is the sentinel a RowField commits when activated, signalling the
// host to expand this row's detail.
const RowSelect = "select"

// RowField is a selectable list row carrying a right-side status annotation
// (e.g. "✓ key   (active)" or "(untested)"). Activating it (enter/space) commits
// RowSelect so the Form's OnCommit routes it to a selection handler. It is never
// in editing mode — selection is host state, not field state.
type RowField struct {
	key, label, annotation string
	accent                 bool // render the annotation in the accent color (active row)
}

// NewRow builds a list row. accent=true highlights the annotation (active row).
func NewRow(key, label, annotation string, accent bool) *RowField {
	return &RowField{key: key, label: label, annotation: annotation, accent: accent}
}

func (f *RowField) Key() string     { return f.key }
func (f *RowField) Label() string   { return f.label }
func (f *RowField) Display() string { return f.annotation }
func (f *RowField) Editing() bool   { return false }

func (f *RowField) Update(msg tea.KeyPressMsg) (tea.Cmd, bool, string) {
	if msg.Code == tea.KeyEnter || msg.Code == tea.KeySpace {
		return nil, true, RowSelect
	}
	return nil, false, ""
}

func (f *RowField) View(focused bool, width int, s theme.Styles) string {
	if f.annotation == "" {
		return ""
	}
	if f.accent {
		return s.Accent.Render(f.annotation)
	}
	return s.Muted.Render(f.annotation)
}
```

If `tea.KeySpace` is not a defined constant in this Bubble Tea version, drop that clause and key only on `tea.KeyEnter` (match `ButtonField`, which keys on `tea.KeyEnter` alone).

- [ ] **Step 4: Run the test to verify it passes**

```bash
cd source/clients/cli && go test ./internal/form/ -run RowField -count=1
```
Expected: PASS. Then full package: `go test ./internal/form/ -count=1` → PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/clients/cli/internal/form/row_field.go source/clients/cli/internal/form/row_field_test.go
git commit -m "feat(cli/form): RowField selectable list row widget"
```

---

### Task 5: ui — provider preset table + row merge

**Files:**
- Create: `source/clients/cli/internal/ui/cloud_presets.go`
- Test: `source/clients/cli/internal/ui/cloud_presets_test.go`

**Interfaces:**
- Consumes: `agentclient.CloudProfileInfo` (Task 3, has `Backend`).
- Produces:
  - `type cloudTier int` with `tierVerified, tierUntested, tierComingSoon, tierCustom`.
  - `type cloudPreset struct { ID, Label, Flavor, Backend, BaseURL string; Tier cloudTier }`.
  - `func cloudPresets() []cloudPreset` — the verbatim list from Global Constraints (10 presets, in this order: anthropic, openai, gemini, groq, deepinfra, together, openrouter, deepseek, bedrock, openai-responses).
  - `type cloudRow struct { ID, Label string; Tier cloudTier; IsProfile, HasKey, Active, ComingSoon bool; Preset *cloudPreset; Profile *agentclient.CloudProfileInfo }`.
  - `func buildCloudRows(presets []cloudPreset, profiles []agentclient.CloudProfileInfo, active string) []cloudRow` — configured-profile rows first (in `profiles` order), then a template row per preset, then a final `other` custom row. Row `ID` is `"profile:<name>"`, `"template:<presetID>"`, or `"other"`. `Active` is set on the profile row whose name == active. `ComingSoon` mirrors `Tier == tierComingSoon`.
  - `func rowAnnotation(r cloudRow) string` — `"✓ key"` / `"— no key"` plus `"  (active)"` for profile rows; the tier label (`"(untested)"` / `"(coming soon)"` / `""`) for template rows; `"(custom endpoint)"` for `other`.

These are pure functions tested directly; Task 6 renders them.

- [ ] **Step 1: Write the failing tests**

Create `source/clients/cli/internal/ui/cloud_presets_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"cercano/source/server/pkg/agentclient"
)

func TestCloudPresetsCoverAllProviders(t *testing.T) {
	got := map[string]cloudPreset{}
	for _, p := range cloudPresets() {
		got[p.ID] = p
	}
	for _, id := range []string{"anthropic", "openai", "gemini", "groq", "deepinfra", "together", "openrouter", "deepseek", "bedrock", "openai-responses"} {
		if _, ok := got[id]; !ok {
			t.Errorf("missing preset %q", id)
		}
	}
	if got["gemini"].BaseURL != "https://generativelanguage.googleapis.com/v1beta/openai" {
		t.Errorf("gemini base URL wrong: %q", got["gemini"].BaseURL)
	}
	if got["gemini"].Tier != tierVerified || got["anthropic"].Tier != tierVerified {
		t.Error("anthropic and gemini must be tierVerified")
	}
	if got["openai"].Tier != tierUntested {
		t.Error("openai must be tierUntested")
	}
	if got["bedrock"].Tier != tierComingSoon || got["openai-responses"].Tier != tierComingSoon {
		t.Error("bedrock and openai-responses must be tierComingSoon")
	}
}

func TestBuildCloudRowsOrderAndStatus(t *testing.T) {
	profiles := []agentclient.CloudProfileInfo{
		{Name: "work-openai", Flavor: "chat_completions", Backend: "openai", BaseURL: "u", Model: "m", HasKey: true},
	}
	rows := buildCloudRows(cloudPresets(), profiles, "work-openai")
	if rows[0].ID != "profile:work-openai" || !rows[0].IsProfile {
		t.Fatalf("first row should be the configured profile, got %+v", rows[0])
	}
	if !rows[0].Active || !rows[0].HasKey {
		t.Error("configured active profile row should be Active + HasKey")
	}
	if rows[len(rows)-1].ID != "other" {
		t.Fatalf("last row should be 'other', got %q", rows[len(rows)-1].ID)
	}
	// A template row for each preset exists.
	haveTemplate := map[string]bool{}
	for _, r := range rows {
		if strings.HasPrefix(r.ID, "template:") {
			haveTemplate[strings.TrimPrefix(r.ID, "template:")] = true
		}
	}
	if !haveTemplate["bedrock"] || !haveTemplate["gemini"] {
		t.Error("expected template rows for bedrock and gemini")
	}
}

func TestRowAnnotation(t *testing.T) {
	profileRow := cloudRow{ID: "profile:x", IsProfile: true, HasKey: true, Active: true}
	a := rowAnnotation(profileRow)
	if !strings.Contains(a, "✓ key") || !strings.Contains(a, "active") {
		t.Errorf("profile annotation wrong: %q", a)
	}
	tmpl := cloudRow{ID: "template:bedrock", Tier: tierComingSoon, ComingSoon: true}
	if rowAnnotation(tmpl) != "(coming soon)" {
		t.Errorf("coming-soon annotation wrong: %q", rowAnnotation(tmpl))
	}
	tmpl2 := cloudRow{ID: "template:openai", Tier: tierUntested}
	if rowAnnotation(tmpl2) != "(untested)" {
		t.Errorf("untested annotation wrong: %q", rowAnnotation(tmpl2))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd source/clients/cli && go test ./internal/ui/ -run 'CloudPresets|BuildCloudRows|RowAnnotation' -count=1
```
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Implement the presets + merge**

Create `source/clients/cli/internal/ui/cloud_presets.go`:

```go
package ui

import "cercano/source/server/pkg/agentclient"

type cloudTier int

const (
	tierVerified cloudTier = iota
	tierUntested
	tierComingSoon
	tierCustom
)

// cloudPreset is a known-provider template: pre-filled flavor/backend/base URL.
type cloudPreset struct {
	ID, Label, Flavor, Backend, BaseURL string
	Tier                                cloudTier
}

// cloudPresets returns the known providers, in display order. Base URLs are
// best-effort defaults; the user can edit them in the detail editor.
func cloudPresets() []cloudPreset {
	return []cloudPreset{
		{ID: "anthropic", Label: "anthropic", Flavor: "messages", BaseURL: "", Tier: tierVerified},
		{ID: "openai", Label: "openai", Flavor: "chat_completions", Backend: "openai", BaseURL: "https://api.openai.com/v1", Tier: tierUntested},
		{ID: "gemini", Label: "gemini", Flavor: "chat_completions", Backend: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Tier: tierVerified},
		{ID: "groq", Label: "groq", Flavor: "chat_completions", Backend: "groq", BaseURL: "https://api.groq.com/openai/v1", Tier: tierUntested},
		{ID: "deepinfra", Label: "deepinfra", Flavor: "chat_completions", Backend: "", BaseURL: "https://api.deepinfra.com/v1/openai", Tier: tierUntested},
		{ID: "together", Label: "together", Flavor: "chat_completions", Backend: "", BaseURL: "https://api.together.xyz/v1", Tier: tierUntested},
		{ID: "openrouter", Label: "openrouter", Flavor: "chat_completions", Backend: "", BaseURL: "https://openrouter.ai/api/v1", Tier: tierUntested},
		{ID: "deepseek", Label: "deepseek", Flavor: "chat_completions", Backend: "", BaseURL: "https://api.deepseek.com", Tier: tierUntested},
		{ID: "bedrock", Label: "bedrock", Flavor: "bedrock", BaseURL: "", Tier: tierComingSoon},
		{ID: "openai-responses", Label: "openai (responses)", Flavor: "responses", BaseURL: "https://api.openai.com/v1", Tier: tierComingSoon},
	}
}

// cloudRow is one rendered list entry: a configured profile, a known-provider
// template, or the trailing custom "other" row.
type cloudRow struct {
	ID, Label  string
	Tier       cloudTier
	IsProfile  bool
	HasKey     bool
	Active     bool
	ComingSoon bool
	Preset     *cloudPreset
	Profile    *agentclient.CloudProfileInfo
}

// buildCloudRows merges configured profiles (first, in order) with the preset
// templates (next) and a trailing "other" custom row.
func buildCloudRows(presets []cloudPreset, profiles []agentclient.CloudProfileInfo, active string) []cloudRow {
	rows := make([]cloudRow, 0, len(profiles)+len(presets)+1)
	for i := range profiles {
		p := profiles[i]
		rows = append(rows, cloudRow{
			ID: "profile:" + p.Name, Label: p.Name, Tier: tierCustom,
			IsProfile: true, HasKey: p.HasKey, Active: p.Name == active, Profile: &profiles[i],
		})
	}
	for i := range presets {
		pr := presets[i]
		rows = append(rows, cloudRow{
			ID: "template:" + pr.ID, Label: pr.Label, Tier: pr.Tier,
			ComingSoon: pr.Tier == tierComingSoon, Preset: &presets[i],
		})
	}
	rows = append(rows, cloudRow{ID: "other", Label: "+ other", Tier: tierCustom})
	return rows
}

// rowAnnotation is the right-side status text for a row.
func rowAnnotation(r cloudRow) string {
	if r.ID == "other" {
		return "(custom endpoint)"
	}
	if r.IsProfile {
		s := "— no key"
		if r.HasKey {
			s = "✓ key"
		}
		if r.Active {
			s += "  (active)"
		}
		return s
	}
	switch r.Tier {
	case tierUntested:
		return "(untested)"
	case tierComingSoon:
		return "(coming soon)"
	}
	return ""
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd source/clients/cli && go test ./internal/ui/ -run 'CloudPresets|BuildCloudRows|RowAnnotation' -count=1
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/clients/cli/internal/ui/cloud_presets.go source/clients/cli/internal/ui/cloud_presets_test.go
git commit -m "feat(cli): cloud provider preset table + row merge"
```

---

### Task 6: ui — Cloud Providers section (render + page state)

**Files:**
- Create: `source/clients/cli/internal/ui/cloud_section.go`
- Modify: `source/clients/cli/internal/ui/settings_page.go` (add fields to `settingsPage`; profiles cache in `snapshotSections`)
- Modify: `source/clients/cli/internal/ui/settings_build.go` (remove the legacy `Cloud` section from `buildSettingsSections`)
- Test: `source/clients/cli/internal/ui/cloud_section_test.go`
- Reference: `source/clients/cli/internal/ui/settings_layout_test.go` (the `sampleSettingsPage` helper pattern — seed caches to avoid live RPC)

**Interfaces:**
- Consumes: `buildCloudRows`, `rowAnnotation`, `cloudPreset`, `cloudRow` (Task 5); `form.NewRow`, `form.NewText`, `form.NewMasked`, `form.NewSelect`, `form.NewButton`, `form.NewReadOnly`, `form.Section`, `form.Field` (Task 4 + existing); `agentclient.CloudProfileInfo`.
- Produces:
  - New `settingsPage` fields: `profiles []agentclient.CloudProfileInfo`, `activeProfile string`, `profilesLoaded bool`, `cloudSelected string` (row ID, "" = none expanded), `cloudDraft cloudDraft`, `cloudDraftNew bool`.
  - `type cloudDraft struct { Name, Flavor, Backend, BaseURL, Model string }`.
  - `func (sp *settingsPage) buildCloudSection() form.Section` — the "Cloud Providers" section: one `RowField` per row, and (immediately after the selected row) the detail fields for `sp.cloudDraft`.
  - `func (sp *settingsPage) selectCloudRow(rowID string)` — sets `cloudSelected`, populates `cloudDraft` + `cloudDraftNew` from the matching profile or preset.

- [ ] **Step 1: Write the failing tests**

Create `source/clients/cli/internal/ui/cloud_section_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"cercano/source/clients/cli/internal/form"
	"cercano/source/clients/cli/internal/theme"
	"cercano/source/server/pkg/agentclient"
)

func cloudSamplePage() *settingsPage {
	p := theme.Cracker()
	sp := &settingsPage{
		palette: p, styles: theme.NewStyles(p), width: 96, height: 60,
		cfg:  &agentclient.Config{Port: "50052", LocusMode: "cloud_only"},
		mode: "permissive",
		themes:  theme.NewRegistry(theme.BuiltinThemes()),
		working: theme.Theme{Name: "cr4k3r_j4x", Palette: p},
		profiles: []agentclient.CloudProfileInfo{
			{Name: "work-openai", Flavor: "chat_completions", Backend: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-x", HasKey: true},
		},
		activeProfile:  "work-openai",
		profilesLoaded: true,
	}
	return sp
}

func TestCloudSectionListsProfilesAndTemplates(t *testing.T) {
	sp := cloudSamplePage()
	sec := sp.buildCloudSection()
	var labels []string
	for _, f := range sec.Fields {
		labels = append(labels, f.Label())
	}
	joined := strings.Join(labels, "|")
	if !strings.Contains(joined, "work-openai") {
		t.Errorf("configured profile row missing: %v", labels)
	}
	if !strings.Contains(joined, "gemini") || !strings.Contains(joined, "+ other") {
		t.Errorf("template/other rows missing: %v", labels)
	}
}

func TestCloudSectionNoDetailWhenNothingSelected(t *testing.T) {
	sp := cloudSamplePage()
	sec := sp.buildCloudSection()
	for _, f := range sec.Fields {
		if f.Key() == "cloud-base-url" {
			t.Fatal("detail fields should not appear until a row is selected")
		}
	}
}

func TestCloudSectionShowsDetailForSelectedProfile(t *testing.T) {
	sp := cloudSamplePage()
	sp.selectCloudRow("profile:work-openai")
	sec := sp.buildCloudSection()
	var keys []string
	for _, f := range sec.Fields {
		keys = append(keys, f.Key())
	}
	j := strings.Join(keys, "|")
	for _, want := range []string{"cloud-base-url", "cloud-model", "cloud-key", "cloud-save", "cloud-activate", "cloud-delete"} {
		if !strings.Contains(j, want) {
			t.Errorf("missing detail field %q in %v", want, keys)
		}
	}
	if sp.cloudDraft.Model != "gpt-x" || sp.cloudDraft.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("draft not seeded from profile: %+v", sp.cloudDraft)
	}
	if sp.cloudDraftNew {
		t.Error("editing an existing profile is not a new draft")
	}
}

func TestCloudSectionTemplateSeedsDraftAndIsNew(t *testing.T) {
	sp := cloudSamplePage()
	sp.selectCloudRow("template:gemini")
	if !sp.cloudDraftNew {
		t.Error("selecting a template is a new draft")
	}
	if sp.cloudDraft.Name != "gemini" || sp.cloudDraft.Backend != "gemini" {
		t.Errorf("template draft wrong: %+v", sp.cloudDraft)
	}
	if sp.cloudDraft.BaseURL != "https://generativelanguage.googleapis.com/v1beta/openai" {
		t.Errorf("template base URL not seeded: %q", sp.cloudDraft.BaseURL)
	}
	// New draft from a template shows a name field; delete is absent.
	sec := sp.buildCloudSection()
	var keys []string
	for _, f := range sec.Fields {
		keys = append(keys, f.Key())
	}
	j := strings.Join(keys, "|")
	if !strings.Contains(j, "cloud-name") {
		t.Errorf("new draft should expose cloud-name: %v", keys)
	}
	if strings.Contains(j, "cloud-delete") {
		t.Errorf("new draft should not expose cloud-delete: %v", keys)
	}
}

func TestCloudSectionComingSoonDisablesActivate(t *testing.T) {
	sp := cloudSamplePage()
	sp.selectCloudRow("template:bedrock")
	sec := sp.buildCloudSection()
	for _, f := range sec.Fields {
		if f.Key() == "cloud-activate" {
			// A disabled ButtonField renders with the Dim style and never commits.
			if _, committed, _ := f.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); committed {
				t.Error("coming-soon activate must be disabled (no commit)")
			}
		}
	}
}
```

Add the import `tea "charm.land/bubbletea/v2"` to the test file's import block (used in the last test).

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd source/clients/cli && go test ./internal/ui/ -run 'CloudSection' -count=1
```
Expected: FAIL — `buildCloudSection` / `selectCloudRow` / new fields undefined.

- [ ] **Step 3: Add the page-state fields**

In `source/clients/cli/internal/ui/settings_page.go`, add to the `settingsPage` struct (after the `cfg`/`mode` cache block, ~line 47):

```go
	// Cloud provider list + inline detail-editor state. profiles/activeProfile
	// cache GetCloudProfiles like cfg caches GetConfig; profilesLoaded gates the
	// fetch. cloudSelected is the expanded row's ID ("" = none); cloudDraft holds
	// the in-progress edit; cloudDraftNew is true when creating (template/other).
	profiles       []agentclient.CloudProfileInfo
	activeProfile  string
	profilesLoaded bool
	cloudSelected  string
	cloudDraft     cloudDraft
	cloudDraftNew  bool
```

- [ ] **Step 4: Fetch the profiles in `snapshotSections` and append the section**

In `source/clients/cli/internal/ui/settings_page.go`, in `snapshotSections` (after the `sp.cfg == nil` block populates cfg/mode, before `secs := buildSettingsSections(...)` at line 75), add a profiles fetch mirroring the cfg cache:

```go
	if !sp.profilesLoaded && sp.agent != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if profs, active, err := sp.agent.GetCloudProfiles(ctx); err == nil {
			sp.profiles = profs
			sp.activeProfile = active
			sp.profilesLoaded = true
		}
	}
```

Then, where the sections slice is assembled (line 75-80), insert the cloud section right after `buildSettingsSections`:

```go
	secs := buildSettingsSections(sp.cfg, sp.mode, sp.accentToken)
	secs = append(secs, sp.buildCloudSection())
	if sp.themes != nil {
		builtin := sp.themes.IsBuiltin(sp.working.Name)
		secs = append(secs, buildThemeSections(sp.working, sp.themes.Names(), builtin, sp.dirty)...)
	}
	return secs
```

- [ ] **Step 5: Remove the legacy Cloud section**

In `source/clients/cli/internal/ui/settings_build.go`, delete the entire `{Title: "Cloud", Fields: []form.Field{...}}` section (lines 32-40) from `buildSettingsSections`. Leave `apiSet := cfg.CloudAPIKeySet` only if still used; it is not used elsewhere, so delete that line too (line 22). The `classifyCommit` cases for `cloud-provider`/`cloud-model`/`cloud-base-url`/`cloud-api-key` become dead — leave them for now (Task 7 owns commit routing) to keep this task's diff focused on rendering.

- [ ] **Step 6: Implement the section builder + row selection**

Create `source/clients/cli/internal/ui/cloud_section.go`:

```go
package ui

import (
	"cercano/source/clients/cli/internal/form"
)

// cloudDraft is the in-progress profile edit backing the detail editor.
type cloudDraft struct {
	Name, Flavor, Backend, BaseURL, Model string
}

// selectCloudRow expands a list row's detail editor and seeds the draft from the
// matching configured profile (edit) or preset template (create).
func (sp *settingsPage) selectCloudRow(rowID string) {
	sp.cloudSelected = rowID
	sp.cloudDraft = cloudDraft{}
	sp.cloudDraftNew = true
	switch {
	case rowID == "other":
		sp.cloudDraft.Flavor = "chat_completions"
	case len(rowID) > 8 && rowID[:8] == "profile:":
		name := rowID[8:]
		for _, p := range sp.profiles {
			if p.Name == name {
				sp.cloudDraft = cloudDraft{Name: p.Name, Flavor: p.Flavor, Backend: p.Backend, BaseURL: p.BaseURL, Model: p.Model}
				sp.cloudDraftNew = false
				return
			}
		}
	case len(rowID) > 9 && rowID[:9] == "template:":
		id := rowID[9:]
		for _, pr := range cloudPresets() {
			if pr.ID == id {
				sp.cloudDraft = cloudDraft{Name: pr.ID, Flavor: pr.Flavor, Backend: pr.Backend, BaseURL: pr.BaseURL}
				return
			}
		}
	}
}

// presetByTemplateID resolves the preset for a "template:<id>" row, or nil.
func presetByTemplateID(rowID string) *cloudPreset {
	if len(rowID) <= 9 || rowID[:9] != "template:" {
		return nil
	}
	id := rowID[9:]
	for i, pr := range cloudPresets() {
		if pr.ID == id {
			ps := cloudPresets()[i]
			return &ps
		}
	}
	return nil
}

// buildCloudSection renders the Cloud Providers list with an inline detail editor
// under the selected row.
func (sp *settingsPage) buildCloudSection() form.Section {
	rows := buildCloudRows(cloudPresets(), sp.profiles, sp.activeProfile)
	fields := make([]form.Field, 0, len(rows)+8)
	for _, r := range rows {
		fields = append(fields, form.NewRow("cloud-row:"+r.ID, r.Label, rowAnnotation(r), r.Active))
		if r.ID == sp.cloudSelected {
			fields = append(fields, sp.cloudDetailFields(r)...)
		}
	}
	return form.Section{Title: "Cloud Providers", Fields: fields}
}

// cloudDetailFields are the editor fields shown beneath the selected row.
func (sp *settingsPage) cloudDetailFields(r cloudRow) []form.Field {
	d := sp.cloudDraft
	var out []form.Field
	if sp.cloudDraftNew {
		out = append(out, form.NewText("cloud-name", "name", d.Name, "profile name"))
	} else {
		out = append(out, form.NewReadOnly("cloud-name", "name", d.Name, ""))
	}
	// flavor/backend: editable only for the custom "other" row; read-only otherwise.
	if r.ID == "other" {
		out = append(out,
			form.NewSelect("cloud-flavor", "flavor", []form.Option{
				{Label: "chat_completions", Value: "chat_completions"},
				{Label: "messages", Value: "messages"},
			}, d.Flavor),
			form.NewSelect("cloud-backend", "backend", []form.Option{
				{Label: "default", Value: ""},
				{Label: "openai", Value: "openai"},
				{Label: "gemini", Value: "gemini"},
				{Label: "groq", Value: "groq"},
			}, d.Backend),
		)
	} else {
		out = append(out, form.NewReadOnly("cloud-flavor", "flavor", d.Flavor, ""))
		if d.Flavor == "chat_completions" {
			be := d.Backend
			if be == "" {
				be = "default"
			}
			out = append(out, form.NewReadOnly("cloud-backend", "backend", be, ""))
		}
	}
	out = append(out,
		form.NewText("cloud-base-url", "base-url", d.BaseURL, "https://…"),
		form.NewText("cloud-model", "model", d.Model, "model id"),
		form.NewMasked("cloud-key", "api-key", sp.draftHasKey(r)),
		form.NewButton("cloud-save", "save", true),
		form.NewButton("cloud-activate", "activate", !r.ComingSoon),
	)
	if !sp.cloudDraftNew {
		out = append(out, form.NewButton("cloud-delete", "delete", true))
	}
	return out
}

// draftHasKey reports whether the row's profile already has a stored key (drives
// the masked field's "(stored)" vs "(not set)" hint).
func (sp *settingsPage) draftHasKey(r cloudRow) bool {
	return r.IsProfile && r.HasKey
}
```

Note: confirm `form.NewMasked`'s signature in `source/clients/cli/internal/form/text_field.go` — the legacy section called `form.NewMasked("cloud-api-key", "cloud-api-key", apiSet)`, i.e. `NewMasked(key, label string, isSet bool)`. Match it.

- [ ] **Step 7: Run the tests to verify they pass**

```bash
cd source/clients/cli && go test ./internal/ui/ -run 'CloudSection' -count=1
```
Expected: PASS.

- [ ] **Step 8: Verify the full UI package + binary build**

```bash
cd source/clients/cli && go test ./internal/ui/ -count=1 && go build -o bin/cercano-cli .
```
Expected: PASS + clean build. (If `settings_build_test.go` or `settings_layout_test.go` asserts on the removed legacy "Cloud" section, update those assertions to the new "Cloud Providers" section — the legacy provider/model/base-url/api-key fields no longer exist.)

- [ ] **Step 9: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/clients/cli/internal/ui/cloud_section.go source/clients/cli/internal/ui/cloud_section_test.go source/clients/cli/internal/ui/settings_page.go source/clients/cli/internal/ui/settings_build.go source/clients/cli/internal/ui/settings_build_test.go source/clients/cli/internal/ui/settings_layout_test.go
git commit -m "feat(cli): Cloud Providers settings section (list + detail render)"
```

---

### Task 7: ui — Cloud detail commit routing (RPC dispatch)

**Files:**
- Create: `source/clients/cli/internal/ui/cloud_commit.go`
- Modify: `source/clients/cli/internal/ui/settings_page.go` (`onCommit` — route `cloud-row:*` and `cloud-*` keys)
- Test: `source/clients/cli/internal/ui/cloud_commit_test.go`

**Interfaces:**
- Consumes: `form.RowSelect`, `form.ButtonActivate`; `sp.cloudDraft`, `sp.cloudSelected`, `sp.selectCloudRow` (Task 6); `agentclient.CloudProfileInfo`.
- Produces:
  - `type cloudCommitKind int` with `cloudCommitNone, cloudCommitSelect, cloudCommitDraftEdit, cloudCommitSave, cloudCommitActivate, cloudCommitDelete, cloudCommitKey`.
  - `type cloudCommitAction struct { kind cloudCommitKind; rowID string; field string; value string }`.
  - `func classifyCloudCommit(key, value string) cloudCommitAction` — pure routing: `cloud-row:<id>` + `RowSelect` → select; `cloud-name|cloud-flavor|cloud-backend|cloud-base-url|cloud-model` → draft edit; `cloud-key` → key; `cloud-save`/`cloud-activate`/`cloud-delete` + `ButtonActivate` → save/activate/delete.
  - `func (sp *settingsPage) applyCloudDraftEdit(field, value string)` — write one field into `sp.cloudDraft`.
  - `onCommit` handles cloud keys by dispatching the RPC and invalidating the profiles cache.

- [ ] **Step 1: Write the failing tests**

Create `source/clients/cli/internal/ui/cloud_commit_test.go`:

```go
package ui

import (
	"testing"

	"cercano/source/clients/cli/internal/form"
)

func TestClassifyCloudCommit(t *testing.T) {
	if a := classifyCloudCommit("cloud-row:template:gemini", form.RowSelect); a.kind != cloudCommitSelect || a.rowID != "template:gemini" {
		t.Errorf("row select misrouted: %+v", a)
	}
	if a := classifyCloudCommit("cloud-base-url", "https://x"); a.kind != cloudCommitDraftEdit || a.field != "cloud-base-url" || a.value != "https://x" {
		t.Errorf("draft edit misrouted: %+v", a)
	}
	if a := classifyCloudCommit("cloud-key", "sk-1"); a.kind != cloudCommitKey || a.value != "sk-1" {
		t.Errorf("key misrouted: %+v", a)
	}
	if a := classifyCloudCommit("cloud-save", form.ButtonActivate); a.kind != cloudCommitSave {
		t.Errorf("save misrouted: %+v", a)
	}
	if a := classifyCloudCommit("cloud-activate", form.ButtonActivate); a.kind != cloudCommitActivate {
		t.Errorf("activate misrouted: %+v", a)
	}
	if a := classifyCloudCommit("cloud-delete", form.ButtonActivate); a.kind != cloudCommitDelete {
		t.Errorf("delete misrouted: %+v", a)
	}
	if a := classifyCloudCommit("local-model", "x"); a.kind != cloudCommitNone {
		t.Errorf("non-cloud key should be cloudCommitNone: %+v", a)
	}
}

func TestApplyCloudDraftEdit(t *testing.T) {
	sp := cloudSamplePage()
	sp.selectCloudRow("template:gemini")
	sp.applyCloudDraftEdit("cloud-model", "gemini-x")
	sp.applyCloudDraftEdit("cloud-base-url", "https://custom")
	sp.applyCloudDraftEdit("cloud-name", "my-gemini")
	if sp.cloudDraft.Model != "gemini-x" || sp.cloudDraft.BaseURL != "https://custom" || sp.cloudDraft.Name != "my-gemini" {
		t.Fatalf("draft edits not applied: %+v", sp.cloudDraft)
	}
}

func TestSelectCommitExpandsRow(t *testing.T) {
	sp := cloudSamplePage()
	// onCommit for a row-select sets cloudSelected and reloads.
	status, _, err := sp.onCommit("cloud-row:template:groq", form.RowSelect)
	if err != nil {
		t.Fatal(err)
	}
	if sp.cloudSelected != "template:groq" {
		t.Fatalf("row select did not expand groq: %q", sp.cloudSelected)
	}
	_ = status
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd source/clients/cli && go test ./internal/ui/ -run 'ClassifyCloudCommit|ApplyCloudDraftEdit|SelectCommitExpandsRow' -count=1
```
Expected: FAIL — undefined symbols / `onCommit` doesn't handle cloud keys.

- [ ] **Step 3: Implement the routing core**

Create `source/clients/cli/internal/ui/cloud_commit.go`:

```go
package ui

import (
	"strings"

	"cercano/source/clients/cli/internal/form"
)

type cloudCommitKind int

const (
	cloudCommitNone cloudCommitKind = iota
	cloudCommitSelect
	cloudCommitDraftEdit
	cloudCommitSave
	cloudCommitActivate
	cloudCommitDelete
	cloudCommitKey
)

type cloudCommitAction struct {
	kind  cloudCommitKind
	rowID string
	field string
	value string
}

// classifyCloudCommit maps a committed (key,value) from the Cloud Providers
// section to an action. Returns cloudCommitNone for non-cloud keys.
func classifyCloudCommit(key, value string) cloudCommitAction {
	if strings.HasPrefix(key, "cloud-row:") {
		return cloudCommitAction{kind: cloudCommitSelect, rowID: strings.TrimPrefix(key, "cloud-row:")}
	}
	switch key {
	case "cloud-name", "cloud-flavor", "cloud-backend", "cloud-base-url", "cloud-model":
		return cloudCommitAction{kind: cloudCommitDraftEdit, field: key, value: value}
	case "cloud-key":
		return cloudCommitAction{kind: cloudCommitKey, value: value}
	case "cloud-save":
		return cloudCommitAction{kind: cloudCommitSave}
	case "cloud-activate":
		return cloudCommitAction{kind: cloudCommitActivate}
	case "cloud-delete":
		return cloudCommitAction{kind: cloudCommitDelete}
	}
	return cloudCommitAction{kind: cloudCommitNone}
}

// applyCloudDraftEdit writes one committed detail field into the draft.
func (sp *settingsPage) applyCloudDraftEdit(field, value string) {
	switch field {
	case "cloud-name":
		sp.cloudDraft.Name = value
	case "cloud-flavor":
		sp.cloudDraft.Flavor = value
	case "cloud-backend":
		sp.cloudDraft.Backend = value
	case "cloud-base-url":
		sp.cloudDraft.BaseURL = value
	case "cloud-model":
		sp.cloudDraft.Model = value
	}
	_ = form.RowSelect // keep the form import meaningful if unused otherwise
}
```

Remove the `_ = form.RowSelect` line if `form` is otherwise referenced in the file (it is, via `classifyCloudCommit` uses only string constants — actually `form` is not referenced by value there; keep the import only if used. If `go build` reports `form` imported and not used, delete the import and the `_ = form.RowSelect` line.)

- [ ] **Step 4: Route cloud keys in `onCommit`**

In `source/clients/cli/internal/ui/settings_page.go`, at the top of `onCommit` (before the `color:` prefix check at line 150), add the cloud dispatch:

```go
	if ca := classifyCloudCommit(key, value); ca.kind != cloudCommitNone {
		return sp.commitCloud(ca)
	}
```

Then add the `commitCloud` method (in `cloud_commit.go`), using the agent client and invalidating the profiles cache after a mutation:

```go
// commitCloud executes a cloud-section action and returns the form status, an
// optional tea.Cmd, and an error. Profile mutations invalidate the cache so the
// next snapshot re-fetches.
func (sp *settingsPage) commitCloud(ca cloudCommitAction) (string, tea.Cmd, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch ca.kind {
	case cloudCommitSelect:
		sp.selectCloudRow(ca.rowID)
		return "", nil, nil
	case cloudCommitDraftEdit:
		sp.applyCloudDraftEdit(ca.field, ca.value)
		return "", nil, nil
	case cloudCommitSave:
		if sp.agent == nil {
			return "no agent", nil, nil
		}
		d := sp.cloudDraft
		err := sp.agent.UpsertCloudProfile(ctx, agentclient.CloudProfileInfo{
			Name: d.Name, Flavor: d.Flavor, Backend: d.Backend, BaseURL: d.BaseURL, Model: d.Model,
		})
		if err != nil {
			return "", nil, err
		}
		sp.profilesLoaded = false
		sp.cloudSelected = "profile:" + d.Name
		sp.cloudDraftNew = false
		return "saved " + d.Name, nil, nil
	case cloudCommitActivate:
		if sp.agent == nil {
			return "no agent", nil, nil
		}
		if err := sp.agent.SetActiveCloudProfile(ctx, sp.cloudDraft.Name); err != nil {
			return "", nil, err
		}
		sp.profilesLoaded = false
		return "active: " + sp.cloudDraft.Name, nil, nil
	case cloudCommitDelete:
		if sp.agent == nil {
			return "no agent", nil, nil
		}
		name := sp.cloudDraft.Name
		if err := sp.agent.RemoveCloudProfile(ctx, name); err != nil {
			return "", nil, err
		}
		sp.profilesLoaded = false
		sp.cloudSelected = ""
		return "deleted " + name, nil, nil
	case cloudCommitKey:
		if sp.agent == nil {
			return "no agent", nil, nil
		}
		if err := sp.agent.SetCloudProfileKey(ctx, sp.cloudDraft.Name, ca.value); err != nil {
			return "", nil, err
		}
		sp.profilesLoaded = false
		return "key stored for " + sp.cloudDraft.Name, nil, nil
	}
	return "", nil, nil
}
```

Add imports to `cloud_commit.go` as needed: `context`, `time`, `tea "charm.land/bubbletea/v2"`, `"cercano/source/server/pkg/agentclient"`.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
cd source/clients/cli && go test ./internal/ui/ -run 'ClassifyCloudCommit|ApplyCloudDraftEdit|SelectCommitExpandsRow' -count=1
```
Expected: PASS.

- [ ] **Step 6: Remove the now-dead legacy cloud commit routing**

In `source/clients/cli/internal/ui/settings_build.go`, delete the `classifyCommit` cases `cloud-provider`, `cloud-model`, `cloud-base-url`, `cloud-api-key` (lines 87-94) — the legacy section that produced those keys is gone (Task 6). Keep the `ConfigUpdate` struct usage for the remaining local-model keys.

- [ ] **Step 7: Verify the full UI package + binary build + full CLI suite**

```bash
cd source/clients/cli && go test ./... -count=1 && go build -o bin/cercano-cli .
```
Expected: all packages PASS + clean build.

- [ ] **Step 8: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add source/clients/cli/internal/ui/cloud_commit.go source/clients/cli/internal/ui/cloud_commit_test.go source/clients/cli/internal/ui/settings_page.go source/clients/cli/internal/ui/settings_build.go
git commit -m "feat(cli): wire Cloud Providers detail editor to profile CRUD RPCs"
```

---

### Task 8: Docs — CLI provider wireup surface

**Files:**
- Modify: `source/.../docs/agent/cloud-profiles.md` → `docs/agent/cloud-profiles.md` (the "Runtime management" §5 and "CLI commands" sections mention RPC/CLI surface)
- Modify: `docs/agent/llm-backend-notes.md` (note the settings-page wireup exists; conformance matrix unchanged)

**Interfaces:** none (docs only).

- [ ] **Step 1: Document the settings-page wireup in cloud-profiles.md**

In `docs/agent/cloud-profiles.md`, under "### CLI commands", add a short subsection after the command table:

```markdown
### Settings page (cercano-cli)

The `/s` settings page has a **Cloud Providers** section: a vertical list of your
configured profiles plus known-provider templates (anthropic, openai, gemini,
groq, deepinfra, together, openrouter, deepseek) and `+ other` for any
OpenAI-compatible endpoint. Selecting a row opens an inline editor for its
base URL, model, and API key (stored in the OS keychain), with save / activate /
delete actions. Untested backends are labeled `(untested)`; flavors not yet
implemented (`bedrock`, the OpenAI Responses API) are labeled `(coming soon)`
and cannot be activated. Backed by the `UpsertCloudProfile` / `RemoveCloudProfile`
/ `SetActiveCloudProfile` / `SetCloudProfileKey` RPCs.
```

- [ ] **Step 2: Cross-reference from llm-backend-notes.md**

In `docs/agent/llm-backend-notes.md`, at the end of the "## Conformance matrix" section (after the Gemini note line 22), add:

```markdown
The cercano-cli settings page wires these up (`/s` → Cloud Providers); untested
rows there carry an `(untested)` label that mirrors the `—` cells above.
```

- [ ] **Step 3: Commit**

```bash
cd /Users/bryancostanich/git_repos/bryan_costanich/Cercano
git add docs/agent/cloud-profiles.md docs/agent/llm-backend-notes.md
git commit -m "docs(agent): document CLI provider wireup surface"
```

---

## Self-Review

**1. Spec coverage:**
- §1 server CRUD RPCs → Tasks 1-3. ✅
- §2 vertical list + inline detail, status indicators → Tasks 4-6. ✅
- §3 preset table + label tiers + coming-soon activate-block → Tasks 5, 6 (`ComingSoon` disables activate button). ✅
- §4 components/files → Tasks 1-7 map to the named files. ✅
- §5 data flow (fetch + cache + invalidation) → Task 6 (fetch/cache), Task 7 (`profilesLoaded=false` after mutations). ✅
- §6 error handling (status line) → Task 7 returns errors through `onCommit`, which the form renders on its status line. ✅
- §7 testing → server CRUD (Task 2), CLI merge/render/route (Tasks 5-7). agentclient wrappers verified by build per repo convention (noted in Task 3). ✅
- §8 out-of-scope honored: no `responses`/`bedrock` impl, no rename, no key-in-yaml.

**2. Placeholder scan:** No TBD/TODO; each code step has full code; each test step has real assertions. The only conditional guidance is the documented "if `go build` says X, do Y" fallbacks for environment-specific symbols (`tea.KeySpace`, unused `form` import, protoc version header) — these are explicit, not placeholders.

**3. Type consistency:** `cloudDraft`, `cloudRow`, `cloudPreset`, `cloudTier`, `cloudCommitAction` are defined once (Tasks 5-7) and used consistently. `CloudProfileInfo.Backend` is added in Task 3 and consumed in Tasks 5-7. Row ID scheme `profile:<name>` / `template:<id>` / `other` is consistent across `buildCloudRows`, `selectCloudRow`, `classifyCloudCommit`. Detail field keys (`cloud-name`, `cloud-flavor`, `cloud-backend`, `cloud-base-url`, `cloud-model`, `cloud-key`, `cloud-save`, `cloud-activate`, `cloud-delete`) match between the builder (Task 6) and the router (Task 7).
