# Developer Settings Section Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "Developer" settings section with **Watchdog enabled** + **Echo watchdog conversation** toggles that persist to the server config and take effect live.

**Architecture:** Plumb `config.Watchdog.{Enabled,Echo}` through the existing `GetConfig`/`UpdateConfig` gRPC: proto fields (bool response, sparse-string request), agentclient mapping, a server apply that rebuilds the watchdog live, and a CLI Developer section of toggles.

**Tech Stack:** Go 1.26 (`cercano/source/server` + `cercano/source/clients/cli`), gRPC/protobuf (protoc v7.34.1, protoc-gen-go v1.36.11), Bubble Tea TUI.

## Global Constraints

- **Sparse-string patch convention:** `UpdateConfigRequest` string fields use `""`=unchanged, `"true"`/`"false"`=set. Matches every existing field. Unrecognized values are ignored (no error).
- **No deadlock:** `UpdateConfig` holds `s.cfgMu.Lock()` for its whole body; anything it calls there must NOT take `cfgMu` (read or write). The watchdog rebuild inside it must use a lock-free builder.
- **No data race on `s.watchdog`:** it is written under `cfgMu` (startup + `UpdateConfig`) and read per-turn — the per-turn read must be guarded by `cfgMu.RLock()`.
- **Watchdog stays default-OFF.** Rebuild-on-toggle intentionally resets the watchdog's per-conversation state (toggling on/off is a fresh start).
- Commit messages contain no "Claude"; no `Co-Authored-By` trailer. gofmt-clean touched files; `go build ./...` + `go test` green in each affected module before every commit; `git status` clean after each commit.

---

## File Structure

- `source/proto/agent.proto` + regenerated `source/server/pkg/proto/agent.pb.go`.
- `source/server/internal/server/watchdog_wire.go` — extract `buildWatchdogFrom`.
- `source/server/internal/server/server.go` — `GetConfig` populate, `UpdateConfig` apply + live rebuild, guard the per-turn `s.watchdog` read.
- `source/server/pkg/agentclient/client.go` — `Config` + `ConfigUpdate` fields + mapping.
- `source/clients/cli/internal/ui/settings_build.go` — Developer section + `classifyCommit` cases.

---

### Task 1: proto fields (contract + regen)

**Files:**
- Modify: `source/proto/agent.proto`
- Regenerate: `source/server/pkg/proto/agent.pb.go` (via protoc — do NOT hand-edit)

**Interfaces:**
- Produces: `GetConfigResponse.GetWatchdogEnabled()/GetWatchdogEcho()` (bool); `UpdateConfigRequest.GetWatchdogEnabled()/GetWatchdogEcho()` (string).

- [ ] **Step 1: Add the fields.** In `source/proto/agent.proto`:
  - `GetConfigResponse` (after `locus_mode = 11;`):
    ```proto
      bool watchdog_enabled = 12;
      bool watchdog_echo = 13;
    ```
  - `UpdateConfigRequest` (after `locus_mode = 8;`):
    ```proto
      string watchdog_enabled = 9;  // "" = unchanged, "true"/"false"
      string watchdog_echo = 10;    // "" = unchanged, "true"/"false"
    ```

- [ ] **Step 2: Install pinned plugins** (protoc is at `/opt/homebrew/bin/protoc`; the Go plugins may already be installed from the prior increment — install if missing):
  ```bash
  go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
  export PATH="$PATH:$(go env GOPATH)/bin"
  ```

- [ ] **Step 3: Regenerate** from the worktree root:
  ```bash
  protoc -I source/proto \
    --go_out=source/server/pkg/proto --go_opt=paths=source_relative \
    --go-grpc_out=source/server/pkg/proto --go-grpc_opt=paths=source_relative \
    source/proto/agent.proto
  ```
  Verify `git -C <worktree> diff --stat` shows changes only in `agent.proto` and `agent.pb.go` (the 4 new fields + descriptor reflow), `agent_grpc.pb.go` unchanged. If unrelated churn appears (version mismatch), STOP and report BLOCKED.

- [ ] **Step 4: Verify.** `cd source/server && go build ./...` clean.

- [ ] **Step 5: Commit**
  ```bash
  git -C <worktree> commit -am "feat(proto): watchdog enabled/echo config fields"
  ```

### Task 2: config round-trip + live rebuild (agentclient + server)

**Files:**
- Modify: `source/server/internal/server/watchdog_wire.go` (extract `buildWatchdogFrom`)
- Modify: `source/server/internal/server/server.go` (`GetConfig` ~1196, `UpdateConfig` ~638, per-turn read ~1901-1927)
- Modify: `source/server/pkg/agentclient/client.go` (`Config` ~150/181, `ConfigUpdate` ~188, `UpdateConfig` ~789)
- Test: `source/server/internal/server/server_test.go` (mirror `TestUpdateConfig_*`), `pkg/agentclient/*_test.go`

**Interfaces:**
- Consumes: `proto` fields (Task 1); `config.WatchdogConfig`; `watchdog.Watchdog`.
- Produces: `func (s *Server) buildWatchdogFrom(wc config.WatchdogConfig) *watchdog.Watchdog`; `agentclient.Config.{WatchdogEnabled,WatchdogEcho bool}`; `agentclient.ConfigUpdate.{WatchdogEnabled,WatchdogEcho string}`.

- [ ] **Step 1: Extract the lock-free builder.** In `watchdog_wire.go`, split `buildWatchdog` so the config read and the construction are separate:
  ```go
  func (s *Server) buildWatchdog() *watchdog.Watchdog {
      s.cfgMu.RLock()
      wc := s.currentConfig.Watchdog
      s.cfgMu.RUnlock()
      return s.buildWatchdogFrom(wc)
  }

  // buildWatchdogFrom constructs the watchdog from an already-read config. It
  // takes NO lock, so a caller already holding s.cfgMu (e.g. UpdateConfig) can
  // rebuild the watchdog without deadlocking.
  func (s *Server) buildWatchdogFrom(wc config.WatchdogConfig) *watchdog.Watchdog {
      // ... the existing body from `if !wc.Enabled { return nil }` through the
      // `return watchdog.New(...)`, unchanged ...
  }
  ```
  Move everything after the old RLock/RUnlock block into `buildWatchdogFrom`. `InitWatchdog` is unchanged (`s.watchdog = s.buildWatchdog()`).

- [ ] **Step 2: Write the failing server test** in `server_test.go` (mirror `TestUpdateConfig_LocalRuntime` for harness). Two behaviors:
  ```go
  func TestUpdateConfig_WatchdogEnable(t *testing.T) {
      s := newTestServer(t) // use whatever constructor the existing UpdateConfig tests use
      // enabling builds a live watchdog
      _, err := s.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{WatchdogEnabled: "true"})
      if err != nil { t.Fatal(err) }
      if !s.currentConfig.Watchdog.Enabled { t.Fatal("Enabled not applied") }
      if s.watchdog == nil { t.Fatal("watchdog not rebuilt/active after enable") }
      // disabling tears it back down
      if _, err := s.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{WatchdogEnabled: "false"}); err != nil { t.Fatal(err) }
      if s.currentConfig.Watchdog.Enabled { t.Fatal("Enabled not cleared") }
      if s.watchdog != nil { t.Fatal("watchdog should be nil after disable") }
  }

  func TestUpdateConfig_WatchdogEcho_and_GetConfig(t *testing.T) {
      s := newTestServer(t)
      if _, err := s.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{WatchdogEcho: "true"}); err != nil { t.Fatal(err) }
      if !s.currentConfig.Watchdog.Echo { t.Fatal("Echo not applied") }
      resp, err := s.GetConfig(context.Background(), &proto.GetConfigRequest{})
      if err != nil { t.Fatal(err) }
      if !resp.GetWatchdogEcho() { t.Fatal("GetConfig did not report echo") }
  }
  ```
  (If the existing tests use a different server constructor/helper, use that; the watchdog OneShot needs `s.dispatchEngine` set — mirror how `watchdog_wire_test.go`'s enabled test sets a dispatch engine.)

- [ ] **Step 3: Server GetConfig** (`server.go` ~1196) — add to the returned `&proto.GetConfigResponse{...}` (GetConfig reads `currentConfig`; keep it consistent with the existing reads there — if GetConfig takes `cfgMu.RLock`, these reads are already covered):
  ```go
      WatchdogEnabled: s.currentConfig.Watchdog.Enabled,
      WatchdogEcho:    s.currentConfig.Watchdog.Echo,
  ```

- [ ] **Step 4: Server UpdateConfig** (`server.go` ~638, inside the held `cfgMu.Lock()` body, alongside the other `if req.X != ""` blocks, before the final persist/return):
  ```go
      watchdogChanged := false
      if req.WatchdogEnabled != "" {
          s.currentConfig.Watchdog.Enabled = req.WatchdogEnabled == "true"
          changes = append(changes, fmt.Sprintf("watchdog_enabled=%s", req.WatchdogEnabled))
          watchdogChanged = true
      }
      if req.WatchdogEcho != "" {
          s.currentConfig.Watchdog.Echo = req.WatchdogEcho == "true"
          changes = append(changes, fmt.Sprintf("watchdog_echo=%s", req.WatchdogEcho))
          watchdogChanged = true
      }
      if watchdogChanged {
          // Rebuild the supervisor from the just-applied config. buildWatchdogFrom
          // takes NO lock, so this is safe under the held cfgMu write lock.
          s.watchdog = s.buildWatchdogFrom(s.currentConfig.Watchdog)
      }
  ```
  Ensure the handler still persists (it appends to `changes` and persists/returns at the end — the watchdog change rides that existing persist; if the handler only persists when `len(changes) > 0`, appending to `changes` above already triggers it. Confirm by reading the handler's tail).

- [ ] **Step 5: Guard the per-turn `s.watchdog` read** (`server.go` ~1901-1907). The block currently reads `s.watchdog` (nil-check) and snapshots `wd := s.watchdog` without a lock. Wrap the snapshot in an RLock and use `wd` everywhere below (including the `s.watchdog.SetEcho(...)` call ~1927 → `wd.SetEcho(...)`):
  ```go
      s.cfgMu.RLock()
      wd := s.watchdog
      s.cfgMu.RUnlock()
      if wd != nil {
          // ... use wd (not s.watchdog) throughout this block, incl. wd.SetEcho(...) ...
      }
  ```

- [ ] **Step 6: agentclient mapping** (`client.go`):
  - `Config` struct (~150): add `WatchdogEnabled bool` and `WatchdogEcho bool`.
  - In `GetConfig` (~181, the `&Config{...}` literal): add `WatchdogEnabled: resp.GetWatchdogEnabled(), WatchdogEcho: resp.GetWatchdogEcho(),`.
  - `ConfigUpdate` struct (~188): add `WatchdogEnabled string` and `WatchdogEcho string`.
  - In `UpdateConfig` (~789, the `&proto.UpdateConfigRequest{...}` literal): add `WatchdogEnabled: u.WatchdogEnabled, WatchdogEcho: u.WatchdogEcho,`.

- [ ] **Step 7: Write the failing agentclient test** (mirror an existing `client_test.go` mapping test): a `GetConfigResponse{WatchdogEcho: true}` → `Config.WatchdogEcho == true`; a `ConfigUpdate{WatchdogEnabled: "true"}` maps to `UpdateConfigRequest.WatchdogEnabled == "true"`. (If the client mapping isn't unit-testable without a live server, assert the struct field wiring directly.)

- [ ] **Step 8: Verify + commit.** `cd source/server && go test ./internal/server/ ./pkg/agentclient/ ./internal/watchdog/ -count=1` green; `go test ./internal/server/ -run Watchdog -race -count=1` (race check on the new guarded read); `gofmt -l` clean on touched files; `go build ./...` clean.
  ```bash
  git -C <worktree> commit -am "feat(server): watchdog enabled/echo config round-trip with live rebuild"
  ```

### Task 3: CLI Developer section

**Files:**
- Modify: `source/clients/cli/internal/ui/settings_build.go`
- Test: `source/clients/cli/internal/ui/settings_build_test.go` (create) or the existing settings test file

**Interfaces:**
- Consumes: `agentclient.Config.{WatchdogEnabled,WatchdogEcho}` (Task 2); `form.NewToggle`; `agentclient.ConfigUpdate.{WatchdogEnabled,WatchdogEcho}`.

- [ ] **Step 1: Write the failing test** in `settings_build_test.go`:
  ```go
  package ui

  import (
      "testing"

      "cercano/source/server/pkg/agentclient"
  )

  func TestDeveloperSectionPresent(t *testing.T) {
      cfg := &agentclient.Config{WatchdogEnabled: true, WatchdogEcho: false}
      secs := buildSettingsSections(cfg, "permissive", "palette:accent")
      var dev *struct{ found bool }
      found := false
      for _, s := range secs {
          if s.Title == "Developer" {
              found = true
              if len(s.Fields) != 2 {
                  t.Fatalf("Developer section: want 2 fields, got %d", len(s.Fields))
              }
              if s.Fields[0].Key() != "watchdog-enabled" || s.Fields[1].Key() != "watchdog-echo" {
                  t.Fatalf("unexpected field keys: %s, %s", s.Fields[0].Key(), s.Fields[1].Key())
              }
          }
      }
      _ = dev
      if !found {
          t.Fatal("no Developer section")
      }
  }

  func TestClassifyCommit_Watchdog(t *testing.T) {
      a := classifyCommit("watchdog-enabled", "true")
      if a.kind != commitConfig || a.update.WatchdogEnabled != "true" {
          t.Fatalf("watchdog-enabled: %+v", a)
      }
      b := classifyCommit("watchdog-echo", "false")
      if b.kind != commitConfig || b.update.WatchdogEcho != "false" {
          t.Fatalf("watchdog-echo: %+v", b)
      }
  }
  ```
  (If `form.Field` has no `Key()` accessor, use whatever the existing tests use to identify fields — read `settings_page.go`/a form test first.)

- [ ] **Step 2: Run; fail.** `cd source/clients/cli && go test ./internal/ui/ -run 'DeveloperSection|ClassifyCommit_Watchdog' -v`.

- [ ] **Step 3: Add the Developer section.** In `settings_build.go` `buildSettingsSections`, append a section to the returned slice (after "Server"):
  ```go
      {Title: "Developer", Fields: []form.Field{
          form.NewToggle("watchdog-enabled", "watchdog-enabled", cfg.WatchdogEnabled),
          form.NewToggle("watchdog-echo", "watchdog-echo", cfg.WatchdogEcho),
      }},
  ```

- [ ] **Step 4: Route the commits.** In `classifyCommit`, add before the `default:`:
  ```go
      case "watchdog-enabled":
          u.WatchdogEnabled = value
      case "watchdog-echo":
          u.WatchdogEcho = value
  ```
  (These fall through to the trailing `return commitAction{kind: commitConfig, update: u}` like the other config keys.)

- [ ] **Step 5: Run; pass.** `go test ./internal/ui/ -run 'DeveloperSection|ClassifyCommit_Watchdog' -v` PASS.

- [ ] **Step 6: Verify + commit.** `cd source/clients/cli && go build ./... && go test ./... -count=1` green; `gofmt -l internal/ui/` shows none of the touched files.
  ```bash
  git -C <worktree> commit -am "feat(cli): Developer settings section with watchdog enable + echo toggles"
  ```

---

## Self-Review

- **Spec coverage:** proto (T1); agentclient + server GetConfig/UpdateConfig + live rebuild + race-guarded read (T2); CLI Developer section + commit routing (T3). Sparse-string convention (T1/T2). Deadlock avoided via `buildWatchdogFrom` (T2 S1/S4). Race avoided via guarded read (T2 S5) + a `-race` test (T2 S8). Rebuild-on-toggle resets state intentionally (Global Constraints).
- **Placeholder scan:** the soft spots are the test-harness constructors (`newTestServer`, the form field accessor) — mitigated by "mirror the existing `TestUpdateConfig_*` / form tests"; all production code is complete and exact.
- **Type consistency:** `buildWatchdogFrom(config.WatchdogConfig)` (T2) consistent; `Config.{WatchdogEnabled,WatchdogEcho} bool` + `ConfigUpdate.{WatchdogEnabled,WatchdogEcho} string` used identically in agentclient mapping (T2) and CLI (`classifyCommit` sets `u.WatchdogEnabled/Echo` strings, T3); proto getters `GetWatchdogEnabled()`(bool response / string request — distinct messages, no clash). Toggle commits `"true"/"false"` (verified) matching the string patch.
- **Dependency order:** T1 (proto) → T2 (needs generated fields) → T3 (needs `agentclient.Config`/`ConfigUpdate` fields from T2).
