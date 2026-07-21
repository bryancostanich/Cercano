# Trajectory Export

## Goal

Add first-party export of a complete Cercano conversation as an agent trajectory bundle. The export should be useful for debugging, sharing, evaluation, supervised fine-tuning, and reinforcement-learning pipelines while preserving Cercano-specific artifacts that do not fit cleanly into a single interchange document.

The canonical interchange format is Harbor's **Agent Trajectory Interchange Format (ATIF)**, targeting **ATIF-v1.7**. The canonical file inside every export is `trajectory.json`.

This feature is intentionally agent-owned. The CLI is a thin client: it may present pickers, forms, progress, and completion actions, but it must not know how conversations are stored, how turns become ATIF, how artifacts are collected, how subagents are resolved, or how bundles are written.

## Non-goals

- Do not invent a Cercano-specific replacement for ATIF.
- Do not make the CLI parse the conversation database or reconstruct tool turns.
- Do not snapshot an entire project checkout in V1.
- Do not promise perfect secret removal; redaction is best-effort unless a later verifier proves otherwise.
- Do not fabricate subagent trajectories when the underlying persisted subagent run is unavailable.

## Format decision

V1 exports a **directory bundle** with an optional zip packaging layer.

ATIF standardizes the trajectory document, not a full filesystem bundle. For full-fidelity Cercano exports, a single JSON file is too limiting: tool outputs can be large, subagents deserve independent trajectories, raw Cercano metadata should be preserved, and future image/media assets need stable paths. Therefore Cercano defines a small bundle convention around ATIF.

The invariant is:

> Whether exported as a directory or a zip, the contents are identical: one top-level Cercano trajectory bundle containing a root `trajectory.json`, a `manifest.json`, artifacts, subagents, and metadata.

## Bundle layout

Default directory export:

```text
cercano-trajectory-<conversation-id>-<timestamp>/
  trajectory.json
  manifest.json

  artifacts/
    tool-results/
    files/
    logs/

  subagents/
    <subagent-trajectory-id>/
      trajectory.json
      manifest.json
      artifacts/
        tool-results/

  images/

  metadata/
    conversation.json
    conversation-turns.raw.jsonl
    config.redacted.json
    environment.json
```

Empty directories may be omitted. Paths stored inside `trajectory.json` and `manifest.json` must be relative to the bundle root. Zip exports must contain exactly one top-level directory with the same layout.

## Root `trajectory.json`

The root trajectory must be valid ATIF-v1.7 JSON.

Minimum shape:

```json
{
  "schema_version": "ATIF-v1.7",
  "session_id": "<cercano conversation id>",
  "trajectory_id": "main",
  "agent": {
    "name": "cercano",
    "version": "<cercano version>",
    "model_name": "<default/current model if known>"
  },
  "steps": [],
  "final_metrics": {},
  "extra": {
    "cercano": {
      "bundle_format": "cercano-trajectory-bundle",
      "bundle_format_version": 1,
      "manifest_path": "manifest.json",
      "conversation_id": "<id>"
    }
  }
}
```

### Turn mapping

| Cercano persisted data | ATIF field |
|---|---|
| user turn | `Step{source:"user", message:...}` |
| assistant/model text | `Step{source:"agent", message:...}` |
| tool-use block | `Step.tool_calls[]` |
| tool-result block | `Step.observation.results[]` |
| system/context-management event | `Step{source:"system"}` |
| model/provider | `Step.model_name` and/or `extra.cercano.provider` |
| token counts | `Step.metrics.prompt_tokens`, `Step.metrics.completion_tokens` when known |
| costs | `Step.metrics.cost_usd` only when already accurately persisted |
| project/workdir | `extra.cercano.work_dir` |
| permission decisions | `extra.cercano.permission_*` |

Step IDs must be sequential starting at `1` within each trajectory document.

### Tool output policy

V1 should externalize full tool outputs into `artifacts/tool-results/` and keep ATIF observations readable.

For each tool result:

- `ObservationResult.content` contains a short preview or pointer.
- `ObservationResult.extra.artifact_path` points to the full output file when externalized.
- command metadata such as exit code, stderr path, timeout, or truncation status lives under `extra`.

Example:

```json
{
  "source_call_id": "call-001",
  "content": "Tool output written to artifacts/tool-results/step-0004-call-001.stdout.txt",
  "extra": {
    "artifact_path": "artifacts/tool-results/step-0004-call-001.stdout.txt",
    "stderr_path": "artifacts/tool-results/step-0004-call-001.stderr.txt",
    "exit_code": 1
  }
}
```

Default preview size should be small enough to keep `trajectory.json` readable, for example 4 KiB or 8 KiB. The manifest records the full artifact path, byte size, and checksum.

### Context management

Context compaction, elision, recap regeneration, and related send-view transformations should be represented as ATIF system steps when they are present in the persisted conversation history or can be reconstructed faithfully.

Example:

```json
{
  "step_id": 12,
  "source": "system",
  "message": "Conversation context compacted.",
  "observation": {
    "results": [
      {
        "content": "Older turns summarized into compacted context layer."
      }
    ]
  },
  "extra": {
    "cercano": {
      "type": "context_management",
      "operation": "compact"
    }
  }
}
```

When steps are copied from an earlier trajectory segment only to provide context, set ATIF `is_copied_context: true` so supervised fine-tuning consumers can filter them out.

## Subagents

V1 must support exporting subagents when Cercano has enough persisted data to identify and reconstruct the delegated run.

Use external sub-bundles under `subagents/` by default:

```text
subagents/
  dispatch-0001/
    trajectory.json
    manifest.json
    artifacts/
      tool-results/
```

The parent trajectory references each subagent through ATIF's `subagent_trajectory_ref` using a relative `trajectory_path`:

```json
{
  "source_call_id": "call-dispatch-0001",
  "content": "Delegated work to subagent dispatch-0001.",
  "subagent_trajectory_ref": [
    {
      "trajectory_id": "dispatch-0001",
      "trajectory_path": "subagents/dispatch-0001/trajectory.json"
    }
  ],
  "extra": {
    "artifact_path": "subagents/dispatch-0001/"
  }
}
```

If the dispatch/workflow call exists only as a normal tool result and no persisted subagent conversation is available, export it as a normal tool call/result and record the limitation:

```json
{
  "extra": {
    "cercano": {
      "subagent_export_status": "not_available",
      "reason": "dispatch result did not include a persisted subagent conversation id"
    }
  }
}
```

Do not synthesize a fake subagent trajectory from a summary-only result.

Embedded ATIF `subagent_trajectories` are allowed by the ATIF spec but are not the V1 default. External sub-bundles are easier to inspect, zip, checksum, and extend with artifacts.

## Manifest

`manifest.json` is Cercano's bundle index. It is not part of ATIF, but it makes the bundle self-describing and verifiable.

Minimum shape:

```json
{
  "format": "cercano-trajectory-bundle",
  "format_version": 1,
  "created_at": "2026-07-20T15:30:12Z",
  "root_trajectory": "trajectory.json",
  "schema_version": "ATIF-v1.7",
  "conversation_id": "<id>",
  "bundle_name": "cercano-trajectory-<id>-<timestamp>",
  "redaction": {
    "mode": "default",
    "warning": "Pattern-based redaction was applied. Review before sharing."
  },
  "artifacts": [
    {
      "path": "artifacts/tool-results/step-0004-call-Bash-01.stdout.txt",
      "kind": "tool_stdout",
      "step_id": 4,
      "tool_call_id": "call-001",
      "bytes": 12345,
      "sha256": "..."
    }
  ],
  "subagents": [
    {
      "trajectory_id": "dispatch-0001",
      "path": "subagents/dispatch-0001/trajectory.json"
    }
  ]
}
```

Manifest arrays should be sorted by path for stable inspection and diffs.

## Metadata preservation

V1 should include raw Cercano data in `metadata/`:

```text
metadata/conversation.json
metadata/conversation-turns.raw.jsonl
metadata/config.redacted.json
metadata/environment.json
```

ATIF is the interchange document; raw Cercano metadata is the lossless recovery layer. If the ATIF mapper has a bug or ATIF evolves, the raw data lets a future exporter regenerate the trajectory.

`config.redacted.json` should contain relevant configuration after redaction, not secrets. `environment.json` should prefer non-sensitive runtime facts such as OS, Cercano version, export timestamp, workdir, and provider/model names.

## Redaction

V1 supports:

```text
--redact default|none
```

Default is `default`.

Default redaction should cover obvious high-risk patterns:

- API keys and bearer tokens,
- OAuth tokens,
- private keys,
- password-looking environment variables,
- passwords embedded in URLs where detectable,
- known cloud provider credential shapes.

The manifest must clearly state that pattern-based redaction is best-effort and that users should review bundles before sharing. `--redact none` is intended for private local archival/debugging.

## Logs

Logs are excluded by default. They can contain unrelated local diagnostics and secrets.

Support an explicit option:

```text
--include-logs
```

When enabled, include relevant logs under `artifacts/logs/` and list them in the manifest.

## Zip packaging

Zip export is a packaging layer over the exact same directory bundle.

Rules:

- `.zip` output contains one top-level directory.
- The top-level directory name should match the zip basename.
- No absolute paths.
- No path may escape the bundle root.
- Symlinks should either be skipped or included as symlinks only when they point inside the bundle root.

If `--out` ends in `.zip`, zip output may be inferred. A separate `--zip` flag may also be supported.

## Command and UX

### Headless command

The non-interactive command should require enough information to run without prompts:

```bash
cercano export trajectory --conversation <id> --out ./exports/my-run.zip
```

Recommended options:

```text
--conversation <id>     Conversation to export. Required in headless mode unless --latest is provided.
--latest                Export the most recent conversation explicitly.
--out <path>            Directory or .zip output path.
--zip                   Force zip packaging.
--redact default|none   Redaction mode. Default: default.
--include-logs          Include server/log artifacts. Default: false.
--overwrite             Replace an existing output path.
```

If required arguments are missing in headless mode, return a usage error with examples. Do not open an interactive picker from headless mode.

### TUI slash command

The TUI should expose:

```text
/export trajectory
/export trajectory ./exports/current-run.zip
/export traj
```

When invoked without a conversation id, the CLI presents an export flow similar to `/resume`:

1. Conversation picker.
2. Destination/options modal.
3. Progress/completion view.

The picker should reuse the `/resume` history UI as closely as possible. It should support fuzzy filtering by title, recap, project path, and conversation id. The current conversation should be the default selection when available; otherwise the most recent conversation should be selected.

After selection, the destination modal collects:

- destination path,
- format: zip or directory,
- redaction mode,
- include logs yes/no.

Default destination should be a readable zip path, for example:

```text
~/Downloads/cercano-trajectory-<slug>-<timestamp>.zip
```

Progress should report major phases:

```text
✓ Loaded conversation turns
✓ Wrote root trajectory.json
✓ Exported tool artifacts
✓ Exported subagent trajectories
✓ Wrote manifest.json
✓ Created zip archive
```

Completion actions can include open in Finder, copy path, and done.

## Agent/CLI boundary

This boundary is load-bearing.

The CLI is a thin client. It owns only presentation and input collection:

- conversation picker UI,
- destination/options modal,
- progress rendering,
- copy/open convenience actions,
- slash command parsing.

The agent/server owns the export behavior:

- reading conversation persistence,
- reconstructing ATIF steps,
- resolving subagent conversations,
- collecting tool artifacts,
- applying redaction,
- writing bundle directories,
- creating zip archives,
- producing manifest entries and checksums,
- validating the export,
- reporting progress events.

The CLI must not read `conversations.db` directly, parse raw `content_json`, inspect telemetry tables, or write trajectory files itself.

A suitable RPC shape is a streaming export call so the server can report progress:

```text
ExportTrajectory(ExportTrajectoryRequest) returns (stream ExportTrajectoryEvent)
```

Request fields should include:

```text
conversation_id
out_path
format: directory|zip|infer
redaction_mode: default|none
include_logs
overwrite
```

Events should include:

```text
started
progress { phase, message, current, total }
warning { message, code }
completed { output_path, manifest_path, artifact_count, subagent_count }
failed { message, code }
```

The same agent-owned exporter should back the headless command and the TUI slash command.

## Validation

V1 should include internal validation before reporting success:

- `trajectory.json` is valid JSON.
- `schema_version` is `ATIF-v1.7`.
- step IDs are sequential starting at `1` within every trajectory.
- observation `source_call_id` values reference tool calls in the same step when applicable.
- every manifest artifact path exists.
- every subagent trajectory path exists.
- every stored path is relative and does not escape the bundle root.
- SHA-256 checksums match artifact bytes.

Tests should include an optional Harbor validator integration if Harbor is installed, skipped otherwise. Normal Go test runs must not require Python Harbor dependencies.

## Implementation sketch

Server-side package:

```text
source/server/internal/trajectory/
  atif.go
  bundle.go
  manifest.go
  redact.go
  export.go
  validate.go
  zip.go
```

Potential server command/RPC wiring:

```text
source/server/cmd/cercano/          # headless command
source/proto/agent.proto            # ExportTrajectory streaming RPC
source/server/internal/server/      # RPC handler
```

CLI wiring should only add slash command UI and call the RPC.

## V1 acceptance criteria

- A headless command exports a selected conversation to a directory bundle.
- `.zip` output creates a zip containing one top-level bundle directory.
- The root `trajectory.json` is valid ATIF-v1.7 and contains all user/agent/tool steps that can be reconstructed from persistence.
- Full tool outputs are preserved under `artifacts/tool-results/` and referenced from ATIF observations.
- Available subagent conversations export under `subagents/` and are referenced from the parent trajectory.
- Raw Cercano conversation metadata is included under `metadata/`.
- Default redaction is applied and recorded in the manifest.
- Logs are excluded unless explicitly requested.
- The TUI `/export trajectory` flow uses a `/resume`-style picker when no conversation id is supplied, then a destination/options modal.
- The CLI remains a thin client; bundle construction happens in the agent/server.
