# Watchdog Increment 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the watchdog observably enforce a reliably-triggerable protocol — add a model-judged `commit-checkpoint` check and render watchdog interventions in the CLI.

**Architecture:** Part 1 adds a self-contained `Check` (deterministic pre-filter + fast-model boundary judgment) to `internal/watchdog`, registered in the server's check-map. Part 2 wires watchdog events across the gRPC boundary: a new `WatchdogEvent` proto message, server-sink cases that emit it, an `agentclient` mapping to a client `StreamMsg`, and CLI scrollback rendering.

**Tech Stack:** Go 1.26 (`cercano/source/server` + the separate `cercano/source/clients/cli` module), gRPC/protobuf (protoc v7.34.1, protoc-gen-go v1.36.11), Bubble Tea TUI.

## Global Constraints

- **Fail-open, never spam/wedge:** the check nudges only on a *clear* boundary; nil `oneShot`, model error, or ambiguous verdict → no violation (no nudge). Reuse the existing `parseVerdict`.
- **Watchdog stays default-OFF.** The new check only activates when the watchdog is enabled.
- **The signal is a work boundary, never a count.** No numeric thresholds anywhere in the check.
- **A passing test/build is evidence the model may weigh, never an independent trigger.**
- Commit messages contain no "Claude"; no `Co-Authored-By` trailer.
- gofmt-clean on touched files; `go build ./...` + `go test` green in each affected module before every commit. Verify `git status` is clean after each commit.

---

## File Structure

- `source/server/internal/watchdog/commitcheckpoint.go` — the new check (+ `_test.go`).
- `source/server/internal/server/watchdog_wire.go` — add the check to the name→Check map.
- `source/server/pkg/config/config.go` — add `commit-checkpoint` to the default checks list.
- `source/proto/agent.proto` — the `WatchdogEvent` message + oneof field.
- `source/server/pkg/proto/agent.pb.go` (+ `agent_grpc.pb.go`) — regenerated, not hand-edited.
- `source/server/internal/agent/toolloop.go` — `LoopWatchdogBlock` kind + emit it from the block branch.
- `source/server/internal/server/server.go` — sink cases mapping watchdog LoopEvents → `WatchdogEvent`.
- `source/server/pkg/agentclient/client.go` — `TypeWatchdog` StreamMsg + fields + proto mapping.
- `source/clients/cli/internal/ui/main_agent_driver.go` — render the `TypeWatchdog` StreamMsg into scrollback (+ a golden/render test).

---

## Part 1 — the commit-checkpoint check

### Task 1: the `commit-checkpoint` check (logic + registration)

**Files:**
- Create: `source/server/internal/watchdog/commitcheckpoint.go`, `commitcheckpoint_test.go`
- Modify: `source/server/internal/server/watchdog_wire.go` (the `switch name` at ~line 33)
- Modify: `source/server/pkg/config/config.go` (Defaults `Watchdog.Checks`)
- Modify: `source/server/pkg/config/config_test.go` (defaults assertion)

**Interfaces:**
- Consumes: `Action`, `Check`, `OneShotFunc`, `parseVerdict` (all in package `watchdog`); `llm.Message`/`llm.Block` (`Block.Type == llm.BlockToolUse`, `Block.ToolName`, `Block.ToolInput`).
- Produces: `func CommitCheckpointCheck() Check`.
- Semantics: `Applies` true only when the action is a work-edit tool call AND ≥1 work-edit tool_use exists in the transcript *after* the most recent commit tool_use. Commit tools = `{checkpoint, git_commit}`. Work-edit tools = `{edit_file, write_file, rm_file}`. `Evaluate` asks the fast model whether the new edit begins a *different* unit than the uncommitted edits; clear boundary → violation.

- [ ] **Step 1: Write the failing test**

```go
package watchdog

import (
	"context"
	"strings"
	"testing"

	"cercano/source/server/internal/llm"
)

// toolUse builds a one-block assistant message representing a tool call.
func toolUse(name, input string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{
		{Type: llm.BlockToolUse, ToolName: name, ToolInput: []byte(input)},
	}}
}

func editAction2() Action {
	return Action{Kind: "tool_call", ToolName: "edit_file", ToolArgs: []byte(`{"path":"b.go"}`)}
}

func TestCommitCheckpointApplies(t *testing.T) {
	c := CommitCheckpointCheck()

	// No prior edits → the first edit of a unit must NOT apply.
	if c.Applies(Action{Kind: "tool_call", ToolName: "edit_file", Transcript: nil}) {
		t.Fatal("no uncommitted work → must not apply")
	}
	// One uncommitted prior edit + a new edit → applies.
	tr := []llm.Message{toolUse("edit_file", `{"path":"a.go"}`)}
	if !c.Applies(Action{Kind: "tool_call", ToolName: "edit_file", Transcript: tr}) {
		t.Fatal("uncommitted prior edit + new edit → must apply")
	}
	// A commit AFTER the prior edit clears uncommitted work → must not apply.
	tr2 := []llm.Message{toolUse("edit_file", `{"path":"a.go"}`), toolUse("checkpoint", `{"subject":"x"}`)}
	if c.Applies(Action{Kind: "tool_call", ToolName: "edit_file", Transcript: tr2}) {
		t.Fatal("commit cleared uncommitted work → must not apply")
	}
	// Non-edit action never applies.
	if c.Applies(Action{Kind: "tool_call", ToolName: "read_file", Transcript: tr}) {
		t.Fatal("read_file must not apply")
	}
}

func TestCommitCheckpointEvaluate(t *testing.T) {
	tr := []llm.Message{toolUse("edit_file", `{"path":"auth.go"}`)}
	var gotPrompt string
	boundary := func(_ context.Context, p string) (string, error) {
		gotPrompt = p
		return "VIOLATION: yes\nCHALLENGE: commit the auth change before starting the parser", nil
	}
	a := Action{Kind: "tool_call", ToolName: "edit_file", ToolArgs: []byte(`{"path":"parser.go"}`), Transcript: tr}
	v, err := CommitCheckpointCheck().Evaluate(context.Background(), a, boundary)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Violation || v.Protocol != "commit-checkpoint" {
		t.Fatalf("verdict: %+v", v)
	}
	if !strings.Contains(gotPrompt, "auth.go") || !strings.Contains(gotPrompt, "parser.go") {
		t.Fatalf("prompt must reference the prior work and the new edit: %q", gotPrompt)
	}
	// Continuation → no nudge.
	cont := func(_ context.Context, _ string) (string, error) { return "VIOLATION: no", nil }
	if vc, _ := CommitCheckpointCheck().Evaluate(context.Background(), a, cont); vc.Violation {
		t.Fatal("continuation verdict must not nudge")
	}
	// nil oneShot → fail open (no violation).
	if vn, _ := CommitCheckpointCheck().Evaluate(context.Background(), a, nil); vn.Violation {
		t.Fatal("nil oneShot must fail open")
	}
}
```

- [ ] **Step 2: Run; fail.** `cd source/server && go test ./internal/watchdog/ -run CommitCheckpoint -v` — `undefined: CommitCheckpointCheck`.

- [ ] **Step 3: Implement `commitcheckpoint.go`**

```go
package watchdog

import (
	"context"
	"fmt"
	"strings"

	"cercano/source/server/internal/llm"
)

type commitCheckpointCheck struct{}

// CommitCheckpointCheck nudges the agent to commit a completed unit of work
// before starting a different one. The trigger is a semantic work boundary, not
// a count.
func CommitCheckpointCheck() Check { return commitCheckpointCheck{} }

func (commitCheckpointCheck) Name() string { return "commit-checkpoint" }

// workEditTools are the code-mutating calls whose accumulation is "uncommitted
// work". git_reset_hard is intentionally excluded (it is not authored work).
var workEditTools = map[string]bool{"edit_file": true, "write_file": true, "rm_file": true}

// commitTools mark a commit boundary. Both the semantic checkpoint capability
// and the lower-level git_commit reset uncommitted work.
var commitTools = map[string]bool{"checkpoint": true, "git_commit": true}

func (commitCheckpointCheck) Applies(a Action) bool {
	if a.Kind != "tool_call" || !workEditTools[a.ToolName] {
		return false
	}
	return uncommittedEditCount(a.Transcript) > 0
}

// uncommittedEditCount counts work-edit tool calls in the transcript that occur
// after the most recent commit tool call.
func uncommittedEditCount(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type != llm.BlockToolUse {
				continue
			}
			if commitTools[b.ToolName] {
				n = 0 // a commit clears the running count
			} else if workEditTools[b.ToolName] {
				n++
			}
		}
	}
	return n
}

func (commitCheckpointCheck) Evaluate(ctx context.Context, a Action, oneShot OneShotFunc) (Verdict, error) {
	if oneShot == nil {
		return Verdict{Protocol: "commit-checkpoint"}, nil // fail open
	}
	out, err := oneShot(ctx, buildCommitCheckpointPrompt(a))
	if err != nil {
		return Verdict{}, err
	}
	return parseVerdict("commit-checkpoint", out), nil
}

func buildCommitCheckpointPrompt(a Action) string {
	var b strings.Builder
	b.WriteString("You are a supervisor enforcing commit discipline.\n")
	b.WriteString("The agent has made edits since the last commit and is about to make another. Judge ONLY whether the NEW edit begins a DIFFERENT unit of work than the uncommitted edits — such that the prior work now forms a complete, committable change that should be committed first. A continuation of the same change is NOT a violation. A passing test or build in the transcript is evidence a unit may have completed, but is not by itself a reason to commit (it can be mid-work).\n\n")
	b.WriteString("Uncommitted edits so far (most recent last):\n")
	for _, line := range uncommittedEditSummary(a.Transcript) {
		fmt.Fprintf(&b, "  - %s\n", line)
	}
	fmt.Fprintf(&b, "\nNew edit about to run: %s %s\n\n", a.ToolName, string(a.ToolArgs))
	b.WriteString("Respond EXACTLY:\nVIOLATION: yes|no\nCHALLENGE: <one line, only if yes>\n")
	return b.String()
}

// uncommittedEditSummary lists the work-edits since the last commit as
// "toolname args" lines (args truncated).
func uncommittedEditSummary(msgs []llm.Message) []string {
	var out []string
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type != llm.BlockToolUse {
				continue
			}
			if commitTools[b.ToolName] {
				out = nil
			} else if workEditTools[b.ToolName] {
				arg := string(b.ToolInput)
				if len(arg) > 120 {
					arg = arg[:120]
				}
				out = append(out, b.ToolName+" "+arg)
			}
		}
	}
	return out
}
```

- [ ] **Step 4: Run; pass.** `go test ./internal/watchdog/ -run CommitCheckpoint -v` — PASS.

- [ ] **Step 5: Register the check.** In `internal/server/watchdog_wire.go`, in the `switch name` that maps check names, add after the `"debug-loop"` case:

```go
		case "commit-checkpoint":
			checks = append(checks, watchdog.CommitCheckpointCheck())
```

In `pkg/config/config.go` `Defaults()`, change the watchdog checks default to:

```go
			Checks:        []string{"debug-loop", "commit-checkpoint"},
```

Update the `TestWatchdogDefaults` assertion in `pkg/config/config_test.go` to expect `["debug-loop", "commit-checkpoint"]`.

- [ ] **Step 6: Verify + commit.** `go test ./internal/watchdog/ ./internal/server/ ./pkg/config/ -count=1` green; `gofmt -l internal/watchdog/ internal/server/ pkg/config/` shows none of the touched files; `go build ./...` clean.

```bash
git -C <worktree> commit -am "feat(watchdog): commit-checkpoint check (work-boundary nudge) + register"
```

---

## Part 2 — client rendering

### Task 2: the `WatchdogEvent` proto message (contract + regen)

**Files:**
- Modify: `source/proto/agent.proto`
- Regenerate: `source/server/pkg/proto/agent.pb.go`, `agent_grpc.pb.go` (via protoc — do NOT hand-edit)

**Interfaces:**
- Produces: `proto.WatchdogEvent{Kind, Protocol, Text, Thread string}` and `StreamProcessResponse_WatchdogEvent` oneof wrapper + `(*StreamProcessResponse).GetWatchdogEvent()`.

- [ ] **Step 1: Add the message + oneof field.** In `source/proto/agent.proto`, add the field to the `StreamProcessResponse` `oneof payload` (next free tag is 10, after `route_selected = 9`):

```proto
    WatchdogEvent watchdog_event = 10;
```

and add the message definition (near the other stream event messages):

```proto
// WatchdogEvent is a protocol-supervisor intervention surfaced to the client.
message WatchdogEvent {
  string kind     = 1; // "challenge" | "block" | "echo"
  string protocol = 2; // e.g. "commit-checkpoint" (empty for echo)
  string text     = 3; // the challenge text, or the echo line
  string thread   = 4; // echo only: "watchdog" | "main" (empty otherwise)
}
```

- [ ] **Step 2: Install the pinned codegen plugins** (protoc is already present at `/opt/homebrew/bin/protoc`; the Go plugins are not):

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
export PATH="$PATH:$(go env GOPATH)/bin"
```

- [ ] **Step 3: Prove the regen command is correct BEFORE relying on it.** First regenerate the *unchanged-shape* files and confirm the command produces the existing output (git diff should show only your new message/field, nothing spurious). Run from the repo root of the worktree:

```bash
protoc -I source/proto \
  --go_out=source/server/pkg/proto --go_opt=paths=source_relative \
  --go-grpc_out=source/server/pkg/proto --go-grpc_opt=paths=source_relative \
  source/proto/agent.proto
```

Expected: `git -C <worktree> diff --stat` shows changes only in `source/server/pkg/proto/agent.pb.go` (the new `WatchdogEvent` type + oneof wrapper) and `agent.proto`; `agent_grpc.pb.go` unchanged (no new RPCs). If unrelated churn appears, the protoc/plugin versions differ from v7.34.1 / v1.36.11 — STOP and report; do not commit a mass-regenerated diff.

- [ ] **Step 4: Verify it compiles.** `cd source/server && go build ./pkg/proto/ && go build ./...` clean.

- [ ] **Step 5: Commit**

```bash
git -C <worktree> commit -am "feat(proto): WatchdogEvent stream message"
```

### Task 3: server sink + agentclient mapping (events cross the wire)

**Files:**
- Modify: `source/server/internal/agent/toolloop.go` (add `LoopWatchdogBlock`; emit it from the block branch)
- Modify: `source/server/internal/server/server.go` (sink cases ~line 1737)
- Modify: `source/server/pkg/agentclient/client.go` (StreamMsg type + fields + mapping ~line 1074)
- Test: `source/server/internal/server/*_test.go` (sink) + `pkg/agentclient/*_test.go` (mapping) — follow existing tests in those files.

**Interfaces:**
- Consumes: `proto.WatchdogEvent` (Task 2); `agent.LoopWatchdogChallenge`/`LoopWatchdogEcho` (existing), `agent.LoopWatchdogBlock` (new here).
- Produces: `agentclient.TypeWatchdog StreamMsgType` and `StreamMsg` fields `WatchdogKind, Protocol, Thread string` (reuse the existing `Summary` field for the text).

- [ ] **Step 1: Add `LoopWatchdogBlock` + emit it.** In `internal/agent/toolloop.go`, add next to `LoopWatchdogChallenge`/`LoopWatchdogEscalate`/`LoopWatchdogEcho`:

```go
	LoopWatchdogBlock LoopEventKind = "watchdog_block"
```

In the wxCalls watchdog switch, change the `"block"` branch's `emit(...)` from `Kind: LoopWatchdogChallenge` to `Kind: LoopWatchdogBlock` (leave the injected tool-result content unchanged).

- [ ] **Step 2: Write the failing sink test** in an `internal/server` test file (mirror an existing sink test that drives `sink func(agent.LoopEvent)` and asserts the `stream.Send` payload). Assert:
  - a `LoopEvent{Kind: LoopWatchdogChallenge, Detail: "commit-checkpoint", Summary: "commit first"}` produces a `StreamProcessResponse_WatchdogEvent` with `Kind=="challenge"`, `Protocol=="commit-checkpoint"`, `Text=="commit first"`.
  - a `LoopEvent{Kind: LoopWatchdogEcho, ToolName: "watchdog", Summary: "boundary shift"}` produces `Kind=="echo"`, `Thread=="watchdog"`, `Text=="boundary shift"`.

- [ ] **Step 3: Implement the sink cases.** In `server.go`, inside the `sink := func(ev agent.LoopEvent) { switch ev.Kind {` block, add:

```go
		case agent.LoopWatchdogChallenge, agent.LoopWatchdogBlock:
			kind := "challenge"
			if ev.Kind == agent.LoopWatchdogBlock {
				kind = "block"
			}
			stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_WatchdogEvent{
					WatchdogEvent: &proto.WatchdogEvent{
						Kind: kind, Protocol: ev.Detail, Text: ev.Summary,
					},
				},
			})
		case agent.LoopWatchdogEcho:
			stream.Send(&proto.StreamProcessResponse{
				Payload: &proto.StreamProcessResponse_WatchdogEvent{
					WatchdogEvent: &proto.WatchdogEvent{
						Kind: "echo", Text: ev.Summary, Thread: ev.ToolName,
					},
				},
			})
```

- [ ] **Step 4: Add the agentclient mapping.** In `pkg/agentclient/client.go`: add `TypeWatchdog` to the `StreamMsgType` const block; add `WatchdogKind`, `Protocol`, `Thread` string fields to `StreamMsg` (document them like the existing field comments); and in the payload loop (near the `GetRouteSelected` case), add:

```go
			if we := msg.GetWatchdogEvent(); we != nil {
				out <- StreamMsg{
					Type:         TypeWatchdog,
					WatchdogKind: we.GetKind(),
					Protocol:     we.GetProtocol(),
					Summary:      we.GetText(),
					Thread:       we.GetThread(),
				}
				continue
			}
```

- [ ] **Step 5: Write the failing agentclient test** (mirror an existing `client_test.go` that feeds a `StreamProcessResponse` through the payload conversion and asserts the emitted `StreamMsg`). Assert a `WatchdogEvent{Kind:"challenge",Protocol:"commit-checkpoint",Text:"x"}` → `StreamMsg{Type:TypeWatchdog, WatchdogKind:"challenge", Protocol:"commit-checkpoint", Summary:"x"}`.

- [ ] **Step 6: Verify + commit.** `go test ./internal/agent/ ./internal/server/ ./pkg/agentclient/ -count=1` green; gofmt clean; `go build ./...` clean.

```bash
git -C <worktree> commit -am "feat(server): forward watchdog events to the client (sink + agentclient StreamMsg)"
```

### Task 4: CLI scrollback rendering

**Files:**
- Modify: `source/clients/cli/internal/ui/main_agent_driver.go` (the `switch` on `msg.Type` at ~line 61-96)
- Test: `source/clients/cli/internal/ui/main_agent_driver_test.go` or a scrollback golden test (mirror an existing one)

**Interfaces:**
- Consumes: `agentclient.TypeWatchdog` + `StreamMsg{WatchdogKind, Protocol, Summary, Thread}` (Task 3).
- Produces: scrollback entries for watchdog events.

- [ ] **Step 1: Read the neighbors.** Read how the `case agentclient.TypeToolExecComplete:` and `case agentclient.TypePermissionRequired:` arms build and append scrollback entries in `main_agent_driver.go`, and read one scrollback golden test (e.g. `scrollback_tool_test.go` / `chat_view_golden_test.go`) to learn the entry/render API. Mirror that pattern — do not invent a new rendering mechanism.

- [ ] **Step 2: Write the failing render test.** Following the golden/render test pattern, assert that a `StreamMsg{Type: TypeWatchdog, WatchdogKind:"challenge", Protocol:"commit-checkpoint", Summary:"commit the auth change before the parser work"}` produces a scrollback line containing `⚡ watchdog` and the protocol and the text; and that a `WatchdogKind:"echo", Thread:"watchdog", Summary:"boundary shift"` produces a dim line containing `watchdog:` and the text.

- [ ] **Step 3: Implement the case.** Add to the `switch` in `main_agent_driver.go`:

```go
	case agentclient.TypeWatchdog:
		return m.appendWatchdogEvent(msg) // build the scrollback entry per the mirrored pattern
```

Implement `appendWatchdogEvent` (in the same file) to render, using the existing scrollback-entry/style helpers:
- `challenge` / `block`: a set-apart callout — a header line `⚡ watchdog · <Protocol>` (append ` (blocked — no override)` when `WatchdogKind=="block"`) followed by the wrapped `Summary` text.
- `echo`: a single dim line `<Thread>: <Summary>` (secondary/subtle style).

Keep it minimal and mirror the styling approach the neighboring cases use (theme styles, not raw ANSI). If the exact entry API differs from what's described, follow the actual neighbor code and note the deviation.

- [ ] **Step 4: Run; pass.** `cd source/clients/cli && go test ./internal/ui/ -run Watchdog -v` (and the golden test) PASS.

- [ ] **Step 5: Verify + commit.** `cd source/clients/cli && go build ./... && go test ./... -count=1` green; `gofmt -l internal/ui/` clean.

```bash
git -C <worktree> commit -am "feat(cli): render watchdog challenge/block/echo in scrollback"
```

---

## Self-Review

- **Spec coverage:** Part 1 check (T1: pre-filter + boundary judgment + registration + defaults); Part 2 rendering (T2 proto, T3 server sink + `LoopWatchdogBlock` + agentclient, T4 CLI). Fail-open in `Evaluate` (nil oneShot + ambiguous verdict via `parseVerdict`). Boundary-not-count enforced (no thresholds). Test/build-as-evidence-not-trigger stated in the prompt. Default-OFF preserved (check only runs when the watchdog is enabled). `LoopWatchdogBlock` closes the review's block-event-kind minor.
- **Placeholder scan:** the one soft spot is T4's exact scrollback-entry API (the CLI's render mechanism isn't fully specified here) — mitigated by "read and mirror the `TypeToolExecComplete`/`TypePermissionRequired` neighbors + an existing golden test," with the exact output content given. T2's regen has a concrete command + a verify-before-trust step.
- **Type consistency:** `CommitCheckpointCheck`, `workEditTools`/`commitTools`/`uncommittedEditCount`/`uncommittedEditSummary` (T1); `WatchdogEvent{Kind,Protocol,Text,Thread}` (T2) consumed identically in the sink (T3) and mapped to `StreamMsg{WatchdogKind,Protocol,Summary,Thread}` + `TypeWatchdog` (T3), rendered in T4. `LoopWatchdogBlock` defined and emitted in T3.
- **Dependency note:** T1 is independent (server module). T2→T3→T4 are ordered (proto → wire → render). T3 depends on T2's generated types; T4 depends on T3's `agentclient` types.
