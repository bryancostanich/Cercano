# Single-dispatch diagnostic tracing

Use this to investigate a capable model that repeatedly explores instead of implementing. The persisted conversation is not necessarily the model's actual request after compaction, trimming, compatibility transforms, and image rewriting.

## Privacy and scope

OFF by default. Captures only the **first agentic dispatch from the selected parent conversation**, with a persistent exclusive `.claimed` file preventing further captures across workers or restarts. Nested dispatches and unrelated parents do not inherit capture permission. A trace already in progress continues to its end if the arming file is removed.

Trace content includes source, prompts and tool output verbatim and **can contain secrets embedded in that content**. This is not a secret scrubber. Do not upload or commit traces. No HTTP authentication headers, client configuration objects or raw transport-error strings are captured. Images, image URLs and opaque provider reasoning blobs are omitted. Ordinary diagnostic events introduced here are metadata-only.

The output directory must be owner-only (normally 0700) and not a symlink; trace files are 0600 and exclusively created. Insecure existing directories are refused rather than silently chmodded. Use a trusted, owner-controlled parent directory. This does not defend against another process already running as your user. No automatic upload, retention cleanup or disk-quota policy is introduced: inspect file size during the bounded reproduction, then archive privately or delete deliberately. Setup/write failure is advisory, never a reason to fail the dispatch; a consumed claim is not automatically reset on failure.

## Enable on the running agent

The updated binary must first be deployed and the old singleton restarted once. After that, arming does **not** require another restart:

```bash
# Substitute the PARENT conversation ID, not the previous child dispatch ID.
# Default location unless the agent inherited CERCANO_DISPATCH_TRACE_DIR.
umask 077
trace_dir="$HOME/.cercano/trace/dispatch"
mkdir -p "$trace_dir"
# If this existing directory is not private, choose/fix its permissions
# deliberately before continuing; the collector will otherwise refuse it.
printf '%s\n' 'PARENT_CONVERSATION_ID' > "$trace_dir/armed"
chmod 600 "$trace_dir/armed"
```

Then start the desired agentic dispatch from that conversation. Other conversations cannot claim it. If several dispatches from the selected parent start concurrently, the first to claim wins; avoid doing that during the reproduction.

A metadata log identifies `capture opened: dispatch=...`. The trace is `<dispatch-id>-<timestamp>.jsonl`. A `.claimed` file means the one-shot capture has been consumed, even if the dispatch subsequently failed. Removing `armed` disables future file-based arming; it does not stop an ongoing capture. Never remove `.claimed` while a captured run is active.

For another deliberate capture, remove `armed`, wait for the captured run to finish, preserve/delete its trace as appropriate, remove **only** `.claimed`, and then re-create `armed`. Do not clear the directory indiscriminately.

Environment opt-in is also supported for an agent launched with:

```bash
CERCANO_DISPATCH_TRACE=1
CERCANO_DISPATCH_TRACE_PARENT=PARENT_CONVERSATION_ID
CERCANO_DISPATCH_TRACE_DIR=/absolute/private/new-capture-directory
```

Export these variables in the agent's launch environment. Setting them only in a tool subprocess does not update a running singleton or warm worker. Environment opt-in takes precedence over the arming file. Unset `CERCANO_DISPATCH_TRACE` to disable that path; removing `armed` alone does not override it.

## What is captured

JSONL records have sequence number, timestamp, dispatch ID, event kind and (where applicable) iteration:

- `dispatch_open`, `dispatch_start`, `dispatch_done`: task, grants, selected route, limits and outcome. Final token counters retain the existing tool-loop result semantics; they are not a new cumulative accounting source.
- `model_request`: system prompt, messages, tool schemas/choice, model, quality routing hints, temperature, output limit, thinking flag, request IDs and request-budget estimates. Captured after compaction, mechanical trimming and loop-side compatibility transforms.
- `model_response`: aggregated returned blocks, stop reason, usage, actual serving route when supplied, and safe error code. Includes initial stream-open failures and partial collected responses.
- `compaction`: before/after history and attributed spend for **every configured pass**, including same-size rewrites, no-ops and history preserved after failure. These describe the accepted post-pass history; summarizer response events expose the raw proposed summary separately.
- `summarizer_request` / `summarizer_response`: exact prompt passed to the summarizer runner, raw returned text before parsing, temperature/output limit, model selection, reported usage and safe failure classification. Correlated by dispatch, loop iteration and per-call request ID, including chunked local calls and cloud fallback.
- `tool_call` / `tool_result`: requested arguments, executed outcomes, window-cap truncation markers and original sizes where known. Result events arrive in execution completion order; model requests show the actual ordered, paired history. Pre-execution rejections appear in the subsequent model request rather than an executed-result event.

## Fidelity limits

This is the exact **tool-loop adapter input**, not the final HTTP payload. Provider adapters and failover routing can still transform it. Provider-specific `ProviderExtras`, images and opaque reasoning are not serialized. Summarizer prompts are captured at the TurnRunner boundary; runner-added system prompts, final wire formatting and finish reasons unavailable at that seam are not claimed as captured. If evidence points at adapter formatting, add a narrowly scoped adapter-level capture rather than inferring wire correctness from these records.

Tracing is for agentic dispatches, not ordinary chat or one-shot dispatches. It does not reconstruct old in-memory summaries, recover already-buffered logs from the old worker host, change compaction policy, retry requests, increase budgets or introduce stuck detection.

## Bounded reproduction and analysis

Before a real-model run, state the predicted observation and a stopping bound. Do not repeat an hour-long dispatch blindly. Use a disposable worktree for writes and preserve the same task/model/settings to avoid confounding the comparison.

1. Arm the original parent immediately before the intended dispatch. Avoid intervening helper dispatches from that parent.
2. Watch the live log and trace. Stop the experiment at the first repeated unchanged read cycle or an agreed time/cost bound. The collector itself is not a watchdog and does not cancel work.
3. Compare the repeated read's outgoing request with the prior tool result. Was the evidence still present verbatim, retained accurately in a summary, truncated, or absent?
4. Locate the immediately preceding compaction and summarizer events. Distinguish missing task intent, lost findings and same-size rewrites from a model that had adequate context but still repeated itself.
5. If needed, perform a short controlled continuation with only the suspected missing history restored. Keep model, task and settings fixed. This patch supplies evidence, not an automated replay engine.

Expected discriminators: missing evidence before repetition supports a history-loss hypothesis; intact paired evidence weakens it; repeated capped outputs point at read scope/truncation; intact loop inputs do not by themselves rule out provider serialization defects.

## Deterministic verification

No live model is needed for the regression tests:

```bash
cd source/server
go test ./internal/dispatchtrace ./internal/agent ./internal/loopcompact ./internal/hostsvc/tools ./internal/worker ./internal/server ./cmd/cercano -count=1
go test -race ./internal/dispatchtrace ./internal/hostsvc/tools ./internal/loopcompact -run 'Test(DispatchTrace|SummarizerDispatchTrace|Capture|Scoped|Disabled|Reject|LiveArm|UnsafeArm)' -count=1
```

Coverage includes private modes, unsafe-directory/arming rejection, concurrent one-shot claims, disabled mode, context isolation, post-close safety, payload omissions, exact scripted-provider input fidelity, same-size compaction changes, production summarizer capture and unrelated/repeated dispatch isolation.
