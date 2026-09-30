# Database-backed dispatch evidence

Every agentic dispatch with conversation persistence now records its request-construction evidence automatically in the existing host-owned conversation database. There are no arming files, parent allowlists, environment switches, capture slots, or separate JSONL trace files. The former one-shot file collector has been removed. The live worker log-forwarding fix is retained.

## Storage and lifecycle

`dispatch_events` is an additive table installed by normal `conversation.Open` initialization, including when opening an existing database. Its key is `(conversation_id, seq)`; duplicates are rejected, not overwritten. Each fresh dispatch has its own child conversation ID and its own event sequence. Records retain all earlier requests and compaction passes rather than overwriting the latest summary. Deleting a conversation cascades deletion of its event records. Parent and child conversation deletion otherwise follows existing conversation-store behavior.

The host remains the only SQLite owner. In-process dispatches append through the narrow `conversation.DispatchEventStore` interface. Worker dispatches use correlated request/response messages over their existing host stream. The host acknowledges a write only after the database operation returns. Only child conversations successfully created under the current turn or its authorized descendants can write events through that stream.

Writes have a bounded wait and respect cancellation. Recording failures do not abort the task: they are logged without payload content, counted in the recorder, reported in the final dispatch result/progress, and recorded in the failure log. Later successful events retain sequence gaps, and a successfully written `dispatch_done` includes the preceding persistence-failure count. A failed final append, cancelled stream or process crash can leave no terminal event; absence of one is not evidence of success. Existing ordinary turn persistence remains best-effort; this change does not claim to repair all historical turn accounting.

## Evidence retained

- `dispatch_start` / `dispatch_done`: task, parent, grants, selected model/route, limits, result accounting and safe failure classification.
- `model_request`: messages after loop compaction, mechanical trimming and compatibility conversion; system prompt, tool definitions and choice; model settings and request-budget estimates.
- `model_response`: collected response blocks, stop reason, usage, serving route and safe error classification, including partial responses and stream-open errors.
- `compaction`: the accepted before/after histories for every configured pass, including same-size rewrites, no-ops and history preserved after failure; attributed summarizer spend.
- `summarizer_request` / `summarizer_response`: prompt and raw summary text, selection/settings and available usage, correlated by dispatch, iteration and request ID. Earlier summaries survive later reductions.
- `tool_call` / `tool_result`: emitted arguments, executed outcomes and window-cap truncation metadata. Ordinary conversation turns continue to retain the transcript. Pre-execution rejections remain visible in subsequent model requests rather than executed-result events.

Request snapshots intentionally retain the actual text view instead of trying to regenerate it later from changed source files, tool schemas or current compaction settings. This increases database size. There is no separate one-shot or silent capture cap; evidence has the same conversation lifetime. Storage deduplication and a new retention policy are not part of this change.

## Reading the evidence

No model run or arming step is needed. After deploying and restarting onto this version, dispatches record automatically.

For a dispatch ID, using SQLite in read-only mode:

```sql
SELECT seq, kind, iteration, created_at, payload
FROM dispatch_events
WHERE conversation_id = 'DISPATCH_CONVERSATION_ID'
ORDER BY seq;
```

To enumerate dispatches for a parent:

```sql
SELECT c.id, c.title, COUNT(e.seq) AS evidence_records
FROM conversations c
LEFT JOIN dispatch_events e ON e.conversation_id = c.id
WHERE c.parent_id = 'PARENT_CONVERSATION_ID'
GROUP BY c.id, c.title;
```

The store API also exposes `ListDispatchEvents`. No new UI or public retrieval RPC is introduced. To investigate rereading, compare the relevant `model_request` with earlier tool results, then inspect the preceding compaction and summarizer records. Do not infer final HTTP serialization from the loop-level request snapshot.

## Privacy and fidelity boundaries

Like ordinary conversation turns, these records contain sensitive prompts, source code and tool results. They stay in the existing local database and its backups; do not upload or commit them. No transport authentication headers, provider configuration objects or arbitrary transport-error bodies are recorded. This is not a secret scrubber for secrets embedded in task/source/tool content.

Images/image URLs and opaque provider reasoning blobs are omitted with explicit markers. Provider-specific extras and final wire serialization are not captured. Summarizer prompts are captured at the runner boundary; runner-added system prompts and finish reasons unavailable there are not fabricated. Ordinary chat and one-shot non-agentic calls are outside this dispatch-loop feature. Historical lost compaction state cannot be reconstructed retroactively.

Existing JSONL captures on disk are not imported or deleted. Their old selectors and claim files are ignored by this version. Deployment may archive the obsolete `armed` file to disarm an old binary without deleting its evidence or interrupting active work.

## Verification

Deterministic tests cover additive old-database migration and reopen, ordering/duplicate rejection, cascade deletion, per-dispatch isolation, immutable earlier snapshots, scripted-provider request fidelity, successive compaction and summarizer records, failure visibility and sequence gaps, concurrent recording, cancellation, worker-built service wiring, and the real host receive loop over an in-memory gRPC stream. No live model is required.
