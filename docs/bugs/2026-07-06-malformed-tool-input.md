# Bug brief: malformed streamed tool input poisons the turn and every later cloud request

**Status:** ROOT-CAUSED + defended 2026-07-06 — collectStream now validates
accumulated tool input and wraps invalid bytes in a safe envelope
(`internal/agent/toolloop.go`); the same change stops dropping whole-input
tool payloads (`ToolInputRaw`), which was silently emptying every streamed
Ollama tool call. Observed in the wild 2026-07-06 morning in conversation
`de88b72ec39e641c` ("CERCANO - SETUP WIZARD"); same class as the incidents on
2026-07-05 evening (`find`/`ls`/`cat` parse errors, conversation
"CERCANO - UX").
**Severity:** critical availability + silent data loss. One bad tool call
knocked out the cloud path for the rest of the session and dropped turns from
the conversation store.

## Symptom

The CLI showed:

```
⚠ anthropic failed (POST "http://127.0.0.1:3456/v1/messages": 500 Internal Server Error
  {"type":"error","error":{"type":"api_error","message":"Unexpected token 'i', ..."":{\"cmd\": find /Users\"... is not valid JSON"}}) — retrying on ollama
```

Agent log (`$TMPDIR/cercano-server.log`), the direct evidence:

```
[tool-loop] call Bash args={"cmd": find /Users/... | head -5; ls ...}      ← unquoted value
[tool-loop] call Bash args={"cmd": ["/bin/zsh", "-c", "cd /}               ← truncated
[tool-loop]   -> run_command: parse args: invalid character 'i' in literal false (expecting 'a')
[tool-loop] marshal blocks failed: json: error calling MarshalJSON for type json.RawMessage: invalid character 'i' in literal false (expecting 'a')
```

(`invalid character 'i' in literal false` = a JSON parser reading the bare word
`find` as the literal `false`: `f` matches, `i` doesn't. Meridian's
JS-side parse of the request body fails the same way — that's the 500.)

## Chain

1. **Meridian's OpenCode adapter emits structurally invalid tool input.**
   Translating SDK tool calls to the client's declared tool schema
   (`bash{command}` → `Bash{cmd}`), it sometimes produced an unquoted value
   (`{"cmd": find /Users ...}`) or a truncated fragment
   (`{"cmd": ["/bin/zsh", "-c", "cd /`). All observed cases were Bash calls.
2. **collectStream accepted the bytes unvalidated** into
   `Block.ToolInput json.RawMessage`.
3. Three downstream failures from the one poisoned block:
   - dispatcher rejects the args (`parse args: ...`) — recoverable, the model
     retried within the turn;
   - **turn persistence fails** (`marshal blocks failed` — encoding/json
     refuses to marshal an invalid RawMessage), so the turn silently never
     reaches the conversation store;
   - the poisoned block **replays in-memory into every subsequent request**;
     the anthropic SDK embeds the raw bytes verbatim, so every later cloud
     call has an invalid body → Meridian 500 → fallback to ollama for the
     rest of the session.

## Second defect found during diagnosis

`collectStream` ignored `StreamEvent.ToolInputRaw` — the field the **Ollama**
adapter uses to deliver complete tool arguments on the start event (every
other adapter streams input as delta fragments). Streamed Ollama tool calls
therefore arrived with `{}` args: `read_file: path is required`,
`glob: pattern is required`, then "aborted: 3 consecutive iterations of tool
errors". This is what made local-model tool calling look broken on 2026-07-05
evening while cloud was down.

## Resolution (Cercano-side guard)

In `collectStream` (`internal/agent/toolloop.go`):

- `EventToolUseStart` seeds the args buffer from `ToolInputRaw` when present
  (whole-input providers now work through the same accumulation path).
- `flushTool` validates the accumulated bytes with `json.Valid`. Invalid
  input is wrapped as `{"_malformed_tool_input": "<raw text>"}` — always
  valid JSON, so the turn persists and replays cleanly, and the raw attempt
  is preserved.
- The dispatch loop short-circuits envelope-wrapped calls with an error
  tool_result that quotes the raw input, so the model sees exactly what it
  sent and can retry with valid JSON. The tool never runs on garbage.

Invariant established: **`Block.ToolInput` is valid JSON everywhere past the
stream collector.** (The ollama adapter's panic on invalid input at
`internal/llm/ollama/adapter.go` remains as the assertion of that invariant.)

## Upstream

The emitter bug is in `@rynfar/meridian`'s OpenCode adapter (arg translation
to client tool schemas). Worth a report with the two observed shapes; the
Cercano guard makes us safe regardless.
