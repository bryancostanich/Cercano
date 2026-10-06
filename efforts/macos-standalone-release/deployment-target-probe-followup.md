# Deployment-target probe — follow-up (flag-aware build & compatibility handshake survey)

**Date:** 2026-09-22 · **Scope:** resolve the previous probe's 15-linker-warning follow-ups: (1) confirm Go's CGO flag/cache behavior with the smallest evidence, (2) repeat the isolated temporary server build explicitly passing `CGO_CFLAGS`/`CGO_LDFLAGS=-mmacosx-version-min=12.0` plus `MACOSX_DEPLOYMENT_TARGET=12.0` with a fresh temp `GOCACHE`, (3) read-only trace of startup/connect behavior and the gRPC RPC surface, sufficient to propose a minimal backwards-compatible compatibility handshake. Read-only: no live agent, no credentials, no signing, no publication, no production edits, no commit. All work in `mktemp` dirs, cleaned afterwards. No macOS 12 runtime claim is made — binaries only ran `--version` on the 26.5.2 host.

## 1. CGO flag vs. environment vs. cache behavior (smallest evidence)

Tiny cgo program (`import "C"; func main() { C.puts(C.CString("ok")) }`), go1.26.3 darwin/arm64, fixed env `CC=clang`, single GOCACHE reused across all three variants (that was the point). `cgoflags` (tiny client printing the runtime cgo build vars) showed Go setting only `CGO_CFLAGS2="-g -O2"`/`CGO_LDFLAGS2="-g -O2"` internally — by default **Go passes no `-mmacosx-version-min` to cgo's clang**; cgo's `cc` invocations are independent compiler subprocesses that read `MACOSX_DEPLOYMENT_TARGET` straight from the environment.

| Variant | cgo recompiles | ld warnings | binary min-OS |
|---|---|---|---|
| B1. default env | 2 | **0** | 26.0 |
| B2. `MACOSX_DEPLOYMENT_TARGET=12.0`, warm cache | **0** | **13** | 12.0 |
| B3. `CGO_CFLAGS=-mmacosx-version-min=12.0 CGO_LDFLAGS=…12.0` (no env change), warm cache | **2** | **0** | 12.0 |

**B2 is the mechanism behind the previous 15 warnings.** With the same GOCACHE, changing only `MACOSX_DEPLOYMENT_TARGET` caused **zero cgo action-cache invalidation** — the runtime/cgo objects previously compiled at min-OS 26.0 were reused unchanged, then ld warned when linking them into a 12.0 target (the exact `was built for newer 'macOS' version (26.0) than being linked (12.0)` class, 13× for the tiny program; the real server's larger object set produced 15). `MACOSX_DEPLOYMENT_TARGET` is a compiler/linker environment input, not a Go-build input, so the Go action cache is not keyed on it.

**B3 shows the flag-aware path is cache-keyed and self-invalidating.** Adding `CGO_CFLAGS`/`CGO_LDFLAGS` on the same warm cache recompiled both cgo objects (cache miss → rebuild at 12.0 → clean link). The flags are recorded in the binary and visible via `go version -m`:

```
build	CGO_CFLAGS=-mmacosx-version-min=12.0
build	CGO_LDFLAGS=-mmacosx-version-min=12.0
```

## 2. Repeated isolated server build — env-only vs. flag-aware, fresh GOCACHE

Fresh `mktemp -d /tmp/cercano-dt-probe2.XXXXXX`, fresh `GOCACHE=$tmp/gocache` (verified empty beforehand → **no stale runtime/cgo objects at 26.0 possible**), inside `source/server`, original release flags:

```
MACOSX_DEPLOYMENT_TARGET=12.0 \
CGO_CFLAGS="-mmacosx-version-min=12.0" \
CGO_LDFLAGS="-mmacosx-version-min=12.0" \
GOCACHE="$tmp/gocache" \
GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -trimpath -buildvcs=false \
  -ldflags "-X main.version=0.0.0-target-probe2" -o "$tmp/cercano" ./cmd/cercano
```

**Result:** BUILD_OK, ~31s (cold cache). **Zero linker warnings, zero warnings of any kind** (previous probe: 15 ld warnings under env-only with a machine-warm cache). Binary 61,820,674 bytes. Evidence captured before cleanup:

- `lipo -info`: `Non-fat file: architecture: arm64`.
- `otool -l` → `LC_BUILD_VERSION`: **platform 1 (macOS), minos 12.0, sdk 26.5**, tool 1267.0; `LC_SOURCE_VERSION 0.0`. **No `LC_VERSION_MIN_MACOSX` entries** (min-OS lives solely in LC_BUILD_VERSION).
- `otool -L`: identical four system-image deps as before — `libresolv.9.dylib`, CoreFoundation, Security, libSystem.B — nothing post-12.0, nothing vendored/bundled.
- `go version -m` records `CGO_CFLAGS/CGO_LDFLAGS=-mmacosx-version-min=12.0` and `MACOSX_DEPLOYMENT_TARGET=12.0` in build settings; `strings` on the binary records both flags.

**Isolated `--version` (only command executed):** isolated `HOME`/`XDG_CONFIG_HOME`/`XDG_CACHE_HOME`/`XDG_DATA_HOME`/`TMPDIR` → `cercano v0.0.0-target-probe2`, exit 0; the isolated dirs stayed empty (no writes, no keychain/credential/network access). Ran on the macOS 26.5.2 host only — **this does not establish macOS 12 runtime support**; it establishes only that a flag-aware, cache-clean build produces a zero-warning binary whose Mach-O floor is 12.0.

### Interpretation: env-only vs. flag-aware

- **Env-only (`MACOSX_DEPLOYMENT_TARGET`), fresh cache:** works — the previous probe's binary also ended at min-OS 12.0 — but the cache is *not keyed* on the env var, so any machine-warm `runtime/cgo` object compiled at a different target is silently reused and the linker warns (15× previously). Deterministic only if GOCACHE is guaranteed fresh or only ever used at one target.
- **Flag-aware (`CGO_CFLAGS`/`CGO_LDFLAGS`):** cache-keyed — changing the flag invalidates affected cgo objects automatically — and ld stop-warnings confirm all linked objects were built at 12.0, not just the final image. Recommended shape for the release script when the deployment target is decided (no production edit made in this probe).
- Neither variant says anything about runtime on macOS 12; the previous probe's NOT-established caveat applies unchanged.

## 3. Startup & connect behavior (read-only trace) + RPC surface

### Server startup

`source/server/cmd/cercano/main.go` → `runServerMode(cfg)` (main.go:1906) tees diagnostics to `~/.cercano-dispatch.log`, opens `~/.config/cercano/crash.log` (crashlog writer, main.go:1920), panics-safe, prints `Starting Cercano gRPC server (v%s)` (main.go:1938), then `startGRPCServer(cfg, ":"+cfg.Port, …)` (main.go:1944) → `net.Listen("tcp", ":"+cfg.Port)` (main.go:554), constructs the gRPC server with a 64 MiB recv limit, panic-recovery and logging interceptors, registers `proto.AgentServer`, records the build version via `srv.SetBuildVersion(version)` (main.go:569 → `server.SetBuildVersion`, server.go:981), and serves in a goroutine. The agent prints a listen banner (`Server listening at %s`, main.go:1953) to stdout — which the auto-launch path redirects to a log.

### Client connect & auto-launch

`source/clients/cli/main.go` → `runCLI` → computes `addr = "localhost:" + cfg.Port` → `agentclient.Dial(ctx, addr)` (`source/server/pkg/agentclient/client.go:96`):

1. **Fast probe:** `connect(ctx, addr, 600ms)` — `grpc.DialContext` with `WithBlock`, insecure credentials, 64 MiB call limits (client.go:246-263). Hit on an already-running server → done, plus a background `watchConn` goroutine is started (client.go:101).
2. **On miss → `ensureServerLaunched`** (client.go:130): file-locks auto-launch (multiple CLIs can race), re-probes (another CLI may have won), then `autoLaunchServer` (client.go:143): finds the `cercano` binary **sibling to cercano-cli first, then `$PATH`** (client.go:175), spawns `cercano agent` detached with `CERCANO_AUTOLAUNCHED=1`, stdout/stderr → `$TMPDIR/cercano-server.log`, and **does not reap** — the server outlives the CLI so IDE clients can share it. `waitForPort` then polls TCP for up to 8s (client.go:204) with a 200ms post-bind sip for service registration, then a full 3s gRPC connect.
3. **Crash recovery** (`reconnect.go`): the background `watchConn` observes gRPC `TRANSIENT_FAILURE` past a 500ms blip → `reconnect()` (reconnect.go:230): fast burst 3 attempts at 1/2/4s (each may respawn), then indefinite 10s slow lane, respawning at most every 3rd slow attempt. A respawn can come from `$PATH` if no sibling exists — i.e. **an older/newer `cercano` than the CLI can be silently (re)introduced**.

### RPC surface

Generated from `source/server/pkg/proto/agent_grpc.pb.go`: the **Agent service has 76 RPCs**, e.g. AcceptRollover, AddMcpServer, AllowToolCall, AttachConversation, DeclineRollover, DeleteConversation, DenyToolCall, DismissSubAgent, DownloadRuntimeModel, ElideContext, ExportContext/ExportImage/ExportTrajectory, GetCloudProviders, GetConfig, GetContextUsage, GetModelRAMEstimate, GetRuntimeStatus, GetSessionProfile, GetTokenMetrics, GetToolCall, InstallOpenRuntime, InvokeCapability, InvokeTool, ListConversations, ListMcpServers, ListModels, ListRuntimeModels, ListSkills, ListSubAgents, ListTools, ProcessRequest, ResumeConversation (+ streaming variants), SetCloudProfileKey, SetPermissionMode, ShutdownAgent, StartChatGPTLogin, StartClaudeLogin, StartRuntimeModel, StopRuntimeModel, StreamProcessRequest, SubscribeEvents, UpdateConfig, UpdateRoutingAssignments, UpsertCloudProfile, … (76 total). Two are auth/login related (`StartClaudeLogin`, `StartChatGPTLogin`) and the server also has an `--mcp` stdio mode; neither was launched by this probe.

### Version exchange today: none

- The server knows its build version (`SetBuildVersion`, server.go:981) but surfaces it **only in exported trajectory metadata** (`trajectory_export.go:18`) — **no RPC message in the current `.proto` carries a server or client version**, and nothing on the wire negotiates capabilities.
- The only de-facto compatibility mechanisms: (a) gRPC `codes.Unimplemented` — e.g. the client's `ResumeConversation` falls back from the streaming RPC to the older unary one when the server predates it (client.go:~460), (b) protobuf's unknown-field tolerance for additive schema growth, and (c) proto accessor `GetX()` zero-value defaults the client treats as "absent/legacy" throughout (e.g. `RuntimeModel.Acquisition` comments, client.go).
- **Skew hazard:** the auto-launched server deliberately outlives the CLI. Update the CLI (or IDE client) and the long-lived background server keeps an old version at the same port — the new client silently negotiates nothing and discovers incompatibilities only as scattered per-RPC `Unimplemented` failures or zero-value fields. The reconnect path's `$PATH` respawn can also resurrect a stale binary.

## 4. Handshake proposal (recommendation only — NOT implemented)

**Recommendation — new additive unary RPC, server-advertised only:**
- Add `Handshake(HandshakeRequest{client_version, client_feature_list}) → HandshakeResponse{server_version, protocol_features, min_compatible_client_version?}` as a *new* RPC. Old servers return `codes.Unimplemented` for it — which the client treats as "pre-handshake server; assume legacy baseline and proceed," mirroring the existing `ResumeConversation` fallback pattern, so the change is fully backwards-compatible in both directions.
- Client calls it once after `connect()` succeeds (before first real use), then (a) surfaces a one-line version/mismatch banner instead of cryptic per-RPC failures, (b) disables UI affordances for RPCs the server says it lacks / enables additive-field-aware behavior, (c) on a mismatch beyond `min_compatible_client_version`, can offer "restart the background agent" — safe because `ShutdownAgent` already exists and the auto-launch/reconnect machinery already handles respawn.
- Feature flags in the response (a repeated-enum or string set) give a single place to gate the growing "empty-from-a-server-predating-the-field" cases that are currently scattered across accessor-default comments.

**Alternatives (each weaker):**
1. *Piggyback on `GetConfig`* — add `server_version`/`feature_flags` fields to the existing response. Additive and zero-new-RPC, but every client must already call GetConfig (true today for the CLI, not guaranteed for embedders), and it conflates config with protocol negotiation.
2. *gRPC per-call metadata* — client sends its version as a `metadata.MD` header on every call; new servers could log/warn on skew. Cheapest, but the server can't steer the client (no response channel on unary calls without a response-side header/trailer convention), so it stays advisory.
3. *Server header-only* — server responds with a version header on the first call (grpc `header`); requires client-side plumbing to read headers on streaming calls and still gives no feature list.

None of this was implemented; this section is analysis and recommendation only.

## 5. Cleanup

All temporary dirs removed after evidence capture: `/tmp/cgo-ev.NXl4` (tiny-program experiments), `/tmp/cercano-dt-probe2.0G2yPg` (isolated flag-aware build, fresh GOCACHE inside), plus the two `/tmp/*.path` pointer files (`/tmp/cgo-ev.path`, `/tmp/cercano-dt-probe2.path`). Verified no `/tmp/cgo-ev.*` or `/tmp/cercano-dt-probe*` entries remain. No repo files modified except this report; nothing committed.
