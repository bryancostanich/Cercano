# Primary / Secondary / Local Provider Tiers

## Status

Supporting routing requirements for the coordinated routing-and-settings effort. The user approved delivering both together. The authoritative spec and single implementation plan live in efforts/deepinfra-cloud-profile-ux/. This document is not a separate prerequisite effort. Historical scope exclusions below are superseded by that approval; the combined plan remains an unapproved implementation draft.

Two shipped specs already name this effort as out of scope and defer to it:

- `efforts/unified-model-catalog-deepinfra/spec.md` lists "The Primary/Secondary/Local
  tier restructure (separate, later effort)" among its non-goals.
- `efforts/deepinfra-cloud-profile-ux/spec.md` states "This effort does not implement
  the larger Primary/Secondary/Local provider-routing redesign."

## Problem and motivation

Provider routing is governed by a binary tier model. `internal/locus/locus.go`
defines `Tier` with exactly two values, `TierLocal` and `TierCloud`, and resolves
a `Mode` into a `Resolution{Preferred, Fallback, CrossAllowed}`. Every routing
decision in the system is a choice between "local" and "cloud" plus a single
boolean saying whether crossing between them is permitted.

That model no longer matches how the system is used. A user may have a frontier
provider for their main work, a cheaper hosted provider such as DeepInfra for
bulk or overflow work, and a local runtime for private or offline work. Those are
three distinct destinations with different cost, privacy, and capability
properties. The binary model can only express two, so the middle one has no home.

The binary model also fuses two unrelated policies into one enum. The `*_only`
modes exist principally to express "do not cross tiers," which is a degradation
policy, not a preference. Because that policy is welded to the preference, a user
who wants to suppress one specific kind of degradation cannot do so without also
changing where unrelated work runs. Concretely: under `CloudPrimary`, `Mode.Coproc()`
returns local-preferred, but under `CloudOnly` it returns `{TierCloud, TierCloud, false}`.
Suppressing cloud-to-local fallback for main work today therefore also forces every
co-processor and sub-agent dispatch onto cloud. There is no way to express the
narrow intent.

## Goals

Replace the binary tier model with three named tiers — Primary, Secondary, and
Local — such that each tier binds to a specific model on a specific provider
profile, and such that routing policy can be expressed without conflating
preference with degradation permission.

Preserve the existing cloud profile authentication system, including OS keychain
storage and per-profile credentials. Preserve the canonical per-provider tier
tables and the existing primary/backup failover mechanism, extending the latter
to the new tier structure rather than replacing it.

## Confirmed routing clarification

Primary chat dispatches delegated work to the independent Secondary tier. Primary and Secondary each have an optional, independently configured backup. Primary's backup keeps the main chat running within Primary; Secondary's backup keeps delegated work running within Secondary. Secondary is not Primary's backup. Both backup assignments may be left unset. Dispatch defaults to Secondary / Premium; Primary chat defaults to Premium. Destination tiers and model quality tiers are distinct.

These confirmed semantics supersede any suggestion below that Primary-to-Secondary failure escalation implements Primary backup. The older discussion of cross-tier degradation is not authorization to substitute one mechanism for the other. Tests must cover independent backup assignments, absent backups, and consistent host/worker routing. Automatic Secondary-to-Local fallback remains prohibited.

## Constraints

### Secondary does not fall through to Local

This is the load-bearing constraint of the effort and the reason this spec exists
before the work does.

Primary and Secondary are both hosted providers differing mainly in cost and
capability. An automatic hop from Primary to Secondary is a reasonable
degradation: the work continues on a comparable remote model, and the user's
privacy and connectivity assumptions are unchanged.

Local is a different beast. It differs from the hosted tiers in privacy boundary,
cost model, capability class, and context capacity. Arriving at Local because a
remote call failed is a category change, not a degradation step. The system must
not perform that hop as an automatic consequence of failure.

When Primary and Secondary are both exhausted, the correct behavior is to report
that the configured remote tiers are unreachable and stop. It is not to quietly
produce a lower-quality answer from whatever model happens to be resident
locally. A silent downgrade of that magnitude is worse than a clear failure,
because the user cannot tell it happened from the output alone.

Local remains fully reachable — by explicit selection. Locus mode, task
assignment, and co-processor bindings may all route work to Local deliberately.
The prohibition is specifically on reaching Local through failure escalation.

This is a structural property, not a user setting. No configuration key should
enable the Secondary-to-Local hop. The permitted-edge structure should make
adding that edge later a small change if this decision is ever revisited, but the
shipped system should not offer it as an option. A setting that nobody should
turn on is worse than no setting.

### Degradation policy must be separable from tier preference

The three tiers are not a single ordered scale with a cutoff. A floor-style
control — "never run below tier X" — is the wrong shape, because it presumes
Local is merely a less capable continuation of the hosted tiers. It is not.

The permitted fallback edges must therefore be represented explicitly rather than
derived from tier ordering or from a single `CrossAllowed` boolean. The current
`Resolution{Preferred, Fallback, CrossAllowed}` shape does not generalize: one
boolean assumes one crossing, and with three tiers there is more than one edge,
and the edges are not equivalent.

### Co-processor routing is out of the blast radius

Co-processor work already *prefers* Local under `CloudPrimary`. That is
deliberate selection of a cheap destination for grunt work, not degradation. The
Secondary-to-Local constraint governs failure escalation for main-role work and
must not be applied to co-processor preference, or local-preferring grunt work
would lose its ability to reach cloud when the local runtime is down.

### Routing policy must reach the worker

Routing policy must be carried into worker-executed turns. `internal/worker/wire.go`
builds `ConfigSnapshot` by copying configuration field by field, and the worker
child runs the same runner core as the host. Any policy added to configuration
alone will be silently ignored for work executed in a worker, producing host and
worker disagreement about when degradation is permitted. Host and worker must
reach the same routing decision for the same configuration.

### Both crossing sites are in scope

Tier crossing is currently decided in two structurally different places, and a
change to the tier model touches both:

- Availability-time selection in `internal/inference/router.go`, where
  `Router.Select` picks the fallback tier when the preferred tier's provider is
  absent or unconfigured — before any request is sent.
- Runtime-failure escalation in `internal/runner/core.go`, where a cross-tier
  fallback is attempted after a request fails, gated on `res.CrossAllowed` and
  `llm.FailoverableToWindow`, and after `internal/inference/resilience` has
  already exhausted the primary profile and its backup.

The Secondary-to-Local prohibition must hold at both. An unconfigured Secondary
and a failing Secondary must not produce different answers to the question of
whether Local may be used.

## Non-goals

This spec does not design the tier data model, the configuration schema, the
provider binding structure, or the shape that replaces `Resolution`. Those are
solution-shape forks to be resolved through the design-decisions protocol when
this effort is planned.

It does not change DeepInfra catalog, context metadata, or vision capability
behavior; those are covered by their own efforts.

## Open questions for planning

How permitted fallback edges are represented, and where that representation
lives.

How the `*_only` modes are expressed once preference and degradation permission
are separable, including whether they survive as named modes or become a
combination of preference plus an empty edge set, and what migration existing
configurations require.

Whether Secondary participates in co-processor routing, and if so with what
preference relative to Local.

How exhaustion of the hosted tiers is surfaced to the user, given that the
current cross-tier fallback path emits a progress notice rather than a terminal
error.

## Acceptance criteria

Deferred until this effort is planned. The Secondary-to-Local constraint above is
the one requirement that must survive into that plan intact: tests must
demonstrate that exhausting the hosted tiers produces a reported failure and no
local execution, at both the availability-time and runtime-failure crossing
sites, in both host and worker paths.
