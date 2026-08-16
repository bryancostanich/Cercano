# Spec: Clean push confirmation UX

## Problem

In the `CERCANO - MOAR UX` conversation, the user typed `push` and saw multiple `y/n/d/c` confirmation prompts.

The root cause was not duplicate prompting for the same tool call. It was two distinct high-risk attempts:

1. The assistant delegated the push through `dispatch` with `git_push` granted. That grant requires approval because it authorizes a sub-agent to use an execute-tier publishing tool.
2. The sub-agent did not call `git_push`; it only inspected git state and reported what *would* be pushed. The dispatch layer flagged this as suspicious but still returned a normal result.
3. The main assistant then called `git_push` directly, which correctly required a second approval.

The user experienced this as repeated confirmation for one intent, but semantically the first approval authorized a delegated attempt that never executed the requested push.

## Goals

1. For explicit user requests to push, prefer a direct scoped `git_push` call rather than dispatching to a sub-agent. One user intent should map to one high-risk approval for the actual push attempt.
2. Make delegated write/execute task false-success a hard failure. If a sub-agent was granted write/execute tools, called none of them, and returned a non-empty completion, the dispatch result should be an error rather than advisory success.
3. Keep safety boundaries intact. Do not reuse or deduplicate approvals across different actors or different tool calls. A sub-agent approval and a main-agent direct push approval are not the same permission grant.
4. Improve prompt/protocol wording so agents understand that noisy git *inspection/plumbing* should be delegated, but explicit user-authorized publishing (`git_push`) should use the scoped tool directly when that is the requested action.

## Non-goals

- Do not implement permission approval caching/deduplication for execute-tier tools.
- Do not make `git_push` lower risk. It remains execute-tier because it publishes changes outside the local checkout.
- Do not remove dispatch's ability to run `git_push` when an explicitly delegated workflow truly requires it. The fix is to avoid unnecessary delegation for simple explicit push and to fail delegated false-success.

## Current evidence

- `source/server/internal/protocols/catalog.go` has a `delegate-git-plumbing` protocol that tells agents to delegate git mechanics before inline execution. Its examples include `git_push`, which makes the push case ambiguous.
- `source/server/internal/capabilities/builtins/dispatch_cap.go` says dispatch should prefer scoped tools such as `git_push` over Bash for git/GitHub workflows and warns that write-capable grants escalate to confirmation.
- `source/server/internal/hostsvc/tools/tools.go` has `detectSuspiciousNoOp`, which identifies the exact failure: granted write/execute tools, called none, yet returned final text. Today this only annotates `dispatch.Result` with `Suspicious` and `SuspicionReason`.
- `source/server/internal/capabilities/builtins/git_write.go` defines `git_push` as execute-tier and implements the direct push.

## Desired behavior

### Explicit `push`

When the user says `push` or otherwise explicitly asks to publish the current branch:

1. The assistant may inspect branch/upstream/status with read-only git tools if needed.
2. The assistant should call `git_push` directly with scoped arguments (`cwd`, remote, branch, `force=false` unless explicitly requested).
3. The user sees one confirmation prompt for `git_push`.
4. On success, the assistant reports the branch/SHA pushed.

### Delegated write/execute false success

When dispatch grants any write/execute tool and the sub-agent returns final text without calling any write/execute tool:

1. Dispatch should return an error result to the parent tool loop.
2. The error text should include the suspicion reason and say the delegated work likely did not happen.
3. The parent assistant must recover as a failed tool call, not treat it as successful completion.
4. Existing progress/logging should remain so the sub-agent trace is inspectable.

## Acceptance criteria

- The git plumbing protocol and dispatch tool description no longer imply that explicit `git_push` should be delegated merely because it is git-related.
- A dispatch unit test verifies that a write/execute grant with no write/execute tool calls returns an error, not a successful suspicious result.
- Existing suspicious/no-op detection tests continue to pass or are updated to assert hard-failure behavior.
- `git_push` remains execute-tier and directly permission-gated.
- No approval deduplication is introduced.

## Follow-up filed from this session

After the local fallback context-budgeting fix, this conversation hit another overflow:

```text
openai-responses failed (aborted: 3 consecutive iterations of tool errors) — retrying on llama_server
local context is smaller — trimmed conversation history to fit the model window
preflight context_overflow (21807 tokens used vs 16384 limit): request is ~21807 tokens including ~12304 tool tokens and 8192 reserved output tokens, but this model holds 16384
```

This is a separate follow-up for the compact fallback effort: even after trimming history, a 16k local window can still be impossible with `8192` reserved output tokens plus `~12304` tool tokens. The likely next fix is to make tight local fallback reduce output reserve and/or use a smaller catalog before declaring overflow. This is intentionally out of scope for the push UX fix.
