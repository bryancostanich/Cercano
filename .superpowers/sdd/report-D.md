# Phase D Implementation Report

## Implemented

### D1 — manager.go + manager_test.go
- `ServerState` (`warming`/`ready`/`failed`), `ServerStatus`, `serverHandle`, `Manager` structs
- `New()` constructor with injectable `dialFn` seam (default: `stdioDial` via `mcp.CommandTransport`)
- `startServer()`: synchronous; connects, lists tools, registers — or marks `StateFailed` on any error
- `serverHandle.fail()`: closes `readyCh` exactly once on error path
- `serverHandle.ready()`: returns `readyFunc` that fast-paths when already ready, blocks on `readyCh` otherwise, selects on timeout + ctx cancel; resolves `errNoSession` dangling reference from `client.go`
- `List()`: snapshot under `Manager.mu`, reads each handle under `serverHandle.mu` (never both simultaneously)

### D2 — Start/Add/Remove/Restart + Registry.Unregister
- `Start()`: fans out `startServer` into goroutines per config entry
- `Add()`: calls `startServer` synchronously then persists to `mcp.yaml`
- `Remove()`: deletes from map, calls `teardown()` (unregisters tools, closes conn), persists removal
- `Restart()`: saves cfg, teardown, delete from map, `startServer` fresh
- `teardown()`: locks `serverHandle.mu`, unregisters all tools via `Unregister`, clears conn
- `persistAdd/persistRemove/writeYAML`: load-modify-write mcp.yaml
- `Registry.Unregister()`: write-locked `delete(r.tools, name)`, no-op if absent

## TDD RED/GREEN

D1: tests written first (undefined symbols = RED), manager.go written = GREEN.
D2: tests extended (undefined Restart/Remove/Unregister = RED), D2 extensions written = GREEN.

## Test Results

```
go test ./internal/mcp_host/ ./internal/agenttools/ -count=1
ok  cercano/source/server/internal/mcp_host    0.186s
ok  cercano/source/server/internal/agenttools  1.579s

go test ./internal/mcp_host/ -run TestManager -race -count=1
ok  cercano/source/server/internal/mcp_host    1.307s   (RACE CLEAN)

go vet ./internal/mcp_host/ ./internal/agenttools/
(no output — clean)
```

## Commits

| SHA | Message |
|-----|---------|
| `0883149` | `feat(mcphost): manager — background connect, register, status, readiness wait` |
| `7be7154` | `feat(mcphost): Start/Add/Remove/Restart + registry Unregister` |

## Files Changed

- Created: `source/server/internal/mcp_host/manager.go`
- Created: `source/server/internal/mcp_host/manager_test.go`
- Modified: `source/server/internal/agenttools/registry.go` (added `Unregister`)

## Self-Review

Locking: `Manager.mu` and `serverHandle.mu` never held simultaneously — taken separately, never nested. `List()` takes `Manager.mu`, iterates handles, takes each `serverHandle.mu` sequentially (not nested). `Restart` takes `Manager.mu` twice with no nesting. No deadlock risk.

`readyCh` closed exactly once: `h.fail()` and the success path in `startServer` are mutually exclusive.

Tool registration: only registers on success; duplicate-name collision silently dropped (matches existing `Register` semantics).

## Concerns

None. Race detector clean. `errNoSession` dangling reference in `client.go` now consumed by `serverHandle.ready()`. Locking follows brief exactly.

---

## Phase D fix

### What changed

**Fix 1 — race: orphaned registration on restart-during-warm**

Added `defunct bool` and `cancel context.CancelFunc` to `serverHandle`.

`startServer` now:
- Derives `cctx, cancel := context.WithCancel(ctx)` before publishing `h` to `m.servers`; stores `h.cancel = cancel` on the local `h` (race-free — `h` is not yet visible to other goroutines).
- Passes `cctx` to `m.dialFn` and `c.listTools`.
- Inside the `h.mu.Lock()` success block, checks `h.defunct` first. If true: closes the just-opened conn, sets `StateFailed` + `"superseded during restart"`, closes `h.readyCh`, returns. Otherwise registers tools as before.

`teardown` now, inside its existing `h.mu.Lock()` block: sets `h.defunct = true` and calls `h.cancel()` (if non-nil) before unregistering tools.

**readyCh single-close reasoning**

Each handle has exactly four terminal paths in `startServer`; exactly one runs per handle:

| Path | Trigger | Closer |
|------|---------|--------|
| 1 | `dialFn` returns error (incl. ctx cancelled by `h.cancel()`) | `h.fail()` |
| 2 | `listTools` returns error (incl. ctx cancelled by `h.cancel()`) | `h.fail()` |
| 3 | Success, `h.defunct == false` | success block `close(h.readyCh)` |
| 4 | Success, `h.defunct == true` | defunct branch `close(h.readyCh)` |

Paths 1 and 2 return before the `h.mu.Lock()` block; paths 3 and 4 are mutually exclusive inside it (`if h.defunct { ... } else { ... }`). `teardown` does not close `readyCh` — it only sets `defunct` and calls `cancel`. If `cancel` causes paths 1 or 2 to fire, `h.fail()` closes `readyCh`. If the goroutine is already past dial/listTools when `cancel` fires, it sees `defunct=true` under `h.mu` (serialized) and takes path 4. One close per handle, guaranteed.

`h.cancel` is written before `m.servers[name] = h` (before the `m.mu.Lock()`). Any goroutine that reads `h` via `m.servers` (under `m.mu`) happens-after the publish, which happens-after the write to `h.cancel`. Race-free by Go memory model (mutex unlock synchronizes with lock).

**Fix 2 — doc: lock ordering**

`serverHandle` struct comment: "Lock ordering: Manager.mu must always be acquired before serverHandle.mu. Never acquire Manager.mu while holding a serverHandle.mu."

`List()` comment: "Lock ordering: Manager.mu is held for the duration; each serverHandle.mu is acquired and released sequentially inside. This is the only place both mutexes are held at once, and the ordering is always Manager.mu → serverHandle.mu." Corrects the erroneous D report claim that they are "never held simultaneously."

**Fix 3 — trivial: log config error in Start()**

`cfg, _ := LoadConfig(m.dir)` → `cfg, err := LoadConfig(m.dir)` + `log.Printf("mcphost: load config: %v", err)` on non-nil err. Added `"log"` import.

### New test: TestManagerDefunctHandleRegistersNothing

Deterministic reproduction of the restart-during-warm race. A `sync.Once`-guarded `dialFn` closes a `published` channel on its first call and then blocks on `select { case <-ctx.Done() ... case <-released ... }`. Main goroutine waits for `<-published` (goroutine is now parked inside dialFn with `h` in `m.servers`), then calls `Restart`. `Restart` calls `teardown(h)` which sets `defunct=true` and fires `h.cancel()`, causing the goroutine's `ctx.Done()` to unblock. The goroutine calls `fail()` and exits. `Restart` then runs a new `startServer` synchronously (second `dialFn` call, not blocked). Assertions: `reg.Get("mcp__test__echo")` succeeds (new handle registered), `len(reg.All()) == 1` (no duplicate from old goroutine).

### Test results

```
go test ./internal/mcp_host/ -run TestManager -race -count=1 -v
=== RUN   TestManagerRegistersToolsOnStart
--- PASS: TestManagerRegistersToolsOnStart (0.00s)
=== RUN   TestManagerMarksFailedOnDialError
--- PASS: TestManagerMarksFailedOnDialError (0.00s)
=== RUN   TestManagerRestartReregisters
--- PASS: TestManagerRestartReregisters (0.00s)
=== RUN   TestManagerRemoveUnregisters
--- PASS: TestManagerRemoveUnregisters (0.00s)
=== RUN   TestManagerDefunctHandleRegistersNothing
--- PASS: TestManagerDefunctHandleRegistersNothing (0.00s)
PASS
ok  cercano/source/server/internal/mcp_host    1.312s   (RACE CLEAN)

go test ./internal/mcp_host/ ./internal/agenttools/ -count=1
ok  cercano/source/server/internal/mcp_host    0.293s
ok  cercano/source/server/internal/agenttools  1.677s

go vet ./internal/mcp_host/
(no output — clean)
```

### Files changed

- Modified: `source/server/internal/mcp_host/manager.go`
- Modified: `source/server/internal/mcp_host/manager_test.go`
