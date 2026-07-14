# Autonomous run — 2026-07-13 — Meridian rip & replace

**Objective:** complete the Meridian removal and native Anthropic
subscription-OAuth replacement across all remaining phases; done = every
phase complete, branch `replace-meridian` builds + tests green, ready to land
on main for Bryan to test. No push, no merge.

**Decision rule:** design-decision matrix; rank correctness → cleanliness →
future cost; implementation effort is not a dimension. Take the unambiguous
cleanest after honest counter-cases; log BLOCKED on a genuine correctness tie
and move on. Fork at architectural decisions only.

---

## RUN STATUS (updated at end)

_In progress._

Entering state (before this run): Phases 1–2 done; Phase 3 factory/call-site
wiring done (commit 63da437d). Remaining: sign-in flow (RPC + handler + CLI),
config/catalog/wizard + migration, Meridian deletion.

---

## Forks

<!-- one entry per architectural fork, appended as encountered -->

### Fork A — subscription sign-in RPC shape

**Decision point.** How to expose the Claude subscription sign-in over gRPC.
The ChatGPT route already has `StartChatGPTLogin` (streaming RPC → device-code
modal). The new Claude flow is a PKCE loopback (agent binds a local port, sends
the authorize URL, blocks on the browser redirect). How should the RPC be
structured?

**Options.**

1. **Dedicated `StartClaudeLogin` RPC** (new request/event messages, own
   handler `claude_login.go`, own agentclient wrapper, own CLI modal),
   parallel to `StartChatGPTLogin`.
   - Cost: ~1 RPC + 2 messages in proto; ~90-line handler; ~30-line client
     wrapper; ~1 CLI modal (~200 lines, mostly view). Additive only.
   - Risk: low — touches no working code. Regression surface = new code only.
   - Reward: honest modeling of a distinct OAuth grant; reversible (mergeable
     later).
   - Side effects: some duplication with the ChatGPT modal/wrapper skeleton.
2. **Generalize to `StartSubscriptionLogin(provider, …)`** — one RPC, one
   union event, server switches on provider; migrate the existing ChatGPT
   caller onto it.
   - Cost: one RPC/modal/wrapper, but a provider switch in the handler + a
     union event with device-only fields (`user_code`, `account_id`) +
     up-front refactor of the working ChatGPT sign-in (proto rename, handler,
     modal, tests).
   - Risk: higher — refactors a shipping feature; regression is loud (sign-in
     breaks) but real; less reversible.
   - Reward: single user-facing surface; marginally lower future cost if a 3rd
     subscription provider appears.
   - Side effects: union carries provider-only fields (mild variant blemish).
3. **Reuse the `StartChatGPTLogin` message for Anthropic** (carry the
   authorize URL in `verification_url`, leave `user_code` empty). **HACK** —
   overloads a ChatGPT-named field for a different purpose; reader must know
   `verification_url` secretly means the authorize URL. Rejected per protocol.

**Counter-case for Option 2 (argued honestly).** "Sign in with a subscription"
is one user-facing concept, so one RPC is arguably more semantically correct,
and it avoids duplicating the modal. — But device-auth (poll) and loopback
(callback) are *different grant mechanisms*, not two providers of one flow; the
"unification" forces provider-only fields into a shared event and a two-branch
handler, and it makes us refactor working ChatGPT code for zero correctness
gain. The shared surface is superficial (just "show a URL, then succeed/fail").

**Decision: Option 1.** Correctness: tie (all correct). Cleanliness: Option 1
models two distinct grants as distinct types with no union blemish and doesn't
entangle a working feature — cleaner, not merely safer. Future cost: if a third
subscription provider ever appears, merging parallel RPCs into a shared surface
is a straightforward, reversible refactor; the reverse (Option 2 now) is the
irreversible, higher-risk direction. The CLI modal is written fresh (lean, no
copy-code/account handling); a shared modal can be extracted later if a third
consumer justifies it (YAGNI until then).

Commits: 4b0ae752 (proto+handler+client+modal).

### Fork B — session-ID context machinery on Meridian removal

**Decision point.** `internal/llm/session.go` holds two context helpers:
`WithSessionID`/`SessionIDFromContext` and `WithIndependentSession`/
`IsIndependentSession`. Both existed to feed Meridian's stateful-SDK lineage
matcher. What survives Meridian's deletion?

**Findings (grep, non-test).** `SessionIDFromContext` is read by
`collect.go` → `RecordAnomaly(...)` (the stream-anomaly-log feature) — a live,
non-Meridian consumer. `IsIndependentSession` is read by *nothing* except the
OpenCode-spoof auth being deleted; its setters (`dispatch/engine.go`,
`hostsvc/tools/tools.go`) mark subagent turns "independent" purely so Meridian
would skip lineage matching.

**Options.**
1. **Keep `WithSessionID`/`SessionIDFromContext`; delete
   `WithIndependentSession`/`IsIndependentSession` + their two setters.**
   - Correctness: the stateless Messages API has no lineage matching — every
     request is independent by construction, so the flag is semantically moot.
     Removing an unread flag changes no behavior. Session-ID plumbing stays for
     anomaly attribution.
   - Cleanliness: removes dead code + two now-meaningless setter calls; the
     surviving helper has a real reader.
   - Cost/risk: ~1 file trimmed + 2 call-site edits + `runner/core.go` switched
     from the deleted `anthropic.WithSessionID` alias to `llm.WithSessionID`.
     Loud failure (compile) if a reader is missed.
2. **Keep both** (independent-session "might be useful for a future provider").
   - A speculative hedge with zero current readers — a dead flag retained on a
     "someday" basis. Correctness same; cleanliness worse (dead code);
     violates the run's "no legacy flags / no hedging" rule.
3. **Delete both pairs** (drop session-ID plumbing entirely).
   - Incorrect: breaks `collect.go`'s anomaly attribution (loud compile
     failure). Rejected.

**Decision: Option 1.** Unambiguous under correctness→cleanliness: Option 3 is
incorrect (breaks a live reader), Option 2 keeps a reader-less flag the run
rules forbid. The independent-session concept is an artifact of Meridian's
stateful multiplexing; the stateless API makes it moot. Bundled into the
Meridian-deletion commits (each with its log line).

Commits: (Meridian deletion, below).
