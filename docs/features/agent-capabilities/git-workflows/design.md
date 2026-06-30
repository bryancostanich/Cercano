# Git Workflow Tools — Design

**Part of:** [Agent Capabilities](../README.md) · Tier 1, sub-project #4.
**Depends on:** the [capability model](../capability-architecture/design.md) (0a) and the
existing low-level git capabilities; reuses the [`review` capability](../dispatch-engine/design.md)
from the dispatch engine for independent risk assessment.

## Goal

Replace the agent's ad-hoc, command-by-command git orchestration with a small set of
**deterministic, high-level git workflows** — set up a workspace, land work to the trunk,
checkpoint a solved unit, recover from trouble — plus a few power-user workflows (bisect,
bounded history shaping). The agent expresses intent ("land this branch"); a Go engine runs
the whole git sequence the same way every time. The model is invoked only where there is no
deterministic answer.

## Core principle: deterministic engine, LLM at seams

A new `internal/gitflow` package holds Go functions that shell out to `git` (the same
`exec.CommandContext` pattern the existing git capabilities use). The control flow — branch
checks, ordering, fast-forward-only, safety refs — is **decisions in code, not a prompt**.

The LLM affects the *work* at exactly two seams, and never drives the sequence:

1. **Conflict resolution** during a feature-branch reconcile (see Land, below). The agent
   edits the conflicted files; the engine resumes.
2. **Commit-message wording** (`checkpoint` subject/body).

Independent *risk assessment* of a resolution is delegated to the existing `review`
capability — a separate model call, not the resolving agent (see Conflict resolution).

**Push is never automated.** Workflows stop at the local fast-forward and report "ready to
push." Pushing stays the existing explicit, gated `git_push` capability.

## Architecture

- **`internal/gitflow`** — deterministic workflow functions. No model in the control flow.
- Exposed as **high-level capabilities** on the 0a model, both standalone agent and MCP
  (`cercano_*`). They reuse the existing low-level git internally; they do not replace
  `git_status`/`git_add`/`git_commit`/`git_push`.
- **Tiers:** `git_worktree`, `checkpoint`, history ops = **W**; `git_land`, `git_recover` =
  **X**. Push stays the existing X-tier `git_push`, unchanged.
- No new CLI slash commands required for v1 (they can wrap these capabilities later).

## Configuration (`.cercano/`)

Workflows resolve project facts deterministically — **never** hardcoded `"main"`, and not
re-decided by the LLM on every call. Resolution order for each value: **explicit per-call
override → `.cercano/` project config → auto-detect → ask** (fail loud, never guess silently).

- `trunk` — the integration branch features land into (`develop`, `main`, …). Auto-detect
  fallback: `git symbolic-ref refs/remotes/origin/HEAD`.
- `test_command` — the command the land gate runs. No value configured → land proceeds but
  reports loudly that it landed without a test gate.
- `sensitive_paths` — globs that trip the deterministic review floor (see below).
- `review_floor` — the conditions that force human review regardless of the `review`
  verdict: the `sensitive_paths` globs above, plus a hand-edited-file count threshold
  (default a small number, e.g. 5). Configurable, not a magic constant.
- `regen` — path-glob → command map for generated files (e.g. `*.pb.go` → the `protoc`
  line), used to suggest regeneration instead of hand-merging generated conflicts.

The **integration trunk** (where features land) is distinct from a **release branch** (what
you promote to on a release — `main` at a `develop`-shop). v1 lands to the integration
trunk; the promote-to-release flow is deferred.

## Workflow set (v1)

- **`git_worktree`** — set up an isolated workspace: create a feature branch off the resolved
  trunk and `git worktree add`, with the safety checks (target dir is git-ignored, clean
  baseline, prefer a native worktree tool if the host provides one). Mirrors/reuses the
  Superpowers using-git-worktrees logic, which already gets this right.
- **`git_land`** — integrate a feature branch into the trunk. The core flow; detailed below.
- **`checkpoint`** — the agent-judged commit of a solved unit. Detailed below.
- **`git_recover`** — the safety valve: abort an in-progress merge/rebase, or undo the last
  workflow by resetting to its recorded safety ref.
- **`git_bisect_run`** — run `git bisect` between good/bad bounds with a test command; return
  the first bad commit. Fully deterministic (bisect is an algorithm) and ends with
  `bisect reset`.
- **Bounded history ops** — `squash-branch-to-one` (`reset --soft` to merge-base + commit),
  `autosquash` (`rebase --autosquash` of `fixup!` commits), and stash/pop. For pre-land
  cleanup. *Freeform* interactive rebase, cherry-pick-which-commit, and blame archaeology are
  intentionally **not** workflows — those are judgment, left to the low-level git the agent
  already has.
- **Branch-hygiene helpers** — safe create/switch/delete, and the "never commit on trunk"
  guard.

**Deferred:** release promotion (integration trunk → release branch).

## The `git_land` flow

All steps are decisions in code:

1. **Pre-checks:** working tree clean (else stop: "commit or checkpoint first"); resolve
   `trunk` and `test_command` from config.
2. **Record safety refs** — current trunk HEAD and feature HEAD — so `git_recover` can undo
   the whole land.
3. **If the feature already fast-forwards onto trunk** → skip to step 6.
4. **Reconcile on the feature branch, by strategy** — the point of reconciling here is that
   the eventual merge into trunk is then a clean fast-forward; trunk never sees a conflict.
   - **Default `rebase`** — rebase feature onto trunk. Keeps trunk linear.
   - **`merge` override** — merge trunk *into* the feature (resolve once). The
     large-divergence exception; leaves a merge commit on the feature side.
   - The engine **reports the divergence first** (e.g. "trunk +90 / feature +55, N
     overlapping files") so the agent/user can choose `merge` for a borderline case — rather
     than the engine guessing with an arbitrary threshold. Which strategy on a borderline is
     judgment, so it is surfaced, not hardcoded. The small-divergence common case just
     rebases with no decision.
5. **On conflict (expected, not exceptional):** the workflow pauses, leaving the repo in the
   conflicted mid-reconcile state, and returns the conflicted file set — flagging generated
   files (a `*.pb.go` whose `.proto` merged clean → "regenerate via the configured `regen`
   command, don't hand-merge"). **The agent resolves the conflicts on the feature branch**
   (semantic edits, or regen). `git_land --continue` validates the conflicts are resolved and
   proceeds. Resolution is always on the feature branch, in isolation — never on trunk.
   `git_recover` aborts the paused operation in one call if you want out.
6. **Test gate:** run `test_command`. Red → stop, do not land. This is what validates the
   resolution actually holds before anything lands. No command configured → proceed, report
   loudly.
7. **Risk review of the resolution** (only when conflicts were resolved): the engine computes
   deterministic signals (files/hunks in conflict, paths touched + `sensitive_paths` hits,
   generated-vs-hand-edited, divergence size) and calls the **`review` capability** — an
   *independent* model, not the resolving agent — with those signals plus the resolution
   diff. Plus a **deterministic floor** (`review_floor` config): its conditions — a
   `sensitive_paths` hit, or more hand-edited files than the configured threshold — force
   human review regardless of the verdict. Review says risky,
   or the floor trips → **pause for human eyes** before the fast-forward. Review clean and
   floor not tripped → continue. Clean lands (no conflicts) skip this entirely.
8. **`merge --ff-only`** onto trunk — clean by construction.
9. **Stop. Report "ready to push"** with the command. Never push.
10. **Offer** worktree teardown + merged-branch delete (do not auto-do it).

### Why the LLM resolving conflicts is safe here

Resolution happens on the **feature branch, in isolation** — trunk only ever receives an
already-resolved, already-test-gated, clean fast-forward. The safety is structural (where
resolution happens + what gates it), not "keep the model out": the test gate proves the
resolution didn't break the build, the `review` capability gives an independent second
opinion anchored to objective signals, the deterministic floor forces human review on
genuinely risky cases, and `git_recover` undoes the whole thing via the safety ref.

## `checkpoint`

Agent-judged commit of a solved unit — the agent owns the *when* (it's the only thing that
knows a unit is "solved"); the engine makes the *how* deterministic and safe.

- **Inputs:** `subject` (required — one line, imperative, house-style conventional-commit
  prefix: `feat(scope):` / `fix(scope):` / `docs:` / `refactor:`) and `body` (optional but
  expected for a substantial checkpoint — the *what* and *why*, decisions, caveats). The LLM
  writes the prose; that is the wording seam.
- **Engine enforces, deterministically:** assemble subject / blank line / wrapped body;
  **reject or strip "Claude" anywhere and never add a `Co-Authored-By` trailer**; commit on
  the **current** branch (never trunk); **never push**.
- **Reinforcement:** the always-on 0b steering block nudges the agent to checkpoint after
  completing a unit — soft reinforcement that keeps the *agent* in charge of the boundary
  while making the habit stick.
- **Future (watchdog, 0b Part C):** the supervisor tracks "a meaningful chunk solved with no
  checkpoint?" and nudges via challenge-and-justify. Captured as a hook; not v1.

A checkpoint is also a natural unit for the task model (#3) and the supervisor to track.

## Best practices baked in (enforced in code, not prose)

- Never commit on the trunk branch (checkpoint/commit guard).
- Never force-push; push is never automatic.
- Atomic commits with house-style messages; no "Claude", no `Co-Authored-By`.
- Record a safety ref before any history-mutating workflow.
- Clean-tree preconditions before a reconcile.
- Verify the current branch before committing (the guard for the subagent-committed-to-main
  failure mode).

## Error handling

Every workflow leaves a **recoverable state** on failure: the safety ref is recorded, and the
result reports what happened plus the `git_recover` command. Paused rebase/merge states are
legitimate, inspectable (`git status`), and recoverable in one call. No silent partial
success.

## Testing

- Deterministic workflows are tested against **ephemeral temp repos** — `git init` in a
  `t.TempDir()`, scripted commits/branches, assert the end state — the way the existing git
  capabilities are already tested.
- The LLM seams (conflict resolution, `review`) are tested with **stubs** — no real model
  calls.
- Land-flow conflict and resume paths are tested by seeding conflicting branches in a temp
  repo and asserting the pause/continue/recover transitions.

## Dependencies & follow-ons

- **`review` structured verdict:** using `review`'s output as an *automatic* gate wants the
  `{risky, reasoning}` structured verdict already on `review`'s follow-on list. Until then,
  the engine surfaces the verdict for the agent to act on. Tracked with the dispatch engine.
- **Watchdog (0b Part C):** when built, it automates *when* the risk review and checkpoint
  nudges fire (independent supervisor), subsuming the agent's first-pass self-judgment.
- **Release promotion** (integration trunk → release branch): a separate future workflow.
- **CLI slash-command wrappers** for the workflows: optional, later.

## Out of scope (here)

- Release promotion / tagging flows.
- Freeform interactive rebase, cherry-pick selection, blame archaeology (left to low-level
  git + the model).
- Any change to how push is gated (unchanged: explicit `git_push`).
