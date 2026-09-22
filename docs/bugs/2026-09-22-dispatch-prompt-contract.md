# Original dispatch prompts are not compactable history

## Failure

A delegated task entered `RunToolLoop` as an ordinary user message in the same
slice as execution history. Inline compaction could freeze that message and
replace it with a generated summary. Keeping the summary's `Goal` did not keep
the full task, its constraints, or its exact wording. Context-window trimming
also had no explicit protected-prefix boundary.

The regression was reproduced through `Service.RunAgenticDispatch`: a scripted
compactor received the original task, replaced it with a summary, and all three
outgoing requests lost the original prompt.

## Invariant and implementation

Every agentic dispatch now enables `ToolLoopInput.PinUserInput`:

- The complete original task is retained verbatim as the first **user** message.
  It is not promoted to system priority, extracted into a short goal, or appended
  repeatedly as new instructions.
- The compactor receives only the execution-history suffix, never that task.
  The protected prefix is reattached to the resulting history for every request.
- `RequestBudgetInput.ProtectedPrefix` prevents mechanical trimming from removing
  the task. Its tokens are still included in the actual request estimate.
- Trimming can remove old execution history and generated summaries, while the
  newest message group is kept at a complete tool-call/result boundary.
- If the protected request cannot fit, preflight returns `ErrContextOverflow`
  before invoking the provider. It does not silently truncate the task.
- The iteration-limit, no-tools final request follows the same protection and
  context-budget check.
- The sub-agent system prompt explicitly identifies the first user message as
  the original task and says execution-history summaries cannot relax it.

The shared dispatch service enables the invariant for both host and worker
execution. Ordinary main-conversation turns retain their existing policy.

## Deterministic regression coverage

- `internal/hostsvc/tools/dispatch_prompt_test.go`: production dispatch entry,
  repeated rewrites, exact prompt preservation, and exclusion from compactor
  input for native local, flattened local, and cloud continuation paths.
- `internal/agent/pinned_prompt_test.go`: rewrite/in-place/no-op/empty/failing
  compactors; raw-history and generated-summary trimming; parallel tool-pair
  retention; complete prompt accounting; oversized-task rejection; and final
  no-tools requests, including final-pass context overflow.
- `internal/worker/dispatch_prompt_test.go`: actual worker-built service and
  production compactor, repeated reductions, and two independent dispatches.
  Scripted model requests retain the exact prompt; summarizer requests exclude it.
- Existing dispatch-evidence and partial-handoff tests now account for the fact
  that task-only initial history incurs no summarization call or spend.

These tests use scripted providers and synthetic tools, not live models,
external downloads, or the original incident's private payloads.

## Scope

This protects the task contract. It does not enforce arbitrary natural-language
constraints on subprocesses, make summaries semantically accurate, stop an
expanding investigation, or impose network byte limits. Those are separate
follow-ups; this fix does not claim to solve them.
