# Task Model — Design

**Roadmap item:** #3 — Task model + tracking + client surfacing (Tier 1).
**Status:** Design (approved model; implementation not started).
**Depends on:** nothing to *build* — but this model is the persistence layer for
planning mode (#7), and planning mode's design has since made **binding decisions**
on it (serialization format, sibling ordering, the `Notes` field's role). Those are
folded in below (§8, §10). Reconcile with [`../planning-mode/design.md`](../planning-mode/design.md)
before implementing — the two are one body of work, not independent items.
**Consumed by:** #6 brainstorming, #7 planning, #8 autonomous mode.

Research backing this design lives in [`research/`](research/). Read
[`research/CAVEATS.md`](research/CAVEATS.md) first: the synthesis contains fabricated
quantitative metrics that must not be trusted. The *directional* findings
(Superpowers' flat model, Claude Code's TodoWrite-vs-Tasks split, Conductor's
hierarchical tracking) are credible and inform this doc.

---

## 1. The core claim

**A task is a task, regardless of scope.** Whether it originated ad-hoc in the middle
of a conversation ("also, fix the flaky test") or was laid out up front as part of a
plan ("Phase 2, task 3: migrate the config loader"), the *thing* is the same shape:
something with a title, a status, some notes, and possibly sub-parts.

The differences people intuit between "my working set for this session" and "the plan"
are **not** properties of a task. They are properties of:

- **where the task is kept** (a session-scoped store vs. a durable plan store), and
- **how the tasks are arranged** (a flat list vs. a tree).

Because the differences live in the *container* and the *arrangement*, not in the task,
we model the task exactly once.

## 2. The model: one recursive node

```
Task {
  ID       string          // stable identifier
  Title    string          // short imperative description
  Status   Status          // explicit; see §3
  Notes    string          // freeform detail, optional
  Children []Task          // subtasks; empty for a leaf
  ParentID *string         // nil for a root-level task
}
```

- A **flat working set** is a forest of root-level tasks (`ParentID == nil`, `Children`
  empty). This is the ad-hoc, session-level case — the equivalent of a TodoWrite list.
- A **plan** is a tree of the same node: `plan > phase > task > subtask`, arbitrarily
  deep. Every level is a `Task`. "Phase" is not a distinct type — it is a `Task` that
  happens to have children. This is the self-similar / recursive property that makes
  the model collapse to one type.

There is deliberately **no `scope` or `lifetime` field on the node.** (See §5 for the
decision record — this was the main fork.) Lifetime is decided by which store holds the
task, so the node stays pure.

## 3. Status is explicit on every node

Every task carries its own status. Status is **set directly** by the agent (or user),
never inferred:

```
Status ∈ { pending, in_progress, done, blocked }
```

- **No derived/rollup status is stored.** A parent does not compute its status from its
  children. A parent that has children still has its own explicit status.
- A UI *may* show a computed rollup for a parent (e.g. "3/5 done") as a **display-only**
  affordance. That rollup is never persisted and is never the source of truth.

Rationale: derived status is seductive for trees but becomes ambiguous the instant a
parent has mixed children (some done, one blocked, one in progress — what is the
parent?). Every credible tool in the research (Superpowers, Conductor) uses explicit
status. Explicit keeps the data model honest and the tree unambiguous.

## 4. Lifetime = container, not field

Two stores implement the same store interface over the same `Task` node:

| Store | Holds | Lifetime |
|---|---|---|
| Session store | Ad-hoc working-set tasks | Dropped at session end |
| Plan store | Plan trees | Durable across sessions |

**Promotion** — turning an ad-hoc task into part of a durable plan — is just *moving the
node into the plan store and attaching a `ParentID`*. No type conversion, no re-modeling.
That falls out for free precisely because both stores speak the same node.

One store interface. One tree-walk. One UI renderer that handles depth-0 (flat list) and
depth-N (plan tree) with the same component.

## 5. Decision record — why one node, no scope field

This was the structural fork. Three options were on the table:

| Axis | A: Two schemas | B: One node, lifetime = container | C: One node + explicit `scope` field |
|---|---|---|---|
| Cost | High: two types, stores, renderers, test sets | Low: one type, one store, one renderer | Low–Medium: B plus a scope enum and save-time policy |
| Risk | Model **drift** over time (Claude Code's own docs are confused about TodoWrite vs. Tasks) | Cannot represent a durable *standalone* task | Low; lifetime explicit, but extra surface |
| Reward | Mirrors prior art | Maximal simplicity; free promotion | B's simplicity plus honest standalone-durable tasks |
| Main drawback | Guarantees the drift it fears | Lifetime is implied by store, not stated on node | Extra field YAGNI unless standalone-durable is real |

**Chosen: B.** The deciding question was: *do we ever want a task that outlives the
session but belongs to no plan (a durable standalone TODO)?* The answer was **no**. Given
that, lifetime is fully determined by which store holds the task, so a `scope` field
would be redundant metadata. Option C's field only earns its place if the
durable-standalone case appears; it hasn't, so YAGNI — and B can adopt C's field later
without disturbing the node's other fields.

Option A was rejected because two parallel schemas guarantee exactly the divergence the
research shows in tools that took that path.

## 6. Client surfacing (Phase 1 scope)

- **Tracking during execution:** the agent updates task status as work proceeds
  (`pending → in_progress → done`, or `→ blocked`).
- **Streaming to clients:** task-state changes stream to connected clients so they can
  render live. CLI target: a right-hand pane when the terminal is wide enough, a bottom
  strip otherwise.
- **MCP exposure:** deferred past Phase 1; noted as a follow-up once the in-process
  surface is proven.

## 7. Phase plan

1. **Phase 1 — model + persistence + CLI surface.** The `Task` node, the two-store
   interface (session + plan), explicit status, and streaming task state to the CLI.
   Note the two stores have different backends behind the one interface: the session
   store is ephemeral in-memory; the plan store is Markdown-file-backed (`plan.md`
   parse/serialize, per §8). Build it that way from the start — do not stand up a
   database-backed plan store.
2. **Phase 2 — planning integration.** Wire the plan store into planning mode (#7):
   Conductor-style plan format producing `Task` trees.
3. **Phase 3 — MCP exposure + promotion UX.** Expose task state over MCP; add an explicit
   "promote this ad-hoc task into the plan" affordance.

## 8. Resolved by planning mode (#7)

Two questions this doc originally left open have since been **decided** by the
planning-mode design ([`../planning-mode/design.md`](../planning-mode/design.md),
Fork 2). Because the plan store *is* planning mode's persistence layer, those
decisions are binding here:

- **Serialization format for the durable plan store — RESOLVED: Markdown on disk is
  canon, not a separate store format.** This doc originally leaned toward flat rows with
  `ParentID` for queryability. Planning mode overrode that: the durable artifact is a
  Conductor-style `plan.md` file, and the `Task` tree is its *in-memory parse*. The file
  wins on disagreement. The store is a working form loaded from and serialized back to
  `plan.md` via a lossless round-trip (heading depth + checkbox indent = tree depth;
  checkbox glyph = status). Flat-rows-in-a-database is therefore **not** the plan store;
  if a queryable index is ever wanted it sits *beside* the files as a derived cache, not
  as the source of truth.
- **Sibling ordering — RESOLVED: significant, and encoded by position, no `order`
  field.** Document order in `plan.md` encodes sibling order; the parser preserves
  sequence. The node stays as specified in §2 (no `order` field), consistent with the
  "keep the node pure" principle.

## 9. Still open

- Whether ad-hoc **session** tasks (the flat working-set store, not the plan store)
  should *auto-clear* on `done` or linger until session end. Genuinely open — the
  planning-mode decisions above only bind the durable plan store.

## 10. Cross-cutting constraint from planning mode

Planning mode packs **phase-level metadata** (objective / files-to-touch / tests-to-write)
into a phase node's `Notes` field, and rides `spec.md` prose alongside the root node as
reference context (surfaced in the `d`/details view, never walked). Implementations of
this model must therefore treat `Notes` as free-form prose that survives a Markdown
round-trip intact — it is not a scratch field. See planning-mode §3.2–3.3.
