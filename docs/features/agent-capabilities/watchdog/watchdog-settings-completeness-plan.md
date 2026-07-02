# Watchdog Settings Completeness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose the watchdog `mode`, per-check on/off, and `escalate_after` in the Developer settings section, persisted + applied live.

**Architecture:** Extends the dev-settings config path (`GetConfig`/`UpdateConfig` gRPC) with three fields, reusing the existing deadlock-safe `buildWatchdogFrom` rebuild and race-guarded `s.watchdog` snapshot. The `Checks` list round-trips as a comma-joined string with a `"-"` empty sentinel.

**Tech Stack:** Go 1.26 (`cercano/source/server` + `cercano/source/clients/cli`), gRPC/protobuf (protoc v7.34.1, protoc-gen-go v1.36.11), Bubble Tea TUI.

## Global Constraints

- **Sparse-string patch:** `UpdateConfigRequest` string fields — `""` = unchanged. `watchdog_checks`: `""` = unchanged, `"-"` = empty list, else comma-split. `watchdog_mode`: applied only if `"challenge-and-justify"`/`"strict"`. `watchdog_escalate_after`: applied only on a valid `>= 1` int parse. Invalid values are ignored (tolerant), never an error.
- **Reuse, don't re-solve concurrency:** the watchdog rebuild uses the existing `watchdogChanged` flag → `s.watchdog = s.buildWatchdogFrom(...)` under the held `cfgMu` write lock; the per-turn read stays the guarded snapshot. No new concurrency surface.
- **Watchdog stays default-OFF.**
- Commit messages contain no "Claude"; no `Co-Authored-By`. gofmt-clean touched files; `go build ./...` + `go test` green before each commit; `git status` clean after.

---

## File Structure

- `source/proto/agent.proto` + regenerated `source/server/pkg/proto/agent.pb.go`.
- `source/server/internal/server/server.go` — `GetConfig` emit, `UpdateConfig` apply (in the existing watchdog block).
- `source/server/pkg/agentclient/client.go` — `Config` + `ConfigUpdate` fields + mapping.
- `source/clients/cli/internal/ui/settings_build.go` — Developer section fields + `classifyCommit`.
- `source/clients/cli/internal/ui/settings_page.go` — pass current checks to `classifyCommit` (1 line, ~190).

---

### Task 1: proto fields (contract + regen)

**Files:**
- Modify: `source/proto/agent.proto`
- Regenerate: `source/server/pkg/proto/agent.pb.go` (via protoc — do NOT hand-edit)

**Interfaces:**
- Produces getters: `GetConfigResponse.GetWatchdogMode()/GetWatchdogChecks()/GetWatchdogEscalateAfter()` (string); same on `UpdateConfigRequest`.

- [ ] **Step 1: Add fields.** In `source/proto/agent.proto`:
  - `GetConfigResponse` (after `watchdog_echo = 13;`):
    ```proto
      string watchdog_mode = 14;
      string watchdog_checks = 15;          // comma-joined
      string watchdog_escalate_after = 16;  // int as string
    ```
  - `UpdateConfigRequest` (after `watchdog_echo = 10;`):
    ```proto
      string watchdog_mode = 11;            // "" = unchanged; "challenge-and-justify"|"strict"
      string watchdog_checks = 12;          // "" = unchanged; "-" = empty; else comma-joined
      string watchdog_escalate_after = 13;  // "" = unchanged; positive int
    ```

- [ ] **Step 2: Regenerate** (plugins likely already installed from prior increments; install `protoc-gen-go@v1.36.11` if `which protoc-gen-go` is empty). From the worktree root:
  ```bash
  protoc -I source/proto \
    --go_out=source/server/pkg/proto --go_opt=paths=source_relative \
    --go-grpc_out=source/server/pkg/proto --go-grpc_opt=paths=source_relative \
    source/proto/agent.proto
  ```
  Verify `git -C <worktree> diff --stat` shows changes only in `agent.proto` + `agent.pb.go` (6 new fields + reflow); `agent_grpc.pb.go` unchanged. If unrelated churn appears, STOP + report BLOCKED.

- [ ] **Step 3: Verify.** `cd source/server && go build ./...` clean.

- [ ] **Step 4: Commit**
  ```bash
  git -C <worktree> commit -am "feat(proto): watchdog mode/checks/escalate_after config fields"
  ```

### Task 2: config round-trip + apply (agentclient + server)

**Files:**
- Modify: `source/server/pkg/agentclient/client.go` (`Config` ~162, `GetConfig` map ~184, `ConfigUpdate` ~201, `UpdateConfig` map ~804)
- Modify: `source/server/internal/server/server.go` (`GetConfig` response literal; `UpdateConfig` watchdog block ~783-793)
- Test: `source/server/internal/server/server_test.go`, `pkg/agentclient/*_test.go`

**Interfaces:**
- Consumes: proto getters (Task 1); `config.WatchdogConfig.{Mode,Checks,EscalateAfter}`.
- Produces: `agentclient.Config.{WatchdogMode string, WatchdogChecks []string, WatchdogEscalateAfter int}`; `agentclient.ConfigUpdate.{WatchdogMode, WatchdogChecks, WatchdogEscalateAfter string}`.

- [ ] **Step 1: Write the failing server test** (mirror the dev-settings `TestUpdateConfig_Watchdog*` harness; the watchdog OneShot needs `s.dispatchEngine` when enabled):
  ```go
  func TestUpdateConfig_WatchdogModeChecksEscalate(t *testing.T) {
      s := newTestServer(t) // same constructor the existing watchdog tests use
      _, err := s.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{
          WatchdogMode: "strict", WatchdogEscalateAfter: "3", WatchdogChecks: "debug-loop,plain-english",
      })
      if err != nil { t.Fatal(err) }
      w := s.currentConfig.Watchdog
      if w.Mode != "strict" { t.Fatalf("mode=%q", w.Mode) }
      if w.EscalateAfter != 3 { t.Fatalf("escalate=%d", w.EscalateAfter) }
      if strings.Join(w.Checks, ",") != "debug-loop,plain-english" { t.Fatalf("checks=%v", w.Checks) }
      // "-" sentinel → empty list
      if _, err := s.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{WatchdogChecks: "-"}); err != nil { t.Fatal(err) }
      if len(s.currentConfig.Watchdog.Checks) != 0 { t.Fatalf("expected empty checks, got %v", s.currentConfig.Watchdog.Checks) }
      // invalid escalate ignored
      before := s.currentConfig.Watchdog.EscalateAfter
      if _, err := s.UpdateConfig(context.Background(), &proto.UpdateConfigRequest{WatchdogEscalateAfter: "nope"}); err != nil { t.Fatal(err) }
      if s.currentConfig.Watchdog.EscalateAfter != before { t.Fatal("invalid escalate must be ignored") }
      // GetConfig reports them
      resp, err := s.GetConfig(context.Background(), &proto.GetConfigRequest{})
      if err != nil { t.Fatal(err) }
      if resp.GetWatchdogMode() != "strict" || resp.GetWatchdogEscalateAfter() != "3" {
          t.Fatalf("GetConfig: mode=%q escalate=%q", resp.GetWatchdogMode(), resp.GetWatchdogEscalateAfter())
      }
  }
  ```

- [ ] **Step 2: Server `UpdateConfig`** — in the watchdog block (~783, after the `watchdog_echo` `if`, before `if watchdogChanged`), add:
  ```go
      if req.WatchdogMode == "challenge-and-justify" || req.WatchdogMode == "strict" {
          s.currentConfig.Watchdog.Mode = req.WatchdogMode
          changes = append(changes, "watchdog_mode="+req.WatchdogMode)
          watchdogChanged = true
      }
      if req.WatchdogEscalateAfter != "" {
          if n, err := strconv.Atoi(req.WatchdogEscalateAfter); err == nil && n >= 1 {
              s.currentConfig.Watchdog.EscalateAfter = n
              changes = append(changes, fmt.Sprintf("watchdog_escalate_after=%d", n))
              watchdogChanged = true
          }
      }
      if req.WatchdogChecks != "" {
          if req.WatchdogChecks == "-" {
              s.currentConfig.Watchdog.Checks = []string{}
          } else {
              parts := strings.Split(req.WatchdogChecks, ",")
              checks := make([]string, 0, len(parts))
              for _, p := range parts {
                  if t := strings.TrimSpace(p); t != "" {
                      checks = append(checks, t)
                  }
              }
              s.currentConfig.Watchdog.Checks = checks
          }
          changes = append(changes, "watchdog_checks="+req.WatchdogChecks)
          watchdogChanged = true
      }
  ```
  Ensure `strconv` is imported in `server.go` (add to the import block if missing; `strings`/`fmt` are already used).

- [ ] **Step 3: Server `GetConfig`** — add to the returned `&proto.GetConfigResponse{...}` (next to the dev-settings `WatchdogEnabled/Echo`):
  ```go
      WatchdogMode:          s.currentConfig.Watchdog.Mode,
      WatchdogChecks:        strings.Join(s.currentConfig.Watchdog.Checks, ","),
      WatchdogEscalateAfter: strconv.Itoa(s.currentConfig.Watchdog.EscalateAfter),
  ```

- [ ] **Step 4: agentclient** (`client.go`):
  - `Config` (~162): add `WatchdogMode string`, `WatchdogChecks []string`, `WatchdogEscalateAfter int`.
  - `GetConfig` map (~184): add
    ```go
        WatchdogMode:          resp.GetWatchdogMode(),
        WatchdogChecks:        splitChecks(resp.GetWatchdogChecks()),
        WatchdogEscalateAfter: atoiOr(resp.GetWatchdogEscalateAfter(), 0),
    ```
    and add the two small helpers (in `client.go`):
    ```go
    func splitChecks(s string) []string {
        if strings.TrimSpace(s) == "" {
            return nil
        }
        parts := strings.Split(s, ",")
        out := make([]string, 0, len(parts))
        for _, p := range parts {
            if t := strings.TrimSpace(p); t != "" {
                out = append(out, t)
            }
        }
        return out
    }

    func atoiOr(s string, def int) int {
        if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
            return n
        }
        return def
    }
    ```
    (Ensure `strconv`/`strings` imported in client.go.)
  - `ConfigUpdate` (~201): add `WatchdogMode string`, `WatchdogChecks string`, `WatchdogEscalateAfter string`.
  - `UpdateConfig` map (~804): add
    ```go
        WatchdogMode:          u.WatchdogMode,
        WatchdogChecks:        u.WatchdogChecks,
        WatchdogEscalateAfter: u.WatchdogEscalateAfter,
    ```

- [ ] **Step 5: Write the failing agentclient test** (mirror the dev-settings mapping test): a `GetConfigResponse{WatchdogChecks:"a,b", WatchdogEscalateAfter:"5", WatchdogMode:"strict"}` → `Config{WatchdogChecks:["a","b"], WatchdogEscalateAfter:5, WatchdogMode:"strict"}`; a `ConfigUpdate{WatchdogChecks:"-"}` maps to `UpdateConfigRequest.WatchdogChecks=="-"`.

- [ ] **Step 6: Verify + commit.** `cd source/server && go test ./internal/server/ ./pkg/agentclient/ ./internal/watchdog/ -count=1` green; `gofmt -l` clean on touched files; `go build ./...` clean.
  ```bash
  git -C <worktree> commit -am "feat(server): watchdog mode/checks/escalate config round-trip + live rebuild"
  ```

### Task 3: CLI Developer section fields

**Files:**
- Modify: `source/clients/cli/internal/ui/settings_build.go`
- Modify: `source/clients/cli/internal/ui/settings_page.go` (~line 190)
- Test: `source/clients/cli/internal/ui/settings_build_test.go`

**Interfaces:**
- Consumes: `agentclient.Config.{WatchdogMode,WatchdogChecks,WatchdogEscalateAfter}` + `ConfigUpdate.{WatchdogMode,WatchdogChecks,WatchdogEscalateAfter}` (Task 2); `form.NewSelect/NewToggle/NewText`.
- Produces: `classifyCommit(key, value string, currentChecks []string) commitAction` (signature change).

- [ ] **Step 1: Write the failing test** in `settings_build_test.go`:
  ```go
  func TestDeveloperSectionWatchdogFields(t *testing.T) {
      cfg := &agentclient.Config{
          WatchdogEnabled: true, WatchdogMode: "strict",
          WatchdogChecks: []string{"debug-loop"}, WatchdogEscalateAfter: 2,
      }
      secs := buildSettingsSections(cfg, "permissive", "palette:accent")
      var dev *form.Section
      for i := range secs {
          if secs[i].Title == "Developer" { dev = &secs[i] }
      }
      if dev == nil { t.Fatal("no Developer section") }
      keys := map[string]bool{}
      for _, f := range dev.Fields { keys[f.Key()] = true }
      for _, want := range []string{"watchdog-mode", "watchdog-check-debug-loop", "watchdog-check-commit-checkpoint", "watchdog-check-plain-english", "watchdog-escalate-after"} {
          if !keys[want] { t.Fatalf("missing field %q", want) }
      }
  }

  func TestClassifyCommit_WatchdogModeChecksEscalate(t *testing.T) {
      cur := []string{"debug-loop", "commit-checkpoint", "plain-english"}
      // mode
      if a := classifyCommit("watchdog-mode", "strict", cur); a.kind != commitConfig || a.update.WatchdogMode != "strict" {
          t.Fatalf("mode: %+v", a)
      }
      // escalate
      if a := classifyCommit("watchdog-escalate-after", "4", cur); a.kind != commitConfig || a.update.WatchdogEscalateAfter != "4" {
          t.Fatalf("escalate: %+v", a)
      }
      // turn a check OFF → new full list without it
      a := classifyCommit("watchdog-check-plain-english", "false", cur)
      if a.kind != commitConfig || a.update.WatchdogChecks != "debug-loop,commit-checkpoint" {
          t.Fatalf("check off: %+v", a)
      }
      // turn a check ON when absent → appended (known-order)
      b := classifyCommit("watchdog-check-plain-english", "true", []string{"debug-loop"})
      if b.update.WatchdogChecks != "debug-loop,plain-english" {
          t.Fatalf("check on: %+v", b)
      }
      // last check OFF → "-" sentinel
      c := classifyCommit("watchdog-check-debug-loop", "false", []string{"debug-loop"})
      if c.update.WatchdogChecks != "-" {
          t.Fatalf("empty sentinel: %+v", c)
      }
  }
  ```

- [ ] **Step 2: Run; fail.** `cd source/clients/cli && go test ./internal/ui/ -run 'DeveloperSectionWatchdog|ClassifyCommit_WatchdogMode' -v`.

- [ ] **Step 3: Build the Developer fields** in `settings_build.go`. Add a package-level known-checks list + a membership helper (near the top of the file):
  ```go
  // knownWatchdogChecks must stay in sync with the check-map in
  // internal/server/watchdog_wire.go.
  var knownWatchdogChecks = []string{"debug-loop", "commit-checkpoint", "plain-english"}

  func hasCheck(list []string, name string) bool {
      for _, c := range list {
          if c == name {
              return true
          }
      }
      return false
  }
  ```
  Replace the `{Title: "Developer", Fields: []form.Field{...}}` literal with a computed slice:
  ```go
      devFields := []form.Field{
          form.NewToggle("watchdog-enabled", "watchdog-enabled", cfg.WatchdogEnabled),
          form.NewToggle("watchdog-echo", "watchdog-echo", cfg.WatchdogEcho),
          form.NewSelect("watchdog-mode", "watchdog-mode", []form.Option{
              {Label: "challenge-and-justify", Value: "challenge-and-justify"},
              {Label: "strict", Value: "strict"},
          }, cfg.WatchdogMode),
      }
      for _, name := range knownWatchdogChecks {
          devFields = append(devFields, form.NewToggle("watchdog-check-"+name, "check: "+name, hasCheck(cfg.WatchdogChecks, name)))
      }
      devFields = append(devFields, form.NewText("watchdog-escalate-after", "watchdog-escalate-after", strconv.Itoa(cfg.WatchdogEscalateAfter), ""))
  ```
  and use `{Title: "Developer", Fields: devFields}` in the returned sections. (Add `strconv` to the imports.)

- [ ] **Step 4: Extend `classifyCommit`.** Change the signature to `func classifyCommit(key, value string, currentChecks []string) commitAction`. At the top of the body (before the `switch`), handle the check-toggle prefix:
  ```go
      if name, ok := strings.CutPrefix(key, "watchdog-check-"); ok {
          var u agentclient.ConfigUpdate
          u.WatchdogChecks = encodeChecks(toggleCheck(currentChecks, name, value == "true"))
          return commitAction{kind: commitConfig, update: u}
      }
  ```
  Add the two `case`s inside the existing `switch key` (before `default`):
  ```go
      case "watchdog-mode":
          u.WatchdogMode = value
      case "watchdog-escalate-after":
          u.WatchdogEscalateAfter = value
  ```
  Add helpers (in `settings_build.go`):
  ```go
  // toggleCheck returns the new active-checks list with `name` added or removed,
  // ordered by knownWatchdogChecks for determinism.
  func toggleCheck(current []string, name string, on bool) []string {
      want := map[string]bool{}
      for _, c := range current {
          want[c] = true
      }
      want[name] = on
      out := []string{}
      for _, c := range knownWatchdogChecks {
          if want[c] {
              out = append(out, c)
          }
      }
      return out
  }

  // encodeChecks joins the list for the sparse update, using "-" for empty
  // (distinguishing it from "" = unchanged).
  func encodeChecks(list []string) string {
      if len(list) == 0 {
          return "-"
      }
      return strings.Join(list, ",")
  }
  ```
  (Add `strings` to the imports if missing.)

- [ ] **Step 5: Update the call site** in `settings_page.go` (~line 190): `action := classifyCommit(key, value, sp.cfg.WatchdogChecks)`.

- [ ] **Step 6: Run; pass.** `go test ./internal/ui/ -run 'DeveloperSectionWatchdog|ClassifyCommit_WatchdogMode' -v` then `go test ./... -count=1` (full module, no regression).

- [ ] **Step 7: Verify + commit.** `cd source/clients/cli && go build ./... && go test ./... -count=1` green; `gofmt -l internal/ui/` clean.
  ```bash
  git -C <worktree> commit -am "feat(cli): watchdog mode/checks/escalate in the Developer settings section"
  ```

---

## Self-Review

- **Spec coverage:** proto (T1); server apply with sentinel + validation + rebuild, agentclient split/map, GetConfig report (T2); CLI mode select + per-check toggles + escalate field + `classifyCommit` list computation (T3). Omitted `model` per spec. Tolerant handling (invalid mode/escalate/checks ignored) in T2.
- **Placeholder scan:** the soft spots are the test-harness constructor (`newTestServer` — "same as the existing watchdog tests") and `form.Section`/`Field.Key()` (used by the dev-settings tests already) — both established. All production code is exact.
- **Type consistency:** `WatchdogChecks` is `[]string` on `Config` (split) but `string` on `ConfigUpdate` (encoded) — intentional and matched at every hop (server splits the request string; agentclient splits the response string; CLI encodes via `encodeChecks`). `knownWatchdogChecks`/`hasCheck`/`toggleCheck`/`encodeChecks` (T3); `splitChecks`/`atoiOr` (T2). The `classifyCommit` signature change (T3 Step 4) is matched at its sole call site (T3 Step 5).
- **Dependency order:** T1 (proto) → T2 (needs generated getters) → T3 (needs `agentclient` fields). The `"-"` sentinel is produced in T3 (`encodeChecks`) and consumed in T2 (server) — both specified.
