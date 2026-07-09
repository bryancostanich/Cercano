# Spec 0b — Steering & Protocol Substrate

**Part of:** [Agent Capabilities](../README.md) · Tier 0, Foundation
**Depends on:** [Spec 0a — Capability architecture](../capability-architecture/design.md)
(uses the Capability model for `get_protocol` and rides the existing skill catalog).
**Blocks:** Tier 2 modes (brainstorming, planning, autonomous) lean on the protocol
library and steering.

## Goal

Steer Cercano's own LLM toward plain English and toward following the workflow
protocols, and make the protocol library available across both surfaces (standalone
system-prompt steering and the host-discoverable skill catalog) from a single source.

## Key asymmetry

Cercano controls its **own** system prompt (the standalone agent loop), but as a plugin
inside Claude Code it **cannot** edit the host's system prompt — it can only influence
the host through the skills and MCP tools it exposes (exactly what the `hardwAIr_hckr`
"Dave" plugin does today). So the protocol library feeds two delivery channels:

- **Standalone:** system-prompt steering + an on-demand protocol pull.
- **Plugin:** the skill catalog (`SKILL.md` files the host discovers) + the
  `get_protocol` MCP tool.

Same content, two channels, one source.

## Decision summary

- **Cercano becomes the home** for protocol-driven discipline. Port Dave's `core/`
  library and the generic `khalkulo/workflow` protocols into Cercano; retire Dave.
- **Standalone delivery (MVP):** an always-on plain-English + protocol-trigger block in
  the system prompt, plus full protocol bodies pulled on demand.
- **Enforcement (post-MVP, in the plan):** a watchdog — a small, separately-configured
  model that monitors the main agent and enforces protocols via challenge-and-justify.

## Design — Part A: the always-on steering block (standalone, MVP)

A short block injected into Cercano's own system prompt at `buildToolLoopSystem`
(`internal/server/server.go:1037`), the single existing injection point. Two parts.

### Plain-English steering

Fixed rules, always present:

- Present decisions, options, and tradeoffs in plain English — not LLM or code
  shorthand. Spell out acronyms. Write like a colleague talking to another engineer.
- Prefer prose over jargon-dense bullet soup when prose is clearer.

This is a hard, repeatedly-stated user preference. It applies to user-facing output the
model produces.

### Protocol triggers

One-line conditional rules that point at the on-demand protocols, e.g.:

- "Facing a real decision with more than one viable approach → STOP and run the decision
  protocol before writing code."
- "Before applying any fix to a bug or test failure → complete the debug loop; don't fix
  on reasoning alone."
- "Match test scope to the size of the change."

### How the block is built (assembled algorithmically — plain-English explanation)

"Assembled algorithmically" means **code builds this block automatically by stitching
pieces together — nobody hand-writes and maintains it as a paragraph.**

Each protocol document carries a one-line summary of itself — its **trigger line**. For
example, the decision protocol's trigger line is *"Facing a real decision with more than
one viable approach → stop and run the decision protocol before writing code."*

When Cercano starts a turn and builds the system prompt, the code does this, in order:

1. Start with the persona line ("You are Cercano…").
2. Add the fixed plain-English rules.
3. **Loop over every protocol in the library, grab its one trigger line, and append them
   all.**

That loop in step 3 is the "algorithm." The steering block is the *output* of that loop,
not a thing a person edits.

**Why it matters:** the alternative is a hand-written rules paragraph that someone has to
remember to update every time a protocol is added or changed. That always drifts — you
add a protocol but forget to mention it in the rules, or you reword a protocol and the
paragraph now lies. By generating the block from the protocols themselves, the rules and
the protocols can never disagree. Add a git-workflow protocol later → its trigger line
shows up automatically, zero extra edits.

Contrast with the full protocol **body** (the detailed steps), which is **not** in this
always-on block — that is pulled on demand only when needed, to keep the prompt small.

This block is standalone-only. As a plugin, Cercano cannot inject it into the host's
prompt; the trigger content reaches the host through the skill catalog instead.

## Design — Part B: the protocol library + dual-channel surfacing

### Authoring (single source)

Each protocol is one structured document with:

- `name` — kebab-case id (e.g. `design-decisions`, `systematic-debugging`).
- `description` — one line, used for skill discovery.
- `domain` — `core` / `software` vs `hardware`, so hardware protocols can be excluded in
  software contexts.
- `trigger` — the one-line always-on rule (feeds the steering block).
- `body` — the full protocol (pulled on demand).

Ported content:

- From Dave's `core/`: `design-decisions`, `systematic-debugging`,
  `verification-strategy`, `compute-before-simulate`.
- From `khalkulo/workflow/`: where they overlap (the decision protocol), merge to the
  richer 7-step `design_decisions.md` version. The debug-loop skeleton (STRIP DOWN →
  OBSERVE → REASON → PREDICT → PROBE → REFERENCE → FIX) informs `systematic-debugging`.
- Hardware-specific protocols (RTL, analog, P&R) port as `domain: hardware` for
  completeness but are not in the default software steering set.

Git-workflow, planning, brainstorming, and autonomous-run protocols are authored by
their own sub-projects (Tiers 1–2); this foundation seeds the library and the mechanism
so adding them later is drop-in.

### Reuse existing infrastructure

Cercano already has an Agent Skills layer: `.agents/skills/`, `.claude/skills/`,
`builtinSkills()` (`internal/server/skills.go`), `ListSkills`/`GetSkill` RPCs, and the
`cercano_skills` MCP tool. Protocols become a **category of skills** — no new parallel
system. The protocol's `description`/`body` map onto the existing skill shape.

### Three outputs from the one source (generation, not copy-paste)

1. → the steering-block assembler (triggers) — standalone, always-on.
2. → the skill catalog, fetched on demand via a **`get_protocol` capability**
   (Spec 0a Capability; surfaces: both) — standalone full body and plugin tool.
3. → emitted as `SKILL.md` files in `.agents/skills/` and `.claude/skills/` so a host
   (Claude Code) discovers them natively — the plugin channel, exactly Dave's mechanism.

There is one authored source per protocol; the steering triggers, the skill-catalog
entries, and the on-disk `SKILL.md` files are all generated from it. No duplicate copies
to keep in sync.

### On-demand pull (MVP)

In the standalone loop the model pulls a full protocol body by calling `get_protocol`
when a trigger fires (explicit pull). Automatic injection on detected conditions is the
watchdog's job (Part C), not MVP.

## Design — Part C: the watchdog (post-MVP, designed now)

An independent supervisor — a small, fast, separately-configured model (a local Ollama
small model, or an external small model the user points it at) — that watches the main
agent and enforces protocols, instead of trusting the main model to police itself. This
is the algorithmic-enforcement layer and the answer to silent protocol-skipping.

### What it watches

The main loop's stream at decision points only — proposed tool calls and end-of-turn
assistant text. Not every token (too costly). A cheap algorithmic pre-filter decides
**whether** to consult the watchdog at all, so the small model runs only when a protocol
could plausibly apply.

### What it checks

Protocol compliance, e.g.: about to `Edit`/`Write` to fix a bug with no
`systematic-debugging` evidence in the transcript; picked among options without running
the `design-decisions` protocol; plain-English violations in user-facing output.

### How it hooks in

A second gate layered over the existing permission gate. The existing gate is algorithmic
(tier-based, R/W/X). The watchdog is a model-backed **protocol** gate that runs alongside
it on W/X calls and at turn boundaries.

### Intervention model — challenge-and-justify (default)

The real problem is **silent** skipping, not the model ever stepping outside a protocol.
A known one-line fix should not trigger the full debug loop, and the model knowing that
is good judgment. So the default converts silent skips into explicit, on-the-record
decisions rather than forcing compliance:

1. **Challenge** — the watchdog injects a note: *"You're editing to fix a bug without the
   debug loop — comply or justify."*
2. **Comply or justify** — the main model either runs the protocol, or states a reason
   (*"one-character typo, root cause is obvious, debug loop is overkill"*).
3. **Proceed + log** — if it justifies, it proceeds, and the override is recorded and
   shown in scrollback: `⚡ watchdog override — proceeded without debug loop · reason:
   "obvious typo"`.
4. **Escalate** — only if it can't produce a justification, or keeps repeating the same
   skip, does it surface to the human.

The **hard block** (deny the action, force the protocol) does not disappear — it becomes
an **opt-in** for zero-tolerance contexts (a "strict" watchdog mode, or auto-enabled
during unattended autonomous runs). Default is challenge / justify / log / escalate.

### How a block works (when enabled)

A block is a machine-to-machine redirect, not a hand-off to the user: the tool call is
not executed; a synthetic tool_result feeds the reason back to the main model, which
re-plans. The user sees a transparency note in scrollback. The user is only prompted on
**escalate** (e.g. the same block repeated N times — a deadlock or a misfiring
detection), which is the safety valve. This is distinct from the existing W/X confirm
gate, which prompts the user directly.

### Shared plumbing

The watchdog consumes the same small-model routing that the subagent engine (Tier 2)
defines — "pick a cheap model for a cheap job." This design seeds that mechanism.

### Config knobs

Enable/disable; which model; which protocols it enforces; default intervention mode
(challenge-and-justify vs. strict/hard-block).

## Data flow (standalone, MVP)

```
turn start
  → buildToolLoopSystem assembles steering block
       (persona + plain-English rules + Σ protocol triggers from the library)
  → model runs; a trigger condition arises
  → model calls get_protocol("systematic-debugging")
  → capability returns the body from the library
  → model follows the protocol
```

## Error handling

- `get_protocol` with an unknown name returns a structured error listing available
  protocol names (does not crash the loop).
- If the protocol library fails to load, the steering block degrades to persona +
  plain-English rules (no triggers) and logs a warning, rather than failing the turn.
- (Watchdog, post-MVP) if the watchdog model is unreachable, the pre-filter fails open
  (no enforcement) and logs, rather than blocking the main loop.

## Testing

- **Steering-block assembly** — given a fake library of N protocols, assert the block
  contains the persona, the plain-English rules, and exactly N trigger lines; adding a
  protocol adds its trigger with no other change.
- **`get_protocol` capability** — returns the right body; unknown name returns the
  structured error; surfaces on both agent and MCP.
- **Generation** — one source produces consistent steering triggers, skill-catalog
  entries, and `SKILL.md` files (no drift between channels).
- **Plain-English steering** — a prompt-level check (the rule text is present and
  well-formed in the assembled system prompt). Behavioral conformance is observed, not
  unit-asserted.
- **Watchdog (when built)** — challenge fires on a seeded violation; justify path
  proceeds and logs an override; repeated skip escalates; strict mode blocks.

## Out of scope (here)

- The watchdog implementation (designed here, built post-MVP).
- Git-workflow / planning / brainstorming / autonomous protocols (their own
  sub-projects; this seeds the library + mechanism).
- Changing how the host's own system prompt works (not possible from a plugin).
</content>
