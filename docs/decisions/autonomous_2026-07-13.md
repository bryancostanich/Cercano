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

## RUN STATUS — COMPLETE (ready to land, not landed)

**Objective met.** Meridian is fully removed and replaced by the native
Anthropic subscription-OAuth route. Both Go modules build; full server + CLI
test suites pass. No `route=meridian` path survives in executable code (only
the migration that detects the legacy value, plus internal doc comments). Not
pushed, not merged — branch `replace-meridian` is ready for Bryan to test.

**What works end-to-end now:** a messages-flavor profile on `route=subscription`
authenticates with a refreshing Bearer from `internal/anthropicauth` (single-
flight, keychain-backed); users sign in via "sign in with Claude (subscription)"
in settings or the first-run wizard, which drives the PKCE loopback modal
(`StartClaudeLogin` RPC) and lands an active profile; existing Meridian profiles
are migrated to `subscription` on config load and forced to re-auth.

**Decisions (matrix-logged below):**
- Fork A — dedicated `StartClaudeLogin` RPC over generalizing the ChatGPT one
  (honest modeling of a distinct grant; no refactor of working code).
- Fork B — on Meridian removal, keep `WithSessionID` (anomaly-log reader),
  delete the `WithIndependentSession` flag (Meridian-only dead code).
- Fork C — migrate `meridian`→`subscription` + clear BaseURL on load, forcing
  one-time re-auth (no coexist, per the run directive).

**Blocked:** none.

**Deferred (non-behavioral; safe to leave):**
- Internal doc comments still mention Meridian: the `worker/*` "mirror
  meridian/manager.go" provenance notes (the pattern was copied from the now-
  deleted file), the "proxy BaseURL (Meridian)" auth carve-out comments, the
  proto route-field doc comments (`direct | meridian | ccr`), `config.go`'s
  route doc, the agentclient route docs, and the system-prompt "OpenCode/
  Meridian adapter" note. None affect behavior or the wire API.
- Token *refresh* is exercised only at the ~8h access-token expiry. The code
  exchange was verified live; refresh reuses the same endpoint + standard grant.
- Subscription profiles use the curated fallback model list in settings (their
  BaseURL is empty, so the `/v1/models` fetch short-circuits) — a live catalog
  fetch via the Bearer token is a possible future enhancement, not a gap.

**Commits this run (oldest→newest), all on `replace-meridian`:**
- `4b0ae752` feat(cloud): subscription sign-in flow (loopback OAuth) — RPC + modal
- `ffcf69cc` refactor(llm): remove the meridian provider route + dead session flag
- `dab41852` refactor(server): delete the meridian proxy manager subsystem (−3064)
- `2973e003` refactor(proto): remove MeridianStatus messages + client plumbing
- `44b17c30` feat(cli): wire the subscription sign-in trigger button
- `75ed0da6` feat(config): migrate meridian profiles to the subscription route
- `ec3eb39a` feat(cli): wizard offers Claude sign-in instead of the meridian proxy
- `080c87db` refactor: retire remaining live meridian route paths
(preceding the run: design doc + Phases 1–3-core through `63da437d`.)

**Review first:** (1) the sign-in path — `internal/server/claude_login.go` +
`internal/anthropicauth/loopback.go` + the CLI `claude_login_modal.go`; (2) the
config migration + its test (`migrateMeridianToSubscription`); (3) the
per-route authenticator strategy (`internal/llm/anthropic/auth*.go`). Smoke
test: run the CLI, pick "sign in with Claude", approve in the browser (should
show the green success page), confirm a chat turn works on the subscription
profile.

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

Commits: ffcf69cc, dab41852, 2973e003 (Meridian deletion steps 1–3).

### Fork C — migrating existing Meridian profiles

**Decision point.** On load, `autoDetectMeridianRoute` promoted profiles at
Meridian's default port (127.0.0.1:3456) to `route=meridian`. Meridian is gone.
What happens to a user's existing meridian profile on the first post-upgrade
load? (Bryan pre-decided the shape: "we can force a re-auth. no coexist.")

**Options.**
1. **Rewrite `meridian` → `subscription` on load + clear the proxy BaseURL;
   force re-auth.** The migrated profile has no token in *our* keychain
   (Meridian read `claude login`'s, not ours), so rebuildCloud lands it
   "absent" until the user signs in through the loopback flow.
   - Correctness: a working-but-now-impossible proxy profile becomes a
     valid-shape subscription profile that transparently prompts sign-in. No
     silent breakage, no dead proxy URL left to confuse the direct client.
   - Cleanliness: one migration function replaces the old auto-detect; no
     meridian route value survives anywhere.
2. **Drop meridian profiles entirely on load.** Loses the user's model choice /
   profile name / backup wiring; more destructive than needed. Rejected.
3. **Leave them as `route=meridian`.** The route no longer exists — the profile
   is inert and the client can't explain why. Violates "no coexist". Rejected.

**Decision: Option 1.** Correctness-first: it's the only option that neither
silently breaks a profile (3) nor discards user state (2). The cleared BaseURL
matters — the subscription route pins api.anthropic.com and a leftover
`:3456` URL would otherwise route a direct call at a dead proxy. Un-routed
`:3456` profiles (pre-route configs) are migrated the same way. This replaces
`autoDetectMeridianRoute` outright — no legacy detect path remains.

Commits: (config migration + wizard/labels, below).

### Follow-up — default runtime/routing policy

**Decision point.** After the native subscription route landed, the stock
configuration still defaulted to `open_runtime: ollama` and
`locus_mode: open_primary`. That made a fresh or partially-rewritten config send
main tool-loop turns to the Ollama adapter first, which is wrong for the
subscription-focused default experience.

**Decision.** Default new configs and empty locus-mode parsing to
`open_runtime: llama_server` plus `locus_mode: cloud_primary`. Main agent work
therefore prefers the active cloud profile with local fallback; co-processor work
keeps the existing CloudPrimary behavior of preferring local first. The managed
local fallback is llama-server, not Ollama. The `models.default_provider` default
is left unchanged in this commit because this request is about runtime/routing
policy, not tier-taxonomy preference.

**Test impact.** The multi-surface attach proof previously synchronized on an
advisory progress event that only appeared on the old route shape. It now waits
only for the load-bearing broker events (`RouteSelected` replay + `Token` live),
which matches the test's own correctness contract.

Commit: this follow-up default-policy commit.

### Follow-up — canonical Claude sign-in activation

**Finding.** The local config had a `claude` subscription profile with token-backed
metadata, but `active_cloud_profile` remained `openai-responses`. That proves
the profile creation/auth path completed, while activation either was not
requested by the client that initiated the sign-in or was later overwritten.
This is not a Meridian coexistence artifact: the migrated profiles are already
`route: subscription`, and the `claude` profile is the native replacement path.

**Decision.** Treat the canonical no-profile Claude sign-in request as
activation-worthy even if an older/stale client omits `set_active`; explicit
named-profile re-auth remains non-activating unless `set_active` is true. Also
broadcast `active_cloud_profile` alongside `cloud_model` when activation lands,
so subscribers have an explicit active-profile event to consume.

Commit: this follow-up activation hardening commit.

### Follow-up — migrated route active-profile pointer repair

**Finding.** A legacy config can name the proxy route itself (`meridian`) as the
active profile pointer, while the actual cloud profile row is named something
else such as `anthropic`. The route migration correctly rewrote the row to the
native subscription route, but it did not repair an active/backup pointer that
now names a non-existent profile. In that shape the migrated subscription row
has nowhere to be selected from until the user manually switches profiles.

**Decision.** During the route migration, remember the first rewritten profile.
If `active_cloud_profile` is empty, equals the removed route name, or points to
no profile, set it to that migrated profile. Apply the same repair to an invalid
backup pointer. Do not override a valid active/backup pointer; if the user has
already selected a different real profile, preserve it.

Commit: this follow-up migration-pointer repair commit.
