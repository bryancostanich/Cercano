# Self-Development Guide

How to build, test, and inspect Cercano when working on Cercano itself. This
doc is read by the agent on entering development mode (`/d`), alongside
`docs/features/cli/README.md` (CLI track status + outstanding work) and
`docs/agent/README.md` (agent architecture).

## Delegate to open models — this is the point of Cercano

**Read this before doing recon work.** Cercano exists to keep the frontier
model's tokens for frontier-grade reasoning and push everything else onto local
("open") models. When *you* — the main thread — grind through a pile of
`Grep`/`Read`/`Glob` calls to understand how some code works, you are burning
frontier tokens on work an open model does perfectly well. Don't. Delegate it.

**The delegation tools are native agent tools, not "just MCP skills."** When
Cercano runs as the agent (which is what you are right now), the capabilities
below are in your tool catalog and you call them directly, exactly like `Read`
or `Bash`. The `SKILL.md` files under `plugins/skills/` and the embedded
`internal/skills/catalog/` are the *same* capabilities described for host agents
— but here they are first-class tools you invoke yourself.

| Tool | Shape | Runs on | Use it for |
|---|---|---|---|
| `dispatch` (alias `workflow`) | agentic sub-agent (bounded tool loop over a granted toolset) | saved task class → redirects → locality policy | tracing a code path, "how/if does X happen," understanding a class, finding something in a subsystem, any read-heavy recon that returns a distilled answer |
| `explain` | one-shot text analysis | Reconnaissance route | explaining a file/snippet you already have in hand |
| `summarize` | one-shot text analysis | Reconnaissance route | condensing a long file or output |
| `extract` | one-shot text analysis | Reconnaissance route | pulling specific facts out of text |
| `classify` | one-shot text analysis | Reconnaissance route | bucketing text/files |

**Canonical delegation example.** Instead of running fifteen Grep/Read calls
yourself to chase a code path, call `dispatch` with a concrete intent and a
read-only grant:

```json
{
  "class": "reconnaissance",
  "task": "Figure out how/if model reloading happens when the backend changes. Trace the code and return the full code path, the relevant code snippets, and their file:line locations.",
  "tools": ["Read", "Grep", "Glob"]
}
```

The sub-agent uses the configured Reconnaissance route (Local/Light by default), which the user can override. Do not assume it is free or local after a redirect.

### Task class, quality, and permissions are independent

Choose `class` at the semantic entry point:

- `reconnaissance`: narrow read-only tracing, extraction and summaries (Local/Light).
- `mechanical_development`: well-specified edits (Local/Standard).
- `investigation`: open-ended diagnosis (Secondary/Premium).
- `implementation`: substantial implementation (Secondary/Premium).
- `review`: standalone adversarial review (Secondary/Premium).
- `research`: source gathering, query generation and synthesis (Secondary/Premium).
- `git_land`: the existing landing workflow including its nested review (Local/Premium).
- `watchdog`: normal Watchdog routing (Local/Standard); no special placement exemption.

Chat is Primary/Premium; omitted dispatch class uses Default dispatch (Secondary/Premium). Unknown explicit classes are rejected. Prefer omitting `tier` so the chosen class's saved quality applies. Explicit `light`, `standard`, or `deep` changes quality only; it never chooses a destination, class or permission grant. Light uses the persisted `economy` value.

Secondary/Local redirects resolve before placement and model selection. Only the final destination's models, credentials and backup chain apply. Saved source assignments remain unchanged. Locus policy still prohibits forbidden placement; final Secondary cannot fall through to Primary/Local. Tool permissions remain the explicit grant and approval system, independent of model quality. See [the routing guide](../cloud-routing.md).

The old RoleCoproc/default-light delegation policy is retired. Text analysis uses Reconnaissance; the explicit `local` tool retains its existing local-offload intent. New first-party callers must pass a task class; do not infer one from role, prompt text or tool/source names.

## Layout & build

Two binaries from two Go modules:

| Binary | Module | Role |
|---|---|---|
| `cercano` | `source/server` | agent gRPC server, MCP host — a singleton |
| `cercano-cli` | `source/clients/cli` | terminal UI, thin gRPC client |

```bash
# Server (agent + MCP)
cd source/server && go build -o bin/cercano ./cmd/cercano/
cd source/server && go test ./... -count=1

# CLI (separate Go module; depends on server pkg/ via replace directive)
cd source/clients/cli && go build -o bin/cercano-cli .
cd source/clients/cli && go test ./... -count=1
```

**Prefer `make build`** (in each module) over bare `go build`: the make targets
also code-sign the binary via `source/server/scripts/codesign-if-available.sh`.
A stable Developer ID signature keeps macOS Keychain ACLs matching across
rebuilds, so the agent's secret reads (cloud API keys, OAuth tokens) don't
prompt for a password after every rebuild. The dev launcher signs its own
rebuilds the same way. Bare `go build` still works — the binary just reverts
to an ad-hoc signature and the first Keychain read will prompt again.
`CERCANO_CODESIGN_ID` overrides the auto-detected identity (`none` disables).

**Singleton caveat:** the agent server is a singleton on `:50052`. A rebuilt
binary changes nothing until the running agent is killed — a stale agent will
happily keep serving old code. The dev launcher (`~/bin/cercano`, generated by
`source/server/scripts/install-launcher.sh`) handles this: it rebuilds either
binary whose sources changed and kills agents older than the fresh binary
before exec'ing the CLI.

## Logs

| Path | What it is |
|---|---|
| `$TMPDIR/cercano-server.log` | stdout/stderr of an auto-launched agent |
| `~/.config/cercano/crash.log` | agent crash reports |
| `~/.config/cercano/meridian.log` | Meridian proxy diagnostics |
| `~/.cercano-dispatch.log` | MCP dispatch-mode diagnostics |

The auto-launch log path comes from `pkg/agentclient/client.go` (it prints as
"auto-launched agent server (log: …)" on CLI startup). When debugging agent
behavior, check the server log first — tool-loop iterations, provider errors,
and permission decisions all land there.

## Databases

SQLite in WAL mode. Query in place with `sqlite3`; if you must copy a
database, take its `-wal` and `-shm` siblings too or you'll read a stale
snapshot. Timestamps are integer unix **seconds**.

### `~/.config/cercano/conversations.db`

Override with `$CERCANO_CONVERSATIONS_DB`. Schema source of truth:
`source/server/internal/conversation/schema.sql`. Tables: `conversations`
(id, title, project_dir, recap, timestamps), `turns` (role, content,
token counts, created_at), `conversation_compaction` (derived summaries).
Tool-calling turns carry an ordered Anthropic-wire-shape block array in the
`content_json` column; text-only turns use `content`.

```bash
# Ten most recent conversations
sqlite3 ~/.config/cercano/conversations.db \
  "SELECT id, title, datetime(last_turn_at,'unixepoch') FROM conversations \
   ORDER BY last_turn_at DESC LIMIT 10;"

# A conversation's turns, in order (truncate long bodies)
sqlite3 ~/.config/cercano/conversations.db \
  "SELECT role, substr(coalesce(nullif(content,''), content_json),1,120) \
   FROM turns WHERE conversation_id='<id>' ORDER BY created_at;"

# Turns whose tool calls mention a given tool
sqlite3 ~/.config/cercano/conversations.db \
  "SELECT conversation_id, datetime(created_at,'unixepoch') FROM turns \
   WHERE content_json LIKE '%\"name\":\"Edit\"%' ORDER BY created_at DESC LIMIT 20;"
```

### `~/.config/cercano/telemetry.db`

Usage metrics: local/cloud requests, token counts, per-tool and per-day
breakdowns. Read via the `cercano_stats` MCP tool or `cercano stats` CLI
command; raw SQL works too (`.tables` to explore — the schema is small).

## Config surfaces

All under `~/.config/cercano/`: `config.yaml` (server: models, endpoints,
cloud provider), `permissions.yaml` (permission mode + allowlist), `ui.yaml`
(CLI), `mcp.yaml` (hosted MCP servers; per-project override in
`.cercano/mcp.yaml`).

## Where outstanding work is tracked

- `docs/features/cli/README.md` — task rollups per phase, deviations from
  plan, and the "still outstanding" list for the CLI track.
- `README.md` (repo root) — Feature TODOs: new features and existing
  improvements across the whole project.
- `docs/features/` and `docs/agent/` — per-feature `design.md` / `plan.md`
  pairs; a plan's unchecked boxes are its remaining work.

## Troubleshooting local models & the agent lifecycle

Hard-won facts from debugging the local-runtime path (2026-07-07). When a
background job (recap, compaction, watchdog) fails, triage by the error's
*shape* in the server log:

| Log error | Meaning | Where to look |
|---|---|---|
| `model "X" not found in configured model_dirs` | name resolution — the requested name matches no GGUF (`matchesModel` in `internal/localruntime/llamaserver/provider.go`; bare names alias `<name>-latest` stems) | tier values in `config.yaml` vs actual filenames |
| `llama-server exited during startup: exit status 1` | the file or binary is the problem, not Cercano — reproduce manually (below) | GGUF/llama.cpp compatibility |
| `chat error (status 400) … exceeds the available context size` | config — `llama_server.context_size` must exceed the caller's prompt (compaction sends ~8k-token segments plus overhead, so 8192 can never fit; 16384+ works) | `llama_server.context_size` |

Reproduce a spawn failure outside the agent (macOS has no `timeout`; use
background + kill):

```bash
/opt/homebrew/bin/llama-server -m ~/.cercano/models/<file>.gguf \
  --host 127.0.0.1 --port 18099 --ctx-size 16384 --gpu-layers auto \
  > /tmp/lls.log 2>&1 & sleep 5; kill %1; grep -iE 'error|missing' /tmp/lls.log
```

Known incompatibility: **Ollama-pulled GGUF blobs are not guaranteed to load
in upstream llama.cpp.** qwen3-coder-next from Ollama's registry fails with
`missing tensor 'blk.0.ssm_dt.bias'` on every upstream build tried — Ollama
runs such models on its own engine. Fix by re-sourcing a HuggingFace-converted
GGUF, not by upgrading llama.cpp.

### Tracing raw tool-call streaming (`cercano_streamtrace`)

When an OpenAI-compatible server (llama-server, vLLM, etc.) produces wrong or
missing tool calls, the culprit is often *how it splits one tool call across
streaming fragments* — the name, id, and argument JSON can arrive on different
`data:` chunks, and the reassembly in `internal/llm/openai/stream.go` has to
tolerate it. Don't infer that wire shape from second-order symptoms; observe it.

A build-tagged tracer dumps every raw tool-call fragment to stderr. It is
**absent from normal builds** (a no-op stub the compiler inlines away — zero
`os.Stat`/env cost on the hot path), so it must be compiled in explicitly:

```bash
go build -tags cercano_streamtrace -o ~/bin/.cercano-libexec/cercano ./source/server/cmd/cercano
# codesign as usual, then restart the agent
```

Once running a trace-tagged binary, enable it at runtime with EITHER:

- `CERCANO_TRACE_OPENAI_STREAM=1` in the server's env (read once at start), or
- `touch ~/.cercano/trace-openai-stream` — checked live per stream, so it
  toggles on the already-running singleton server with no restart (remove the
  file to turn it off).

Output, one line per fragment, in the server log/stderr:

```
[openai-stream-trace] tool_call fragment idx=0 id="abc" name="Read" args="{"
[openai-stream-trace] tool_call fragment idx=0 id="" name="" args="\"path\":"
```

This is how the 2026-08-12 "delegated sub-agents log `called=[]`" case was
resolved: the trace proved GLM-4.5-Air sends the tool name on the *first*
fragment (ruling out a deferred-name streaming bug), which redirected the fix
to the real cause — the sub-agent flatten path stripping `BlockToolUse` from
the history the low-signal detector inspected (now read from
`ToolLoopResult.CalledTools` instead).

### Opt-in live open-model dispatch smoke

Most dispatch/sub-agent tests are hermetic and should run in normal `go test
./...`. To validate the real local/open-model path on a configured development
machine, use the skipped-by-default live smoke test:

```bash
cd source/server
CERCANO_LIVE_OPEN_MODEL_TEST=1 \
CERCANO_LIVE_OPEN_MODEL_BASE_URL=http://127.0.0.1:8080/v1 \
CERCANO_LIVE_OPEN_MODEL_MODEL=glm-4.5-air \
go test ./internal/server/ -run TestLiveOpenModelDispatch_ReadToolSmoke -count=1 -v
```

Optional: set `CERCANO_LIVE_OPEN_MODEL_API_KEY` if the endpoint requires one;
otherwise the test uses a dummy `local` key. The test creates a temp file with a
sentinel token, grants only `Read`, runs an agentic dispatch against the local
OpenAI-compatible provider, and asserts both the returned text and dispatch log
show a real tool-grounded run (`called=[Read]`). It is skipped unless
`CERCANO_LIVE_OPEN_MODEL_TEST=1` is set, so CI and normal development never
require a local model.

Model-resolution wiring (all resolved once at agent startup in
`cmd/cercano/main.go`): compaction summarizer ← `fast_light.open`;
recap and watchdog ← `fast_light_text.open`; interactive local chat ←
`open_model`. A broken `open_model` no longer breaks background jobs.

Agent-bounce pitfalls:

- Killing the agent does **not** kill its spawned llama-server children —
  orphans linger with the old `--ctx-size`. Check `pgrep -fl llama-server`
  and kill stale ones after a config change.
- The auto-relaunch path (CLI reconnect) execs the installed
  `~/bin/.cercano-libexec/cercano` binary **without** the launcher's
  rebuild-if-stale step. After changing server code, rebuild into libexec
  (`go build -o ~/bin/.cercano-libexec/cercano ./cmd/cercano/`) or run
  `cercano` itself before trusting a bounce.
- **Never `cp` over an installed signed binary in place** — macOS caches the
  code signature per vnode, and the overwritten binary dies with
  `Killed: 9` on launch even though `codesign -dv` looks fine. `rm` the
  destination first, then copy (fresh vnode, fresh signature), or build
  directly to the destination path.
- Verify a bounce actually happened: compare the agent PID/start time
  (`ps -o pid,lstart -p $(lsof -tiTCP:50052 -sTCP:LISTEN)`) against your kill,
  and count startup banners in the server log. A phantom "killed" echo from a
  mangled PID extraction cost a full debugging round.
- Compaction pass **success is silent** in the server log — falling token
  counts across consecutive `pass start` lines are the success signal.
  Recap success is visible in `conversations.db` (`recap`,
  `recap_updated_at`).
